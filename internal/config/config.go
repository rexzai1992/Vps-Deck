package config

import (
	"encoding/base64"
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
	App          AppConfig          `yaml:"app"`
	Security     SecurityConfig     `yaml:"security"`
	Paths        PathsConfig        `yaml:"paths"`
	Deployments  DeploymentConfig   `yaml:"deployments"`
	Monitoring   MonitoringConfig   `yaml:"monitoring"`
	Docker       DockerConfig       `yaml:"docker"`
	Updates      UpdateConfig       `yaml:"updates"`
	Integrations IntegrationsConfig `yaml:"integrations"`
}

type IntegrationsConfig struct {
	GitHub GitHubConfig `yaml:"github"`
}

type GitHubConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	CallbackURL  string `yaml:"callback_url"`
	Scopes       string `yaml:"scopes"`
	TokenKey     string `yaml:"token_key"`
}

// DecodedKey returns the 32-byte AES key used to encrypt stored OAuth tokens.
func (g GitHubConfig) DecodedKey() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(g.TokenKey))
	if err != nil {
		return nil, errors.New("integrations.github.token_key must be base64-encoded")
	}
	if len(key) != 32 {
		return nil, errors.New("integrations.github.token_key must decode to exactly 32 bytes")
	}
	return key, nil
}

