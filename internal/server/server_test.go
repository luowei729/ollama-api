package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ollama-api/internal/config"
)

func TestProxyRewritesModelAlias(t *testing.T) {
	var receivedModel string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}

		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		receivedModel, _ = payload["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.UpstreamBaseURL = upstream.URL
	cfg.APIKey = "secret"
	cfg.ModelAliases = map[string]string{
		"gpt-3.5-turbo": "qwen2.5:7b",
	}
	cfg.LogRequests = false

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	proxy := httptest.NewServer(srv.routes())
	defer proxy.Close()

	reqBody := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hello"}]}`
	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	if receivedModel != "qwen2.5:7b" {
		t.Fatalf("unexpected upstream model: %s", receivedModel)
	}
}

func TestModelsIncludeAlias(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"qwen2.5:7b","object":"model","owned_by":"library"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.UpstreamBaseURL = upstream.URL
	cfg.APIKey = "secret"
	cfg.ModelAliases = map[string]string{
		"gpt-3.5-turbo": "qwen2.5:7b",
	}
	cfg.LogRequests = false

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	proxy := httptest.NewServer(srv.routes())
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	foundActual := false
	foundAlias := false
	for _, item := range payload.Data {
		if item.ID == "qwen2.5:7b" {
			foundActual = true
		}
		if item.ID == "gpt-3.5-turbo" {
			foundAlias = true
		}
	}

	if !foundActual || !foundAlias {
		t.Fatalf("models response missing entries, actual=%v alias=%v body=%s", foundActual, foundAlias, string(body))
	}
}

func TestAuthMiddleware(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.UpstreamBaseURL = upstream.URL
	cfg.APIKey = "secret"
	cfg.LogRequests = false

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	proxy := httptest.NewServer(srv.routes())
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authorized request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestNewFailsWithoutAPIKey(t *testing.T) {
	cfg := config.Default()
	cfg.UpstreamBaseURL = "http://127.0.0.1:11434"
	cfg.APIKey = ""

	if _, err := New(cfg); err == nil {
		t.Fatalf("expected missing api_key validation error")
	}
}
