package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMonitoringDefaultsAndEnvironmentOverride(t *testing.T) {
	base := t.TempDir()
	configPath := filepath.Join(base, "config.yaml")
	content := "paths:\n  database: " + filepath.Join(base, "db.sqlite") + "\n  data_dir: " + filepath.Join(base, "data") + "\n  log_dir: " + filepath.Join(base, "logs") + "\n  backup_dir: " + filepath.Join(base, "backups") + "\n  apps_dir: " + filepath.Join(base, "apps") + "\n  simple_mode_roots:\n    - " + filepath.Join(base, "apps") + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VPSDECK_OLLAMA_BASE_URL", "http://10.0.0.25:11434/")
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Monitoring.RefreshSeconds != 5 || !cfg.Monitoring.Ports.Enabled || !cfg.Monitoring.Ollama.Enabled {
		t.Fatalf("monitoring defaults not preserved: %#v", cfg.Monitoring)
	}
	if cfg.Monitoring.Ollama.BaseURL != "http://10.0.0.25:11434" {
		t.Fatalf("unexpected Ollama URL: %q", cfg.Monitoring.Ollama.BaseURL)
	}
}

func TestRejectsUnsafeOllamaURL(t *testing.T) {
	cfg := Default()
	cfg.Monitoring.Ollama.BaseURL = "http://user:password@example.com:11434/api?secret=yes"
	if err := cfg.normalize(); err == nil {
		t.Fatal("unsafe Ollama URL was accepted")
	}
}

func TestReverseProxyDefaultsAndEnvironmentOverride(t *testing.T) {
	base := t.TempDir()
	configPath := filepath.Join(base, "config.yaml")
	content := "paths:\n  database: " + filepath.Join(base, "db.sqlite") + "\n  data_dir: " + filepath.Join(base, "data") + "\n  log_dir: " + filepath.Join(base, "logs") + "\n  backup_dir: " + filepath.Join(base, "backups") + "\n  apps_dir: " + filepath.Join(base, "apps") + "\n  simple_mode_roots:\n    - " + filepath.Join(base, "apps") + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VPSDECK_REVERSE_PROXY_PANEL_BIND_HOST", "127.0.0.1")
	t.Setenv("VPSDECK_REVERSE_PROXY_PANEL_BIND_PORT", "7999")
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReverseProxy.PanelBindHost != "127.0.0.1" || cfg.ReverseProxy.PanelBindPort != 7999 {
		t.Fatalf("reverse proxy environment overrides not applied: %#v", cfg.ReverseProxy)
	}
	if cfg.ReverseProxy.InternalPortStart != 31000 || cfg.ReverseProxy.InternalPortEnd != 31999 {
		t.Fatalf("unexpected reverse proxy port pool: %#v", cfg.ReverseProxy)
	}
	if cfg.ReverseProxy.NginxBridgeInclude != "/etc/nginx/sites-enabled/vpsdeck-managed.conf" {
		t.Fatalf("unexpected bridge path: %q", cfg.ReverseProxy.NginxBridgeInclude)
	}
}

func TestNormalizesPublicProductionBindToLocalhost(t *testing.T) {
	cfg := Default()
	cfg.App.Environment = "production"
	cfg.App.Host = "0.0.0.0"
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.App.Host != "127.0.0.1" {
		t.Fatalf("production panel bind was not normalized: %q", cfg.App.Host)
	}
}