type AppConfig struct {
	Name        string `yaml:"name"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	BaseURL     string `yaml:"base_url"`
	Environment string `yaml:"environment"`
	DemoMode    bool   `yaml:"demo_mode"`
}

type SecurityConfig struct {
	CookieSecure            bool               `yaml:"cookie_secure"`
	SessionLifetime         time.Duration      `yaml:"-"`
	SessionLifetimeHours    int                `yaml:"session_lifetime_hours"`
	LoginRateLimitPerMinute int                `yaml:"login_rate_limit_per_minute"`
	AdvancedMode            AdvancedModeConfig `yaml:"advanced_mode"`
}

type AdvancedModeConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Root           string `yaml:"root"`
	TimeoutMinutes int    `yaml:"timeout_minutes"`
	SecondPassword string `yaml:"second_password"`
}

type PathsConfig struct {
	Database        string   `yaml:"database"`
	DataDir         string   `yaml:"data_dir"`
	LogDir          string   `yaml:"log_dir"`
	BackupDir       string   `yaml:"backup_dir"`
	AppsDir         string   `yaml:"apps_dir"`
	SimpleModeRoots []string `yaml:"simple_mode_roots"`
}

type DeploymentConfig struct {
	Enabled        bool   `yaml:"enabled"`
	GitCommand     string `yaml:"git_command"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
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

type UpdateConfig struct {
	Enabled              bool   `yaml:"enabled"`
	SourceDir            string `yaml:"source_dir"`
	Branch               string `yaml:"branch"`
	GitCommand           string `yaml:"git_command"`
	CheckIntervalMinutes int    `yaml:"check_interval_minutes"`
	RequestPath          string `yaml:"request_path"`
	StatusPath           string `yaml:"status_path"`
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
			AdvancedMode: AdvancedModeConfig{
				Enabled:        true,
				Root:           "/",
				TimeoutMinutes: 15,
			},
		},
		Paths: PathsConfig{
			Database:        "./data/vpsdeck.db",
			DataDir:         "./data",
			LogDir:          "./data/logs",
			BackupDir:       "./data/backups",
			AppsDir:         "./managed-apps",
			SimpleModeRoots: []string{"./managed-apps"},
		},
		Deployments: DeploymentConfig{
			Enabled:        true,
			GitCommand:     "git",
			TimeoutSeconds: 300,
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
		Updates: UpdateConfig{
			Enabled:              true,
			SourceDir:            ".",
			Branch:               "main",
			GitCommand:           "git",
			CheckIntervalMinutes: 30,
		},
		Integrations: IntegrationsConfig{
			GitHub: GitHubConfig{
				Enabled: false,
				Scopes:  "repo",
			},
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
	if strings.TrimSpace(c.Security.AdvancedMode.Root) == "" {
		c.Security.AdvancedMode.Root = "/"
	}
	advancedRoot, advancedErr := absolutePath(c.Security.AdvancedMode.Root)
	if advancedErr != nil {
		return fmt.Errorf("advanced mode root: %w", advancedErr)
	}
	c.Security.AdvancedMode.Root = advancedRoot
	if c.Security.AdvancedMode.TimeoutMinutes < 1 {
		c.Security.AdvancedMode.TimeoutMinutes = 15
	}
	if c.Security.AdvancedMode.TimeoutMinutes > 240 {
		c.Security.AdvancedMode.TimeoutMinutes = 240
	}
	c.Deployments.GitCommand = strings.TrimSpace(c.Deployments.GitCommand)
	if c.Deployments.Enabled && c.Deployments.GitCommand == "" {
		return errors.New("deployments.git_command cannot be empty when deployments are enabled")
	}
	if c.Deployments.TimeoutSeconds < 10 || c.Deployments.TimeoutSeconds > 1800 {
		return errors.New("deployments.timeout_seconds must be between 10 and 1800")
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

	if err := c.normalizeUpdates(); err != nil {
		return err
	}
	if err := c.normalizeGitHub(); err != nil {
		return err
	}
	return nil
}

func (c *Config) normalizeGitHub() error {
	github := &c.Integrations.GitHub
	github.ClientID = strings.TrimSpace(github.ClientID)
	github.ClientSecret = strings.TrimSpace(github.ClientSecret)
	github.CallbackURL = strings.TrimSpace(github.CallbackURL)
	github.TokenKey = strings.TrimSpace(github.TokenKey)
	if strings.TrimSpace(github.Scopes) == "" {
		github.Scopes = "repo"
	}
	if !github.Enabled {
		return nil
	}
	if github.ClientID == "" || github.ClientSecret == "" {
		return errors.New("integrations.github requires client_id and client_secret when enabled")
	}
	parsed, err := url.Parse(github.CallbackURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("integrations.github.callback_url must be a valid http or https URL")
	}
	if _, err := github.DecodedKey(); err != nil {
		return err
	}
	return nil
}

func (c *Config) normalizeUpdates() error {
	c.Updates.GitCommand = strings.TrimSpace(c.Updates.GitCommand)
	if c.Updates.GitCommand == "" {
		c.Updates.GitCommand = "git"
	}
	if strings.TrimSpace(c.Updates.Branch) == "" {
		c.Updates.Branch = "main"
	}
	if c.Updates.CheckIntervalMinutes < 1 {
		c.Updates.CheckIntervalMinutes = 30
	}
	if c.Updates.CheckIntervalMinutes > 1440 {
		c.Updates.CheckIntervalMinutes = 1440
	}
	sourceDir := strings.TrimSpace(c.Updates.SourceDir)
	if sourceDir == "" {
		sourceDir = "."
	}
	absSource, err := absolutePath(sourceDir)
	if err != nil {
		return fmt.Errorf("updates source directory: %w", err)
	}
	c.Updates.SourceDir = absSource

	if strings.TrimSpace(c.Updates.RequestPath) == "" {
		c.Updates.RequestPath = filepath.Join(c.Paths.DataDir, "update.request")
	}
	if c.Updates.RequestPath, err = absolutePath(c.Updates.RequestPath); err != nil {
		return fmt.Errorf("updates request path: %w", err)
	}
	if strings.TrimSpace(c.Updates.StatusPath) == "" {
		c.Updates.StatusPath = filepath.Join(c.Paths.DataDir, "update.status")
	}
	if c.Updates.StatusPath, err = absolutePath(c.Updates.StatusPath); err != nil {
		return fmt.Errorf("updates status path: %w", err)
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
	if value := os.Getenv("VPSDECK_GITHUB_CLIENT_ID"); value != "" {
		cfg.Integrations.GitHub.ClientID = value
	}
	if value := os.Getenv("VPSDECK_GITHUB_CLIENT_SECRET"); value != "" {
		cfg.Integrations.GitHub.ClientSecret = value
	}
	if value := os.Getenv("VPSDECK_GITHUB_CALLBACK_URL"); value != "" {
		cfg.Integrations.GitHub.CallbackURL = value
	}
	if value := os.Getenv("VPSDECK_GITHUB_TOKEN_KEY"); value != "" {
		cfg.Integrations.GitHub.TokenKey = value
	}
	if os.Getenv("VPSDECK_GITHUB_ENABLED") == "true" {
		cfg.Integrations.GitHub.Enabled = true
	}
	if os.Getenv("VPSDECK_DEMO_MODE") == "true" {
		cfg.App.DemoMode = true
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
