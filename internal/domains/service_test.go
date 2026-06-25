package domains

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

type fakeRunner struct {
	calls []string
	fail  map[string]string
}

func (r *fakeRunner) Run(_ context.Context, command string, arguments ...string) (string, error) {
	key := command + " " + strings.Join(arguments, " ")
	r.calls = append(r.calls, key)
	if message, ok := r.fail[key]; ok {
		return message, errors.New(message)
	}
	return "ok", nil
}

func testConfig(base string) config.ReverseProxyConfig {
	return config.ReverseProxyConfig{
		PanelBindHost:       "127.0.0.1",
		PanelBindPort:       7788,
		InternalPortStart:   31000,
		InternalPortEnd:     31010,
		NginxSitesAvailable: filepath.Join(base, "available"),
		NginxSitesEnabled:   filepath.Join(base, "enabled"),
		NginxBridgeInclude:  filepath.Join(base, "sites-enabled", "vpsdeck-managed.conf"),
		NginxCommand:        "nginx",
		SystemctlCommand:    "systemctl",
	}
}

func TestValidateHostname(t *testing.T) {
	valid, err := ValidateHostname(" App.Example.COM. ")
	if err != nil {
		t.Fatal(err)
	}
	if valid != "app.example.com" {
		t.Fatalf("unexpected hostname: %q", valid)
	}
	for _, value := range []string{
		"",
		"localhost",
		"127.0.0.1",
		"*.example.com",
		"https://example.com/path",
		"example.com;rm -rf /",
		"bad label.example.com",
	} {
		if _, err := ValidateHostname(value); err == nil {
			t.Fatalf("unsafe hostname accepted: %q", value)
		}
	}
}

