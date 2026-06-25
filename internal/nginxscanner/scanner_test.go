package nginxscanner

import (
	"os"
	"path/filepath"
	"testing"
)

// Fixture nginx configs matching the VPS structure.
const (
	confPanelSSL = `
server {
    listen 443 ssl;
    server_name vps.example.com;
    ssl_certificate /etc/letsencrypt/live/vps.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vps.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
}
server {
    if ($host = vps.example.com) { return 301 https://$host$request_uri; }
    listen 80;
    server_name vps.example.com;
    return 404;
}
`
	confProjectSSL = `
server {
    listen 443 ssl;
    server_name api.example.com;
    ssl_certificate /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}
server {
    listen 80;
    server_name api.example.com;
    return 301 https://$host$request_uri;
}
`
	confOllamaPort = `
server {
    listen 8088;
    server_name _;

    location / {
        proxy_pass http://127.0.0.1:11434;
        proxy_read_timeout 300s;
    }
}
`
	confNoProxy = `
server {
    listen 80;
    server_name static.example.com;
    root /var/www/html;
    index index.html;
}
`
	confWithComment = `
# Main VPS panel
server {
    listen 443 ssl; # SSL listener
    server_name vps.example.com; # primary hostname
    ssl_certificate /etc/letsencrypt/live/vps.example.com/fullchain.pem;

    location / {
        proxy_pass http://127.0.0.1:8080; # panel
    }
}
`
)

func TestRemoveComments(t *testing.T) {
	input := "server { # comment\n    listen 80; # another\n}"
	got := removeComments(input)
	if got != "server { \n    listen 80; \n}" {
		t.Errorf("removeComments got %q", got)
	}
}

func TestExtractServerBlocksCount(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"panel with redirect", confPanelSSL, 2},
		{"project with redirect", confProjectSSL, 2},
		{"ollama port-based", confOllamaPort, 1},
		{"no proxy", confNoProxy, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := extractServerBlocks(removeComments(tc.input))
			if len(blocks) != tc.want {
				t.Errorf("got %d blocks, want %d", len(blocks), tc.want)
			}
		})
	}
}

func TestParseBlockPanel(t *testing.T) {
	blocks := extractServerBlocks(removeComments(confPanelSSL))
	if len(blocks) < 1 {
		t.Fatal("no blocks")
	}
	block := parseBlock(blocks[0])

	if len(block.ServerNames) == 0 || block.ServerNames[0] != "vps.example.com" {
		t.Errorf("ServerNames: got %v", block.ServerNames)
	}
	if !block.SSLEnabled {
		t.Error("SSLEnabled should be true")
	}
	if block.CertPath != "/etc/letsencrypt/live/vps.example.com/fullchain.pem" {
		t.Errorf("CertPath: got %q", block.CertPath)
	}
	if block.ProxyPass != "http://127.0.0.1:8080" {
		t.Errorf("ProxyPass: got %q", block.ProxyPass)
	}
}

func TestParseBlockRedirectHasNoProxyPass(t *testing.T) {
	blocks := extractServerBlocks(removeComments(confPanelSSL))
	if len(blocks) < 2 {
		t.Fatal("expected 2 blocks")
	}
	block := parseBlock(blocks[1])
	if block.ProxyPass != "" {
		t.Errorf("redirect block should have no proxy_pass, got %q", block.ProxyPass)
	}
}

func TestParseBlockWildcardServerNameFiltered(t *testing.T) {
	blocks := extractServerBlocks(removeComments(confOllamaPort))
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	block := parseBlock(blocks[0])
	if len(block.ServerNames) != 0 {
		t.Errorf("wildcard server_name _ should be filtered, got %v", block.ServerNames)
	}
	if block.ProxyPass != "http://127.0.0.1:11434" {
		t.Errorf("ProxyPass: got %q", block.ProxyPass)
	}
}

func TestParseBlockNoProxy(t *testing.T) {
	blocks := extractServerBlocks(removeComments(confNoProxy))
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	block := parseBlock(blocks[0])
	if block.ProxyPass != "" {
		t.Errorf("no proxy_pass expected, got %q", block.ProxyPass)
	}
}

