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
