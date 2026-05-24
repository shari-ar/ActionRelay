package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"actionrelay/client/internal/agent"
	"actionrelay/client/internal/protocol"
)

const (
	maxProxyRequestBodyBytes = 1 << 20
	defaultHTTPSPort         = 443
)

var allowedProxyMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodOptions: {},
}

var rejectedProxyHeaders = map[string]struct{}{
	"proxy-authenticate":  {},
	"proxy-authorization": {},
}

var strippedProxyHeaders = map[string]struct{}{
	"host":              {},
	"connection":        {},
	"keep-alive":        {},
	"proxy-connection":  {},
	"transfer-encoding": {},
	"te":                {},
	"trailer":           {},
	"upgrade":           {},
}

type Config struct {
	ListenAddr string
}

type Server struct {
	config Config
	agent  requestSubmitter
}

type requestSubmitter interface {
	Submit(ctx context.Context, request agent.SubmitRequest) (protocol.RequestResult, error)
	Snapshot() agent.Snapshot
}

func NewServer(cfg Config, routeAgent requestSubmitter) (*Server, error) {
	if routeAgent == nil {
		return nil, fmt.Errorf("proxy agent is required")
	}
	return &Server{config: cfg, agent: routeAgent}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleProxyRequest)
	return mux
}

func (s *Server) handleProxyRequest(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodConnect {
		s.handleConnect(writer, request)
		return
	}

	targetURL, err := proxyTargetURL(request)
	if err != nil {
		s.writeProxyError(writer, http.StatusBadRequest, err.Error())
		return
	}

	body, err := io.ReadAll(io.LimitReader(request.Body, maxProxyRequestBodyBytes))
	if err != nil {
		s.writeProxyError(writer, http.StatusBadRequest, "failed to read proxy request body")
		return
	}

	submission, err := normalizeSubmission(request.Method, targetURL, request.Header, body)
	if err != nil {
		s.writeProxyError(writer, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("actionrelay: proxy request accepted method=%s url=%s", submission.Method, submission.URL)
	s.submitAndWriteHTTP(request.Context(), writer, submission)
}

func (s *Server) handleConnect(writer http.ResponseWriter, request *http.Request) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		s.writeProxyError(writer, http.StatusInternalServerError, "proxy does not support connection hijacking")
		return
	}

	hostPort, err := validateConnectAuthority(request.Host, request.RequestURI)
	if err != nil {
		s.writeProxyError(writer, http.StatusBadRequest, err.Error())
		return
	}

	conn, readWriter, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	if _, err := readWriter.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := readWriter.Flush(); err != nil {
		return
	}

	tlsConn := tls.Server(conn, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return nil, fmt.Errorf("dynamic MITM certificate issuance is not implemented")
	}})
	defer tlsConn.Close()
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("actionrelay: CONNECT handshake failed host=%s err=%v", hostPort, err)
		return
	}

	reader := bufio.NewReader(tlsConn)
	for {
		proxiedRequest, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				log.Printf("actionrelay: CONNECT read failed host=%s err=%v", hostPort, err)
			}
			return
		}

		targetURL, err := httpsTargetURL(proxiedRequest, hostPort)
		if err != nil {
			_ = proxiedRequest.Body.Close()
			_ = writeTunnelHTTPError(tlsConn, http.StatusBadRequest, err.Error())
			return
		}

		body, err := io.ReadAll(io.LimitReader(proxiedRequest.Body, maxProxyRequestBodyBytes))
		_ = proxiedRequest.Body.Close()
		if err != nil {
			_ = writeTunnelHTTPError(tlsConn, http.StatusBadRequest, "failed to read tunneled request body")
			return
		}

		submission, err := normalizeSubmission(proxiedRequest.Method, targetURL, proxiedRequest.Header, body)
		if err != nil {
			_ = writeTunnelHTTPError(tlsConn, http.StatusBadRequest, err.Error())
			return
		}

		log.Printf("actionrelay: CONNECT request accepted method=%s url=%s", submission.Method, submission.URL)
		if err := s.submitAndWriteTunnelResponse(request.Context(), tlsConn, submission); err != nil {
			log.Printf("actionrelay: CONNECT response failed host=%s err=%v", hostPort, err)
			return
		}

		if proxiedRequest.Close {
			return
		}
	}
}

