package proxyplatform

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const stateEnvVar = "ACTIONRELAY_PROXY_STATE_FILE"

var defaultBypassEntries = []string{
	"localhost",
	"127.0.0.1",
	"::1",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"*.local",
}

type platformState struct {
	Platform        string `json:"platform"`
	HTTPProxy       string `json:"http_proxy,omitempty"`
	HTTPSProxy      string `json:"https_proxy,omitempty"`
	NoProxy         string `json:"no_proxy,omitempty"`
	HTTPProxyUpper  string `json:"HTTP_PROXY,omitempty"`
	HTTPSProxyUpper string `json:"HTTPS_PROXY,omitempty"`
	NoProxyUpper    string `json:"NO_PROXY,omitempty"`
}

type RuntimeStatus struct {
	Platform            string   `json:"platform"`
	Supported           bool     `json:"supported"`
	SupportedError      string   `json:"supported_error,omitempty"`
	CurrentProxyTargets []string `json:"current_proxy_targets,omitempty"`
	BypassEntries       []string `json:"bypass_entries,omitempty"`
	StateFilePath       string   `json:"state_file_path,omitempty"`
	StateFilePresent    bool     `json:"state_file_present"`
	StateFileError      string   `json:"state_file_error,omitempty"`
}

func Detect() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	default:
		return runtime.GOOS
	}
}

func Status() RuntimeStatus {
	status := RuntimeStatus{
		Platform: Detect(),
	}
	if err := ValidateSupported(); err != nil {
		status.Supported = false
		status.SupportedError = err.Error()
		return status
	}
	status.Supported = true
	status.BypassEntries = append([]string(nil), defaultBypassEntries...)

	statePath, pathErr := stateFilePath()
	if pathErr == nil {
		status.StateFilePath = statePath
	} else {
		status.StateFileError = pathErr.Error()
	}

	state, exists, stateErr := loadState()
	if stateErr != nil {
		status.StateFileError = stateErr.Error()
		return status
	}
	status.StateFilePresent = exists
	if exists {
		status.CurrentProxyTargets = collectNonEmpty(
			state.HTTPProxy,
			state.HTTPSProxy,
			state.HTTPProxyUpper,
			state.HTTPSProxyUpper,
		)
	}
	return status
}

func ValidateSupported() error {
	switch Detect() {
	case "windows", "macos", "linux":
		return nil
	default:
		return fmt.Errorf("proxy integration is not supported on platform %q", Detect())
	}
}

func Install(proxyListenAddr string) error {
	if err := ValidateSupported(); err != nil {
		return err
	}
	host, port, err := splitListenAddr(proxyListenAddr)
	if err != nil {
		return err
	}
	if host != "127.0.0.1" && host != "localhost" {
		return fmt.Errorf("proxy listen host must be localhost or 127.0.0.1")
	}

	switch Detect() {
	case "linux":
		return installLinuxProxy(host, port)
	case "macos":
		return installMacOSProxy(host, port)
	case "windows":
		return installWindowsProxy(host, port)
	default:
		return fmt.Errorf("proxy integration is not supported on platform %q", Detect())
	}
}

func Uninstall() error {
	if err := ValidateSupported(); err != nil {
		return err
	}

	switch Detect() {
	case "linux":
		return uninstallLinuxProxy()
	case "macos":
		return uninstallMacOSProxy()
	case "windows":
		return uninstallWindowsProxy()
	default:
		return fmt.Errorf("proxy integration is not supported on platform %q", Detect())
	}
}

func splitListenAddr(listenAddr string) (string, string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return "", "", fmt.Errorf("invalid proxy listen address %q", listenAddr)
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return "", "", fmt.Errorf("invalid proxy listen address %q", listenAddr)
	}
	return strings.TrimSpace(host), strings.TrimSpace(port), nil
}

func stateFilePath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(stateEnvVar)); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".actionrelay", "proxy-platform-state.json"), nil
}

func loadState() (platformState, bool, error) {
	path, err := stateFilePath()
	if err != nil {
		return platformState{}, false, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return platformState{}, false, nil
		}
		return platformState{}, false, err
	}
	state := platformState{}
	if err := json.Unmarshal(content, &state); err != nil {
		return platformState{}, false, err
	}
	return state, true, nil
}

func saveState(state platformState) error {
	path, err := stateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o600)
}

