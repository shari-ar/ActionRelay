package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"actionrelay/client/internal/agent"
	"actionrelay/client/internal/protocol"
)

type fakeSubmitter struct {
	submitFunc func(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error)
}

func (f *fakeSubmitter) Submit(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error) {
	return f.submitFunc(ctx, request)
}

func (f *fakeSubmitter) Snapshot() agent.Snapshot {
	return agent.Snapshot{}
}

func TestNormalizeProxyURL(t *testing.T) {
	url, err := normalizeProxyURL("HTTPS://Example.COM:443/path?q=1#frag")
	if err != nil {
		t.Fatalf("normalizeProxyURL returned error: %v", err)
	}
	if strings.Contains(url, "#") {
		t.Fatalf("expected fragment to be stripped, got %q", url)
	}
	if !strings.HasPrefix(url, "https://example.com:443/") {
		t.Fatalf("expected normalized host and scheme, got %q", url)
	}
}

func TestNormalizeProxyURLRejectsUnsupportedScheme(t *testing.T) {
	_, err := normalizeProxyURL("ftp://example.com/file")
	if err == nil {
		t.Fatal("expected unsupported scheme to fail")
	}
}

func TestNormalizeProxyHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("Host", "example.com")
	headers.Set("Connection", "keep-alive")
	headers.Set("Accept", "application/json")
	headers.Set("X-Custom", "abc")

	normalized, err := normalizeProxyHeaders(headers)
	if err != nil {
		t.Fatalf("normalizeProxyHeaders returned error: %v", err)
	}
	if normalized["accept"] != "application/json" {
		t.Fatalf("expected accept header to remain, got %#v", normalized)
	}
	if normalized["x-custom"] != "abc" {
		t.Fatalf("expected x-custom header to remain, got %#v", normalized)
	}
	if _, exists := normalized["host"]; exists {
		t.Fatalf("expected host header to be stripped, got %#v", normalized)
	}
	if _, exists := normalized["connection"]; exists {
		t.Fatalf("expected connection header to be stripped, got %#v", normalized)
	}
}

func TestNormalizeProxyHeadersRejectsUpgrade(t *testing.T) {
	headers := http.Header{}
	headers.Set("Connection", "Upgrade")
	headers.Set("Upgrade", "websocket")
	_, err := normalizeProxyHeaders(headers)
	if err == nil {
		t.Fatal("expected upgrade headers to fail")
	}
}

func TestProxyHTTPSubmissionAndResponseMapping(t *testing.T) {
	fake := &fakeSubmitter{
		submitFunc: func(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error) {
			if request.Method != http.MethodGet {
				t.Fatalf("expected GET method, got %s", request.Method)
			}
			if request.URL != "http://example.com/test?a=1" {
				t.Fatalf("unexpected url: %s", request.URL)
			}
			return protocol.RequestResult{
				RequestID: "req-1",
				OK:        true,
				Response: &protocol.HTTPResponse{
					Status: 201,
					Headers: map[string]string{
						"content-type": "text/plain",
					},
					Body: protocol.ResponseBody{
						Encoding: "base64",
						Data:     base64.StdEncoding.EncodeToString([]byte("ok")),
					},
				},
			}, nil
		},
	}

	server, err := NewServer(Config{ListenAddr: "127.0.0.1:8788"}, fake)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://proxy.local/test?a=1", nil)
	req.URL.Scheme = "http"
	req.URL.Host = "example.com"
	req.URL.Path = "/test"
	req.URL.RawQuery = "a=1"
	req.Host = "example.com"
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != 201 {
		t.Fatalf("expected status 201, got %d", resp.Code)
	}
	if body := strings.TrimSpace(resp.Body.String()); body != "ok" {
		t.Fatalf("expected body 'ok', got %q", body)
	}
}

func TestProxyHTTPValidationError(t *testing.T) {
	fake := &fakeSubmitter{
		submitFunc: func(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error) {
			t.Fatalf("submit should not be called when validation fails")
			return protocol.RequestResult{}, nil
		},
	}
	server, err := NewServer(Config{ListenAddr: "127.0.0.1:8788"}, fake)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	req := httptest.NewRequest("TRACE", "http://example.com/test", nil)
	req.Host = "example.com"
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
	decoded := map[string]any{}
	if err := json.NewDecoder(bytes.NewReader(resp.Body.Bytes())).Decode(&decoded); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if decoded["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", decoded)
	}
}

func TestProxyResultErrorMapping(t *testing.T) {
	fake := &fakeSubmitter{
		submitFunc: func(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error) {
			return protocol.RequestResult{
				RequestID: "req-2",
				OK:        false,
				Error: &protocol.ResultError{
					Code:    "METHOD_REJECTED",
					Message: "not allowed",
				},
			}, nil
		},
	}
	server, err := NewServer(Config{ListenAddr: "127.0.0.1:8788"}, fake)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://proxy.local/path", io.NopCloser(strings.NewReader("")))
	req.URL.Scheme = "http"
	req.URL.Host = "example.com"
	req.URL.Path = "/path"
	req.Host = "example.com"
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", resp.Code)
	}
}

func TestProxyConnectReturnsNotImplemented(t *testing.T) {
	fake := &fakeSubmitter{
		submitFunc: func(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error) {
			t.Fatalf("submit should not be called for CONNECT")
			return protocol.RequestResult{}, nil
		},
	}
	server, err := NewServer(Config{ListenAddr: "127.0.0.1:8788"}, fake)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodConnect, "http://proxy.local", nil)
	req.Host = "example.com:443"
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("expected status 501, got %d", resp.Code)
	}
}

func TestProxyOversizedBodyRejected(t *testing.T) {
	fake := &fakeSubmitter{
		submitFunc: func(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error) {
			t.Fatalf("submit should not be called for oversized body")
			return protocol.RequestResult{}, nil
		},
	}
	server, err := NewServer(Config{ListenAddr: "127.0.0.1:8788"}, fake)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	oversized := strings.Repeat("a", maxProxyRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/upload", strings.NewReader(oversized))
	req.URL.Scheme = "http"
	req.URL.Host = "example.com"
	req.URL.Path = "/upload"
	req.Host = "example.com"
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d", resp.Code)
	}
}

func TestWriteResultAsHTTPStripsHopByHopHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeResultAsHTTP(rec, protocol.RequestResult{
		RequestID: "req-headers",
		OK:        true,
		Response: &protocol.HTTPResponse{
			Status: 200,
			Headers: map[string]string{
				"content-type": "text/plain",
				"connection":   "keep-alive",
			},
			Body: protocol.ResponseBody{Data: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("writeResultAsHTTP returned error: %v", err)
	}
	if got := rec.Header().Get("Connection"); got != "" {
		t.Fatalf("expected Connection header to be stripped, got %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("expected Content-Type header, got %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "2" {
		t.Fatalf("expected Content-Length=2, got %q", got)
	}
}

func TestWriteResultAsHTTPRejectsInvalidStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeResultAsHTTP(rec, protocol.RequestResult{
		RequestID: "req-status",
		OK:        true,
		Response: &protocol.HTTPResponse{
			Status: 999,
			Body:   protocol.ResponseBody{Data: "ok"},
		},
	})
	if err == nil {
		t.Fatal("expected invalid status error")
	}
}
