package domains

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

const (
	TargetPanel   = "panel"
	TargetProject = "project"
)

var hostnameLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type CommandRunner interface {
	Run(ctx context.Context, command string, arguments ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, command string, arguments ...string) (string, error) {
	item := exec.CommandContext(ctx, command, arguments...)
	output, err := item.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(output), errors.New("command timed out")
	}
	return string(output), err
}

type CreateInput struct {
	Hostname   string
	TargetType string
	ProjectID  int64
	TargetPort int
}

type Service struct {
	db     *database.DB
	cfg    config.ReverseProxyConfig
	runner CommandRunner
}

func NewService(db *database.DB, cfg config.ReverseProxyConfig) *Service {
	return &Service{db: db, cfg: cfg, runner: execRunner{}}
}

func (s *Service) SetRunner(runner CommandRunner) {
	if runner != nil {
		s.runner = runner
	}
}

func (s *Service) List(ctx context.Context) ([]database.ProxyRoute, error) {
	return s.db.ListProxyRoutes(ctx)
}

func (s *Service) Create(ctx context.Context, input CreateInput) (database.ProxyRoute, error) {
	hostname, err := ValidateHostname(input.Hostname)
	if err != nil {
		return database.ProxyRoute{}, err
	}

	route := database.ProxyRoute{
		Hostname:     hostname,
		TargetHost:   "127.0.0.1",
		TargetScheme: "http",
		SSLStatus:    "inactive",
		Enabled:      true,
	}

	switch strings.TrimSpace(input.TargetType) {
	case TargetPanel:
		route.TargetType = TargetPanel
		route.TargetHost = s.cfg.PanelBindHost
		route.TargetPort = s.cfg.PanelBindPort
	case TargetProject:
		if input.ProjectID < 1 {
			return database.ProxyRoute{}, errors.New("select a project for this route")
		}
		project, err := s.db.ProjectByID(ctx, input.ProjectID)
		if err != nil {
			if database.IsNotFound(err) {
				return database.ProxyRoute{}, errors.New("project was not found")
			}
			return database.ProxyRoute{}, err
		}
		port := input.TargetPort
		if port == 0 {
			port = project.Port
		}
		if port == 0 {
			port, err = s.AllocatePort(ctx)
			if err != nil {
				return database.ProxyRoute{}, err
			}
		}
		if err := ValidateTargetPort(port); err != nil {
			return database.ProxyRoute{}, err
		}
		route.TargetType = TargetProject
		route.ProjectID = sql.NullInt64{Int64: project.ID, Valid: true}
		route.TargetPort = port
	default:
		return database.ProxyRoute{}, errors.New("route target must be panel or project")
	}

	if err := ValidateTargetHost(route.TargetHost); err != nil {
		return database.ProxyRoute{}, err
	}
	if err := ValidateTargetPort(route.TargetPort); err != nil {
		return database.ProxyRoute{}, err
	}

	id, err := s.db.CreateProxyRoute(ctx, route)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return database.ProxyRoute{}, errors.New("a route for this hostname already exists")
		}
		return database.ProxyRoute{}, err
	}
	route, err = s.db.ProxyRouteByID(ctx, id)
	if err != nil {
		return database.ProxyRoute{}, err
	}
	if route.TargetType == TargetProject && route.ProjectID.Valid {
		_ = s.db.UpdateProjectDomainPort(ctx, route.ProjectID.Int64, route.Hostname, route.TargetPort)
	}
	return route, nil
}

