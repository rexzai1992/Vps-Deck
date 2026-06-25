package web

import (
	"database/sql"
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
	"time"

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
	response, err = client.Get(server.URL + "/projects/new")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Deploy from GitHub") {
		t.Fatalf("GitHub import form did not render: status=%d body=%s", response.StatusCode, body)
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
	if err := db.CreateProjectSource(t.Context(), database.ProjectSource{
		ProjectID:     1,
		Provider:      "github",
		RepositoryURL: "https://github.com/example/test-app.git",
		Branch:        "main",
		DeployMode:    "git",
		LastCommit:    "1234567890abcdef",
	}); err != nil {
		t.Fatal(err)
	}
	deploymentID, err := db.CreateDeployment(t.Context(), database.Deployment{
		ProjectID:   1,
		Action:      "clone",
		State:       "running",
		CommitAfter: "1234567890abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishDeployment(t.Context(), deploymentID, "success", "", "1234567890abcdef", "Clone complete", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/projects/1", "/deployments"} {
		response, err = client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "12345678") {
			t.Fatalf("%s did not render GitHub deployment data: status=%d body=%s", path, response.StatusCode, body)
		}
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

func TestDomainsRequireLoginRenderRoutesAndRejectInvalidHostname(t *testing.T) {
	handler, db, projectPath := testServer(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	anonymous := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := anonymous.Get(server.URL + "/domains")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/login") {
		t.Fatalf("anonymous domains request should redirect to login, got %d %q", response.StatusCode, response.Header.Get("Location"))
	}

	client := loginTestClient(t, server.URL)
	projectID, err := db.CreateProject(t.Context(), database.Project{
		Name:       "Domain App",
		Type:       "custom",
		WorkingDir: filepath.Join(projectPath, "domain-app"),
		Port:       39999,
		Status:     "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProxyRoute(t.Context(), database.ProxyRoute{
		Hostname:     "domains.example.com",
		ProjectID:    sqlNullInt64(projectID),
		TargetType:   "project",
		TargetHost:   "127.0.0.1",
		TargetPort:   39999,
		TargetScheme: "http",
		SSLStatus:    "inactive",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	page, err := client.Get(server.URL + "/domains")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "domains.example.com") || !strings.Contains(string(body), "Target port is not listening") {
		t.Fatalf("domains page did not render route: status=%d body=%s", page.StatusCode, body)
	}

	csrf := cookieValue(t, client.Jar, server.URL, csrfCookie)
	invalid, err := client.PostForm(server.URL+"/domains", url.Values{
		"csrf_token":  {csrf},
		"hostname":    {"https://bad.example.com/path"},
		"target_type": {"panel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidBody, _ := io.ReadAll(invalid.Body)
	invalid.Body.Close()
	if invalid.StatusCode != http.StatusOK || !strings.Contains(string(invalidBody), "hostname must be a plain domain") {
		t.Fatalf("invalid hostname was not rejected clearly: status=%d body=%s", invalid.StatusCode, invalidBody)
	}
	routes, err := db.ListProxyRoutes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("invalid create should not add a route, got %#v", routes)
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

func TestFileExplorerAPI(t *testing.T) {
	handler, db, projectPath := testServer(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	page, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	csrf := cookieValue(t, jar, server.URL, csrfCookie)
	login, err := client.PostForm(server.URL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {"StrongPassword123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	csrf = cookieValue(t, jar, server.URL, csrfCookie)
	register, err := client.PostForm(server.URL+"/projects", url.Values{
		"csrf_token":  {csrf},
		"name":        {"Files App"},
		"type":        {"auto"},
		"working_dir": {projectPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	register.Body.Close()

	apiPost := func(path string, form url.Values) (int, string) {
		t.Helper()
		token := cookieValue(t, jar, server.URL, csrfCookie)
		request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response.StatusCode, string(body)
	}

	// listing
	list, err := client.Get(server.URL + "/api/projects/1/files?path=")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(list.Body)
	list.Body.Close()
	if list.StatusCode != http.StatusOK || !strings.Contains(string(listBody), "package.json") {
		t.Fatalf("files listing failed: status=%d body=%s", list.StatusCode, listBody)
	}

	if status, body := apiPost("/api/projects/1/files/new-folder", url.Values{"path": {"."}, "name": {"sub"}}); status != http.StatusOK {
		t.Fatalf("new-folder failed: status=%d body=%s", status, body)
	}
	if status, body := apiPost("/api/projects/1/files/new-file", url.Values{"path": {"sub"}, "name": {"hello.txt"}}); status != http.StatusOK || !strings.Contains(body, "sub/hello.txt") {
		t.Fatalf("new-file failed: status=%d body=%s", status, body)
	}
	if status, body := apiPost("/api/projects/1/files/move", url.Values{"target": {"sub/hello.txt"}, "dest": {"."}}); status != http.StatusOK {
		t.Fatalf("move failed: status=%d body=%s", status, body)
	}
	if status, body := apiPost("/api/projects/1/files/copy", url.Values{"target": {"hello.txt"}, "dest": {"sub"}}); status != http.StatusOK {
		t.Fatalf("copy failed: status=%d body=%s", status, body)
	}
	if status, body := apiPost("/api/projects/1/files/rename", url.Values{"target": {"hello.txt"}, "name": {"renamed.txt"}}); status != http.StatusOK || !strings.Contains(body, "renamed.txt") {
		t.Fatalf("rename failed: status=%d body=%s", status, body)
	}
	if status, body := apiPost("/api/projects/1/files/delete", url.Values{"target": {"sub"}, "recursive": {"true"}}); status != http.StatusOK {
		t.Fatalf("recursive delete failed: status=%d body=%s", status, body)
	}

	if _, err := os.Stat(filepath.Join(projectPath, "renamed.txt")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectPath, "sub")); !os.IsNotExist(err) {
		t.Fatal("sub folder should have been recursively deleted")
	}

	// folder ZIP download
	zipResponse, err := client.Get(server.URL + "/projects/1/files/download-zip?path=")
	if err != nil {
		t.Fatal(err)
	}
	zipBody, _ := io.ReadAll(zipResponse.Body)
	zipResponse.Body.Close()
	if zipResponse.StatusCode != http.StatusOK || zipResponse.Header.Get("Content-Type") != "application/zip" || len(zipBody) == 0 {
		t.Fatalf("zip download failed: status=%d type=%s len=%d", zipResponse.StatusCode, zipResponse.Header.Get("Content-Type"), len(zipBody))
	}

	// unauthenticated API access is rejected
	anon, err := http.Get(server.URL + "/api/projects/1/files")
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous file API, got %d", anon.StatusCode)
	}

	entries, err := db.ListAudit(t.Context(), 50)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"folder_create": false, "file_new": false, "file_move": false, "file_copy": false, "file_rename": false, "file_delete": false}
	for _, entry := range entries {
		if _, ok := wanted[entry.Action]; ok {
			wanted[entry.Action] = true
		}
	}
	for action, found := range wanted {
		if !found {
			t.Fatalf("missing audit entry for %s", action)
		}
	}
}

func TestGitHubIntegrationDisabledByDefault(t *testing.T) {
	handler, _, _ := testServer(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	page, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	csrf := cookieValue(t, jar, server.URL, csrfCookie)
	login, err := client.PostForm(server.URL+"/login", url.Values{
		"csrf_token": {csrf}, "username": {"admin"}, "password": {"StrongPassword123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	response, err := client.Get(server.URL + "/api/integrations/github/repos")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "disabled") {
		t.Fatalf("expected disabled repos API, got status=%d body=%s", response.StatusCode, body)
	}

	// The Add Project page still offers the manual GitHub URL form.
	newPage, err := client.Get(server.URL + "/projects/new")
	if err != nil {
		t.Fatal(err)
	}
	newBody, _ := io.ReadAll(newPage.Body)
	newPage.Body.Close()
	if !strings.Contains(string(newBody), "GitHub repository URL") {
		t.Fatalf("manual GitHub form missing from Add Project page")
	}
}

func TestAdvancedModeGateAndFilesystemExplorer(t *testing.T) {
	handler, _, _ := testServer(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Do not auto-follow redirects so we can assert the gate's 303.
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	page, _ := client.Get(server.URL + "/login")
	page.Body.Close()
	csrf := cookieValue(t, jar, server.URL, csrfCookie)
	login, err := client.PostForm(server.URL+"/login", url.Values{
		"csrf_token": {csrf}, "username": {"admin"}, "password": {"StrongPassword123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	post := func(path string, form url.Values) (int, string) {
		t.Helper()
		token := cookieValue(t, jar, server.URL, csrfCookie)
		request, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response.StatusCode, string(body)
	}

	// Before enabling: page redirects to /advanced, API returns 403.
	pageResp, _ := client.Get(server.URL + "/system/files")
	pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("system files page should redirect when not advanced, got %d", pageResp.StatusCode)
	}
	apiResp, _ := client.Get(server.URL + "/api/system/files")
	apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusForbidden {
		t.Fatalf("system files API should be 403 when not advanced, got %d", apiResp.StatusCode)
	}

	// Wrong password is refused.
	if status, _ := post("/advanced-mode/enable", url.Values{"password": {"wrong"}}); status != http.StatusSeeOther {
		t.Fatalf("enable should redirect, got %d", status)
	}
	stillBlocked, _ := client.Get(server.URL + "/api/system/files")
	stillBlocked.Body.Close()
	if stillBlocked.StatusCode != http.StatusForbidden {
		t.Fatal("advanced mode should not be active after a wrong password")
	}

	// Correct password enables advanced mode.
	if status, _ := post("/advanced-mode/enable", url.Values{"password": {"StrongPassword123"}}); status != http.StatusSeeOther {
		t.Fatalf("enable with correct password should redirect, got %d", status)
	}
	listResp, _ := client.Get(server.URL + "/api/system/files?path=")
	listBody, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("system files API should work once advanced, got %d: %s", listResp.StatusCode, listBody)
	}

	// Create a folder at the advanced root and confirm it lands in the temp root.
	if status, body := post("/api/system/files/new-folder", url.Values{"path": {"."}, "name": {"adv-made"}}); status != http.StatusOK {
		t.Fatalf("advanced new-folder failed: %d %s", status, body)
	}

	// Disabling removes access again.
	if status, _ := post("/advanced-mode/disable", url.Values{}); status != http.StatusSeeOther {
		t.Fatalf("disable should redirect, got %d", status)
	}
	gone, _ := client.Get(server.URL + "/api/system/files")
	gone.Body.Close()
	if gone.StatusCode != http.StatusForbidden {
		t.Fatal("advanced mode should be inactive after disable")
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
	configBody := "app:\n  host: 127.0.0.1\n  port: 8080\n  environment: test\nsecurity:\n  cookie_secure: false\n  session_lifetime_hours: 1\n  login_rate_limit_per_minute: 5\n  advanced_mode:\n    enabled: true\n    root: " + base + "\n    timeout_minutes: 15\npaths:\n  database: " + filepath.Join(base, "vpsdeck.db") + "\n  data_dir: " + filepath.Join(base, "data") + "\n  log_dir: " + filepath.Join(base, "logs") + "\n  backup_dir: " + filepath.Join(base, "backups") + "\n  apps_dir: " + apps + "\n  simple_mode_roots:\n    - " + apps + "\nmonitoring:\n  refresh_seconds: 5\n  ports:\n    enabled: false\n  ollama:\n    enabled: false\n    base_url: http://127.0.0.1:11434\n    timeout_seconds: 1\ndocker:\n  enabled: false\n  discovery_enabled: false\n  command: docker\n  timeout_seconds: 1\n"
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

func loginTestClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	page, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	csrf := cookieValue(t, jar, serverURL, csrfCookie)
	response, err := client.PostForm(serverURL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {"StrongPassword123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return client
}

func sqlNullInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}
