package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

type Dispatcher interface {
	ProcessBatch(ctx context.Context, batch protocol.RequestBatch) (protocol.ResultPackage, error)
}

type Settings struct {
	BatchInterval         time.Duration
	RequestTimeout        time.Duration
	MaxRequestBodyBytes   int
	MaxResponseBytes      int
	WorkerConcurrency     int
	MaxWorkerConcurrency  int
	MaxBatchRequests      int
	MaxBatchBytes         int
	MaxQueueRequests      int
	CacheTTL              time.Duration
	CacheMaxEntries       int
	BackpressureCooldown  time.Duration
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
	LastResultAt            string `json:"last_result_at,omitempty"`
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
}

type runtimeState struct {
	StartedAt               string
	PendingInBatchBuffer    int
	DispatchInFlight        bool
	LastBatchID             string
	LastBatchSentAt         string
	LastBatchRequestCount   int
	LastDispatchError       string
	LastResultAt            string
	TotalSubmittedRequests  uint64
	TotalLocalRejected      uint64
	TotalDispatchedBatches  uint64
	TotalDispatchedRequests uint64
	TotalCompletedRequests  uint64
	TotalFailedRequests     uint64
	TotalCacheHits          uint64
	TotalDeduplicated       uint64
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

	if classification := classifyRequest(item, len(request.Body), a.settings.MaxRequestBodyBytes); classification != nil {
		result := errorResult(item.RequestID, classification.Code, classification.Message)
		a.recordLocalRejection(result)
		return result, nil
	}

	if reason, active := a.backpressureState(time.Now().UTC()); active {
		result := errorResult(item.RequestID, "RUN_DELAYED", reason)
		a.recordLocalRejection(result)
		return result, nil
	}

	resultCh := make(chan protocol.RequestResult, 1)
	pending := pendingRequest{request: item, resultCh: resultCh}

	select {
	case a.queue <- pending:
		a.recordSubmitted()
	default:
		result := errorResult(item.RequestID, "QUEUE_OVERFLOW", "local queue is full")
		a.recordLocalRejection(result)
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
		LastResultAt:            state.LastResultAt,
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

	for _, unit := range units {
		batch.Requests = append(batch.Requests, unit.primary.request)
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

	a.recordBatchDispatched(batchID, len(units))
	resultPackage, err := a.dispatcher.ProcessBatch(ctx, batch)
	if err != nil {
		a.recordDispatchError(err)
		errorCode := "DISPATCH_FAILED"
		if isRunDelayedError(err) {
			errorCode = "RUN_DELAYED"
			a.activateBackpressure("GitHub Actions run start delayed")
		}
		a.failUnits(units, errorCode, err.Error())
		return
	}
	a.recordDispatchSuccess()

	resultByID := make(map[string]protocol.RequestResult, len(resultPackage.Results))
	for _, result := range resultPackage.Results {
		resultByID[result.RequestID] = result
	}

	for _, unit := range units {
		primaryResult, exists := resultByID[unit.primary.request.RequestID]
		if !exists {
			primaryResult = errorResult(unit.primary.request.RequestID, "RESULT_PACKAGE_NOT_FOUND", "request result missing from package")
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
			Message: message,
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
			Status: result.Response.Status,
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
	a.runtime.TotalDispatchedBatches++
	a.runtime.TotalDispatchedRequests += uint64(requestCount)
	a.mu.Unlock()
}

func (a *Agent) recordDispatchSuccess() {
	a.mu.Lock()
	a.runtime.DispatchInFlight = false
	a.runtime.LastDispatchError = ""
	a.mu.Unlock()
}

func (a *Agent) recordDispatchError(err error) {
	a.mu.Lock()
	a.runtime.DispatchInFlight = false
	a.runtime.LastDispatchError = err.Error()
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
	for _, unit := range units {
		primaryResult := errorResult(unit.primary.request.RequestID, code, message)
		a.recordResult(primaryResult)
		unit.primary.resultCh <- primaryResult
		for _, duplicate := range unit.duplicates {
			duplicateResult := errorResult(duplicate.request.RequestID, code, message)
			a.recordResult(duplicateResult)
			duplicate.resultCh <- duplicateResult
		}
	}
}

func isRunDelayedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "RUN_DELAYED")
}