func clearState() error {
	path, err := stateFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func installLinuxProxy(host, port string) error {
	proxyURL := fmt.Sprintf("http://%s:%s", host, port)
	bypass := strings.Join(defaultBypassEntries, ",")
	previous := platformState{
		Platform:        "linux",
		HTTPProxy:       os.Getenv("http_proxy"),
		HTTPSProxy:      os.Getenv("https_proxy"),
		NoProxy:         os.Getenv("no_proxy"),
		HTTPProxyUpper:  os.Getenv("HTTP_PROXY"),
		HTTPSProxyUpper: os.Getenv("HTTPS_PROXY"),
		NoProxyUpper:    os.Getenv("NO_PROXY"),
	}
	if err := saveState(previous); err != nil {
		return err
	}
	if err := os.Setenv("http_proxy", proxyURL); err != nil {
		return rollbackLinux(previous, err)
	}
	if err := os.Setenv("https_proxy", proxyURL); err != nil {
		return rollbackLinux(previous, err)
	}
	if err := os.Setenv("HTTP_PROXY", proxyURL); err != nil {
		return rollbackLinux(previous, err)
	}
	if err := os.Setenv("HTTPS_PROXY", proxyURL); err != nil {
		return rollbackLinux(previous, err)
	}
	if err := os.Setenv("no_proxy", bypass); err != nil {
		return rollbackLinux(previous, err)
	}
	if err := os.Setenv("NO_PROXY", bypass); err != nil {
		return rollbackLinux(previous, err)
	}
	return nil
}

func rollbackLinux(previous platformState, cause error) error {
	_ = restoreLinux(previous)
	return fmt.Errorf("linux proxy install failed and was rolled back: %w", cause)
}

func restoreLinux(state platformState) error {
	_ = restoreEnv("http_proxy", state.HTTPProxy)
	_ = restoreEnv("https_proxy", state.HTTPSProxy)
	_ = restoreEnv("no_proxy", state.NoProxy)
	_ = restoreEnv("HTTP_PROXY", state.HTTPProxyUpper)
	_ = restoreEnv("HTTPS_PROXY", state.HTTPSProxyUpper)
	_ = restoreEnv("NO_PROXY", state.NoProxyUpper)
	return nil
}

func uninstallLinuxProxy() error {
	state, exists, err := loadState()
	if err != nil {
		return err
	}
	if exists && state.Platform == "linux" {
		if err := restoreLinux(state); err != nil {
			return err
		}
		return clearState()
	}
	_ = os.Unsetenv("http_proxy")
	_ = os.Unsetenv("https_proxy")
	_ = os.Unsetenv("no_proxy")
	_ = os.Unsetenv("HTTP_PROXY")
	_ = os.Unsetenv("HTTPS_PROXY")
	_ = os.Unsetenv("NO_PROXY")
	return nil
}

func installMacOSProxy(host, port string) error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return fmt.Errorf("no active macOS network services found")
	}

	for _, service := range services {
		if err := runCommand("networksetup", "-setwebproxy", service, host, port); err != nil {
			_ = uninstallMacOSProxy()
			return fmt.Errorf("failed to set web proxy for %q: %w", service, err)
		}
		if err := runCommand("networksetup", "-setsecurewebproxy", service, host, port); err != nil {
			_ = uninstallMacOSProxy()
			return fmt.Errorf("failed to set secure web proxy for %q: %w", service, err)
		}
		if err := runCommand("networksetup", "-setwebproxystate", service, "on"); err != nil {
			_ = uninstallMacOSProxy()
			return fmt.Errorf("failed to enable web proxy for %q: %w", service, err)
		}
		if err := runCommand("networksetup", "-setsecurewebproxystate", service, "on"); err != nil {
			_ = uninstallMacOSProxy()
			return fmt.Errorf("failed to enable secure web proxy for %q: %w", service, err)
		}
		bypassArgs := append([]string{"-setproxybypassdomains", service}, defaultBypassEntries...)
		if err := runCommand("networksetup", bypassArgs...); err != nil {
			_ = uninstallMacOSProxy()
			return fmt.Errorf("failed to set bypass domains for %q: %w", service, err)
		}
	}
	return saveState(platformState{Platform: "macos"})
}

func uninstallMacOSProxy() error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, service := range services {
		_ = runCommand("networksetup", "-setwebproxystate", service, "off")
		_ = runCommand("networksetup", "-setsecurewebproxystate", service, "off")
	}
	return clearState()
}

func macOSNetworkServices() ([]string, error) {
	output, err := exec.Command("networksetup", "-listallnetworkservices").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list network services: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	lines := strings.Split(string(output), "\n")
	services := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

func installWindowsProxy(host, port string) error {
	proxyValue := fmt.Sprintf("%s:%s", host, port)
	bypassValue := strings.Join(defaultBypassEntries, ";")
	previous := platformState{
		Platform:        "windows",
		HTTPProxy:       os.Getenv("http_proxy"),
		HTTPSProxy:      os.Getenv("https_proxy"),
		HTTPProxyUpper:  os.Getenv("HTTP_PROXY"),
		HTTPSProxyUpper: os.Getenv("HTTPS_PROXY"),
	}
	if err := saveState(previous); err != nil {
		return err
	}

	if err := runCommand("netsh", "winhttp", "set", "proxy", proxyValue, bypassValue); err != nil {
		_ = clearState()
		return fmt.Errorf("set WinHTTP proxy failed: %w", err)
	}
	if err := os.Setenv("http_proxy", "http://"+proxyValue); err != nil {
		_ = uninstallWindowsProxy()
		return err
	}
	if err := os.Setenv("https_proxy", "http://"+proxyValue); err != nil {
		_ = uninstallWindowsProxy()
		return err
	}
	if err := os.Setenv("HTTP_PROXY", "http://"+proxyValue); err != nil {
		_ = uninstallWindowsProxy()
		return err
	}
	if err := os.Setenv("HTTPS_PROXY", "http://"+proxyValue); err != nil {
		_ = uninstallWindowsProxy()
		return err
	}
	return nil
}

func uninstallWindowsProxy() error {
	_ = runCommand("netsh", "winhttp", "reset", "proxy")
	state, exists, err := loadState()
	if err != nil {
		return err
	}
	if exists && state.Platform == "windows" {
		_ = restoreEnv("http_proxy", state.HTTPProxy)
		_ = restoreEnv("https_proxy", state.HTTPSProxy)
		_ = restoreEnv("HTTP_PROXY", state.HTTPProxyUpper)
		_ = restoreEnv("HTTPS_PROXY", state.HTTPSProxyUpper)
		return clearState()
	}
	_ = os.Unsetenv("http_proxy")
	_ = os.Unsetenv("https_proxy")
	_ = os.Unsetenv("HTTP_PROXY")
	_ = os.Unsetenv("HTTPS_PROXY")
	return nil
}

func restoreEnv(key, value string) error {
	if value == "" {
		return os.Unsetenv(key)
	}
	return os.Setenv(key, value)
}

func runCommand(name string, args ...string) error {
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func collectNonEmpty(values ...string) []string {
	seen := map[string]struct{}{}
	collected := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		collected = append(collected, value)
	}
	return collected
}
