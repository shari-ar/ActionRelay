package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"actionrelay/client/internal/protocol"
)

type Dispatcher interface {
	ProcessBatch(ctx context.Context, batch protocol.RequestBatch) (protocol.ResultPackage, error)
}

type Settings struct {
	BatchInterval     time.Duration
	RequestTimeout    time.Duration
	MaxResponseBytes  int
	WorkerConcurrency int
	MaxBatchRequests  int
	MaxBatchBytes     int
	MaxQueueRequests  int
}

type SubmitRequest struct {
	Method string
	URL    string
	Body   []byte
	Header map[string]string
}

type pendingRequest struct {
	request  protocol.RequestItem
	resultCh chan protocol.RequestResult
}

type Agent struct {
	dispatcher Dispatcher
	settings   Settings

	queue chan pendingRequest
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
	}, nil
}

func (a *Agent) Run(ctx context.Context) {
	ticker := time.NewTicker(a.settings.BatchInterval)
	defer ticker.Stop()

	pending := make([]pendingRequest, 0, a.settings.MaxBatchRequests)
	for {
		select {
		case <-ctx.Done():
			for _, item := range pending {
				item.resultCh <- errorResult(item.request.RequestID, "WORKER_ERROR", "agent stopped before dispatch")
			}
			return
		case item := <-a.queue:
			pending = append(pending, item)
		case <-ticker.C:
			if len(pending) == 0 {
				continue
			}
			batchChunk, remaining := a.pickBatchChunk(pending)
			pending = remaining
			if len(batchChunk) == 0 {
				continue
			}
			a.dispatchBatch(ctx, batchChunk)
		}
	}
}

func (a *Agent) Submit(ctx context.Context, request SubmitRequest) (protocol.RequestResult, error) {
	item, err := normalizeRequest(request)
	if err != nil {
		return protocol.RequestResult{}, err
	}
	item.RequestID = newID("req")
	resultCh := make(chan protocol.RequestResult, 1)
	pending := pendingRequest{request: item, resultCh: resultCh}

	select {
	case a.queue <- pending:
	default:
		return errorResult(item.RequestID, "QUEUE_OVERFLOW", "local queue is full"), nil
	}

	select {
	case <-ctx.Done():
		return protocol.RequestResult{}, ctx.Err()
	case result := <-resultCh:
		return result, nil
	}
}

func (a *Agent) dispatchBatch(ctx context.Context, requests []pendingRequest) {
	batch := protocol.RequestBatch{
		Protocol: protocol.RequestBatchProtocol,
		BatchID:  newID("batch"),
		SentAt:   time.Now().UTC().Format(time.RFC3339),
		Client: protocol.ClientMeta{
			BatchIntervalMS: int(a.settings.BatchInterval / time.Millisecond),
			RouteMode:       "whole_device",
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

	resultPackage, err := a.dispatcher.ProcessBatch(ctx, batch)
	if err != nil {
		for _, item := range requests {
			item.resultCh <- errorResult(item.request.RequestID, "DISPATCH_FAILED", err.Error())
		}
		return
	}

	resultByID := make(map[string]protocol.RequestResult, len(resultPackage.Results))
	for _, result := range resultPackage.Results {
		resultByID[result.RequestID] = result
	}

	for _, item := range requests {
		result, exists := resultByID[item.request.RequestID]
		if !exists {
			item.resultCh <- errorResult(item.request.RequestID, "RESULT_PACKAGE_NOT_FOUND", "request result missing from package")
			continue
		}
		item.resultCh <- result
	}
}

func normalizeRequest(request SubmitRequest) (protocol.RequestItem, error) {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	if request.URL == "" {
		return protocol.RequestItem{}, fmt.Errorf("url is required")
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
	}, nil
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
			item.resultCh <- errorResult(item.request.RequestID, "BATCH_TOO_LARGE", "failed to size request")
			continue
		}
		if requestBytes > a.settings.MaxBatchBytes {
			item.resultCh <- errorResult(item.request.RequestID, "BATCH_TOO_LARGE", "request exceeds max batch bytes")
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
