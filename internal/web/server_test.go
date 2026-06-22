package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vpsdeck/vpsdeck/internal/auth"
	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

func TestLoginProjectRegistrationAndAudit(t *testing.T) {
	handler, db, projectPath := testServer(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	loginPage, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	loginPage.Body.Close()
	csrf := cookieValue(t, jar, server.URL, csrfCookie)

	response, err := client.PostForm(server.URL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {"StrongPassword123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Everything looks calm") {
		t.Fatalf("login did not reach dashboard: status=%d body=%s", response.StatusCode, body)
	}

	csrf = cookieValue(t, jar, server.URL, csrfCookie)
	response, err = client.PostForm(server.URL+"/projects", url.Values{
		"csrf_token":  {csrf},
		"name":        {"Test App"},
		"type":        {"auto"},
		"working_dir": {projectPath},
		"port":        {"9090"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Test App") {
		t.Fatalf("project registration failed: status=%d body=%s", response.StatusCode, body)
	}

	csrf = cookieValue(t, jar, server.URL, csrfCookie)
	response, err = client.PostForm(server.URL+"/projects/1/env", url.Values{
		"csrf_token": {csrf},
		"key":        {"APP_ENV", "API_TOKEN"},
		"value":      {"test", "secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	envContent, err := os.ReadFile(filepath.Join(projectPath, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envContent), "API_TOKEN=secret-value") {
		t.Fatalf("environment was not saved: %s", envContent)
	}

	csrf = cookieValue(t, jar, server.URL, csrfCookie)
	response, err = client.PostForm(server.URL+"/projects/1/files/edit", url.Values{
		"csrf_token": {csrf},
		"path":       {"package.json"},
		"content":    {`{"name":"updated"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	updated, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != `{"name":"updated"}` {
		t.Fatalf("file editor did not save content: %s", updated)
	}
	response, err = client.Get(server.URL + "/projects/1/files")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "package.json") {
		t.Fatalf("file manager did not render: status=%d body=%s", response.StatusCode, body)
	}
	response, err = client.Get(server.URL + "/files")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Open files") {
		t.Fatalf("global files page did not render: status=%d body=%s", response.StatusCode, body)
	}
	response, err = client.Get(server.URL + "/api/folders?root=0&path=")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"absolute_path"`) {
		t.Fatalf("folder picker API failed: status=%d body=%s", response.StatusCode, body)
	}
	for _, path := range []string{"/network/ports", "/ollama"} {
		response, err = client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s failed: status=%d body=%s", path, response.StatusCode, body)
		}
	}

	entries, err := db.ListAudit(t.Context(), 20)
	if err != nil {
		t.Fatal(err)
	}
	var loginFound, projectFound, envFound, fileFound bool
	for _, entry := range entries {
		loginFound = loginFound || entry.Action == "login"
		projectFound = projectFound || entry.Action == "project_create"
		envFound = envFound || entry.Action == "env_edit"
		fileFound = fileFound || entry.Action == "file_edit"
	}
	if !loginFound || !projectFound || !envFound || !fileFound {
		t.Fatalf("missing audit entries: login=%v project=%v env=%v file=%v", loginFound, projectFound, envFound, fileFound)
	}
}

func TestCSRFRejectsStateChange(t *testing.T) {
	handler, _, _ := testServer(t)
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=StrongPassword123"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", response.Code)
	}
}

func TestMonitorAPIRequiresJSONAuthentication(t *testing.T) {
	handler, _, _ := testServer(t)
	for _, path := range []string{"/api/folders", "/api/monitors/ports", "/api/monitors/ollama"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s expected 401, got %d", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "authentication required") {
			t.Fatalf("%s did not return JSON auth error: %s", path, response.Body.String())
		}
	}
}

func testServer(t *testing.T) (http.Handler, *database.DB, string) {
	t.Helper()
	base := t.TempDir()
	apps := filepath.Join(base, "apps")
	projectPath := filepath.Join(apps, "test-app")
	if err := os.MkdirAll(projectPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "package.json"), []byte(`{"name":"test-app"}`), 0o640); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(base, "config.yaml")
	configBody := "app:\n  host: 127.0.0.1\n  port: 8080\n  environment: test\nsecurity:\n  cookie_secure: false\n  session_lifetime_hours: 1\n  login_rate_limit_per_minute: 5\npaths:\n  database: " + filepath.Join(base, "vpsdeck.db") + "\n  data_dir: " + filepath.Join(base, "data") + "\n  log_dir: " + filepath.Join(base, "logs") + "\n  backup_dir: " + filepath.Join(base, "backups") + "\n  apps_dir: " + apps + "\n  simple_mode_roots:\n    - " + apps + "\nmonitoring:\n  refresh_seconds: 5\n  ports:\n    enabled: false\n  ollama:\n    enabled: false\n    base_url: http://127.0.0.1:11434\n    timeout_seconds: 1\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(cfg.Paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	authService := auth.NewService(db, cfg.Security.SessionLifetime)
	if _, err := authService.BootstrapAdmin("admin", "StrongPassword123"); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := New(cfg, db, authService, logger)
	if err != nil {
		t.Fatal(err)
	}
	return handler, db, projectPath
}

func cookieValue(t *testing.T, jar http.CookieJar, rawURL, name string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	t.Fatalf("cookie %q not found", name)
	return ""
}
