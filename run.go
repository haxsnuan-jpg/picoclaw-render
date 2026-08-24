package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const gwPort = "18790"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "10000"
	}
	home := os.Getenv("PICOCLAW_HOME")
	if home == "" {
		home = "/opt/data"
	}
	cfgPath := os.Getenv("PICOCLAW_CONFIG")
	if cfgPath == "" {
		cfgPath = filepath.Join(home, "config.json")
	}

	ensureConfig(cfgPath, home)

	// Run the gateway as a supervised child; restart on exit so the
	// service stays up (and webhooks keep flowing) even if it crashes.
	go func() {
		for {
			cmd := exec.Command("picoclaw", "gateway", "--allow-empty")
			cmd.Env = append(os.Environ(),
				"PICOCLAW_GATEWAY_HOST=0.0.0.0",
				"PICOCLAW_GATEWAY_PORT="+gwPort,
				"PICOCLAW_HOME="+home,
				"PICOCLAW_CONFIG="+cfgPath,
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Println("[run] starting picoclaw gateway")
			if err := cmd.Start(); err != nil {
				log.Println("[run] gateway start error:", err)
				time.Sleep(5 * time.Second)
				continue
			}
			if err := cmd.Wait(); err != nil {
				log.Println("[run] gateway exited:", err)
			}
			time.Sleep(3 * time.Second)
		}
	}()

	target, _ := url.Parse("http://127.0.0.1:" + gwPort)
	proxy := httputil.NewSingleHostReverseProxy(target)

	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/readyz", health)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			health(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	log.Printf("[run] listening on :%s (proxy -> %s)\n", port, target)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

// ensureConfig writes a minimal v2 config.json from Render env vars the
// first time the service boots (or whenever the config file is missing).
func ensureConfig(path, home string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	model := os.Getenv("PICOCLAW_MODEL")
	if model == "" {
		model = "openai/gpt-5.4"
	}
	modelName := os.Getenv("PICOCLAW_MODEL_NAME")
	if modelName == "" {
		modelName = "default"
	}
	apiKey := os.Getenv("PICOCLAW_API_KEY")

	entry := map[string]any{
		"model_name": modelName,
		"model":      model,
	}
	if apiKey != "" {
		entry["api_keys"] = []string{apiKey}
	}

	ch := map[string]any{}
	if t := os.Getenv("PICOCLAW_TELEGRAM_TOKEN"); t != "" {
		ch["telegram"] = map[string]any{"enabled": true, "token": t}
	}
	if t := os.Getenv("PICOCLAW_DISCORD_TOKEN"); t != "" {
		ch["discord"] = map[string]any{"enabled": true, "token": t}
	}
	if bt := os.Getenv("PICOCLAW_SLACK_BOT_TOKEN"); bt != "" {
		slack := map[string]any{"enabled": true, "bot_token": bt}
		if at := os.Getenv("PICOCLAW_SLACK_APP_TOKEN"); at != "" {
			slack["app_token"] = at
		}
		ch["slack"] = slack
	}

	cfg := map[string]any{
		"version": 2,
		"agents": map[string]any{
			"defaults": map[string]any{
				"model_name": modelName,
				"workspace":  filepath.Join(home, "workspace"),
			},
		},
		"model_list": []any{entry},
		"gateway": map[string]any{
			"host": "0.0.0.0",
			"port": 18790,
		},
		// Write both keys: docs use "channels", some builds read
		// "channel_list"; unknown keys are ignored by the loader.
		"channels":     ch,
		"channel_list": ch,
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Println("[run] marshal config error:", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Println("[run] mkdir for config failed:", err)
		return
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Println("[run] write config failed:", err)
		return
	}
	log.Printf("[run] generated config at %s\n", path)
}
