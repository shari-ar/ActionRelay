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
)

const defaultConfigPath = ".actionrelay/config.json"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
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
	fmt.Println("ActionRelay Phase 1")
	fmt.Println("Usage:")
	fmt.Println("  actionrelay init --repo owner/repo --workflow actionrelay.yml")
	fmt.Println("  actionrelay serve [--config .actionrelay/config.json] [--listen 127.0.0.1:8787]")
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
	fs.StringVar(&agentURL, "agent", "http://127.0.0.1:8787", "ActionRelay agent base URL")
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
	token, err := cfg.Token()
	if err != nil {
		return err
	}

	dispatcher, err := githubapi.NewDispatcher(cfg, token)
	if err != nil {
		return err
	}

	routeAgent, err := agent.New(dispatcher, agent.Settings{
		BatchInterval:     time.Duration(cfg.BatchIntervalMS) * time.Millisecond,
		RequestTimeout:    time.Duration(cfg.RequestTimeoutMS) * time.Millisecond,
		MaxResponseBytes:  cfg.MaxResponseBytes,
		WorkerConcurrency: cfg.WorkerConcurrency,
		MaxBatchRequests:  cfg.MaxBatchRequests,
		MaxBatchBytes:     cfg.MaxBatchBytes,
		MaxQueueRequests:  cfg.MaxQueueRequests,
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

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
