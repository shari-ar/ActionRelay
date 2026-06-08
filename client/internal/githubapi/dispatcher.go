package githubapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"actionrelay/client/internal/config"
	"actionrelay/client/internal/protocol"
)

const gitHubAPIVersion = "2022-11-28"

type Dispatcher struct {
	httpClient           *http.Client
	baseURL              string
	owner                string
	repo                 string
	workflow             string
	workflowRef          string
	token                string
	pollInterval         time.Duration
	runStartTimeout      time.Duration
	runCompletionTimeout time.Duration

	mu                sync.Mutex
	serverClockOffset time.Duration
	serverClockSynced bool
}

func NewDispatcher(cfg config.Config, token string) (*Dispatcher, error) {
	owner, repo, err := cfg.RepoOwnerAndName()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("github token is required")
	}
	return &Dispatcher{
		httpClient:           &http.Client{Timeout: 2 * time.Minute},
		baseURL:              strings.TrimRight(cfg.GitHubAPIBaseURL, "/"),
		owner:                owner,
		repo:                 repo,
		workflow:             cfg.Workflow,
		workflowRef:          cfg.WorkflowRef,
		token:                token,
		pollInterval:         time.Duration(cfg.PollIntervalMS) * time.Millisecond,
		runStartTimeout:      time.Duration(cfg.RunStartTimeoutSec) * time.Second,
		runCompletionTimeout: time.Duration(cfg.RunWaitTimeoutSec) * time.Second,
	}, nil
}

func (d *Dispatcher) ProcessBatch(ctx context.Context, batch protocol.RequestBatch) (protocol.ResultPackage, error) {
	if err := protocol.ValidateRequestBatch(batch); err != nil {
		return protocol.ResultPackage{}, fmt.Errorf("request batch validation failed: %w", err)
	}
	if err := d.syncServerClock(ctx); err != nil {
		return protocol.ResultPackage{}, err
	}

	log.Printf("actionrelay: github dispatch begin batch_id=%s workflow=%s ref=%s", batch.BatchID, d.workflow, d.workflowRef)
	dispatchedAtServer, err := d.dispatchBatch(ctx, batch)
	if err != nil {
		return protocol.ResultPackage{}, err
	}
	log.Printf("actionrelay: github dispatch accepted batch_id=%s server_time=%s", batch.BatchID, dispatchedAtServer.Format(time.RFC3339))

	runID, err := d.waitForRun(ctx, batch.BatchID, normalizeGitHubTimestamp(dispatchedAtServer))
	if err != nil {
		return protocol.ResultPackage{}, err
	}
	log.Printf("actionrelay: github run discovered batch_id=%s run_id=%d", batch.BatchID, runID)
	if err := d.waitForRunCompletion(ctx, runID); err != nil {
		return protocol.ResultPackage{}, err
	}
	log.Printf("actionrelay: github run completed batch_id=%s run_id=%d", batch.BatchID, runID)
	pkg, err := d.waitForResultPackage(ctx, batch.BatchID)
	if err != nil {
		return protocol.ResultPackage{}, err
	}
	log.Printf("actionrelay: github result fetched batch_id=%s run_id=%d result_count=%d", batch.BatchID, runID, len(pkg.Results))
	if err := protocol.ValidateResultPackage(pkg); err != nil {
		return protocol.ResultPackage{}, fmt.Errorf("result package validation failed: %w", err)
	}
	if pkg.Protocol != protocol.ResultPackageProtocol {
		return protocol.ResultPackage{}, fmt.Errorf("unexpected result protocol %q", pkg.Protocol)
	}
	if pkg.BatchID != batch.BatchID {
		return protocol.ResultPackage{}, fmt.Errorf("batch id mismatch: want %s got %s", batch.BatchID, pkg.BatchID)
	}
	return pkg, nil
}

