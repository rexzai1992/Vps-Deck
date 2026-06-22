package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App        AppConfig        `yaml:"app"`
	Security   SecurityConfig   `yaml:"security"`
	Paths      PathsConfig      `yaml:"paths"`
	Monitoring MonitoringConfig `yaml:"monitoring"`
	Docker     DockerConfig     `yaml:"docker"`
}

type AppConfig struct {
	Name        string `yaml:"name"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	BaseURL     string `yaml:"base_url"`
	Environment string `yaml:"environment"`
}

type SecurityConfig struct {
	CookieSecure            bool          `yaml:"cookie_secure"`
	SessionLifetime         time.Duration `yaml:"-"`
	SessionLifetimeHours    int           `yaml:"session_lifetime_hours"`
	LoginRateLimitPerMinute int           `yaml:"login_rate_limit_per_minute"`
}

type PathsConfig struct {
	Database        string   `yaml:"database"`
	DataDir         string   `yaml:"data_dir"`
	LogDir          string   `yaml:"log_dir"`
	BackupDir       string   `yaml:"backup_dir"`
	AppsDir         string   `yaml:"apps_dir"`
	SimpleModeRoots []string `yaml:"simple_mode_roots"`
}

type MonitoringConfig struct {
	RefreshSeconds int                 `yaml:"refresh_seconds"`
	Ports          PortMonitorConfig   `yaml:"ports"`
	Ollama         OllamaMonitorConfig `yaml:"ollama"`
}

type PortMonitorConfig struct {
	Enabled bool `yaml:"enabled"`
}

type OllamaMonitorConfig struct {
	Enabled        bool   `yaml:"enabled"`
	BaseURL        string `yaml:"base_url"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type DockerConfig struct {
	Enabled          bool   `yaml:"enabled"`
	DiscoveryEnabled bool   `yaml:"discovery_enabled"`
	Command          string `yaml:"command"`
	TimeoutSeconds   int    `yaml:"timeout_seconds"`
}

func Default() Config {
	return Config{
		App: AppConfig{
			Name:        "VPSDeck",
			Host:        "127.0.0.1",
			Port:        8080,
			BaseURL:     "http://127.0.0.1:8080",
			Environment: "development",
		},
		Security: SecurityConfig{
			CookieSecure:            false,
			SessionLifetimeHours:    12,
			LoginRateLimitPerMinute: 5,
		},
		Paths: PathsConfig{
			Database:        "./data/vpsdeck.db",
			DataDir:         "./data",
			LogDir:          "./data/logs",
			BackupDir:       "./data/backups",
			AppsDir:         "./managed-apps",
			SimpleModeRoots: []string{"./managed-apps"},
		},
		Monitoring: MonitoringConfig{
			RefreshSeconds: 5,
			Ports: PortMonitorConfig{
				Enabled: true,
			},
			Ollama: OllamaMonitorConfig{
				Enabled:        true,
				BaseURL:        "http://127.0.0.1:11434",
				TimeoutSeconds: 2,
			},
		},
		Docker: DockerConfig{
			Enabled:          true,
			DiscoveryEnabled: true,
			Command:          "docker",
			TimeoutSeconds:   10,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(content, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}

	applyEnvironment(&cfg)
	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Address() string {
	return c.App.Host + ":" + strconv.Itoa(c.App.Port)
}

func (c *Config) normalize() error {
	if strings.TrimSpace(c.App.Name) == "" {
		c.App.Name = "VPSDeck"
	}
	if strings.TrimSpace(c.App.Host) == "" {
		c.App.Host = "127.0.0.1"
	}
	if c.App.Port < 1 || c.App.Port > 65535 {
		return fmt.Errorf("app.port must be between 1 and 65535")
	}
	if c.Security.SessionLifetimeHours < 1 {
		c.Security.SessionLifetimeHours = 12
	}
	c.Security.SessionLifetime = time.Duration(c.Security.SessionLifetimeHours) * time.Hour
	if c.Security.LoginRateLimitPerMinute < 1 {
		c.Security.LoginRateLimitPerMinute = 5
	}
	if c.Monitoring.RefreshSeconds < 2 || c.Monitoring.RefreshSeconds > 60 {
		return errors.New("monitoring.refresh_seconds must be between 2 and 60")
	}
	if c.Monitoring.Ollama.TimeoutSeconds < 1 || c.Monitoring.Ollama.TimeoutSeconds > 30 {
		return errors.New("monitoring.ollama.timeout_seconds must be between 1 and 30")
	}
	if err := normalizeOllamaURL(&c.Monitoring.Ollama); err != nil {
		return err
	}
	c.Docker.Command = strings.TrimSpace(c.Docker.Command)
	if c.Docker.Enabled && c.Docker.Command == "" {
		return errors.New("docker.command cannot be empty when Docker is enabled")
	}
	if c.Docker.TimeoutSeconds < 1 || c.Docker.TimeoutSeconds > 60 {
		return errors.New("docker.timeout_seconds must be between 1 and 60")
	}

	var err error
	c.Paths.Database, err = absolutePath(c.Paths.Database)
	if err != nil {
		return fmt.Errorf("database path: %w", err)
	}
	c.Paths.DataDir, err = absolutePath(c.Paths.DataDir)
	if err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	c.Paths.LogDir, err = absolutePath(c.Paths.LogDir)
	if err != nil {
		return fmt.Errorf("log directory: %w", err)
	}
	c.Paths.BackupDir, err = absolutePath(c.Paths.BackupDir)
	if err != nil {
		return fmt.Errorf("backup directory: %w", err)
	}
	c.Paths.AppsDir, err = absolutePath(c.Paths.AppsDir)
	if err != nil {
		return fmt.Errorf("apps directory: %w", err)
	}
	if len(c.Paths.SimpleModeRoots) == 0 {
		c.Paths.SimpleModeRoots = []string{c.Paths.AppsDir}
	}
	for i, root := range c.Paths.SimpleModeRoots {
		c.Paths.SimpleModeRoots[i], err = absolutePath(root)
		if err != nil {
			return fmt.Errorf("simple mode root %q: %w", root, err)
		}
	}

	for _, dir := range []string{
		c.Paths.DataDir,
		c.Paths.LogDir,
		c.Paths.BackupDir,
		c.Paths.AppsDir,
		filepath.Dir(c.Paths.Database),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

func absolutePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path cannot be empty")
	}
	return filepath.Abs(filepath.Clean(path))
}

func applyEnvironment(cfg *Config) {
	if value := os.Getenv("VPSDECK_HOST"); value != "" {
		cfg.App.Host = value
	}
	if value := os.Getenv("VPSDECK_PORT"); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			cfg.App.Port = port
		}
	}
	if value := os.Getenv("VPSDECK_DATABASE"); value != "" {
		cfg.Paths.Database = value
	}
	if value := os.Getenv("VPSDECK_COOKIE_SECURE"); value != "" {
		if secure, err := strconv.ParseBool(value); err == nil {
			cfg.Security.CookieSecure = secure
		}
	}
	if value := os.Getenv("VPSDECK_OLLAMA_BASE_URL"); value != "" {
		cfg.Monitoring.Ollama.BaseURL = value
	}
}

func normalizeOllamaURL(cfg *OllamaMonitorConfig) error {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if !cfg.Enabled && cfg.BaseURL == "" {
		return nil
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" {
		return errors.New("monitoring.ollama.base_url must be a valid HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("monitoring.ollama.base_url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("monitoring.ollama.base_url must not include credentials, query parameters, or fragments")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return errors.New("monitoring.ollama.base_url must not include an API path")
	}
	cfg.BaseURL = parsed.Scheme + "://" + parsed.Host
	return nil
}