func (s *Service) AllocatePort(ctx context.Context) (int, error) {
	used := map[int]bool{
		80:                  true,
		443:                 true,
		s.cfg.PanelBindPort: true,
	}
	projects, err := s.db.ListProjects(ctx)
	if err != nil {
		return 0, err
	}
	for _, project := range projects {
		if project.Port > 0 {
			used[project.Port] = true
		}
	}
	routes, err := s.db.ListProxyRoutes(ctx)
	if err != nil {
		return 0, err
	}
	for _, route := range routes {
		if route.TargetPort > 0 {
			used[route.TargetPort] = true
		}
	}
	for port := s.cfg.InternalPortStart; port <= s.cfg.InternalPortEnd; port++ {
		if used[port] || !portAvailable(port) {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("no available internal port in %d-%d", s.cfg.InternalPortStart, s.cfg.InternalPortEnd)
}

func (s *Service) Test(ctx context.Context, id int64) (database.ProxyRoute, error) {
	route, err := s.db.ProxyRouteByID(ctx, id)
	if err != nil {
		return database.ProxyRoute{}, err
	}
	if err := os.MkdirAll(s.cfg.NginxSitesAvailable, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-available: %w", err)
	}
	if err := os.MkdirAll(s.cfg.NginxSitesEnabled, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-enabled: %w", err)
	}
	if err := s.ensureBridge(); err != nil {
		return route, err
	}

	availablePath := s.availablePath(route)
	enabledPath := s.enabledPath(route)
	availableBefore := capturePath(availablePath)
	enabledBefore := capturePath(enabledPath)
	defer func() {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
	}()

	if err := writeFileAtomic(availablePath, []byte(RenderConfig(route)), 0o644); err != nil {
		return route, fmt.Errorf("write Nginx test route: %w", err)
	}
	_ = os.Remove(enabledPath)
	if err := os.Symlink(availablePath, enabledPath); err != nil {
		return route, fmt.Errorf("enable Nginx test route: %w", err)
	}
	if output, err := s.runner.Run(ctx, s.cfg.NginxCommand, "-t"); err != nil {
		message := "Nginx config test failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	if err := s.db.SetProxyRouteLastError(ctx, route.ID, ""); err != nil {
		return route, err
	}
	route.LastError = ""
	return route, nil
}

func (s *Service) Apply(ctx context.Context, id int64) (database.ProxyRoute, error) {
	route, err := s.db.ProxyRouteByID(ctx, id)
	if err != nil {
		return database.ProxyRoute{}, err
	}
	if err := os.MkdirAll(s.cfg.NginxSitesAvailable, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-available: %w", err)
	}
	if err := os.MkdirAll(s.cfg.NginxSitesEnabled, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-enabled: %w", err)
	}
	if err := s.ensureBridge(); err != nil {
		return route, err
	}

	availablePath := s.availablePath(route)
	enabledPath := s.enabledPath(route)
	availableBefore := capturePath(availablePath)
	enabledBefore := capturePath(enabledPath)

	configText := RenderConfig(route)
	if err := writeFileAtomic(availablePath, []byte(configText), 0o644); err != nil {
		return route, fmt.Errorf("write Nginx route: %w", err)
	}
	_ = os.Remove(enabledPath)
	if err := os.Symlink(availablePath, enabledPath); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		return route, fmt.Errorf("enable Nginx route: %w", err)
	}

	if output, err := s.runner.Run(ctx, s.cfg.NginxCommand, "-t"); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		message := "Nginx config test failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	if output, err := s.runner.Run(ctx, s.cfg.SystemctlCommand, "reload", "nginx"); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		message := "Nginx reload failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	if err := s.db.MarkProxyRouteApplied(ctx, route.ID); err != nil {
		return route, err
	}
	route.Enabled = true
	route.LastError = ""
	return route, nil
}

func (s *Service) Disable(ctx context.Context, id int64) (database.ProxyRoute, error) {
	route, err := s.db.ProxyRouteByID(ctx, id)
	if err != nil {
		return database.ProxyRoute{}, err
	}
	if err := os.MkdirAll(s.cfg.NginxSitesAvailable, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-available: %w", err)
	}
	if err := os.MkdirAll(s.cfg.NginxSitesEnabled, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-enabled: %w", err)
	}
	availablePath := s.availablePath(route)
	enabledPath := s.enabledPath(route)
	availableBefore := capturePath(availablePath)
	enabledBefore := capturePath(enabledPath)

	_ = os.Remove(enabledPath)
	if err := s.ensureBridge(); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		return route, err
	}
	if output, err := s.runner.Run(ctx, s.cfg.NginxCommand, "-t"); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		message := "Nginx config test failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	if output, err := s.runner.Run(ctx, s.cfg.SystemctlCommand, "reload", "nginx"); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		message := "Nginx reload failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	if err := s.db.DisableProxyRoute(ctx, route.ID); err != nil {
		return route, err
	}
	route.Enabled = false
	return route, nil
}

func (s *Service) Delete(ctx context.Context, id int64) (database.ProxyRoute, error) {
	route, err := s.db.ProxyRouteByID(ctx, id)
	if err != nil {
		return database.ProxyRoute{}, err
	}
	if err := os.MkdirAll(s.cfg.NginxSitesAvailable, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-available: %w", err)
	}
	if err := os.MkdirAll(s.cfg.NginxSitesEnabled, 0o755); err != nil {
		return route, fmt.Errorf("create managed sites-enabled: %w", err)
	}
	availablePath := s.availablePath(route)
	enabledPath := s.enabledPath(route)
	availableBefore := capturePath(availablePath)
	enabledBefore := capturePath(enabledPath)

	_ = os.Remove(enabledPath)
	_ = os.Remove(availablePath)
	if err := s.ensureBridge(); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		return route, err
	}
	if output, err := s.runner.Run(ctx, s.cfg.NginxCommand, "-t"); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		message := "Nginx config test failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	if output, err := s.runner.Run(ctx, s.cfg.SystemctlCommand, "reload", "nginx"); err != nil {
		restorePath(availablePath, availableBefore)
		restorePath(enabledPath, enabledBefore)
		message := "Nginx reload failed: " + cleanCommandOutput(output, err)
		_ = s.db.SetProxyRouteLastError(ctx, route.ID, message)
		route.LastError = message
		return route, errors.New(message)
	}
	return route, s.db.DeleteProxyRoute(ctx, route.ID)
}

