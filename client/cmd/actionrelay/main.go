package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"actionrelay/client/internal/agent"
	"actionrelay/client/internal/config"
	"actionrelay/client/internal/githubapi"
	"actionrelay/client/internal/protocol"
	"actionrelay/client/internal/proxy"
	"actionrelay/client/internal/proxyplatform"
	"actionrelay/client/internal/route"
)

const (
	defaultConfigPath = ".actionrelay/config.json"
	defaultAgentURL   = "http://127.0.0.1:8787"
	fetchTimeout      = 60 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(os.Args[2:])
	case "route":
		err = runRoute(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "proxy":
		err = runProxy(os.Args[2:])
	case "status":
		err = runStatus(os.Args[2:])
	case "fetch":
		err = runFetch(os.Args[2:])
	default:
		printUsage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("ActionRelay Phase 3")
	fmt.Println("Usage:")
	fmt.Println("  actionrelay init --repo owner/repo --workflow actionrelay.yml")
	fmt.Println("  actionrelay route install --yes [--config .actionrelay/config.json]")
	fmt.Println("  actionrelay route uninstall --yes [--config .actionrelay/config.json]")
	fmt.Println("  actionrelay proxy install --yes [--config .actionrelay/config.json]")
	fmt.Println("  actionrelay proxy uninstall --yes [--config .actionrelay/config.json]")
	fmt.Println("  actionrelay proxy status [--config .actionrelay/config.json]")
	fmt.Println("  actionrelay serve [--config .actionrelay/config.json] [--listen 127.0.0.1:8787]")
	fmt.Println("                 [--proxy-listen 127.0.0.1:8788] [--proxy-enabled=true]")
	fmt.Println("  actionrelay status [--config .actionrelay/config.json] [--agent http://127.0.0.1:8787]")
	fmt.Println("  actionrelay fetch [--agent http://127.0.0.1:8787] [--method GET] <url>")
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	var repo string
	var workflow string
	var workflowRef string
	var tokenEnv string
	var configPath string
	fs.StringVar(&repo, "repo", "", "GitHub repo in owner/repo format")
	fs.StringVar(&workflow, "workflow", "actionrelay.yml", "Workflow file name or ID")
	fs.StringVar(&workflowRef, "workflow-ref", "main", "Git ref used for workflow dispatch")
	fs.StringVar(&tokenEnv, "token-env", "ACTIONRELAY_GITHUB_TOKEN", "Environment variable containing GitHub token")
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.Default()
	cfg.Repo = repo
	cfg.Workflow = workflow
	cfg.WorkflowRef = workflowRef
	cfg.GitHubTokenEnv = tokenEnv
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := config.Save(configPath, cfg); err != nil {
		return err
	}
	fmt.Printf("wrote config to %s\n", configPath)
	return nil
}

func runRoute(args []string) error {
	if len(args) == 0 {
		return errors.New("route requires a subcommand: install or uninstall")
	}
	switch args[0] {
	case "install":
		return runRouteInstall(args[1:])
	case "uninstall":
		return runRouteUninstall(args[1:])
	default:
		return fmt.Errorf("unknown route subcommand %q", args[0])
	}
}

func runProxy(args []string) error {
	if len(args) == 0 {
		return errors.New("proxy requires a subcommand: install, uninstall, or status")
	}
	switch args[0] {
	case "install":
		return runProxyInstall(args[1:])
	case "uninstall":
		return runProxyUninstall(args[1:])
	case "status":
		return runProxyStatus(args[1:])
	default:
		return fmt.Errorf("unknown proxy subcommand %q", args[0])
	}
}

func runProxyInstall(args []string) error {
	fs := flag.NewFlagSet("proxy install", flag.ContinueOnError)
	var configPath string
	var proxyListenAddr string
	var yes bool
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.StringVar(&proxyListenAddr, "proxy-listen", "", "Override proxy listen address recorded in proxy state")
	fs.BoolVar(&yes, "yes", false, "Confirm local proxy install action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("proxy install does not accept positional arguments")
	}
	if !yes {
		return errors.New("proxy install requires --yes to confirm local authorization")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if proxyListenAddr != "" {
		cfg.ProxyListenAddr = proxyListenAddr
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := proxyplatform.Install(cfg.ProxyListenAddr); err != nil {
		return err
	}

	statePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	state, err := route.LoadOrDefault(statePath)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	state.Version = route.StateVersion
	state.RouteMode = route.ModeWholeDevice
	state.ProxyInstalled = true
	state.ProxyListenAddr = cfg.ProxyListenAddr
	state.Platform = proxyplatform.Detect()
	state.UpdatedAt = now
	state.LastAction = "proxy_install"
	if err := route.Save(statePath, state); err != nil {
		return err
	}

	fmt.Printf("proxy installed (%s)\n", statePath)
	return nil
}

func runProxyUninstall(args []string) error {
	fs := flag.NewFlagSet("proxy uninstall", flag.ContinueOnError)
	var configPath string
	var yes bool
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.BoolVar(&yes, "yes", false, "Confirm local proxy uninstall action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("proxy uninstall does not accept positional arguments")
	}
	if !yes {
		return errors.New("proxy uninstall requires --yes to confirm local authorization")
	}
	if err := proxyplatform.Uninstall(); err != nil {
		return err
	}

	statePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	state, err := route.LoadOrDefault(statePath)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	state.Version = route.StateVersion
	state.RouteMode = route.ModeWholeDevice
	state.ProxyInstalled = false
	state.ProxyListenAddr = ""
	state.Platform = proxyplatform.Detect()
	state.UpdatedAt = now
	state.LastAction = "proxy_uninstall"
	if err := route.Save(statePath, state); err != nil {
		return err
	}

	fmt.Printf("proxy uninstalled (%s)\n", statePath)
	return nil
}

func runProxyStatus(args []string) error {
	fs := flag.NewFlagSet("proxy status", flag.ContinueOnError)
	var configPath string
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("proxy status does not accept positional arguments")
	}

	statePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	state, err := route.LoadOrDefault(statePath)
	if err != nil {
		return err
	}
	platformRuntime := proxyplatform.Status()
	supported := platformRuntime.Supported
	supportedError := platformRuntime.SupportedError
	output := map[string]any{
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
		"route_state_path": statePath,
		"proxy": map[string]any{
			"installed":       state.ProxyInstalled,
			"listen_addr":     state.ProxyListenAddr,
			"platform":        state.Platform,
			"supported":       supported,
			"supported_error": supportedError,
			"last_action":     state.LastAction,
		},
		"platform_runtime": platformRuntime,
	}
	formatted, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("format proxy status response: %w", err)
	}
	fmt.Println(string(formatted))
	return nil
}

func runRouteInstall(args []string) error {
	fs := flag.NewFlagSet("route install", flag.ContinueOnError)
	var configPath string
	var listenAddr string
	var yes bool
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.StringVar(&listenAddr, "listen", "", "Override listen address recorded in route state")
	fs.BoolVar(&yes, "yes", false, "Confirm local route install action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("route install does not accept positional arguments")
	}
	if !yes {
		return errors.New("route install requires --yes to confirm local authorization")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if listenAddr != "" {
		cfg.AgentListenAddr = listenAddr
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	statePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	state, err := route.LoadOrDefault(statePath)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	state.Version = route.StateVersion
	state.RouteMode = route.ModeWholeDevice
	state.Installed = true
	state.CleanupRequired = false
	state.CleanupReason = ""
	state.InstalledAt = now
	state.UninstalledAt = ""
	state.UpdatedAt = now
	state.ListenAddr = cfg.AgentListenAddr
	state.LastAction = "install"
	if err := route.Save(statePath, state); err != nil {
		return err
	}
	fmt.Printf("route installed (%s)\n", statePath)
	return nil
}

func runRouteUninstall(args []string) error {
	fs := flag.NewFlagSet("route uninstall", flag.ContinueOnError)
	var configPath string
	var yes bool
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.BoolVar(&yes, "yes", false, "Confirm local route uninstall action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("route uninstall does not accept positional arguments")
	}
	if !yes {
		return errors.New("route uninstall requires --yes to confirm local authorization")
	}

	statePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	state, err := route.LoadOrDefault(statePath)
	if err != nil {
		return err
	}
	if !state.Installed && !state.CleanupRequired {
		return errors.New("route is already uninstalled")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	state.Version = route.StateVersion
	state.RouteMode = route.ModeWholeDevice
	state.Installed = false
	state.CleanupRequired = false
	state.CleanupReason = ""
	state.UninstalledAt = now
	state.UpdatedAt = now
	state.LastAction = "uninstall"
	if err := route.Save(statePath, state); err != nil {
		return err
	}
	fmt.Printf("route uninstalled (%s)\n", statePath)
	return nil
}

type headerFlags struct {
	values map[string]string
}

func (h *headerFlags) String() string {
	if len(h.values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(h.values))
	for key, value := range h.values {
		parts = append(parts, key+":"+value)
	}
	return strings.Join(parts, ",")
}

func (h *headerFlags) Set(value string) error {
	if h.values == nil {
		h.values = make(map[string]string)
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("header must be key:value")
	}
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return errors.New("header key cannot be empty")
	}
	h.values[key] = strings.TrimSpace(parts[1])
	return nil
}

func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	var method string
	var body string
	var agentURL string
	var headers headerFlags
	fs.StringVar(&method, "method", http.MethodGet, "HTTP method")
	fs.StringVar(&body, "body", "", "Request body as UTF-8 text")
	fs.StringVar(&agentURL, "agent", defaultAgentURL, "ActionRelay agent base URL")
	fs.Var(&headers, "header", "Request header in key:value format; can be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("fetch requires a single URL argument")
	}

	payload := map[string]any{
		"method":  method,
		"url":     fs.Arg(0),
		"headers": headers.values,
	}
	if body != "" {
		payload["body_base64"] = base64.StdEncoding.EncodeToString([]byte(body))
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode fetch payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(agentURL, "/")+"/v1/requests", bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: fetchTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("submit to local agent: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read agent response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var result protocol.RequestResult
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return fmt.Errorf("decode agent response: %w", err)
	}

	formatted, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("format response: %w", err)
	}
	fmt.Println(string(formatted))
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var configPath string
	var listenAddr string
	var proxyListenAddr string
	var proxyEnabled bool
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.StringVar(&listenAddr, "listen", "", "Override listen address")
	fs.StringVar(&proxyListenAddr, "proxy-listen", "", "Override proxy listen address")
	fs.BoolVar(&proxyEnabled, "proxy-enabled", true, "Enable proxy foundation listener")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if listenAddr != "" {
		cfg.AgentListenAddr = listenAddr
	}
	if proxyListenAddr != "" {
		cfg.ProxyListenAddr = proxyListenAddr
	}
	cfg.ProxyEnabled = proxyEnabled
	if err := cfg.Validate(); err != nil {
		return err
	}
	routeStatePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	cleanupReason := "agent stopped while route remained installed; run route uninstall --yes"
	defer func() {
		_ = route.MarkCleanupRequired(routeStatePath, cleanupReason)
	}()
	token, err := cfg.Token()
	if err != nil {
		return err
	}

	dispatcher, err := githubapi.NewDispatcher(cfg, token)
	if err != nil {
		return err
	}

	routeAgent, err := agent.New(dispatcher, agent.Settings{
		BatchInterval:        time.Duration(cfg.BatchIntervalMS) * time.Millisecond,
		RequestTimeout:       time.Duration(cfg.RequestTimeoutMS) * time.Millisecond,
		MaxRequestBodyBytes:  cfg.MaxRequestBodyBytes,
		MaxResponseBytes:     cfg.MaxResponseBytes,
		WorkerConcurrency:    cfg.WorkerConcurrency,
		MaxBatchRequests:     cfg.MaxBatchRequests,
		MaxBatchBytes:        cfg.MaxBatchBytes,
		MaxQueueRequests:     cfg.MaxQueueRequests,
		CacheTTL:             time.Duration(cfg.CacheTTLMS) * time.Millisecond,
		CacheMaxEntries:      cfg.CacheMaxEntries,
		StaleIfErrorTTL:      time.Duration(cfg.StaleIfErrorTTLMS) * time.Millisecond,
		BackpressureCooldown: time.Duration(cfg.BackpressureCooldownMS) * time.Millisecond,
		ReliabilityMode:      cfg.ReliabilityMode,
	})
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go routeAgent.Run(ctx)

	var proxyHTTPServer *http.Server
	if cfg.ProxyEnabled {
		proxyServer, err := proxy.NewServer(proxy.Config{ListenAddr: cfg.ProxyListenAddr}, routeAgent)
		if err != nil {
			return err
		}
		proxyHTTPServer = &http.Server{
			Addr:              cfg.ProxyListenAddr,
			Handler:           proxyServer.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			log.Printf("actionrelay proxy foundation listening on %s", cfg.ProxyListenAddr)
			if err := proxyHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("actionrelay: proxy foundation server stopped: %v", err)
				cancel()
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/v1/status", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writeJSON(writer, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		state, err := route.LoadOrDefault(routeStatePath)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "failed to load route state"})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"ok":      true,
			"route":   state,
			"runtime": routeAgent.Snapshot(),
		})
	})
	mux.HandleFunc("/v1/requests", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writeJSON(writer, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var payload struct {
			Method     string            `json:"method"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
			BodyBase64 string            `json:"body_base64"`
		}
		if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		var rawBody []byte
		if payload.BodyBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(payload.BodyBase64)
			if err != nil {
				writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid body_base64"})
				return
			}
			rawBody = decoded
		}

		result, err := routeAgent.Submit(request.Context(), agent.SubmitRequest{
			Method: payload.Method,
			URL:    payload.URL,
			Header: payload.Headers,
			Body:   rawBody,
		})
		if err != nil {
			writeJSON(writer, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	})

	server := &http.Server{
		Addr:              cfg.AgentListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := route.ClearCleanupRequired(routeStatePath); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if proxyHTTPServer != nil {
			_ = proxyHTTPServer.Shutdown(shutdownCtx)
		}
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("actionrelay serve listening on %s\n", cfg.AgentListenAddr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type statusOutput struct {
	Timestamp      string       `json:"timestamp"`
	RouteStatePath string       `json:"route_state_path"`
	Route          route.State  `json:"route"`
	Agent          statusAgent  `json:"agent"`
	Policy         statusPolicy `json:"policy"`
	Diagnostics    diagnostics  `json:"diagnostics"`
}

type statusAgent struct {
	URL       string          `json:"url"`
	Reachable bool            `json:"reachable"`
	LatencyMS int64           `json:"latency_ms,omitempty"`
	Error     string          `json:"error,omitempty"`
	Runtime   *agent.Snapshot `json:"runtime,omitempty"`
}

type diagnostics struct {
	Severity string   `json:"severity"`
	Issues   []string `json:"issues"`
}

type statusPolicy struct {
	GitHubServerOnly       bool     `json:"github_actions_server_only"`
	GitHubDomainOnly       bool     `json:"github_domain_only"`
	ReliabilityMode        string   `json:"reliability_mode,omitempty"`
	ProxyEnabled           bool     `json:"proxy_enabled"`
	ProxyListenAddr        string   `json:"proxy_listen_addr,omitempty"`
	AgentListenAddr        string   `json:"agent_listen_addr,omitempty"`
	BypassEntries          []string `json:"bypass_entries,omitempty"`
	ProxyPlatformSupported bool     `json:"proxy_platform_supported"`
}

type agentStatusResponse struct {
	OK      bool           `json:"ok"`
	Route   *route.State   `json:"route"`
	Runtime agent.Snapshot `json:"runtime"`
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var configPath string
	var agentURL string
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.StringVar(&agentURL, "agent", "", "ActionRelay agent base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("status does not accept positional arguments")
	}

	resolvedAgentURL := agentURL
	if resolvedAgentURL == "" {
		cfg, err := config.Load(configPath)
		if err == nil {
			resolvedAgentURL = "http://" + cfg.AgentListenAddr
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if resolvedAgentURL == "" {
		resolvedAgentURL = defaultAgentURL
	}

	statePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	state, err := route.LoadOrDefault(statePath)
	if err != nil {
		return err
	}

	output := statusOutput{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		RouteStatePath: statePath,
		Route:          state,
		Agent: statusAgent{
			URL: resolvedAgentURL,
		},
		Policy: statusPolicy{
			GitHubServerOnly: true,
			GitHubDomainOnly: true,
		},
	}
	if cfg, err := config.Load(configPath); err == nil {
		platformRuntime := proxyplatform.Status()
		output.Policy.ReliabilityMode = cfg.ReliabilityMode
		output.Policy.ProxyEnabled = cfg.ProxyEnabled
		output.Policy.ProxyListenAddr = cfg.ProxyListenAddr
		output.Policy.AgentListenAddr = cfg.AgentListenAddr
		output.Policy.BypassEntries = platformRuntime.BypassEntries
		output.Policy.ProxyPlatformSupported = platformRuntime.Supported
	}

	statusResp, latencyMS, err := fetchAgentStatus(resolvedAgentURL)
	if err != nil {
		output.Agent.Reachable = false
		output.Agent.Error = err.Error()
	} else {
		output.Agent.Reachable = true
		output.Agent.LatencyMS = latencyMS
		output.Agent.Runtime = &statusResp.Runtime
		if statusResp.Route != nil {
			output.Route = *statusResp.Route
		}
	}
	output.Diagnostics = buildDiagnostics(output)

	formatted, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("format status response: %w", err)
	}
	fmt.Println(string(formatted))
	return nil
}

func fetchAgentStatus(agentURL string) (agentStatusResponse, int64, error) {
	endpoint := strings.TrimRight(agentURL, "/") + "/v1/status"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return agentStatusResponse{}, 0, err
	}
	start := time.Now()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return agentStatusResponse{}, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return agentStatusResponse{}, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return agentStatusResponse{}, 0, fmt.Errorf("agent returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded agentStatusResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return agentStatusResponse{}, 0, err
	}
	return decoded, time.Since(start).Milliseconds(), nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func buildDiagnostics(output statusOutput) diagnostics {
	issues := make([]string, 0, 8)
	severity := "ok"
	now := time.Now().UTC()

	if !output.Agent.Reachable {
		severity = "critical"
		issues = append(issues, "agent_unreachable")
	}

	runtime := output.Agent.Runtime
	if runtime != nil {
		if runtime.LastBatchLatencyMS > 120000 {
			severity = maxSeverity(severity, "warning")
			issues = append(issues, "batch_latency_high")
		}
		if runtime.BackpressureActive {
			severity = maxSeverity(severity, "warning")
			issues = append(issues, "backpressure_active")
		}
		if runtime.QueueDepth > int(float64(runtime.QueueCapacity)*0.8) {
			severity = maxSeverity(severity, "warning")
			issues = append(issues, "queue_pressure_high")
		}
		if runtime.LastDispatchErrorCode != "" {
			severity = maxSeverity(severity, "warning")
			issues = append(issues, "last_dispatch_error:"+runtime.LastDispatchErrorCode)
		}
		if runtime.DispatchInFlight && runtime.LastBatchSentAt != "" {
			if sentAt, err := time.Parse(time.RFC3339, runtime.LastBatchSentAt); err == nil {
				if now.Sub(sentAt) > 2*time.Minute {
					severity = maxSeverity(severity, "critical")
					issues = append(issues, "dispatch_inflight_stuck")
				}
			}
		}
		if runtime.TotalFailedRequests > runtime.TotalCompletedRequests && runtime.TotalFailedRequests > 10 {
			severity = maxSeverity(severity, "warning")
			issues = append(issues, "error_rate_elevated")
		}
	}

	return diagnostics{
		Severity: severity,
		Issues:   issues,
	}
}

func maxSeverity(current, next string) string {
	order := map[string]int{"ok": 0, "warning": 1, "critical": 2}
	if order[next] > order[current] {
		return next
	}
	return current
}
