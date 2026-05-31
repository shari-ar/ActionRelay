package config

import (
	"testing"
)

func TestDefaultConfigVersion(t *testing.T) {
	cfg := Default()
	if cfg.ConfigVersion != 1 {
		t.Fatalf("expected config_version=1, got %d", cfg.ConfigVersion)
	}
}

func TestValidateRejectsUnknownConfigVersion(t *testing.T) {
	cfg := Default()
	cfg.ConfigVersion = 2
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate to reject config_version=2")
	}
}

func TestValidateAcceptsGitHubAPIBaseURL(t *testing.T) {
	cfg := Default()
	cfg.Repo = "owner/repo"
	cfg.Workflow = "actionrelay.yml"
	cfg.GitHubAPIBaseURL = "https://api.github.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected github api base url to be valid: %v", err)
	}
}

func TestValidateRejectsNonGitHubAPIBaseURL(t *testing.T) {
	cfg := Default()
	cfg.Repo = "owner/repo"
	cfg.Workflow = "actionrelay.yml"
	cfg.GitHubAPIBaseURL = "https://example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate to reject non-github api base url")
	}
}

func TestValidateRejectsNonHTTPSGitHubAPIBaseURL(t *testing.T) {
	cfg := Default()
	cfg.Repo = "owner/repo"
	cfg.Workflow = "actionrelay.yml"
	cfg.GitHubAPIBaseURL = "http://api.github.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate to reject non-https github api base url")
	}
}
