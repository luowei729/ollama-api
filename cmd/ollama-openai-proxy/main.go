package main

import (
	"flag"
	"fmt"
	"log"

	"ollama-api/internal/config"
	"ollama-api/internal/server"
)

var version = "dev"

func main() {
	var (
		configPath = flag.String("config", "", "Path to the JSON config file")
		listen     = flag.String("listen", "", "Listen address, e.g. 0.0.0.0:8080")
		upstream   = flag.String("upstream", "", "Ollama upstream base URL, e.g. http://127.0.0.1:11434")
		apiKey     = flag.String("api-key", "", "Optional OpenAI-style Bearer token")
		showVer    = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *listen != "" {
		cfg.Listen = *listen
	}
	if *upstream != "" {
		cfg.UpstreamBaseURL = *upstream
	}
	if *apiKey != "" {
		cfg.APIKey = *apiKey
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	log.Printf("ollama-openai-proxy %s listening on %s -> %s", version, cfg.Listen, cfg.UpstreamBaseURL)
	log.Printf("bearer auth is enabled and required for /v1/*")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