func (s *Server) submitAndWriteHTTP(ctx context.Context, writer http.ResponseWriter, submission agent.SubmitRequest) {
	result, err := s.agent.Submit(ctx, submission)
	if err != nil {
		s.writeProxyError(writer, http.StatusBadGateway, fmt.Sprintf("proxy submit failed: %v", err))
		return
	}
	if err := writeResultAsHTTP(writer, result); err != nil {
		s.writeProxyError(writer, http.StatusBadGateway, err.Error())
	}
}

func (s *Server) submitAndWriteTunnelResponse(ctx context.Context, writer io.Writer, submission agent.SubmitRequest) error {
	result, err := s.agent.Submit(ctx, submission)
	if err != nil {
		return writeTunnelHTTPError(writer, http.StatusBadGateway, fmt.Sprintf("proxy submit failed: %v", err))
	}
	return writeResultToTunnel(writer, result)
}

func normalizeSubmission(method, rawURL string, headers http.Header, body []byte) (agent.SubmitRequest, error) {
	normalizedMethod := strings.ToUpper(strings.TrimSpace(method))
	if _, ok := allowedProxyMethods[normalizedMethod]; !ok {
		return agent.SubmitRequest{}, fmt.Errorf("method %q is not supported", normalizedMethod)
	}

	normalizedURL, err := normalizeProxyURL(rawURL)
	if err != nil {
		return agent.SubmitRequest{}, err
	}

	normalizedHeaders, err := normalizeProxyHeaders(headers)
	if err != nil {
		return agent.SubmitRequest{}, err
	}

	return agent.SubmitRequest{
		Method: normalizedMethod,
		URL:    normalizedURL,
		Header: normalizedHeaders,
		Body:   body,
	}, nil
}

func proxyTargetURL(request *http.Request) (string, error) {
	if request.URL == nil {
		return "", fmt.Errorf("missing request URL")
	}
	if request.URL.IsAbs() {
		return request.URL.String(), nil
	}
	if request.Host == "" {
		return "", fmt.Errorf("proxy request must use absolute-form URL or Host header")
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + request.Host + request.URL.RequestURI(), nil
}

func httpsTargetURL(request *http.Request, hostPort string) (string, error) {
	if request.URL == nil {
		return "", fmt.Errorf("missing tunneled request URL")
	}
	host := strings.TrimSpace(request.Host)
	if host == "" {
		host = strings.TrimSpace(hostPort)
	}
	if host == "" {
		return "", fmt.Errorf("missing tunneled request host")
	}
	uri := request.URL.RequestURI()
	if uri == "" {
		uri = "/"
	}
	return normalizeProxyURL("https://" + host + uri)
}

func normalizeProxyURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed == nil || !parsed.IsAbs() {
		return "", fmt.Errorf("url must be absolute")
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("url scheme %q is not supported", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("url credentials are not allowed")
	}

	hostname := strings.TrimSpace(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("url host is required")
	}
	port := strings.TrimSpace(parsed.Port())
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber <= 0 || portNumber > 65535 {
			return "", fmt.Errorf("url port is invalid")
		}
		port = strconv.Itoa(portNumber)
	}

	parsed.Scheme = scheme
	parsed.User = nil
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if port == "" {
		parsed.Host = strings.ToLower(hostname)
	} else {
		parsed.Host = net.JoinHostPort(strings.ToLower(hostname), port)
	}
	return parsed.String(), nil
}

func validateConnectAuthority(host, requestURI string) (string, error) {
	target := strings.TrimSpace(host)
	if target == "" {
		target = strings.TrimSpace(requestURI)
	}
	if target == "" {
		return "", fmt.Errorf("CONNECT requires host:port")
	}

	hostname, port, err := net.SplitHostPort(target)
	if err != nil {
		if strings.Contains(err.Error(), "missing port in address") {
			target = net.JoinHostPort(target, strconv.Itoa(defaultHTTPSPort))
			hostname, port, err = net.SplitHostPort(target)
		}
		if err != nil {
			return "", fmt.Errorf("CONNECT target must be host:port")
		}
	}

	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return "", fmt.Errorf("CONNECT target host is required")
	}

	portNumber, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return "", fmt.Errorf("CONNECT target port is invalid")
	}

	return net.JoinHostPort(strings.ToLower(hostname), strconv.Itoa(portNumber)), nil
}