func TestAllocatePortAvoidsExistingProjectAndRoutes(t *testing.T) {
	base := t.TempDir()
	portBase := freePortBlock(t, 6)
	occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(portBase+3)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateProject(t.Context(), database.Project{
		Name:       "App",
		Type:       "custom",
		WorkingDir: filepath.Join(base, "app"),
		Port:       portBase,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProxyRoute(t.Context(), database.ProxyRoute{
		Hostname:     "api.example.com",
		TargetType:   TargetProject,
		TargetHost:   "127.0.0.1",
		TargetPort:   portBase + 1,
		TargetScheme: "http",
		SSLStatus:    "inactive",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(base)
	cfg.PanelBindPort = portBase + 2
	cfg.InternalPortStart = portBase
	cfg.InternalPortEnd = portBase + 5
	service := NewService(db, cfg)
	port, err := service.AllocatePort(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if port != portBase+4 {
		t.Fatalf("expected %d, got %d", portBase+4, port)
	}
}

func TestValidateTargetPort(t *testing.T) {
	for _, port := range []int{1, 31000, 65535} {
		if err := ValidateTargetPort(port); err != nil {
			t.Fatalf("valid port %d rejected: %v", port, err)
		}
	}
	for _, port := range []int{0, -1, 65536} {
		if err := ValidateTargetPort(port); err == nil {
			t.Fatalf("invalid port %d accepted", port)
		}
	}
}

func TestRenderConfigIncludesManagedHeaderTargetAndProxyHeaders(t *testing.T) {
	rendered := RenderConfig(database.ProxyRoute{
		ID:           12,
		Hostname:     "app.example.com",
		TargetType:   TargetProject,
		TargetHost:   "127.0.0.1",
		TargetPort:   31002,
		TargetScheme: "http",
	})
	for _, want := range []string{
		"# Managed by VPSDeck. Do not edit manually.",
		"# domain_id: 12",
		"server_name app.example.com;",
		"proxy_pass http://127.0.0.1:31002;",
		"proxy_set_header Host $host;",
		"proxy_set_header X-Real-IP $remote_addr;",
		"proxy_set_header Upgrade $http_upgrade;",
		`proxy_set_header Connection "upgrade";`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, rendered)
		}
	}
}

func TestApplyRollsBackWhenNginxTestFails(t *testing.T) {
	base := t.TempDir()
	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := testConfig(base)
	service := NewService(db, cfg)
	runner := &fakeRunner{fail: map[string]string{"nginx -t": "nginx test failed"}}
	service.SetRunner(runner)

	route, err := service.Create(t.Context(), CreateInput{
		Hostname:   "panel.example.com",
		TargetType: TargetPanel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(t.Context(), route.ID); err == nil {
		t.Fatal("apply succeeded despite failed nginx -t")
	}

	entries, err := os.ReadDir(cfg.NginxSitesAvailable)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("candidate config was not rolled back: %#v", entries)
	}
	enabled, err := os.ReadDir(cfg.NginxSitesEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Fatalf("enabled symlink was not rolled back: %#v", enabled)
	}
	reloaded := false
	for _, call := range runner.calls {
		if call == "systemctl reload nginx" {
			reloaded = true
		}
	}
	if reloaded {
		t.Fatal("nginx reload should not run after failed nginx -t")
	}
	updated, err := db.ProxyRouteByID(t.Context(), route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.LastError, "nginx test failed") {
		t.Fatalf("last_error not saved: %#v", updated)
	}
}

func TestTestRouteRestoresCandidateWithoutReload(t *testing.T) {
	base := t.TempDir()
	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := testConfig(base)
	service := NewService(db, cfg)
	runner := &fakeRunner{}
	service.SetRunner(runner)

	route, err := service.Create(t.Context(), CreateInput{
		Hostname:   "test.example.com",
		TargetType: TargetPanel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(t.Context(), route.ID); err != nil {
		t.Fatal(err)
	}
	available, err := os.ReadDir(cfg.NginxSitesAvailable)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 0 {
		t.Fatalf("test left candidate config behind: %#v", available)
	}
	enabled, err := os.ReadDir(cfg.NginxSitesEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Fatalf("test left enabled symlink behind: %#v", enabled)
	}
	for _, call := range runner.calls {
		if call == "systemctl reload nginx" {
			t.Fatal("test should not reload nginx")
		}
	}
}

func TestDisableAndApplyToggleManagedSymlink(t *testing.T) {
	base := t.TempDir()
	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := testConfig(base)
	service := NewService(db, cfg)
	service.SetRunner(&fakeRunner{})

	route, err := service.Create(t.Context(), CreateInput{
		Hostname:   "toggle.example.com",
		TargetType: TargetPanel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route, err = service.Apply(t.Context(), route.ID); err != nil {
		t.Fatal(err)
	}
	if !route.Enabled {
		t.Fatal("applied route should be enabled")
	}
	enabled, err := os.ReadDir(cfg.NginxSitesEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected one enabled symlink, got %#v", enabled)
	}
	if route, err = service.Disable(t.Context(), route.ID); err != nil {
		t.Fatal(err)
	}
	if route.Enabled {
		t.Fatal("disabled route should be marked disabled")
	}
	enabled, err = os.ReadDir(cfg.NginxSitesEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Fatalf("disable left enabled symlink: %#v", enabled)
	}
	available, err := os.ReadDir(cfg.NginxSitesAvailable)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 {
		t.Fatalf("disable should keep available config, got %#v", available)
	}
	if route, err = service.Apply(t.Context(), route.ID); err != nil {
		t.Fatal(err)
	}
	if !route.Enabled {
		t.Fatal("apply should re-enable a disabled route")
	}
}

func TestTargetListening(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	service := NewService(nil, config.ReverseProxyConfig{})
	if !service.TargetListening(database.ProxyRoute{TargetHost: "127.0.0.1", TargetPort: port}) {
		t.Fatal("listening target was not detected")
	}
}

func freePortBlock(t *testing.T, count int) int {
	t.Helper()
	for start := 32000; start < 60000-count; start += count {
		var listeners []net.Listener
		ok := true
		for offset := 0; offset < count; offset++ {
			listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(start+offset)))
			if err != nil {
				ok = false
				break
			}
			listeners = append(listeners, listener)
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		if ok {
			return start
		}
	}
	t.Fatal("could not find a free local port block")
	return 0
}
