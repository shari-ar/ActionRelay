package protocol

import (
	"fmt"
	"strings"
	"time"
)

const routeModeWholeDevice = "whole_device"

func ValidateRequestBatch(batch RequestBatch) error {
	if batch.Protocol != RequestBatchProtocol {
		return fmt.Errorf("invalid request batch protocol %q", batch.Protocol)
	}
	if strings.TrimSpace(batch.BatchID) == "" {
		return fmt.Errorf("batch_id is required")
	}
	if _, err := time.Parse(time.RFC3339, batch.SentAt); err != nil {
		return fmt.Errorf("sent_at must be RFC3339: %w", err)
	}
	if batch.Client.BatchIntervalMS <= 0 {
		return fmt.Errorf("client.batch_interval_ms must be > 0")
	}
	if batch.Client.RouteMode != routeModeWholeDevice {
		return fmt.Errorf("client.route_mode must be %q", routeModeWholeDevice)
	}
	if batch.Limits.MaxResponseBytesPerRequest <= 0 {
		return fmt.Errorf("limits.max_response_bytes_per_request must be > 0")
	}
	if batch.Limits.RequestTimeoutMS <= 0 {
		return fmt.Errorf("limits.request_timeout_ms must be > 0")
	}
	if batch.Limits.WorkerConcurrency <= 0 {
		return fmt.Errorf("limits.worker_concurrency must be > 0")
	}
	if len(batch.Requests) == 0 {
		return fmt.Errorf("requests must contain at least one item")
	}

	seen := make(map[string]struct{}, len(batch.Requests))
	for idx, item := range batch.Requests {
		if strings.TrimSpace(item.RequestID) == "" {
			return fmt.Errorf("requests[%d].request_id is required", idx)
		}
		if _, exists := seen[item.RequestID]; exists {
			return fmt.Errorf("duplicate request_id %q", item.RequestID)
		}
		seen[item.RequestID] = struct{}{}

		if strings.TrimSpace(item.Method) == "" {
			return fmt.Errorf("requests[%d].method is required", idx)
		}
		if strings.TrimSpace(item.URL) == "" {
			return fmt.Errorf("requests[%d].url is required", idx)
		}
		for key := range item.Headers {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("requests[%d].headers contains an empty key", idx)
			}
		}
		if item.Body == nil {
			continue
		}
		if item.Body.Encoding != "base64" {
			return fmt.Errorf("requests[%d].body.encoding must be %q", idx, "base64")
		}
	}
	return nil
}

func ValidateResultPackage(pkg ResultPackage) error {
	if pkg.Protocol != ResultPackageProtocol {
		return fmt.Errorf("invalid result package protocol %q", pkg.Protocol)
	}
	if strings.TrimSpace(pkg.BatchID) == "" {
		return fmt.Errorf("batch_id is required")
	}

	seen := make(map[string]struct{}, len(pkg.Results))
	for idx, result := range pkg.Results {
		if strings.TrimSpace(result.RequestID) == "" {
			return fmt.Errorf("results[%d].request_id is required", idx)
		}
		if _, exists := seen[result.RequestID]; exists {
			return fmt.Errorf("duplicate result request_id %q", result.RequestID)
		}
		seen[result.RequestID] = struct{}{}

		if result.OK {
			if result.Response == nil || result.Error != nil {
				return fmt.Errorf("results[%d] must include response and no error when ok=true", idx)
			}
		} else {
			if result.Response != nil || result.Error == nil {
				return fmt.Errorf("results[%d] must include error and no response when ok=false", idx)
			}
		}

		if result.Response != nil {
			if strings.TrimSpace(result.Response.URL) == "" {
				return fmt.Errorf("results[%d].response.url is required", idx)
			}
			if result.Response.Body.Bytes < 0 {
				return fmt.Errorf("results[%d].response.body.bytes must be >= 0", idx)
			}
			if result.Response.TimingMS < 0 {
				return fmt.Errorf("results[%d].response.timing_ms must be >= 0", idx)
			}
			for key := range result.Response.Headers {
				if strings.TrimSpace(key) == "" {
					return fmt.Errorf("results[%d].response.headers contains an empty key", idx)
				}
			}
		}
		if result.Error != nil {
			if strings.TrimSpace(result.Error.Code) == "" {
				return fmt.Errorf("results[%d].error.code is required", idx)
			}
		}
	}
	return nil
}

func VerifyResultPackageForBatch(pkg ResultPackage, expectedBatchID string, requestIDs []string) error {
	if err := ValidateResultPackage(pkg); err != nil {
		return err
	}
	if pkg.BatchID != expectedBatchID {
		return fmt.Errorf("batch id mismatch: want %s got %s", expectedBatchID, pkg.BatchID)
	}

	resultsByID := make(map[string]struct{}, len(pkg.Results))
	for _, result := range pkg.Results {
		resultsByID[result.RequestID] = struct{}{}
	}
	expected := make(map[string]struct{}, len(requestIDs))
	for _, requestID := range requestIDs {
		expected[requestID] = struct{}{}
		if _, ok := resultsByID[requestID]; !ok {
			return fmt.Errorf("missing result for request_id %s", requestID)
		}
	}
	for resultID := range resultsByID {
		if _, ok := expected[resultID]; !ok {
			return fmt.Errorf("unexpected result for request_id %s", resultID)
		}
	}
	return nil
}