func normalizeProxyHeaders(headers http.Header) (map[string]string, error) {
	if tokenListContains(headers.Get("Connection"), "upgrade") || strings.TrimSpace(headers.Get("Upgrade")) != "" {
		return nil, fmt.Errorf("protocol upgrades are not supported")
	}

	normalized := make(map[string]string, len(headers))
	for key, values := range headers {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey == "" {
			continue
		}
		if _, rejected := rejectedProxyHeaders[normalizedKey]; rejected {
			return nil, fmt.Errorf("header %q is not allowed", normalizedKey)
		}
		if _, stripped := strippedProxyHeaders[normalizedKey]; stripped {
			continue
		}
		if len(values) == 0 {
			continue
		}
		merged := strings.TrimSpace(strings.Join(values, ", "))
		if merged == "" {
			continue
		}
		normalized[normalizedKey] = merged
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func tokenListContains(list, token string) bool {
	if strings.TrimSpace(list) == "" {
		return false
	}
	for _, part := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func copyHeaders(target http.Header, headers map[string]string) {
	for key, value := range headers {
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		target.Set(key, value)
	}
}

func decodeResponseBody(body protocol.ResponseBody) ([]byte, error) {
	if body.Encoding == "" {
		return []byte(body.Data), nil
	}
	if body.Encoding != "base64" {
		return nil, fmt.Errorf("unsupported body encoding %q", body.Encoding)
	}
	return base64.StdEncoding.DecodeString(body.Data)
}

func writeResultAsHTTP(writer http.ResponseWriter, result protocol.RequestResult) error {
	if !result.OK {
		message := "request failed"
		if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
			message = result.Error.Message
		}
		status := http.StatusBadGateway
		if result.Error != nil && (result.Error.Code == "UNSUPPORTED_METHOD" || result.Error.Code == "METHOD_REJECTED") {
			status = http.StatusMethodNotAllowed
		}
		writeProxyErrorResponse(writer, status, message)
		return nil
	}

	response := result.Response
	if response == nil {
		return fmt.Errorf("missing response payload")
	}

	payload, err := decodeResponseBody(response.Body)
	if err != nil {
		return fmt.Errorf("invalid response body: %w", err)
	}
	copyHeaders(writer.Header(), response.Headers)
	writer.WriteHeader(response.Status)
	_, _ = writer.Write(payload)
	return nil
}

func writeResultToTunnel(writer io.Writer, result protocol.RequestResult) error {
	if !result.OK {
		message := "request failed"
		if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
			message = result.Error.Message
		}
		status := http.StatusBadGateway
		if result.Error != nil && (result.Error.Code == "UNSUPPORTED_METHOD" || result.Error.Code == "METHOD_REJECTED") {
			status = http.StatusMethodNotAllowed
		}
		return writeTunnelHTTPError(writer, status, message)
	}

	response := result.Response
	if response == nil {
		return writeTunnelHTTPError(writer, http.StatusBadGateway, "missing response payload")
	}

	payload, err := decodeResponseBody(response.Body)
	if err != nil {
		return writeTunnelHTTPError(writer, http.StatusBadGateway, fmt.Sprintf("invalid response body: %v", err))
	}

	statusText := http.StatusText(response.Status)
	if statusText == "" {
		statusText = "Status"
	}
	if _, err := fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\n", response.Status, statusText); err != nil {
		return err
	}
	for key, value := range response.Headers {
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		if _, err := fmt.Fprintf(writer, "%s: %s\r\n", key, value); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}

func writeTunnelHTTPError(writer io.Writer, status int, message string) error {
	payload, _ := json.Marshal(map[string]any{
		"ok":        false,
		"error":     message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "Status"
	}
	if _, err := fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\n", status, statusText); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func (s *Server) writeProxyError(writer http.ResponseWriter, status int, message string) {
	writeProxyErrorResponse(writer, status, message)
}

func writeProxyErrorResponse(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"ok":        false,
		"error":     message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
