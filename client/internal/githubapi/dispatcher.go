package githubapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
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
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL: strings.TrimRight(cfg.GitHubAPIBaseURL, "/"),
		owner: owner,
		repo: repo,
		workflow: cfg.Workflow,
		workflowRef: cfg.WorkflowRef,
		token: token,
		pollInterval: time.Duration(cfg.PollIntervalMS) * time.Millisecond,
		runStartTimeout: time.Duration(cfg.RunStartTimeoutSec) * time.Second,
		runCompletionTimeout: time.Duration(cfg.RunWaitTimeoutSec) * time.Second,
	}, nil
}

func (d *Dispatcher) ProcessBatch(ctx context.Context, batch protocol.RequestBatch) (protocol.ResultPackage, error) {
	dispatchedAt := time.Now().UTC()
	if err := d.dispatchBatch(ctx, batch); err != nil {
		return protocol.ResultPackage{}, err
	}
	runID, err := d.waitForRun(ctx, batch.BatchID, dispatchedAt.Add(-15*time.Second))
	if err != nil {
		return protocol.ResultPackage{}, err
	}
	if err := d.waitForRunCompletion(ctx, runID); err != nil {
		return protocol.ResultPackage{}, err
	}
	pkg, err := d.downloadResultPackage(ctx, runID, batch.BatchID)
	if err != nil {
		return protocol.ResultPackage{}, err
	}
	if pkg.Protocol != protocol.ResultPackageProtocol {
		return protocol.ResultPackage{}, fmt.Errorf("unexpected result protocol %q", pkg.Protocol)
	}
	if pkg.BatchID != batch.BatchID {
		return protocol.ResultPackage{}, fmt.Errorf("batch id mismatch: want %s got %s", batch.BatchID, pkg.BatchID)
	}
	return pkg, nil
}

func (d *Dispatcher) dispatchBatch(ctx context.Context, batch protocol.RequestBatch) error {
	serializedBatch, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("serialize batch: %w", err)
	}

	body := map[string]any{
		"ref": d.workflowRef,
		"inputs": map[string]string{
			"batch_id":                        batch.BatchID,
			"batch_payload_b64":              base64.StdEncoding.EncodeToString(serializedBatch),
			"max_response_bytes_per_request": strconv.Itoa(batch.Limits.MaxResponseBytesPerRequest),
			"request_timeout_ms":             strconv.Itoa(batch.Limits.RequestTimeoutMS),
			"worker_concurrency":             strconv.Itoa(batch.Limits.WorkerConcurrency),
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("serialize dispatch payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/dispatches", d.baseURL, d.owner, d.repo, url.PathEscape(d.workflow))
	req, err := d.newJSONRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dispatch batch request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("dispatch batch failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
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
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("list workflow runs: %w", err)
	}
	defer resp.Body.Close()
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
		if createdAt.Before(earliestCreatedAt) {
			continue
		}
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
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return workflowRun{}, fmt.Errorf("get run: %w", err)
	}
	defer resp.Body.Close()
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

type runArtifactsResponse struct {
	Artifacts []artifact `json:"artifacts"`
}

type artifact struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Expired bool   `json:"expired"`
}

func (d *Dispatcher) downloadResultPackage(ctx context.Context, runID int64, batchID string) (protocol.ResultPackage, error) {
	artifactID, err := d.findArtifactID(ctx, runID, "actionrelay-result-"+batchID)
	if err != nil {
		return protocol.ResultPackage{}, err
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/artifacts/%d/zip", d.baseURL, d.owner, d.repo, artifactID)
	req, err := d.newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return protocol.ResultPackage{}, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return protocol.ResultPackage{}, fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return protocol.ResultPackage{}, fmt.Errorf("download artifact failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}

	artifactZip, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return protocol.ResultPackage{}, fmt.Errorf("read artifact: %w", err)
	}
	return parseResultPackageFromZip(artifactZip)
}

func (d *Dispatcher) findArtifactID(ctx context.Context, runID int64, expectedName string) (int64, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d/artifacts?per_page=100", d.baseURL, d.owner, d.repo, runID)
	req, err := d.newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list run artifacts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("list run artifacts failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var parsed runArtifactsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("decode artifacts: %w", err)
	}
	for _, item := range parsed.Artifacts {
		if item.Name == expectedName && !item.Expired {
			return item.ID, nil
		}
	}
	return 0, fmt.Errorf("RESULT_PACKAGE_NOT_FOUND: artifact %s not found for run %d", expectedName, runID)
}

func parseResultPackageFromZip(payload []byte) (protocol.ResultPackage, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return protocol.ResultPackage{}, fmt.Errorf("open artifact zip: %w", err)
	}
	for _, file := range reader.File {
		if path.Base(file.Name) != "result-package.json" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return protocol.ResultPackage{}, fmt.Errorf("open result file: %w", err)
		}
		defer stream.Close()
		var result protocol.ResultPackage
		if err := json.NewDecoder(stream).Decode(&result); err != nil {
			return protocol.ResultPackage{}, fmt.Errorf("decode result package: %w", err)
		}
		return result, nil
	}
	return protocol.ResultPackage{}, errors.New("RESULT_PACKAGE_NOT_FOUND: result-package.json missing in artifact")
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
