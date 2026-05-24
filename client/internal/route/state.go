package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	StateVersion    = 1
	ModeWholeDevice = "whole_device"
)

type State struct {
	Version         int    `json:"version"`
	RouteMode       string `json:"route_mode"`
	Installed       bool   `json:"installed"`
	ProxyInstalled  bool   `json:"proxy_installed"`
	CleanupRequired bool   `json:"cleanup_required"`
	CleanupReason   string `json:"cleanup_reason,omitempty"`
	InstalledAt     string `json:"installed_at,omitempty"`
	UninstalledAt   string `json:"uninstalled_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	ListenAddr      string `json:"listen_addr,omitempty"`
	ProxyListenAddr string `json:"proxy_listen_addr,omitempty"`
	Platform        string `json:"platform,omitempty"`
	LastAction      string `json:"last_action,omitempty"`
}

func DefaultState() State {
	return State{
		Version:         StateVersion,
		RouteMode:       ModeWholeDevice,
		Installed:       false,
		ProxyInstalled:  false,
		CleanupRequired: false,
	}
}

func Load(path string) (State, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	state := DefaultState()
	if err := json.Unmarshal(content, &state); err != nil {
		return State{}, fmt.Errorf("decode route state: %w", err)
	}
	if err := validate(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func LoadOrDefault(path string) (State, error) {
	state, err := Load(path)
	if err == nil {
		return state, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return DefaultState(), nil
	}
	return State{}, err
}

func Save(path string, state State) error {
	if state.Version == 0 {
		state.Version = StateVersion
	}
	if state.RouteMode == "" {
		state.RouteMode = ModeWholeDevice
	}
	if state.UpdatedAt == "" {
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := validate(state); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create route state dir: %w", err)
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode route state: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write route state: %w", err)
	}
	return nil
}

func validate(state State) error {
	if state.Version <= 0 {
		return errors.New("route state version must be > 0")
	}
	if state.RouteMode != ModeWholeDevice {
		return fmt.Errorf("route_mode must be %q", ModeWholeDevice)
	}
	if state.UpdatedAt == "" {
		return errors.New("updated_at is required")
	}
	return nil
}

func MarkCleanupRequired(path, reason string) error {
	state, err := LoadOrDefault(path)
	if err != nil {
		return err
	}
	if !state.Installed {
		return nil
	}
	state.CleanupRequired = true
	state.CleanupReason = reason
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return Save(path, state)
}

func ClearCleanupRequired(path string) error {
	state, err := LoadOrDefault(path)
	if err != nil {
		return err
	}
	if !state.CleanupRequired && state.CleanupReason == "" {
		return nil
	}
	state.CleanupRequired = false
	state.CleanupReason = ""
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return Save(path, state)
}
