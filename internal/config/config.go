package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type CORSConfig struct {
	AllowOrigin  string `json:"allow_origin"`
	AllowMethods string `json:"allow_methods"`
	AllowHeaders string `json:"allow_headers"`
}

type Config struct {
	Listen                string            `json:"listen"`
	UpstreamBaseURL       string            `json:"upstream_base_url"`
	APIKey                string            `json:"api_key"`
	RequestTimeoutSeconds int               `json:"request_timeout_seconds"`
	LogRequests           bool              `json:"log_requests"`
	ModelAliases          map[string]string `json:"model_aliases"`
	CORS                  CORSConfig        `json:"cors"`
}

func Default() Config {
	return Config{
		Listen:                "0.0.0.0:8080",
		UpstreamBaseURL:       "http://127.0.0.1:11434",
		RequestTimeoutSeconds: 120,
		LogRequests:           true,
		ModelAliases:          map[string]string{},
		CORS: CORSConfig{
			AllowOrigin:  "*",
			AllowMethods: "GET,POST,OPTIONS",
			AllowHeaders: "Authorization,Content-Type",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return cfg, err
		}
		defer file.Close()

		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return cfg, err
		}
	}

	applyEnvOverrides(&cfg)
	return cfg, cfg.Validate()
}

func (c *Config) Validate() error {
	c.normalize()

	parsed, err := url.Parse(c.UpstreamBaseURL)
	if err != nil {
		return fmt.Errorf("parse upstream_base_url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("upstream_base_url must include scheme and host")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("api_key is required")
	}
	return nil
}

func (c *Config) normalize() {
	def := Default()

	c.Listen = strings.TrimSpace(c.Listen)
	if c.Listen == "" {
		c.Listen = def.Listen
	}

	c.UpstreamBaseURL = strings.TrimSpace(c.UpstreamBaseURL)
	if c.UpstreamBaseURL == "" {
		c.UpstreamBaseURL = def.UpstreamBaseURL
	}
	c.UpstreamBaseURL = strings.TrimRight(c.UpstreamBaseURL, "/")
	if strings.HasSuffix(c.UpstreamBaseURL, "/v1") {
		c.UpstreamBaseURL = strings.TrimSuffix(c.UpstreamBaseURL, "/v1")
	}

	if c.RequestTimeoutSeconds <= 0 {
		c.RequestTimeoutSeconds = def.RequestTimeoutSeconds
	}
	if c.ModelAliases == nil {
		c.ModelAliases = map[string]string{}
	}
	if c.CORS.AllowOrigin == "" {
		c.CORS.AllowOrigin = def.CORS.AllowOrigin
	}
	if c.CORS.AllowMethods == "" {
		c.CORS.AllowMethods = def.CORS.AllowMethods
	}
	if c.CORS.AllowHeaders == "" {
		c.CORS.AllowHeaders = def.CORS.AllowHeaders
	}
}

func applyEnvOverrides(cfg *Config) {
	if value := os.Getenv("OLLAMA_PROXY_LISTEN"); value != "" {
		cfg.Listen = value
	}
	if value := os.Getenv("OLLAMA_PROXY_UPSTREAM"); value != "" {
		cfg.UpstreamBaseURL = value
	}
	if value := os.Getenv("OLLAMA_PROXY_API_KEY"); value != "" {
		cfg.APIKey = value
	}
	if value := os.Getenv("OLLAMA_PROXY_TIMEOUT"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.RequestTimeoutSeconds = parsed
		}
	}
}