func TestParseBlockWithComments(t *testing.T) {
	blocks := extractServerBlocks(removeComments(confWithComment))
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	block := parseBlock(blocks[0])
	if block.ProxyPass != "http://127.0.0.1:8080" {
		t.Errorf("ProxyPass after comment strip: got %q", block.ProxyPass)
	}
}

func TestProxyPassParts(t *testing.T) {
	cases := []struct {
		input    string
		wantHost string
		wantPort int
	}{
		{"http://127.0.0.1:8080", "127.0.0.1", 8080},
		{"http://127.0.0.1:3000", "127.0.0.1", 3000},
		{"http://127.0.0.1:11434", "127.0.0.1", 11434},
		{"https://127.0.0.1", "127.0.0.1", 443},
		{"http://127.0.0.1", "127.0.0.1", 80},
	}
	for _, tc := range cases {
		host, port := proxyPassParts(tc.input)
		if host != tc.wantHost || port != tc.wantPort {
			t.Errorf("proxyPassParts(%q): got host=%q port=%d, want host=%q port=%d",
				tc.input, host, port, tc.wantHost, tc.wantPort)
		}
	}
}

func TestGuessTargetType(t *testing.T) {
	panelPort := 8080
	if got := guessTargetType(panelPort, panelPort); got != "panel" {
		t.Errorf("panel: got %q", got)
	}
	if got := guessTargetType(11434, panelPort); got != "ollama" {
		t.Errorf("ollama: got %q", got)
	}
	if got := guessTargetType(3000, panelPort); got != "project" {
		t.Errorf("project: got %q", got)
	}
	if got := guessTargetType(0, panelPort); got != "unknown" {
		t.Errorf("unknown: got %q", got)
	}
}

func TestScanFileSkipsNoProxyBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(path, []byte(confProjectSSL), 0644); err != nil {
		t.Fatal(err)
	}
	routes, err := scanFile(path, map[string]bool{path: true}, 8080)
	if err != nil {
		t.Fatal(err)
	}
	// Only one block has proxy_pass (the SSL block); the redirect block is skipped.
	if len(routes) != 1 {
		t.Errorf("got %d routes, want 1", len(routes))
	}
	if routes[0].Hostname != "api.example.com" {
		t.Errorf("Hostname: got %q", routes[0].Hostname)
	}
	if !routes[0].Enabled {
		t.Error("should be enabled (path is in enabled set)")
	}
	if routes[0].TargetType != "project" {
		t.Errorf("TargetType: got %q", routes[0].TargetType)
	}
}

func TestScanResultPanelDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vpsdeck")
	if err := os.WriteFile(path, []byte(confPanelSSL), 0644); err != nil {
		t.Fatal(err)
	}
	enabledDir := t.TempDir()
	// Create a symlink in enabledDir pointing to path.
	link := filepath.Join(enabledDir, "vpsdeck")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}

	result := Scan(ScanOptions{
		SitesAvailDirs:   []string{dir},
		SitesEnabledDirs: []string{enabledDir},
		PanelPort:        8080,
	})
	if len(result.Routes) != 1 {
		t.Fatalf("got %d routes, want 1 (redirect block should be skipped)", len(result.Routes))
	}
	r := result.Routes[0]
	if r.TargetType != "panel" {
		t.Errorf("TargetType: got %q, want panel", r.TargetType)
	}
	if !r.Enabled {
		t.Error("should be enabled via symlink")
	}
}

func TestScanMissingDirIsGraceful(t *testing.T) {
	result := Scan(ScanOptions{
		SitesAvailDirs:   []string{"/nonexistent/sites-available"},
		SitesEnabledDirs: []string{"/nonexistent/sites-enabled"},
		PanelPort:        8080,
	})
	// Should return no routes, no panicking, no fatal errors.
	if len(result.Routes) != 0 {
		t.Errorf("expected empty routes for missing dirs, got %d", len(result.Routes))
	}
}
