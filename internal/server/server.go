package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"ollama-api/internal/config"
)

type Server struct {
	cfg      config.Config
	upstream *url.URL
	proxy    *httputil.ReverseProxy
	client   *http.Client
	httpSrv  *http.Server
}

func New(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	upstream, err := url.Parse(cfg.UpstreamBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = upstream.Host
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "ollama-openai-proxy")
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": map[string]interface{}{
				"message": fmt.Sprintf("upstream request failed: %v", err),
				"type":    "proxy_error",
			},
		})
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	proxy.Transport = transport

	s := &Server{
		cfg:      cfg,
		upstream: upstream,
		proxy:    proxy,
		client: &http.Client{
			Timeout:   time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
			Transport: transport,
		},
	}

	s.httpSrv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

func (s *Server) ListenAndServe() error {
	return s.httpSrv.ListenAndServe()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/models/", s.handleModel)
	mux.HandleFunc("/v1/", s.handleProxy)

	var handler http.Handler = mux
	handler = s.withAuth(handler)
	handler = s.withLogging(handler)
	handler = s.withCORS(handler)
	return handler
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":          "ollama-openai-proxy",
		"status":        "ok",
		"upstream":      s.cfg.UpstreamBaseURL,
		"listen":        s.cfg.Listen,
		"models_path":   "/v1/models",
		"chat_path":     "/v1/chat/completions",
		"healthz_path":  "/healthz",
		"auth_required": s.cfg.APIKey != "",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.upstreamURL("/api/tags", ""), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":   "error",
			"upstream": s.cfg.UpstreamBaseURL,
			"message":  err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":   "error",
			"upstream": s.cfg.UpstreamBaseURL,
			"message":  string(body),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"upstream":   s.cfg.UpstreamBaseURL,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.handleProxy(w, r)
		return
	}

	resp, err := s.fetchUpstream(r.Context(), http.MethodGet, "/v1/models", r.URL.RawQuery, nil, r.Header)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "proxy_error",
			},
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "proxy_error",
			},
		})
		return
	}
	if resp.StatusCode >= 400 {
		copyResponse(w, resp.StatusCode, resp.Header, body)
		return
	}

	rewritten, err := injectAliasModels(body, s.cfg.ModelAliases)
	if err != nil {
		copyResponse(w, resp.StatusCode, resp.Header, body)
		return
	}

	copyResponse(w, resp.StatusCode, resp.Header, rewritten)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.handleProxy(w, r)
		return
	}

	modelID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	modelID, err := url.PathUnescape(modelID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]interface{}{
				"message": "invalid model id",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	requestedID := modelID
	if actual, ok := s.cfg.ModelAliases[modelID]; ok {
		modelID = actual
	}

	resp, err := s.fetchUpstream(r.Context(), http.MethodGet, "/v1/models/"+url.PathEscape(modelID), r.URL.RawQuery, nil, r.Header)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "proxy_error",
			},
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "proxy_error",
			},
		})
		return
	}
	if resp.StatusCode >= 400 {
		copyResponse(w, resp.StatusCode, resp.Header, body)
		return
	}

	rewritten, err := rewriteModelID(body, requestedID)
	if err != nil {
		copyResponse(w, resp.StatusCode, resp.Header, body)
		return
	}

	copyResponse(w, resp.StatusCode, resp.Header, rewritten)
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if err := rewriteRequestModel(r, s.cfg.ModelAliases); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *Server) fetchUpstream(ctx context.Context, method, path, rawQuery string, body []byte, headers http.Header) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.upstreamURL(path, rawQuery), reader)
	if err != nil {
		return nil, err
	}

	if headers != nil {
		req.Header = headers.Clone()
	}
	req.Header.Del("Authorization")
	req.Header.Del("Content-Length")
	if body != nil {
		req.ContentLength = int64(len(body))
	}

	return s.client.Do(req)
}

func (s *Server) upstreamURL(path, rawQuery string) string {
	u := *s.upstream
	u.Path = singleJoiningSlash(s.upstream.Path, path)
	u.RawQuery = rawQuery
	return u.String()
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" || r.Method == http.MethodOptions || r.URL.Path == "/" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{
					"message": "missing bearer token",
					"type":    "authentication_error",
				},
			})
			return
		}

		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token == "" || token != s.cfg.APIKey {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{
					"message": "invalid api key",
					"type":    "authentication_error",
				},
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.cfg.CORS.AllowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", s.cfg.CORS.AllowMethods)
		w.Header().Set("Access-Control-Allow-Headers", s.cfg.CORS.AllowHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.LogRequests {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

func rewriteRequestModel(req *http.Request, aliases map[string]string) error {
	if len(aliases) == 0 || req.Body == nil {
		return nil
	}
	if !strings.HasPrefix(req.URL.Path, "/v1/") {
		return nil
	}
	if req.Method != http.MethodPost {
		return nil
	}
	contentType := strings.ToLower(req.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "application/json") {
		return nil
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	restoreRequestBody(req, body)

	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}

	modelName, ok := payload["model"].(string)
	if !ok {
		return nil
	}
	actualModel, ok := aliases[modelName]
	if !ok {
		return nil
	}

	payload["model"] = actualModel
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	restoreRequestBody(req, rewritten)
	return nil
}

func injectAliasModels(body []byte, aliases map[string]string) ([]byte, error) {
	if len(aliases) == 0 {
		return body, nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	items, ok := payload["data"].([]interface{})
	if !ok {
		return body, nil
	}

	existing := make(map[string]map[string]interface{}, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		if id != "" {
			existing[id] = entry
		}
	}

	for alias, actual := range aliases {
		if _, ok := existing[alias]; ok {
			continue
		}

		if source, ok := existing[actual]; ok {
			clone := cloneMap(source)
			clone["id"] = alias
			clone["root"] = actual
			items = append(items, clone)
			continue
		}

		items = append(items, map[string]interface{}{
			"id":       alias,
			"object":   "model",
			"owned_by": "ollama-proxy",
			"root":     actual,
		})
	}

	payload["data"] = items
	return json.Marshal(payload)
}

func rewriteModelID(body []byte, modelID string) ([]byte, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	payload["id"] = modelID
	return json.Marshal(payload)
}

func copyResponse(w http.ResponseWriter, status int, headers http.Header, body []byte) {
	copyHeaders(w.Header(), headers)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func restoreRequestBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
