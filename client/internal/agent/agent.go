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

const routeModeWholeDevice = "whole_device"

var allowedMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodOptions: {},
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
	BatchInterval       time.Duration
	RequestTimeout      time.Duration
	MaxRequestBodyBytes int
	MaxResponseBytes    int
	WorkerConcurrency   int
	MaxBatchRequests    int
	MaxBatchBytes       int
	MaxQueueRequests    int
}

type SubmitRequest struct {
	Method string
	URL    string
	Body   []byte
	Header map[string]string
}

type Snapshot struct {
	StartedAt                string `json:"started_at"`
	QueueCapacity            int    `json:"queue_capacity"`
	QueueDepth               int    `json:"queue_depth"`
	PendingInBatchBuffer     int    `json:"pending_in_batch_buffer"`
	DispatchInFlight         bool   `json:"dispatch_in_flight"`
	LastBatchID              string `json:"last_batch_id,omitempty"`
	LastBatchSentAt          string `json:"last_batch_sent_at,omitempty"`
	LastBatchRequestCount    int    `json:"last_batch_request_count"`
	LastDispatchError        string `json:"last_dispatch_error,omitempty"`
	LastResultAt             string `json:"last_result_at,omitempty"`
	TotalSubmittedRequests   uint64 `json:"total_submitted_requests"`
	TotalLocalRejected       uint64 `json:"total_local_rejected_requests"`
	TotalDispatchedBatches   uint64 `json:"total_dispatched_batches"`
	TotalDispatchedRequests  uint64 `json:"total_dispatched_requests"`
	TotalCompletedRequests   uint64 `json:"total_completed_requests"`
	TotalFailedRequests      uint64 `json:"total_failed_requests"`
}

type pendingRequest struct {
	request  protocol.RequestItem
	resultCh chan protocol.RequestResult
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
}

type Agent struct {
	dispatcher Dispatcher
	settings   Settings

	queue chan pendingRequest

	mu      sync.Mutex
	runtime runtimeState
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
	return &Agent{
		dispatcher: dispatcher,
		settings:   settings,
		queue:      make(chan pendingRequest, settings.MaxQueueRequests),
		runtime: runtimeState{
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}, nil
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
			if len(pending) == 0 {
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
	a.mu.Lock()
	state := a.runtime
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
	}
}

func (a *Agent) dispatchBatch(ctx context.Context, requests []pendingRequest) {
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
			WorkerConcurrency:          a.settings.WorkerConcurrency,
		},
		Requests: make([]protocol.RequestItem, 0, len(requests)),
	}

	for _, item := range requests {
		batch.Requests = append(batch.Requests, item.request)
	}

	a.recordBatchDispatched(batchID, len(requests))
	resultPackage, err := a.dispatcher.ProcessBatch(ctx, batch)
	if err != nil {
		a.recordDispatchError(err)
		for _, item := range requests {
			result := errorResult(item.request.RequestID, "DISPATCH_FAILED", err.Error())
			a.recordResult(result)
			item.resultCh <- result
		}
		return
	}
	a.recordDispatchSuccess()

	resultByID := make(map[string]protocol.RequestResult, len(resultPackage.Results))
	for _, result := range resultPackage.Results {
		resultByID[result.RequestID] = result
	}

	for _, item := range requests {
		result, exists := resultByID[item.request.RequestID]
		if !exists {
			result = errorResult(item.request.RequestID, "RESULT_PACKAGE_NOT_FOUND", "request result missing from package")
		}
		a.recordResult(result)
		item.resultCh <- result
	}
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

func newID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		fallback := time.Now().UnixNano()
		return fmt.Sprintf("%s-%d", prefix, fallback)
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

func toBase64(body []byte) string {
	return base64.StdEncoding.EncodeToString(body)
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

func (a *Agent) pickBatchChunk(pending []pendingRequest) ([]pendingRequest, []pendingRequest) {
	chunk := make([]pendingRequest, 0, min(a.settings.MaxBatchRequests, len(pending)))
	remaining := make([]pendingRequest, 0, len(pending))
	totalBytes := 0

	for _, item := range pending {
		requestBytes, err := requestSize(item.request)
		if err != nil {
			result := errorResult(item.request.RequestID, "BATCH_TOO_LARGE", "failed to size request")
			a.recordResult(result)
			item.resultCh <- result
			continue
		}
		if requestBytes > a.settings.MaxBatchBytes {
			result := errorResult(item.request.RequestID, "BATCH_TOO_LARGE", "request exceeds max batch bytes")
			a.recordResult(result)
			item.resultCh <- result
			continue
		}
		if len(chunk) >= a.settings.MaxBatchRequests || (len(chunk) > 0 && totalBytes+requestBytes > a.settings.MaxBatchBytes) {
			remaining = append(remaining, item)
			continue
		}
		chunk = append(chunk, item)
		totalBytes += requestBytes
	}

	return chunk, remaining
}

func requestSize(request protocol.RequestItem) (int, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
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
