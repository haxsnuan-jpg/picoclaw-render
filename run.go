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

const uiPort = "18800"

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

	// Run the launcher (browser UI on uiPort + manages the gateway) as a
	// supervised child; restart on exit so the service stays up.
	go func() {
		for {
			cmd := exec.Command("picoclaw-launcher", "-console", "-public", "-no-browser")
			env := append(os.Environ(),
				"PICOCLAW_GATEWAY_HOST=0.0.0.0",
				"PICOCLAW_HOME="+home,
				"PICOCLAW_CONFIG="+cfgPath,
			)
			// PicoClaw gateway reads OPENAI_API_KEY for the model entry
			// (api_key is not a valid model_list field). Mirror
			// PICOCLAW_API_KEY into OPENAI_API_KEY so users only set one.
			if v := os.Getenv("PICOCLAW_API_KEY"); v != "" && os.Getenv("OPENAI_API_KEY") == "" {
				env = append(env, "OPENAI_API_KEY="+v)
			}
			cmd.Env = env
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Println("[run] starting picoclaw-launcher")
			if err := cmd.Start(); err != nil {
				log.Println("[run] launcher start error:", err)
				time.Sleep(5 * time.Second)
				continue
			}
			if err := cmd.Wait(); err != nil {
				log.Println("[run] launcher exited:", err)
			}
			time.Sleep(3 * time.Second)
		}
	}()

	target, _ := url.Parse("http://127.0.0.1:" + uiPort)
	proxy := httputil.NewSingleHostReverseProxy(target)
	origDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		origDirector(r)
		// The launcher only serves the dashboard when the upstream Host is
		// "localhost"; rewrite it so the launcher accepts the request while
		// the browser still sees the real public host.
		r.Host = "localhost:" + uiPort
		if r.Header.Get("X-Forwarded-Proto") == "" {
			r.Header.Set("X-Forwarded-Proto", "https")
		}
	}

	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/readyz", health)
	// Everything else (including "/") is proxied to the launcher UI.
	mux.HandleFunc("/", proxy.ServeHTTP)

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
	entry := map[string]any{
		"model_name": modelName,
		"provider":   "",
		"model":      model,
	}
	if apiBase := os.Getenv("PICOCLAW_API_BASE"); apiBase != "" {
		entry["api_base"] = apiBase
	}
	// api_key is NOT written to config - PicoClaw's config schema rejects
	// model_list[].api_key/api_keys. The key is supplied at runtime via
	// the OPENAI_API_KEY env var (see launcher Env below).

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
