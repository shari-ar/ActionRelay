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
	"actionrelay/client/internal/route"
)

const (
	defaultConfigPath = ".actionrelay/config.json"
	defaultAgentURL   = "http://127.0.0.1:8787"
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
	fmt.Println("  actionrelay serve [--config .actionrelay/config.json] [--listen 127.0.0.1:8787]")
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

	now := time.Now().UTC().Format(time.RFC3339)
	state.Version = route.StateVersion
	state.RouteMode = route.ModeWholeDevice
	state.Installed = false
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

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
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
	fs.StringVar(&configPath, "config", defaultConfigPath, "Config file path")
	fs.StringVar(&listenAddr, "listen", "", "Override listen address")
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
	routeStatePath, err := config.RouteStatePath(configPath)
	if err != nil {
		return err
	}
	token, err := cfg.Token()
	if err != nil {
		return err
	}

	dispatcher, err := githubapi.NewDispatcher(cfg, token)
	if err != nil {
		return err
	}

	routeAgent, err := agent.New(dispatcher, agent.Settings{
		BatchInterval:       time.Duration(cfg.BatchIntervalMS) * time.Millisecond,
		RequestTimeout:      time.Duration(cfg.RequestTimeoutMS) * time.Millisecond,
		MaxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		MaxResponseBytes:    cfg.MaxResponseBytes,
		WorkerConcurrency:   cfg.WorkerConcurrency,
		MaxBatchRequests:    cfg.MaxBatchRequests,
		MaxBatchBytes:       cfg.MaxBatchBytes,
		MaxQueueRequests:    cfg.MaxQueueRequests,
		CacheTTL:            time.Duration(cfg.CacheTTLMS) * time.Millisecond,
		CacheMaxEntries:     cfg.CacheMaxEntries,
		BackpressureCooldown: time.Duration(cfg.BackpressureCooldownMS) * time.Millisecond,
	})
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go routeAgent.Run(ctx)

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
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
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
	Timestamp      string      `json:"timestamp"`
	RouteStatePath string      `json:"route_state_path"`
	Route          route.State `json:"route"`
	Agent          statusAgent `json:"agent"`
}

type statusAgent struct {
	URL       string          `json:"url"`
	Reachable bool            `json:"reachable"`
	Error     string          `json:"error,omitempty"`
	Runtime   *agent.Snapshot `json:"runtime,omitempty"`
}

type agentStatusResponse struct {
	OK      bool            `json:"ok"`
	Route   *route.State    `json:"route"`
	Runtime agent.Snapshot  `json:"runtime"`
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
	}

	statusResp, err := fetchAgentStatus(resolvedAgentURL)
	if err != nil {
		output.Agent.Reachable = false
		output.Agent.Error = err.Error()
	} else {
		output.Agent.Reachable = true
		output.Agent.Runtime = &statusResp.Runtime
		if statusResp.Route != nil {
			output.Route = *statusResp.Route
		}
	}

	formatted, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("format status response: %w", err)
	}
	fmt.Println(string(formatted))
	return nil
}

func fetchAgentStatus(agentURL string) (agentStatusResponse, error) {
	endpoint := strings.TrimRight(agentURL, "/") + "/v1/status"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return agentStatusResponse{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return agentStatusResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return agentStatusResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return agentStatusResponse{}, fmt.Errorf("agent returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded agentStatusResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return agentStatusResponse{}, err
	}
	return decoded, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