func (d *Dispatcher) dispatchBatch(ctx context.Context, batch protocol.RequestBatch) (time.Time, error) {
	serializedBatch, err := json.Marshal(batch)
	if err != nil {
		return time.Time{}, fmt.Errorf("serialize batch: %w", err)
	}

	body := map[string]any{
		"ref": d.workflowRef,
		"inputs": map[string]string{
			"batch_id":                       batch.BatchID,
			"batch_payload_b64":              base64.StdEncoding.EncodeToString(serializedBatch),
			"max_response_bytes_per_request": strconv.Itoa(batch.Limits.MaxResponseBytesPerRequest),
			"request_timeout_ms":             strconv.Itoa(batch.Limits.RequestTimeoutMS),
			"worker_concurrency":             strconv.Itoa(batch.Limits.WorkerConcurrency),
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return time.Time{}, fmt.Errorf("serialize dispatch payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/dispatches", d.baseURL, d.owner, d.repo, url.PathEscape(d.workflow))
	req, err := d.newJSONRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return time.Time{}, err
	}

	localStart := time.Now().UTC()
	resp, err := d.httpClient.Do(req)
	localEnd := time.Now().UTC()
	if err != nil {
		return time.Time{}, fmt.Errorf("dispatch batch request: %w", err)
	}
	defer resp.Body.Close()

	serverAcceptedAt, ok := d.updateServerClockFromDateHeader(resp.Header.Get("Date"), localStart, localEnd)
	if resp.StatusCode != http.StatusNoContent {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return time.Time{}, fmt.Errorf("dispatch batch failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if !ok {
		serverAcceptedAt = d.serverNow()
	}
	return serverAcceptedAt, nil
}

type workflowRunsResponse struct {
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowRun struct {
	ID           int64  `json:"id"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	DisplayTitle string `json:"display_title"`
	Name         string `json:"name"`
	CreatedAt    string `json:"created_at"`
}

func (d *Dispatcher) waitForRun(ctx context.Context, batchID string, earliestCreatedAt time.Time) (int64, error) {
	deadline := time.Now().Add(d.runStartTimeout)
	for {
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("RUN_DELAYED: timed out waiting for run for batch %s", batchID)
		}
		runID, found, err := d.findRunForBatch(ctx, batchID, earliestCreatedAt)
		if err != nil {
			return 0, err
		}
		if found {
			return runID, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}

func (d *Dispatcher) findRunForBatch(ctx context.Context, batchID string, earliestCreatedAt time.Time) (int64, bool, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/runs?event=workflow_dispatch&per_page=30", d.baseURL, d.owner, d.repo, url.PathEscape(d.workflow))
	req, err := d.newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false, err
	}
	localStart := time.Now().UTC()
	resp, err := d.httpClient.Do(req)
	localEnd := time.Now().UTC()
	if err != nil {
		return 0, false, fmt.Errorf("list workflow runs: %w", err)
	}
	defer resp.Body.Close()
	d.updateServerClockFromDateHeader(resp.Header.Get("Date"), localStart, localEnd)
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, false, fmt.Errorf("list workflow runs failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var parsed workflowRunsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, false, fmt.Errorf("decode workflow runs: %w", err)
	}
	for _, run := range parsed.WorkflowRuns {
		if !(strings.Contains(run.DisplayTitle, batchID) || strings.Contains(run.Name, batchID)) {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339, run.CreatedAt)
		if err != nil {
			continue
		}
		createdAt = normalizeGitHubTimestamp(createdAt)
		if createdAt.Before(earliestCreatedAt) {
			continue
		}
		log.Printf(
			"actionrelay: github run candidate matched batch_id=%s run_id=%d created_at_server=%s created_at_local=%s status=%s",
			batchID,
			run.ID,
			run.CreatedAt,
			d.serverTimeToLocal(createdAt).Format(time.RFC3339),
			run.Status,
		)
		return run.ID, true, nil
	}
	return 0, false, nil
}

func (d *Dispatcher) waitForRunCompletion(ctx context.Context, runID int64) error {
	deadline := time.Now().Add(d.runCompletionTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("RUN_DELAYED: run %d did not complete in time", runID)
		}
		run, err := d.getRun(ctx, runID)
		if err != nil {
			return err
		}
		if run.Status == "completed" {
			if run.Conclusion != "success" {
				return fmt.Errorf("WORKER_ERROR: run %d completed with conclusion %s", runID, run.Conclusion)
			}
			return nil
		}
		log.Printf("actionrelay: github run pending run_id=%d status=%s conclusion=%s", runID, run.Status, run.Conclusion)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}

func (d *Dispatcher) getRun(ctx context.Context, runID int64) (workflowRun, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d", d.baseURL, d.owner, d.repo, runID)
	req, err := d.newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return workflowRun{}, err
	}
	localStart := time.Now().UTC()
	resp, err := d.httpClient.Do(req)
	localEnd := time.Now().UTC()
	if err != nil {
		return workflowRun{}, fmt.Errorf("get run: %w", err)
	}
	defer resp.Body.Close()
	d.updateServerClockFromDateHeader(resp.Header.Get("Date"), localStart, localEnd)
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return workflowRun{}, fmt.Errorf("get run failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var run workflowRun
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return workflowRun{}, fmt.Errorf("decode run: %w", err)
	}
	return run, nil
}

type repoContentResponse struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func (d *Dispatcher) waitForResultPackage(ctx context.Context, batchID string) (protocol.ResultPackage, error) {
	deadline := time.Now().Add(d.runCompletionTimeout)
	for {
		pkg, found, err := d.downloadResultPackage(ctx, batchID)
		if err != nil {
			return protocol.ResultPackage{}, err
		}
		if found {
			return pkg, nil
		}
		if time.Now().After(deadline) {
			return protocol.ResultPackage{}, fmt.Errorf("RESULT_PACKAGE_NOT_FOUND: timed out waiting for result %s", batchID)
		}
		select {
		case <-ctx.Done():
			return protocol.ResultPackage{}, ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}

func (d *Dispatcher) downloadResultPackage(ctx context.Context, batchID string) (protocol.ResultPackage, bool, error) {
	endpoint := fmt.Sprintf(
		"%s/repos/%s/%s/contents/results/%s.json?ref=actionrelay-results",
		d.baseURL,
		d.owner,
		d.repo,
		url.PathEscape(batchID),
	)
	req, err := d.newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return protocol.ResultPackage{}, false, err
	}
	localStart := time.Now().UTC()
	resp, err := d.httpClient.Do(req)
	localEnd := time.Now().UTC()
	if err != nil {
		return protocol.ResultPackage{}, false, fmt.Errorf("download result package: %w", err)
	}
	defer resp.Body.Close()
	d.updateServerClockFromDateHeader(resp.Header.Get("Date"), localStart, localEnd)
	if resp.StatusCode == http.StatusNotFound {
		return protocol.ResultPackage{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return protocol.ResultPackage{}, false, fmt.Errorf("download result package failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var payload repoContentResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 10<<20))
	if err := decoder.Decode(&payload); err != nil {
		return protocol.ResultPackage{}, false, fmt.Errorf("decode result package metadata: %w", err)
	}
	if payload.Type != "file" {
		return protocol.ResultPackage{}, false, fmt.Errorf("RESULT_PACKAGE_NOT_FOUND: unexpected content type %q", payload.Type)
	}
	if payload.Encoding != "base64" {
		return protocol.ResultPackage{}, false, fmt.Errorf("RESULT_PACKAGE_NOT_FOUND: unexpected content encoding %q", payload.Encoding)
	}
	raw := strings.ReplaceAll(payload.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return protocol.ResultPackage{}, false, fmt.Errorf("decode result package content: %w", err)
	}
	var result protocol.ResultPackage
	pkgDecoder := json.NewDecoder(bytes.NewReader(decoded))
	pkgDecoder.DisallowUnknownFields()
	if err := pkgDecoder.Decode(&result); err != nil {
		return protocol.ResultPackage{}, false, fmt.Errorf("decode result package: %w", err)
	}
	return result, true, nil
}

func (d *Dispatcher) syncServerClock(ctx context.Context) error {
	if d.hasServerClockSync() {
		return nil
	}
	endpoint := strings.TrimRight(d.baseURL, "/") + "/rate_limit"
	req, err := d.newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	localStart := time.Now().UTC()
	resp, err := d.httpClient.Do(req)
	localEnd := time.Now().UTC()
	if err != nil {
		return fmt.Errorf("server clock sync request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("SERVER_CLOCK_SYNC_FAILED: status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if _, ok := d.updateServerClockFromDateHeader(resp.Header.Get("Date"), localStart, localEnd); !ok {
		return errors.New("SERVER_CLOCK_SYNC_FAILED: GitHub response did not include a valid Date header")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

func (d *Dispatcher) updateServerClockFromDateHeader(dateHeader string, localStart, localEnd time.Time) (time.Time, bool) {
	serverTime, err := http.ParseTime(strings.TrimSpace(dateHeader))
	if err != nil {
		return time.Time{}, false
	}
	serverTime = serverTime.UTC()
	midpoint := localStart.Add(localEnd.Sub(localStart) / 2).UTC()

	d.mu.Lock()
	d.serverClockOffset = serverTime.Sub(midpoint)
	d.serverClockSynced = true
	d.mu.Unlock()

	return serverTime, true
}

func (d *Dispatcher) hasServerClockSync() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.serverClockSynced
}

func (d *Dispatcher) serverNow() time.Time {
	d.mu.Lock()
	offset := d.serverClockOffset
	d.mu.Unlock()
	return time.Now().UTC().Add(offset)
}

func (d *Dispatcher) serverTimeToLocal(serverTime time.Time) time.Time {
	d.mu.Lock()
	offset := d.serverClockOffset
	d.mu.Unlock()
	return serverTime.UTC().Add(-offset)
}

func normalizeGitHubTimestamp(timestamp time.Time) time.Time {
	return timestamp.UTC().Truncate(time.Second)
}

func (d *Dispatcher) newJSONRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