func (s *Service) TargetListening(route database.ProxyRoute) bool {
	if route.TargetHost == "" || route.TargetPort == 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(route.TargetHost, strconv.Itoa(route.TargetPort)), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *Service) ensureBridge() error {
	content := "# Managed by VPSDeck. Do not edit manually.\ninclude " + filepath.ToSlash(filepath.Join(s.cfg.NginxSitesEnabled, "*.conf")) + ";\n"
	if err := os.MkdirAll(filepath.Dir(s.cfg.NginxBridgeInclude), 0o755); err != nil {
		return fmt.Errorf("create Nginx bridge directory: %w", err)
	}
	if err := writeFileAtomic(s.cfg.NginxBridgeInclude, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write Nginx bridge include: %w", err)
	}
	return nil
}

func (s *Service) availablePath(route database.ProxyRoute) string {
	return filepath.Join(s.cfg.NginxSitesAvailable, routeFilename(route))
}

func (s *Service) enabledPath(route database.ProxyRoute) string {
	return filepath.Join(s.cfg.NginxSitesEnabled, routeFilename(route))
}

func RenderConfig(route database.ProxyRoute) string {
	projectID := "none"
	if route.ProjectID.Valid {
		projectID = strconv.FormatInt(route.ProjectID.Int64, 10)
	}
	scheme := strings.TrimSpace(route.TargetScheme)
	if scheme == "" {
		scheme = "http"
	}
	return fmt.Sprintf(`# Managed by VPSDeck. Do not edit manually.
# domain_id: %d
# project_id: %s

server {
    listen 80;
    listen [::]:80;
    server_name %s;

    client_max_body_size 256m;

    location / {
        proxy_pass %s://%s:%d;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 90s;
    }
}
`, route.ID, projectID, route.Hostname, scheme, route.TargetHost, route.TargetPort)
}

func ValidateHostname(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", errors.New("hostname is required")
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, " /\\:@?#$;&|`'\"<>(){}[]*") {
		return "", errors.New("hostname must be a plain domain without protocol, path, wildcard, or shell characters")
	}
	if value == "localhost" || strings.HasSuffix(value, ".localhost") {
		return "", errors.New("localhost hostnames are not allowed")
	}
	if net.ParseIP(value) != nil {
		return "", errors.New("IP addresses are not allowed as route hostnames")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", errors.New("hostname must include a domain and suffix")
	}
	for _, label := range labels {
		if !hostnameLabelPattern.MatchString(label) {
			return "", fmt.Errorf("invalid hostname label %q", label)
		}
	}
	return value, nil
}

func ValidateTargetHost(value string) error {
	if strings.TrimSpace(value) != "127.0.0.1" {
		return errors.New("v1 reverse proxy targets must use 127.0.0.1")
	}
	return nil
}

func ValidateTargetPort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("target port must be between 1 and 65535")
	}
	return nil
}

func routeFilename(route database.ProxyRoute) string {
	hostname := strings.ToLower(route.Hostname)
	var safe strings.Builder
	for _, r := range hostname {
		switch {
		case r >= 'a' && r <= 'z':
			safe.WriteRune(r)
		case r >= '0' && r <= '9':
			safe.WriteRune(r)
		case r == '.' || r == '-':
			safe.WriteRune(r)
		default:
			safe.WriteByte('-')
		}
	}
	name := strings.Trim(safe.String(), ".-")
	if name == "" {
		name = "route"
	}
	return fmt.Sprintf("%06d-%s.conf", route.ID, name)
}

func portAvailable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

type pathState struct {
	exists     bool
	isSymlink  bool
	linkTarget string
	content    []byte
	mode       os.FileMode
}

func capturePath(path string) pathState {
	info, err := os.Lstat(path)
	if err != nil {
		return pathState{}
	}
	state := pathState{exists: true, mode: info.Mode().Perm()}
	if info.Mode()&os.ModeSymlink != 0 {
		state.isSymlink = true
		state.linkTarget, _ = os.Readlink(path)
		return state
	}
	if info.Mode().IsRegular() {
		state.content, _ = os.ReadFile(path)
	}
	return state
}

func restorePath(path string, state pathState) {
	_ = os.Remove(path)
	if !state.exists {
		return
	}
	if state.isSymlink {
		_ = os.Symlink(state.linkTarget, path)
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, state.content, state.mode)
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".vpsdeck-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func cleanCommandOutput(output string, err error) string {
	output = strings.TrimSpace(strings.ReplaceAll(output, "\x00", ""))
	if output == "" && err != nil {
		output = err.Error()
	}
	if len(output) > 4000 {
		output = output[:4000]
	}
	return output
}

func SortRoutes(routes []database.ProxyRoute) {
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Hostname < routes[j].Hostname
	})
}
