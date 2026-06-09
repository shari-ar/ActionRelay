package proxyplatform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCollectNonEmpty(t *testing.T) {
	values := collectNonEmpty("", "http://127.0.0.1:8788", "http://127.0.0.1:8788", " https://x ", "")
	if len(values) != 2 {
		t.Fatalf("expected 2 unique values, got %d (%v)", len(values), values)
	}
	if values[0] != "http://127.0.0.1:8788" {
		t.Fatalf("unexpected first value: %q", values[0])
	}
	if values[1] != "https://x" {
		t.Fatalf("unexpected second value: %q", values[1])
	}
}

func TestSplitListenAddr(t *testing.T) {
	host, port, err := splitListenAddr("127.0.0.1:8788")
	if err != nil {
		t.Fatalf("splitListenAddr returned error: %v", err)
	}
	if host != "127.0.0.1" || port != "8788" {
		t.Fatalf("unexpected split result: host=%q port=%q", host, port)
	}
}

func TestSplitListenAddrRejectsInvalid(t *testing.T) {
	_, _, err := splitListenAddr("127.0.0.1")
	if err == nil {
		t.Fatal("expected invalid listen address error")
	}
}

func TestStatusReadsStateAndBypassEntries(t *testing.T) {
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "proxy-platform-state.json")
	if err := os.Setenv(stateEnvVar, statePath); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(stateEnvVar) })

	state := platformState{
		Platform:        Detect(),
		HTTPProxy:       "http://127.0.0.1:8788",
		HTTPSProxy:      "http://127.0.0.1:8788",
		HTTPProxyUpper:  "http://127.0.0.1:8788",
		HTTPSProxyUpper: "http://127.0.0.1:8788",
	}
	if err := saveState(state); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	status := Status()
	if !status.Supported {
		t.Fatalf("expected supported platform, got unsupported: %s", status.SupportedError)
	}
	if !status.StateFilePresent {
		t.Fatal("expected state file to be present")
	}
	if len(status.BypassEntries) == 0 {
		t.Fatal("expected default bypass entries")
	}
	if len(status.CurrentProxyTargets) == 0 {
		t.Fatal("expected current proxy targets from saved state")
	}
}

func TestLinuxInstallUninstallSetsBypass(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-specific test")
	}
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "proxy-platform-state.json")
	if err := os.Setenv(stateEnvVar, statePath); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(stateEnvVar) })

	previousHTTPProxy := os.Getenv("http_proxy")
	previousHTTPSProxy := os.Getenv("https_proxy")
	previousNoProxy := os.Getenv("no_proxy")
	previousHTTPProxyUpper := os.Getenv("HTTP_PROXY")
	previousHTTPSProxyUpper := os.Getenv("HTTPS_PROXY")
	previousNoProxyUpper := os.Getenv("NO_PROXY")
	t.Cleanup(func() {
		_ = restoreEnv("http_proxy", previousHTTPProxy)
		_ = restoreEnv("https_proxy", previousHTTPSProxy)
		_ = restoreEnv("no_proxy", previousNoProxy)
		_ = restoreEnv("HTTP_PROXY", previousHTTPProxyUpper)
		_ = restoreEnv("HTTPS_PROXY", previousHTTPSProxyUpper)
		_ = restoreEnv("NO_PROXY", previousNoProxyUpper)
	})

	if err := Install("127.0.0.1:8788"); err != nil {
		t.Fatalf("Install failed on linux: %v", err)
	}

	if got := os.Getenv("http_proxy"); got != "http://127.0.0.1:8788" {
		t.Fatalf("unexpected http_proxy: %q", got)
	}
	if got := os.Getenv("https_proxy"); got != "http://127.0.0.1:8788" {
		t.Fatalf("unexpected https_proxy: %q", got)
	}
	noProxy := os.Getenv("no_proxy")
	if !strings.Contains(noProxy, "localhost") || !strings.Contains(noProxy, "127.0.0.1") {
		t.Fatalf("expected localhost bypass defaults, got %q", noProxy)
	}

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall failed on linux: %v", err)
	}
}

func TestLinuxUninstallClearsBypassEnvWhenNoState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-specific test")
	}
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "proxy-platform-state.json")
	if err := os.Setenv(stateEnvVar, statePath); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(stateEnvVar) })

	if err := os.Setenv("no_proxy", "localhost,127.0.0.1"); err != nil {
		t.Fatalf("set no_proxy failed: %v", err)
	}
	if err := os.Setenv("NO_PROXY", "localhost,127.0.0.1"); err != nil {
		t.Fatalf("set NO_PROXY failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("no_proxy")
		_ = os.Unsetenv("NO_PROXY")
	})

	if err := uninstallLinuxProxy(); err != nil {
		t.Fatalf("uninstallLinuxProxy failed: %v", err)
	}
	if got := os.Getenv("no_proxy"); got != "" {
		t.Fatalf("expected no_proxy to be cleared, got %q", got)
	}
	if got := os.Getenv("NO_PROXY"); got != "" {
		t.Fatalf("expected NO_PROXY to be cleared, got %q", got)
	}
}
