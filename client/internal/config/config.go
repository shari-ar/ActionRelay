package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Repo               string `json:"repo"`
	Workflow           string `json:"workflow"`
	WorkflowRef        string `json:"workflow_ref"`
	GitHubTokenEnv     string `json:"github_token_env"`
	GitHubAPIBaseURL   string `json:"github_api_base_url"`
	AgentListenAddr    string `json:"agent_listen_addr"`
	BatchIntervalMS    int    `json:"batch_interval_ms"`
	RequestTimeoutMS   int    `json:"request_timeout_ms"`
	MaxResponseBytes   int    `json:"max_response_bytes"`
	WorkerConcurrency  int    `json:"worker_concurrency"`
	MaxBatchRequests   int    `json:"max_batch_requests"`
	MaxBatchBytes      int    `json:"max_batch_bytes"`
	MaxQueueRequests   int    `json:"max_queue_requests"`
	RunStartTimeoutSec int    `json:"run_start_timeout_sec"`
	RunWaitTimeoutSec  int    `json:"run_wait_timeout_sec"`
	PollIntervalMS     int    `json:"poll_interval_ms"`
}

func Default() Config {
	return Config{
		WorkflowRef:        "main",
		GitHubTokenEnv:     "ACTIONRELAY_GITHUB_TOKEN",
		GitHubAPIBaseURL:   "https://api.github.com",
		AgentListenAddr:    "127.0.0.1:8787",
		BatchIntervalMS:    1000,
		RequestTimeoutMS:   8000,
		MaxResponseBytes:   65536,
		WorkerConcurrency:  4,
		MaxBatchRequests:   32,
		MaxBatchBytes:      262144,
		MaxQueueRequests:   256,
		RunStartTimeoutSec: 120,
		RunWaitTimeoutSec:  900,
		PollIntervalMS:     2000,
	}
}

func ResolvePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty config path")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func Load(path string) (Config, error) {
	resolved, err := ResolvePath(path)
	if err != nil {
		return Config{}, err
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	resolved, err := ResolvePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(resolved, payload, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func (c Config) Token() (string, error) {
	token := strings.TrimSpace(os.Getenv(c.GitHubTokenEnv))
	if token == "" {
		return "", fmt.Errorf("token env %q is empty", c.GitHubTokenEnv)
	}
	return token, nil
}

func (c Config) RepoOwnerAndName() (string, string, error) {
	parts := strings.Split(strings.TrimSpace(c.Repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be owner/repo, got %q", c.Repo)
	}
	return parts[0], parts[1], nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Repo) == "" {
		return errors.New("repo is required")
	}
	if strings.TrimSpace(c.Workflow) == "" {
		return errors.New("workflow is required")
	}
	if strings.TrimSpace(c.GitHubTokenEnv) == "" {
		return errors.New("github_token_env is required")
	}
	if strings.TrimSpace(c.GitHubAPIBaseURL) == "" {
		return errors.New("github_api_base_url is required")
	}
	if c.BatchIntervalMS <= 0 {
		return errors.New("batch_interval_ms must be > 0")
	}
	if c.RequestTimeoutMS <= 0 {
		return errors.New("request_timeout_ms must be > 0")
	}
	if c.MaxResponseBytes <= 0 {
		return errors.New("max_response_bytes must be > 0")
	}
	if c.WorkerConcurrency <= 0 {
		return errors.New("worker_concurrency must be > 0")
	}
	if c.MaxBatchRequests <= 0 {
		return errors.New("max_batch_requests must be > 0")
	}
	if c.MaxBatchBytes <= 0 {
		return errors.New("max_batch_bytes must be > 0")
	}
	if c.MaxQueueRequests <= 0 {
		return errors.New("max_queue_requests must be > 0")
	}
	if c.RunStartTimeoutSec <= 0 {
		return errors.New("run_start_timeout_sec must be > 0")
	}
	if c.RunWaitTimeoutSec <= 0 {
		return errors.New("run_wait_timeout_sec must be > 0")
	}
	if c.PollIntervalMS <= 0 {
		return errors.New("poll_interval_ms must be > 0")
	}
	return nil
}
