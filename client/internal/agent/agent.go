package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"actionrelay/client/internal/protocol"
)

const (
	routeModeWholeDevice      = "whole_device"
	maxWorkerConcurrencyLimit = 8
	safeCacheStatusMin        = 200
	safeCacheStatusMax        = 299
	redactedValue             = "[REDACTED]"
	reliabilityFailClosed     = "fail_closed"
	reliabilityFailOpen       = "fail_open"
)

var allowedMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodOptions: {},
}

var cacheableMethods = map[string]struct{}{
	http.MethodGet:  {},
	http.MethodHead: {},
}

var rejectedHeaders = map[string]struct{}{
	"proxy-connection":    {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
}

var sensitiveHeaderNames = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
}

var metadataBlockedHosts = map[string]struct{}{
	"metadata":                   {},
	"metadata.google.internal":   {},
	"metadata.google":            {},
	"metadata.azure.internal":    {},
	"metadata.aliyun.internal":   {},
	"instance-data.ec2.internal": {},
}

var metadataBlockedIPs = func() map[netip.Addr]struct{} {
	raw := []string{
		"169.254.169.254",
		"100.100.100.200",
		"192.0.0.192",
		"192.0.0.170",
	}
	parsed := make(map[netip.Addr]struct{}, len(raw))
	for _, candidate := range raw {
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			continue
		}
		parsed[addr] = struct{}{}
	}
	return parsed
}()

var secretRedactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization|proxy-authorization|cookie|set-cookie|x-api-key)\s*[:=]\s*[^,\s;]+`),
	regexp.MustCompile(`(?i)(token|access_token|id_token|client_secret|password)=([^&\s]+)`),
}

type Dispatcher interface {
	ProcessBatch(ctx context.Context, batch protocol.RequestBatch) (protocol.ResultPackage, error)
}

type Settings struct {
	BatchInterval        time.Duration
	RequestTimeout       time.Duration
	MaxRequestBodyBytes  int
	MaxResponseBytes     int
	WorkerConcurrency    int
	MaxWorkerConcurrency int
	MaxBatchRequests     int
	MaxBatchBytes        int
	MaxQueueRequests     int
	CacheTTL             time.Duration
	CacheMaxEntries      int
	StaleIfErrorTTL      time.Duration
	BackpressureCooldown time.Duration
	ReliabilityMode      string
}

type SubmitRequest struct {
	Method string
	URL    string
	Body   []byte
	Header map[string]string
}

type Snapshot struct {
	StartedAt               string `json:"started_at"`
	QueueCapacity           int    `json:"queue_capacity"`
	QueueDepth              int    `json:"queue_depth"`
	PendingInBatchBuffer    int    `json:"pending_in_batch_buffer"`
	DispatchInFlight        bool   `json:"dispatch_in_flight"`
	LastBatchID             string `json:"last_batch_id,omitempty"`
	LastBatchSentAt         string `json:"last_batch_sent_at,omitempty"`
	LastBatchRequestCount   int    `json:"last_batch_request_count"`
	LastDispatchError       string `json:"last_dispatch_error,omitempty"`
	LastDispatchErrorCode   string `json:"last_dispatch_error_code,omitempty"`
	LastDispatchErrorAt     string `json:"last_dispatch_error_at,omitempty"`
	LastResultAt            string `json:"last_result_at,omitempty"`
	LastBatchCompletedAt    string `json:"last_batch_completed_at,omitempty"`
	LastBatchLatencyMS      int64  `json:"last_batch_latency_ms"`
	TotalSubmittedRequests  uint64 `json:"total_submitted_requests"`
	TotalLocalRejected      uint64 `json:"total_local_rejected_requests"`
	TotalDispatchedBatches  uint64 `json:"total_dispatched_batches"`
	TotalDispatchedRequests uint64 `json:"total_dispatched_requests"`
	TotalCompletedRequests  uint64 `json:"total_completed_requests"`
	TotalFailedRequests     uint64 `json:"total_failed_requests"`
	CacheEnabled            bool   `json:"cache_enabled"`
	CacheTTLMS              int    `json:"cache_ttl_ms"`
	CacheEntries            int    `json:"cache_entries"`
	CacheMaxEntries         int    `json:"cache_max_entries"`
	TotalCacheHits          uint64 `json:"total_cache_hits"`
	TotalDeduplicated       uint64 `json:"total_deduplicated_requests"`
	TotalFailOpenServed     uint64 `json:"total_fail_open_served"`
	BackpressureActive      bool   `json:"backpressure_active"`
	BackpressureUntil       string `json:"backpressure_until,omitempty"`
	BackpressureReason      string `json:"backpressure_reason,omitempty"`
}

type pendingRequest struct {
	request  protocol.RequestItem
	resultCh chan protocol.RequestResult
}

type dispatchUnit struct {
	primary    pendingRequest
	duplicates []pendingRequest
	cacheKey   string
	cacheable  bool
}

type cacheEntry struct {
	ExpiresAt time.Time
	Result    protocol.RequestResult
	StoredAt  time.Time
}

type runtimeState struct {
	StartedAt               string
	PendingInBatchBuffer    int
	DispatchInFlight        bool
	LastBatchID             string
	LastBatchSentAt         string
	LastBatchRequestCount   int
	LastDispatchError       string
	LastDispatchErrorCode   string
	LastDispatchErrorAt     string
	LastResultAt            string
	LastBatchCompletedAt    string
	LastBatchLatencyMS      int64
	TotalSubmittedRequests  uint64
	TotalLocalRejected      uint64
	TotalDispatchedBatches  uint64
	TotalDispatchedRequests uint64
	TotalCompletedRequests  uint64
	TotalFailedRequests     uint64
	TotalCacheHits          uint64
	TotalDeduplicated       uint64
	TotalFailOpenServed     uint64
	BackpressureUntil       time.Time
	BackpressureReason      string
}

type Agent struct {
	dispatcher Dispatcher
	settings   Settings
	queue      chan pendingRequest

	mu      sync.Mutex
	runtime runtimeState
	cache   map[string]cacheEntry
}

func New(dispatcher Dispatcher, settings Settings) (*Agent, error) {
	if dispatcher == nil {
		return nil, fmt.Errorf("dispatcher is required")
	}
	if settings.BatchInterval <= 0 {
		return nil, fmt.Errorf("batch interval must be > 0")
	}
	if settings.MaxQueueRequests <= 0 {
		return nil, fmt.Errorf("max queue requests must be > 0")
	}
	if settings.MaxBatchRequests <= 0 {
		return nil, fmt.Errorf("max batch requests must be > 0")
	}
	if settings.MaxBatchBytes <= 0 {
		return nil, fmt.Errorf("max batch bytes must be > 0")
	}
	if settings.MaxRequestBodyBytes <= 0 {
		return nil, fmt.Errorf("max request body bytes must be > 0")
	}
	if settings.MaxResponseBytes <= 0 {
		return nil, fmt.Errorf("max response bytes must be > 0")
	}
	if settings.RequestTimeout <= 0 {
		return nil, fmt.Errorf("request timeout must be > 0")
	}
	if settings.WorkerConcurrency <= 0 {
		return nil, fmt.Errorf("worker concurrency must be > 0")
	}
	if settings.MaxWorkerConcurrency <= 0 {
		settings.MaxWorkerConcurrency = maxWorkerConcurrencyLimit
	}
	if settings.MaxWorkerConcurrency > maxWorkerConcurrencyLimit {
		settings.MaxWorkerConcurrency = maxWorkerConcurrencyLimit
	}
	if settings.WorkerConcurrency > settings.MaxWorkerConcurrency {
		settings.WorkerConcurrency = settings.MaxWorkerConcurrency
	}
	if settings.CacheTTL < 0 {
		return nil, fmt.Errorf("cache ttl must be >= 0")
	}
	if settings.CacheMaxEntries < 0 {
		return nil, fmt.Errorf("cache max entries must be >= 0")
	}
	if settings.BackpressureCooldown <= 0 {
		return nil, fmt.Errorf("backpressure cooldown must be > 0")
	}
	if settings.StaleIfErrorTTL < 0 {
		return nil, fmt.Errorf("stale-if-error ttl must be >= 0")
	}
	settings.ReliabilityMode = strings.ToLower(strings.TrimSpace(settings.ReliabilityMode))
	if settings.ReliabilityMode == "" {
		settings.ReliabilityMode = reliabilityFailClosed
	}
	if settings.ReliabilityMode != reliabilityFailClosed && settings.ReliabilityMode != reliabilityFailOpen {
		return nil, fmt.Errorf("reliability mode must be %q or %q", reliabilityFailClosed, reliabilityFailOpen)
	}

	agent := &Agent{
		dispatcher: dispatcher,
		settings:   settings,
		queue:      make(chan pendingRequest, settings.MaxQueueRequests),
		runtime: runtimeState{
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
	if agent.cacheEnabled() {
		agent.cache = make(map[string]cacheEntry, settings.CacheMaxEntries)
	}
	return agent, nil
}

func (a *Agent) Run(ctx context.Context) {
	ticker := time.NewTicker(a.settings.BatchInterval)
	defer ticker.Stop()

	pending := make([]pendingRequest, 0, a.settings.MaxBatchRequests)
	a.setPendingInBatchBuffer(0)
	for {
		select {
		case <-ctx.Done():
			for _, item := range pending {
				result := errorResult(item.request.RequestID, "WORKER_ERROR", "agent stopped before dispatch")
				a.recordResult(result)
				item.resultCh <- result
			}
			a.setPendingInBatchBuffer(0)
			return
		case item := <-a.queue:
			pending = append(pending, item)
			a.setPendingInBatchBuffer(len(pending))
		case <-ticker.C:
			now := time.Now().UTC()
			a.evictExpiredCache(now)
			if len(pending) == 0 {
				continue
			}
			if reason, active := a.backpressureState(now); active {
				pending = a.rejectPendingForBackpressure(pending, reason)
				a.setPendingInBatchBuffer(len(pending))
				continue
			}
			batchChunk, remaining := a.pickBatchChunk(pending)
			pending = remaining
			a.setPendingInBatchBuffer(len(pending))
			if len(batchChunk) == 0 {
				continue
			}
			a.dispatchBatch(ctx, batchChunk)
		}
	}
}

func (a *Agent) Submit(ctx context.Context, request SubmitRequest) (protocol.RequestResult, error) {
	item := normalizeRequest(request)
	item.RequestID = newID("req")
	log.Printf("actionrelay: request accepted request_id=%s method=%s url=%s", item.RequestID, item.Method, item.URL)

	if classification := classifyRequest(item, len(request.Body), a.settings.MaxRequestBodyBytes); classification != nil {
		result := errorResult(item.RequestID, classification.Code, classification.Message)
		a.recordLocalRejection(result)
		log.Printf("actionrelay: request rejected request_id=%s code=%s", item.RequestID, classification.Code)
		return result, nil
	}

	if reason, active := a.backpressureState(time.Now().UTC()); active {
		result := errorResult(item.RequestID, "RUN_DELAYED", reason)
		a.recordLocalRejection(result)
		log.Printf("actionrelay: request backpressured request_id=%s reason=%q", item.RequestID, reason)
		return result, nil
	}

	resultCh := make(chan protocol.RequestResult, 1)
	pending := pendingRequest{request: item, resultCh: resultCh}

	select {
	case a.queue <- pending:
		a.recordSubmitted()
		log.Printf("actionrelay: request queued request_id=%s", item.RequestID)
	default:
		result := errorResult(item.RequestID, "QUEUE_OVERFLOW", "local queue is full")
		a.recordLocalRejection(result)
		log.Printf("actionrelay: request queue_overflow request_id=%s", item.RequestID)
		return result, nil
	}

	select {
	case <-ctx.Done():
		return protocol.RequestResult{}, ctx.Err()
	case result := <-resultCh:
		return result, nil
	}
}

func (a *Agent) Snapshot() Snapshot {
	now := time.Now().UTC()
	a.mu.Lock()
	state := a.runtime
	cacheEntries := len(a.cache)
	backpressureActive := false
	if !state.BackpressureUntil.IsZero() && now.Before(state.BackpressureUntil) {
		backpressureActive = true
	}
	backpressureUntil := ""
	if !state.BackpressureUntil.IsZero() {
		backpressureUntil = state.BackpressureUntil.UTC().Format(time.RFC3339)
	}
	a.mu.Unlock()

	return Snapshot{
		StartedAt:               state.StartedAt,
		QueueCapacity:           cap(a.queue),
		QueueDepth:              len(a.queue),
		PendingInBatchBuffer:    state.PendingInBatchBuffer,
		DispatchInFlight:        state.DispatchInFlight,
		LastBatchID:             state.LastBatchID,
		LastBatchSentAt:         state.LastBatchSentAt,
		LastBatchRequestCount:   state.LastBatchRequestCount,
		LastDispatchError:       state.LastDispatchError,
		LastDispatchErrorCode:   state.LastDispatchErrorCode,
		LastDispatchErrorAt:     state.LastDispatchErrorAt,
		LastResultAt:            state.LastResultAt,
		LastBatchCompletedAt:    state.LastBatchCompletedAt,
		LastBatchLatencyMS:      state.LastBatchLatencyMS,
		TotalSubmittedRequests:  state.TotalSubmittedRequests,
		TotalLocalRejected:      state.TotalLocalRejected,
		TotalDispatchedBatches:  state.TotalDispatchedBatches,
		TotalDispatchedRequests: state.TotalDispatchedRequests,
		TotalCompletedRequests:  state.TotalCompletedRequests,
		TotalFailedRequests:     state.TotalFailedRequests,
		CacheEnabled:            a.cacheEnabled(),
		CacheTTLMS:              int(a.settings.CacheTTL / time.Millisecond),
		CacheEntries:            cacheEntries,
		CacheMaxEntries:         a.settings.CacheMaxEntries,
		TotalCacheHits:          state.TotalCacheHits,
		TotalDeduplicated:       state.TotalDeduplicated,
		TotalFailOpenServed:     state.TotalFailOpenServed,
		BackpressureActive:      backpressureActive,
		BackpressureUntil:       backpressureUntil,
		BackpressureReason:      state.BackpressureReason,
	}
}

func (a *Agent) dispatchBatch(ctx context.Context, requests []pendingRequest) {
	units := a.prepareDispatchUnits(requests)
	if len(units) == 0 {
		return
	}

	batchID := newID("batch")
	batch := protocol.RequestBatch{
		Protocol: protocol.RequestBatchProtocol,
		BatchID:  batchID,
		SentAt:   time.Now().UTC().Format(time.RFC3339),
		Client: protocol.ClientMeta{
			BatchIntervalMS: int(a.settings.BatchInterval / time.Millisecond),
			RouteMode:       routeModeWholeDevice,
		},
		Limits: protocol.BatchLimits{
			MaxResponseBytesPerRequest: a.settings.MaxResponseBytes,
			RequestTimeoutMS:           int(a.settings.RequestTimeout / time.Millisecond),
			WorkerConcurrency:          min(a.settings.WorkerConcurrency, a.settings.MaxWorkerConcurrency),
		},
		Requests: make([]protocol.RequestItem, 0, len(units)),
	}

	requestIDs := make([]string, 0, len(units))
	for _, unit := range units {
		batch.Requests = append(batch.Requests, unit.primary.request)
		requestIDs = append(requestIDs, unit.primary.request.RequestID)
	}

	if err := protocol.ValidateRequestBatch(batch); err != nil {
		a.failUnits(units, "WORKER_ERROR", "request batch validation failed: "+err.Error())
		return
	}

	batchSize, err := batchSizeBytes(batch)
	if err != nil {
		a.failUnits(units, "BATCH_TOO_LARGE", "failed to size request batch")
		return
	}
	if batchSize > a.settings.MaxBatchBytes {
		a.failUnits(units, "BATCH_TOO_LARGE", "request batch exceeds max bytes")
		return
	}

	log.Printf("actionrelay: batch dispatch start batch_id=%s request_count=%d batch_bytes=%d", batchID, len(units), batchSize)
	a.recordBatchDispatched(batchID, len(units))
	dispatchedAt := time.Now().UTC()
	resultPackage, err := a.dispatcher.ProcessBatch(ctx, batch)
	if err != nil {
		a.recordDispatchError(err)
		errorCode := "DISPATCH_FAILED"
		if isRunDelayedError(err) {
			errorCode = "RUN_DELAYED"
			a.activateBackpressure("GitHub Actions run start delayed")
		}
		log.Printf("actionrelay: batch dispatch failed batch_id=%s code=%s error=%q", batchID, errorCode, err.Error())
		a.failUnits(units, errorCode, err.Error())
		return
	}
	a.recordDispatchSuccess(time.Since(dispatchedAt))
	log.Printf("actionrelay: batch dispatch complete batch_id=%s result_count=%d ok=%t", batchID, len(resultPackage.Results), resultPackage.OK)
	if err := protocol.VerifyResultPackageForBatch(resultPackage, batchID, requestIDs); err != nil {
		a.recordDispatchError(err)
		log.Printf("actionrelay: batch result verification failed batch_id=%s error=%q", batchID, err.Error())
		a.failUnits(units, "RESULT_PACKAGE_NOT_FOUND", "result package verification failed: "+err.Error())
		return
	}

	resultByID := make(map[string]protocol.RequestResult, len(resultPackage.Results))
	for _, result := range resultPackage.Results {
		if _, exists := resultByID[result.RequestID]; exists {
			log.Printf("actionrelay: duplicate result suppressed batch_id=%s request_id=%s", batchID, result.RequestID)
			continue
		}
		resultByID[result.RequestID] = result
	}

	for _, unit := range units {
		primaryResult, exists := resultByID[unit.primary.request.RequestID]
		if !exists {
			recoveredResult, recovered := a.recoverStaleForMissing(unit, "request result missing from package")
			if recovered {
				primaryResult = recoveredResult
			} else {
				primaryResult = errorResult(unit.primary.request.RequestID, "RESULT_PACKAGE_NOT_FOUND", "request result missing from package")
			}
		}
		primaryResult = sanitizeResult(primaryResult)
		if primaryResult.OK && primaryResult.Response != nil {
			log.Printf("actionrelay: request completed request_id=%s status=%d url=%s", primaryResult.RequestID, primaryResult.Response.Status, primaryResult.Response.URL)
		} else if primaryResult.Error != nil {
			log.Printf("actionrelay: request failed request_id=%s code=%s", primaryResult.RequestID, primaryResult.Error.Code)
		}
		a.recordResult(primaryResult)
		unit.primary.resultCh <- primaryResult

		if unit.cacheable && shouldCacheResult(primaryResult) {
			a.storeCacheEntry(unit.cacheKey, cloneResultForRequest(primaryResult, unit.primary.request.RequestID), time.Now().UTC())
		}

		for _, duplicate := range unit.duplicates {
			duplicateResult := cloneResultForRequest(primaryResult, duplicate.request.RequestID)
			a.recordResult(duplicateResult)
			duplicate.resultCh <- duplicateResult
		}
	}
}

func (a *Agent) prepareDispatchUnits(requests []pendingRequest) []dispatchUnit {
	units := make([]dispatchUnit, 0, len(requests))
	dedupeIndex := make(map[string]int, len(requests))
	now := time.Now().UTC()

	for _, item := range requests {
		cacheKey, cacheable := cacheKeyForRequest(item.request)
		if cacheable {
			if cached, ok := a.loadCacheEntry(cacheKey, now); ok {
				result := cloneResultForRequest(cached, item.request.RequestID)
				a.recordCacheHit()
				a.recordResult(result)
				item.resultCh <- result
				continue
			}
			if idx, exists := dedupeIndex[cacheKey]; exists {
				units[idx].duplicates = append(units[idx].duplicates, item)
				a.recordDeduplicated()
				continue
			}
		}

		unit := dispatchUnit{
			primary:   item,
			cacheKey:  cacheKey,
			cacheable: cacheable,
		}
		units = append(units, unit)
		if cacheable {
			dedupeIndex[cacheKey] = len(units) - 1
		}
	}
	return units
}

func (a *Agent) pickBatchChunk(pending []pendingRequest) ([]pendingRequest, []pendingRequest) {
	chunk := make([]pendingRequest, 0, min(a.settings.MaxBatchRequests, len(pending)))
	remaining := make([]pendingRequest, 0, len(pending))

	for _, item := range pending {
		if len(chunk) >= a.settings.MaxBatchRequests {
			remaining = append(remaining, item)
			continue
		}

		candidate := append(append([]pendingRequest(nil), chunk...), item)
		size, err := batchSizeForPending(candidate, a.settings)
		if err != nil {
			result := errorResult(item.request.RequestID, "BATCH_TOO_LARGE", "failed to size request")
			a.recordResult(result)
			item.resultCh <- result
			continue
		}
		if size > a.settings.MaxBatchBytes {
			if len(chunk) == 0 {
				result := errorResult(item.request.RequestID, "BATCH_TOO_LARGE", "request exceeds max batch bytes")
				a.recordResult(result)
				item.resultCh <- result
				continue
			}
			remaining = append(remaining, item)
			continue
		}
		chunk = candidate
	}

	return chunk, remaining
}

type classificationError struct {
	Code    string
	Message string
}

func classifyRequest(request protocol.RequestItem, bodyBytes, maxBodyBytes int) *classificationError {
	if _, ok := allowedMethods[request.Method]; !ok {
		return &classificationError{
			Code:    "METHOD_REJECTED",
			Message: fmt.Sprintf("method %q is not supported", request.Method),
		}
	}

	parsedURL, err := url.Parse(request.URL)
	if err != nil || parsedURL == nil || parsedURL.Host == "" {
		return &classificationError{
			Code:    "URL_REJECTED",
			Message: "url must be an absolute http or https URL",
		}
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme == "ws" || scheme == "wss" {
		return &classificationError{
			Code:    "ROUTE_UNSUPPORTED",
			Message: "websocket traffic is not supported",
		}
	}
	if scheme != "http" && scheme != "https" {
		return &classificationError{
			Code:    "URL_REJECTED",
			Message: "only http and https URLs are supported",
		}
	}
	if parsedURL.User != nil {
		return &classificationError{
			Code:    "URL_REJECTED",
			Message: "url credentials are not allowed",
		}
	}
	if guardrailFailure := validateDestinationHost(parsedURL); guardrailFailure != nil {
		return guardrailFailure
	}

	for key, value := range request.Headers {
		if _, blocked := rejectedHeaders[key]; blocked {
			return &classificationError{
				Code:    "HEADER_REJECTED",
				Message: fmt.Sprintf("header %q is not allowed", key),
			}
		}
		if key == "accept" && strings.Contains(strings.ToLower(value), "text/event-stream") {
			return &classificationError{
				Code:    "ROUTE_UNSUPPORTED",
				Message: "long-lived streaming responses are not supported",
			}
		}
	}

	if tokenListContains(request.Headers["connection"], "upgrade") || strings.EqualFold(request.Headers["upgrade"], "websocket") {
		return &classificationError{
			Code:    "ROUTE_UNSUPPORTED",
			Message: "websocket upgrades are not supported",
		}
	}

	if bodyBytes > maxBodyBytes {
		return &classificationError{
			Code:    "BODY_TOO_LARGE",
			Message: fmt.Sprintf("request body exceeds max bytes: %d > %d", bodyBytes, maxBodyBytes),
		}
	}

	return nil
}

func normalizeRequest(request SubmitRequest) protocol.RequestItem {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	headers := make(map[string]string, len(request.Header))
	for key, value := range request.Header {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			continue
		}
		headers[normalized] = strings.TrimSpace(value)
	}
	if len(headers) == 0 {
		headers = nil
	} else {
		headers = stableHeaders(headers)
	}

	var body *protocol.BodyData
	if len(request.Body) > 0 {
		body = &protocol.BodyData{Encoding: "base64", Data: toBase64(request.Body)}
	}

	return protocol.RequestItem{
		Method:  method,
		URL:     strings.TrimSpace(request.URL),
		Headers: headers,
		Body:    body,
	}
}

func errorResult(requestID, code, message string) protocol.RequestResult {
	return protocol.RequestResult{
		RequestID: requestID,
		OK:        false,
		Response:  nil,
		Error: &protocol.ResultError{
			Code:    code,
			Message: redactSecretText(message),
		},
	}
}

func cacheKeyForRequest(request protocol.RequestItem) (string, bool) {
	if _, ok := cacheableMethods[request.Method]; !ok {
		return "", false
	}
	if request.Body != nil && request.Body.Data != "" {
		return "", false
	}
	payload := struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
	}{
		Method:  request.Method,
		URL:     request.URL,
		Headers: request.Headers,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func shouldCacheResult(result protocol.RequestResult) bool {
	if !result.OK || result.Response == nil {
		return false
	}
	return result.Response.Status >= safeCacheStatusMin && result.Response.Status <= safeCacheStatusMax
}

func cloneResultForRequest(result protocol.RequestResult, requestID string) protocol.RequestResult {
	cloned := protocol.RequestResult{
		RequestID: requestID,
		OK:        result.OK,
	}
	if result.Response != nil {
		headers := cloneHeaders(result.Response.Headers)
		cloned.Response = &protocol.HTTPResponse{
			Status:  result.Response.Status,
			Headers: headers,
			Body: protocol.ResponseBody{
				Encoding:  result.Response.Body.Encoding,
				Data:      result.Response.Body.Data,
				Bytes:     result.Response.Body.Bytes,
				Truncated: result.Response.Body.Truncated,
			},
			URL:      result.Response.URL,
			TimingMS: result.Response.TimingMS,
		}
	}
	if result.Error != nil {
		cloned.Error = &protocol.ResultError{
			Code:    result.Error.Code,
			Message: result.Error.Message,
		}
	}
	return cloned
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func sanitizeResult(result protocol.RequestResult) protocol.RequestResult {
	sanitized := cloneResultForRequest(result, result.RequestID)
	if sanitized.Error != nil {
		sanitized.Error.Message = redactSecretText(sanitized.Error.Message)
	}
	if sanitized.Response != nil {
		sanitized.Response.Headers = redactSensitiveHeaders(sanitized.Response.Headers)
		sanitized.Response.URL = redactURLCredentials(sanitized.Response.URL)
	}
	return sanitized
}

func redactSensitiveHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	safe := make(map[string]string, len(headers))
	for key, value := range headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if _, sensitive := sensitiveHeaderNames[lower]; sensitive {
			safe[key] = redactedValue
			continue
		}
		safe[key] = value
	}
	return safe
}

func redactURLCredentials(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if parsed.User == nil {
		return rawURL
	}
	parsed.User = nil
	return parsed.String()
}

func redactSecretText(text string) string {
	redacted := text
	for _, pattern := range secretRedactionPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			separator := ":"
			if strings.Contains(match, "=") {
				separator = "="
			}
			parts := strings.SplitN(match, separator, 2)
			if len(parts) == 0 {
				return redactedValue
			}
			if separator == "=" {
				return strings.TrimSpace(parts[0]) + separator + redactedValue
			}
			return strings.TrimSpace(parts[0]) + separator + " " + redactedValue
		})
	}
	return redacted
}

func batchSizeForPending(items []pendingRequest, settings Settings) (int, error) {
	requests := make([]protocol.RequestItem, 0, len(items))
	for _, item := range items {
		requests = append(requests, item.request)
	}
	batch := protocol.RequestBatch{
		Protocol: protocol.RequestBatchProtocol,
		BatchID:  "batch-sizing-0000000000000000",
		SentAt:   "2026-01-01T00:00:00Z",
		Client: protocol.ClientMeta{
			BatchIntervalMS: int(settings.BatchInterval / time.Millisecond),
			RouteMode:       routeModeWholeDevice,
		},
		Limits: protocol.BatchLimits{
			MaxResponseBytesPerRequest: settings.MaxResponseBytes,
			RequestTimeoutMS:           int(settings.RequestTimeout / time.Millisecond),
			WorkerConcurrency:          min(settings.WorkerConcurrency, settings.MaxWorkerConcurrency),
		},
		Requests: requests,
	}
	return batchSizeBytes(batch)
}

func batchSizeBytes(batch protocol.RequestBatch) (int, error) {
	payload, err := json.Marshal(batch)
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

func tokenListContains(raw, target string) bool {
	if raw == "" {
		return false
	}
	loweredTarget := strings.ToLower(strings.TrimSpace(target))
	for _, token := range strings.Split(raw, ",") {
		if strings.ToLower(strings.TrimSpace(token)) == loweredTarget {
			return true
		}
	}
	return false
}

func stableHeaders(headers map[string]string) map[string]string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	stable := make(map[string]string, len(headers))
	for _, key := range keys {
		stable[key] = headers[key]
	}
	return stable
}

func validateDestinationHost(parsedURL *url.URL) *classificationError {
	host := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
	if host == "" {
		return &classificationError{
			Code:    "URL_REJECTED",
			Message: "url host is required",
		}
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return &classificationError{
			Code:    "REQUEST_BLOCKED",
			Message: "localhost destinations are blocked",
		}
	}
	if _, blocked := metadataBlockedHosts[host]; blocked {
		return &classificationError{
			Code:    "REQUEST_BLOCKED",
			Message: "metadata destinations are blocked",
		}
	}
	ipAddr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	if blocked, reason := isBlockedIPAddress(ipAddr); blocked {
		return &classificationError{
			Code:    "REQUEST_BLOCKED",
			Message: reason,
		}
	}
	return nil
}

func isBlockedIPAddress(ipAddr netip.Addr) (bool, string) {
	if ipAddr.IsUnspecified() {
		return true, "unspecified destinations are blocked"
	}
	if ipAddr.IsLoopback() {
		return true, "loopback destinations are blocked"
	}
	if ipAddr.IsPrivate() {
		return true, "private network destinations are blocked"
	}
	if ipAddr.IsLinkLocalUnicast() || ipAddr.IsLinkLocalMulticast() {
		return true, "link-local destinations are blocked"
	}
	if ipAddr.IsMulticast() {
		return true, "multicast destinations are blocked"
	}
	if isMetadataIPAddress(ipAddr) {
		return true, "metadata service destinations are blocked"
	}
	return false, ""
}

func isMetadataIPAddress(ipAddr netip.Addr) bool {
	_, blocked := metadataBlockedIPs[ipAddr]
	return blocked
}

func toBase64(body []byte) string {
	return base64.StdEncoding.EncodeToString(body)
}

func newID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		fallback := time.Now().UnixNano()
		return fmt.Sprintf("%s-%d", prefix, fallback)
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (a *Agent) cacheEnabled() bool {
	return a.settings.CacheTTL > 0 && a.settings.CacheMaxEntries > 0
}

func (a *Agent) loadCacheEntry(key string, now time.Time) (protocol.RequestResult, bool) {
	if !a.cacheEnabled() || key == "" {
		return protocol.RequestResult{}, false
	}
	a.mu.Lock()
	entry, exists := a.cache[key]
	if !exists {
		a.mu.Unlock()
		return protocol.RequestResult{}, false
	}
	if now.After(entry.ExpiresAt) {
		delete(a.cache, key)
		a.mu.Unlock()
		return protocol.RequestResult{}, false
	}
	result := cloneResultForRequest(entry.Result, entry.Result.RequestID)
	a.mu.Unlock()
	return result, true
}

func (a *Agent) storeCacheEntry(key string, result protocol.RequestResult, now time.Time) {
	if !a.cacheEnabled() || key == "" {
		return
	}
	entry := cacheEntry{
		ExpiresAt: now.Add(a.settings.CacheTTL),
		Result:    result,
		StoredAt:  now,
	}
	a.mu.Lock()
	if len(a.cache) >= a.settings.CacheMaxEntries {
		var oldestKey string
		var oldestExpires time.Time
		for cacheKey, cacheValue := range a.cache {
			if oldestKey == "" || cacheValue.ExpiresAt.Before(oldestExpires) {
				oldestKey = cacheKey
				oldestExpires = cacheValue.ExpiresAt
			}
		}
		if oldestKey != "" {
			delete(a.cache, oldestKey)
		}
	}
	a.cache[key] = entry
	a.mu.Unlock()
}

func (a *Agent) loadStaleCacheEntry(key string, now time.Time) (protocol.RequestResult, bool) {
	if !a.cacheEnabled() || key == "" || a.settings.StaleIfErrorTTL <= 0 {
		return protocol.RequestResult{}, false
	}
	a.mu.Lock()
	entry, exists := a.cache[key]
	a.mu.Unlock()
	if !exists || entry.StoredAt.IsZero() {
		return protocol.RequestResult{}, false
	}
	if now.Sub(entry.StoredAt) > a.settings.StaleIfErrorTTL {
		return protocol.RequestResult{}, false
	}
	return cloneResultForRequest(entry.Result, entry.Result.RequestID), true
}

func (a *Agent) evictExpiredCache(now time.Time) {
	if !a.cacheEnabled() {
		return
	}
	a.mu.Lock()
	for cacheKey, cacheValue := range a.cache {
		if now.After(cacheValue.ExpiresAt) {
			delete(a.cache, cacheKey)
		}
	}
	a.mu.Unlock()
}

func (a *Agent) recordSubmitted() {
	a.mu.Lock()
	a.runtime.TotalSubmittedRequests++
	a.mu.Unlock()
}

func (a *Agent) recordLocalRejection(result protocol.RequestResult) {
	a.mu.Lock()
	a.runtime.TotalLocalRejected++
	if !result.OK {
		a.runtime.TotalFailedRequests++
	}
	a.runtime.LastResultAt = time.Now().UTC().Format(time.RFC3339)
	a.mu.Unlock()
}

func (a *Agent) recordBatchDispatched(batchID string, requestCount int) {
	a.mu.Lock()
	a.runtime.DispatchInFlight = true
	a.runtime.LastBatchID = batchID
	a.runtime.LastBatchSentAt = time.Now().UTC().Format(time.RFC3339)
	a.runtime.LastBatchRequestCount = requestCount
	a.runtime.LastDispatchError = ""
	a.runtime.LastDispatchErrorCode = ""
	a.runtime.TotalDispatchedBatches++
	a.runtime.TotalDispatchedRequests += uint64(requestCount)
	a.mu.Unlock()
}

func (a *Agent) recordDispatchSuccess(latency time.Duration) {
	a.mu.Lock()
	a.runtime.DispatchInFlight = false
	a.runtime.LastDispatchError = ""
	a.runtime.LastDispatchErrorCode = ""
	a.runtime.LastBatchCompletedAt = time.Now().UTC().Format(time.RFC3339)
	a.runtime.LastBatchLatencyMS = latency.Milliseconds()
	a.mu.Unlock()
}

func (a *Agent) recordDispatchError(err error) {
	a.mu.Lock()
	a.runtime.DispatchInFlight = false
	a.runtime.LastDispatchError = redactSecretText(err.Error())
	a.runtime.LastDispatchErrorCode = inferErrorCode(err)
	a.runtime.LastDispatchErrorAt = time.Now().UTC().Format(time.RFC3339)
	a.mu.Unlock()
}

func (a *Agent) setPendingInBatchBuffer(count int) {
	a.mu.Lock()
	a.runtime.PendingInBatchBuffer = count
	a.mu.Unlock()
}

func (a *Agent) recordResult(result protocol.RequestResult) {
	a.mu.Lock()
	if result.OK {
		a.runtime.TotalCompletedRequests++
	} else {
		a.runtime.TotalFailedRequests++
	}
	a.runtime.LastResultAt = time.Now().UTC().Format(time.RFC3339)
	a.mu.Unlock()
}

func (a *Agent) recordCacheHit() {
	a.mu.Lock()
	a.runtime.TotalCacheHits++
	a.mu.Unlock()
}

func (a *Agent) recordDeduplicated() {
	a.mu.Lock()
	a.runtime.TotalDeduplicated++
	a.mu.Unlock()
}

func (a *Agent) activateBackpressure(reason string) {
	if reason == "" {
		reason = "GitHub Actions run start delayed"
	}
	a.mu.Lock()
	a.runtime.BackpressureUntil = time.Now().UTC().Add(a.settings.BackpressureCooldown)
	a.runtime.BackpressureReason = reason
	a.mu.Unlock()
}

func (a *Agent) backpressureState(now time.Time) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runtime.BackpressureUntil.IsZero() {
		return "", false
	}
	if !now.Before(a.runtime.BackpressureUntil) {
		a.runtime.BackpressureUntil = time.Time{}
		a.runtime.BackpressureReason = ""
		return "", false
	}
	return a.runtime.BackpressureReason, true
}

func (a *Agent) rejectPendingForBackpressure(pending []pendingRequest, reason string) []pendingRequest {
	if reason == "" {
		reason = "GitHub Actions run start delayed"
	}
	for _, item := range pending {
		result := errorResult(item.request.RequestID, "RUN_DELAYED", reason)
		a.recordResult(result)
		item.resultCh <- result
	}
	return pending[:0]
}

func (a *Agent) failUnits(units []dispatchUnit, code, message string) {
	safeMessage := redactSecretText(message)
	for _, unit := range units {
		primaryResult, usedStale := a.recoverStaleForError(unit, code, safeMessage)
		if !usedStale {
			primaryResult = errorResult(unit.primary.request.RequestID, code, safeMessage)
		}
		a.recordResult(primaryResult)
		unit.primary.resultCh <- primaryResult
		for _, duplicate := range unit.duplicates {
			duplicateResult := cloneResultForRequest(primaryResult, duplicate.request.RequestID)
			a.recordResult(duplicateResult)
			duplicate.resultCh <- duplicateResult
		}
	}
}

func (a *Agent) recoverStaleForError(unit dispatchUnit, code, message string) (protocol.RequestResult, bool) {
	if !a.failOpenEnabled() || !unit.cacheable || !isRecoverableCode(code) {
		return protocol.RequestResult{}, false
	}
	stale, ok := a.loadStaleCacheEntry(unit.cacheKey, time.Now().UTC())
	if !ok {
		return protocol.RequestResult{}, false
	}
	log.Printf("actionrelay: fail-open stale response used request_id=%s code=%s", unit.primary.request.RequestID, code)
	a.recordFailOpenServed()
	return cloneResultForRequest(stale, unit.primary.request.RequestID), true
}

func (a *Agent) recoverStaleForMissing(unit dispatchUnit, reason string) (protocol.RequestResult, bool) {
	if !a.failOpenEnabled() || !unit.cacheable {
		return protocol.RequestResult{}, false
	}
	stale, ok := a.loadStaleCacheEntry(unit.cacheKey, time.Now().UTC())
	if !ok {
		return protocol.RequestResult{}, false
	}
	log.Printf("actionrelay: fail-open stale response used request_id=%s reason=%q", unit.primary.request.RequestID, reason)
	a.recordFailOpenServed()
	return cloneResultForRequest(stale, unit.primary.request.RequestID), true
}

func isRecoverableCode(code string) bool {
	switch code {
	case "RUN_DELAYED", "DISPATCH_FAILED", "WORKER_ERROR", "RESULT_PACKAGE_NOT_FOUND":
		return true
	default:
		return false
	}
}

func (a *Agent) failOpenEnabled() bool {
	return a.settings.ReliabilityMode == reliabilityFailOpen
}

func isRunDelayedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "RUN_DELAYED")
}

func inferErrorCode(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "RUN_DELAYED"):
		return "RUN_DELAYED"
	case strings.Contains(message, "RESULT_PACKAGE_NOT_FOUND"):
		return "RESULT_PACKAGE_NOT_FOUND"
	case strings.Contains(message, "WORKER_ERROR"):
		return "WORKER_ERROR"
	case strings.Contains(message, "BATCH_TOO_LARGE"):
		return "BATCH_TOO_LARGE"
	default:
		return "DISPATCH_FAILED"
	}
}

func (a *Agent) recordFailOpenServed() {
	a.mu.Lock()
	a.runtime.TotalFailOpenServed++
	a.mu.Unlock()
}
