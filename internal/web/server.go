package web

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/auth"
	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/dashboard"
	"github.com/vpsdeck/vpsdeck/internal/database"
	dockerdiscovery "github.com/vpsdeck/vpsdeck/internal/docker"
	projectfiles "github.com/vpsdeck/vpsdeck/internal/files"
	"github.com/vpsdeck/vpsdeck/internal/monitoring"
	"github.com/vpsdeck/vpsdeck/internal/projects"
	webassets "github.com/vpsdeck/vpsdeck/web"
)

const (
	sessionCookie = "vpsdeck_session"
	csrfCookie    = "vpsdeck_csrf"
	userKey       = "current_user"
)

type Server struct {
	cfg       config.Config
	db        *database.DB
	auth      *auth.Service
	projects  *projects.Service
	files     *projectfiles.Service
	dashboard *dashboard.Service
	ports     *monitoring.PortService
	ollama    *monitoring.OllamaService
	docker    *dockerdiscovery.Discovery
	limiter   *auth.RateLimiter
	logger    *slog.Logger
	templates *template.Template
}

type PageData struct {
	Title     string
	Active    string
	User      database.User
	CSRFToken string
	Success   string
	Error     string
	Data      any
}

type FilePageData struct {
	Project     database.Project
	Entries     []projectfiles.Entry
	CurrentPath string
	ParentPath  string
	Breadcrumbs []Breadcrumb
}

type Breadcrumb struct {
	Name string
	Path string
}

type FileEditData struct {
	Project database.Project
	Path    string
	Content string
}

type EnvEntry struct {
	Key    string
	Value  string
	Secret bool
}

type EnvPageData struct {
	Project database.Project
	Entries []EnvEntry
	Exists  bool
}

type DashboardPageData struct {
	dashboard.Summary
	Ports          monitoring.PortSnapshot
	Ollama         monitoring.OllamaSnapshot
	Docker         dockerdiscovery.Snapshot
	RefreshSeconds int
}

type ProjectsPageData struct {
	Projects []database.Project
	Docker   dockerdiscovery.Snapshot
}

type MonitorPageData struct {
	RefreshSeconds int
	Ports          monitoring.PortSnapshot
	Ollama         monitoring.OllamaSnapshot
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func New(cfg config.Config, db *database.DB, authService *auth.Service, logger *slog.Logger) (http.Handler, error) {
	if cfg.App.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	projectService, err := projects.NewService(db, cfg.Paths.SimpleModeRoots)
	if err != nil {
		return nil, err
	}
	templates, err := template.New("").Funcs(template.FuncMap{
		"bytes":    humanBytes,
		"duration": humanDuration,
		"percent":  func(value float64) string { return fmt.Sprintf("%.1f%%", value) },
		"timeago":  timeAgo,
		"initial":  initial,
		"base":     filepath.Base,
		"dir": func(path string) string {
			dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
			if dir == "." {
				return ""
			}
			return dir
		},
	}).ParseFS(webassets.Assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	server := &Server{
		cfg:       cfg,
		db:        db,
		auth:      authService,
		projects:  projectService,
		files:     projectfiles.NewService(cfg.Paths.BackupDir),
		dashboard: dashboard.NewService(db),
		ports: monitoring.NewPortService(
			db,
			cfg.Monitoring.Ports.Enabled,
			time.Duration(cfg.Monitoring.RefreshSeconds)*time.Second,
		),
		ollama: monitoring.NewOllamaService(
			cfg.Monitoring.Ollama.Enabled,
			cfg.Monitoring.Ollama.BaseURL,
			time.Duration(cfg.Monitoring.Ollama.TimeoutSeconds)*time.Second,
			time.Duration(cfg.Monitoring.RefreshSeconds)*time.Second,
		),
		docker: dockerdiscovery.NewDiscovery(
			db,
			cfg.Docker.Enabled && cfg.Docker.DiscoveryEnabled,
			cfg.Docker.Command,
			time.Duration(cfg.Docker.TimeoutSeconds)*time.Second,
			time.Duration(cfg.Monitoring.RefreshSeconds)*time.Second,
		),
		limiter:   auth.NewRateLimiter(cfg.Security.LoginRateLimitPerMinute, time.Minute),
		logger:    logger,
		templates: templates,
	}

	router := gin.New()
	router.Use(gin.Recovery(), server.requestLogger(), server.securityHeaders())
	router.SetHTMLTemplate(templates)

	staticFiles, err := fs.Sub(webassets.Assets, "static")
	if err != nil {
		return nil, fmt.Errorf("prepare static assets: %w", err)
	}
	router.StaticFS("/static", http.FS(staticFiles))
	router.GET("/healthz", server.health)
	router.GET("/", server.home)
	router.GET("/login", server.loginPage)
	router.POST("/login", server.limitBody(1<<20), server.csrfRequired(), server.login)

	protected := router.Group("/")
	protected.Use(server.requireAuth())
	protected.GET("/dashboard", server.dashboardPage)
	protected.POST("/logout", server.limitBody(1<<20), server.csrfRequired(), server.logout)
	protected.GET("/projects", server.projectsPage)
	protected.GET("/projects/new", server.newProjectPage)
	protected.POST("/projects", server.limitBody(1<<20), server.csrfRequired(), server.createProject)
	protected.POST("/projects/import/docker-compose", server.limitBody(1<<20), server.csrfRequired(), server.importDockerCompose)
	protected.GET("/projects/:id", server.projectPage)
	protected.POST("/projects/:id/delete", server.limitBody(1<<20), server.csrfRequired(), server.deleteProject)
	protected.GET("/files", server.fileProjectsPage)
	protected.GET("/projects/:id/files", server.filesPage)
	protected.POST("/projects/:id/files/upload", server.limitBody(100<<20), server.csrfRequired(), server.uploadFile)
	protected.POST("/projects/:id/files/folders", server.limitBody(1<<20), server.csrfRequired(), server.createFolder)
	protected.POST("/projects/:id/files/delete", server.limitBody(1<<20), server.csrfRequired(), server.deleteFile)
	protected.GET("/projects/:id/files/edit", server.editFilePage)
	protected.POST("/projects/:id/files/edit", server.limitBody(4<<20), server.csrfRequired(), server.saveFile)
	protected.GET("/projects/:id/files/download", server.downloadFile)
	protected.GET("/projects/:id/env", server.envPage)
	protected.POST("/projects/:id/env", server.limitBody(4<<20), server.csrfRequired(), server.saveEnv)
	protected.GET("/audit", server.auditPage)
	protected.GET("/network/ports", server.portsPage)
	protected.GET("/ollama", server.ollamaPage)

	api := router.Group("/api")
	api.Use(server.requireAPIAuth())
	api.GET("/folders", server.folderBrowserAPI)
	api.GET("/monitors/ports", server.portsAPI)
	api.GET("/monitors/ollama", server.ollamaAPI)

	return router, nil
}

func (s *Server) home(c *gin.Context) {
	if _, err := s.currentUser(c); err == nil {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) health(c *gin.Context) {
	if err := s.db.PingContext(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "vpsdeck"})
}

func (s *Server) loginPage(c *gin.Context) {
	if _, err := s.currentUser(c); err == nil {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	token := s.ensureCSRF(c)
	s.render(c, http.StatusOK, "login.html", PageData{
		Title:     "Sign in",
		CSRFToken: token,
		Error:     c.Query("error"),
	})
}

func (s *Server) login(c *gin.Context) {
	ip := c.ClientIP()
	if !s.limiter.Allow(ip) {
		s.audit(c, nil, "login_rate_limited", "session", "", "Too many login attempts", false, "rate limit exceeded")
		s.render(c, http.StatusTooManyRequests, "login.html", PageData{
			Title:     "Sign in",
			CSRFToken: s.ensureCSRF(c),
			Error:     "Too many login attempts. Wait one minute and try again.",
		})
		return
	}

	username := c.PostForm("username")
	user, err := s.auth.Authenticate(c.Request.Context(), username, c.PostForm("password"))
	if err != nil {
		s.audit(c, nil, "login_failed", "session", "", "Username: "+strings.TrimSpace(username), false, "invalid credentials")
		s.render(c, http.StatusUnauthorized, "login.html", PageData{
			Title:     "Sign in",
			CSRFToken: s.ensureCSRF(c),
			Error:     "The username or password is incorrect.",
		})
		return
	}

	token, expiresAt, err := s.auth.CreateSession(c.Request.Context(), user.ID)
	if err != nil {
		s.logger.Error("create session", "error", err)
		s.render(c, http.StatusInternalServerError, "login.html", PageData{
			Title:     "Sign in",
			CSRFToken: s.ensureCSRF(c),
			Error:     "Could not create a secure session.",
		})
		return
	}
	s.setSessionCookie(c, token, expiresAt)
	s.limiter.Reset(ip)
	s.audit(c, &user, "login", "session", "", "", true, "")
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

func (s *Server) logout(c *gin.Context) {
	user := s.mustUser(c)
	token, _ := c.Cookie(sessionCookie)
	if err := s.auth.DeleteSession(c.Request.Context(), token); err != nil {
		s.logger.Warn("delete session", "error", err)
	}
	s.clearSessionCookie(c)
	s.audit(c, &user, "logout", "session", "", "", true, "")
	c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) dashboardPage(c *gin.Context) {
	dockerSnapshot := s.docker.Snapshot(c.Request.Context(), false)
	s.docker.SyncRegistered(c.Request.Context(), dockerSnapshot)
	summary, err := s.dashboard.Collect(c.Request.Context())
	if err != nil {
		s.logger.Error("collect dashboard", "error", err)
		s.renderProtected(c, http.StatusInternalServerError, "dashboard.html", "Dashboard", "dashboard", nil, "", "Could not collect server health.")
		return
	}
	data := DashboardPageData{
		Summary:        summary,
		Ports:          s.ports.Snapshot(c.Request.Context()),
		Ollama:         s.ollama.Snapshot(c.Request.Context()),
		Docker:         dockerSnapshot,
		RefreshSeconds: s.cfg.Monitoring.RefreshSeconds,
	}
	s.renderProtected(c, http.StatusOK, "dashboard.html", "Dashboard", "dashboard", data, c.Query("success"), "")
}

func (s *Server) projectsPage(c *gin.Context) {
	dockerSnapshot := s.docker.Snapshot(c.Request.Context(), false)
	s.docker.SyncRegistered(c.Request.Context(), dockerSnapshot)
	items, err := s.projects.List(c.Request.Context())
	if err != nil {
		s.logger.Error("list projects", "error", err)
		s.renderProtected(c, http.StatusInternalServerError, "projects.html", "Projects", "projects", nil, "", "Could not load projects.")
		return
	}
	s.renderProtected(c, http.StatusOK, "projects.html", "Projects", "projects", ProjectsPageData{
		Projects: items,
		Docker:   dockerSnapshot,
	}, c.Query("success"), c.Query("error"))
}

func (s *Server) newProjectPage(c *gin.Context) {
	data := struct {
		Roots []string
	}{Roots: s.projects.AllowedRoots()}
	s.renderProtected(c, http.StatusOK, "project_new.html", "Add existing project", "projects", data, "", c.Query("error"))
}

func (s *Server) folderBrowserAPI(c *gin.Context) {
	rootIndex, err := strconv.Atoi(c.DefaultQuery("root", "0"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project root"})
		return
	}
	showHidden, err := strconv.ParseBool(c.DefaultQuery("hidden", "false"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hidden-folder option"})
		return
	}
	listing, err := s.projects.BrowseFolders(rootIndex, c.Query("path"), showHidden)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, listing)
}

func (s *Server) portsPage(c *gin.Context) {
	data := MonitorPageData{
		RefreshSeconds: s.cfg.Monitoring.RefreshSeconds,
		Ports:          s.ports.Snapshot(c.Request.Context()),
	}
	s.renderProtected(c, http.StatusOK, "ports.html", "Running ports", "ports", data, "", "")
}

func (s *Server) ollamaPage(c *gin.Context) {
	data := MonitorPageData{
		RefreshSeconds: s.cfg.Monitoring.RefreshSeconds,
		Ollama:         s.ollama.Snapshot(c.Request.Context()),
	}
	s.renderProtected(c, http.StatusOK, "ollama.html", "Ollama monitor", "ollama", data, "", "")
}

func (s *Server) portsAPI(c *gin.Context) {
	c.JSON(http.StatusOK, s.ports.Snapshot(c.Request.Context()))
}

func (s *Server) ollamaAPI(c *gin.Context) {
	c.JSON(http.StatusOK, s.ollama.Snapshot(c.Request.Context()))
}

func (s *Server) fileProjectsPage(c *gin.Context) {
	items, err := s.projects.List(c.Request.Context())
	if err != nil {
		s.logger.Error("list projects for files", "error", err)
		s.renderProtected(c, http.StatusInternalServerError, "file_projects.html", "Files", "files", nil, "", "Could not load project folders.")
		return
	}
	s.renderProtected(c, http.StatusOK, "file_projects.html", "Files", "files", items, "", "")
}

func (s *Server) createProject(c *gin.Context) {
	port, err := optionalPort(c.PostForm("port"))
	if err != nil {
		s.redirectError(c, "/projects/new", err)
		return
	}
	project, err := s.projects.CreateExisting(c.Request.Context(), projects.CreateInput{
		Name:           c.PostForm("name"),
		Type:           c.PostForm("type"),
		Domain:         c.PostForm("domain"),
		Port:           port,
		WorkingDir:     c.PostForm("working_dir"),
		HealthcheckURL: c.PostForm("healthcheck_url"),
	})
	user := s.mustUser(c)
	if err != nil {
		s.audit(c, &user, "project_create", "project", "", "Name: "+c.PostForm("name"), false, err.Error())
		s.redirectError(c, "/projects/new", err)
		return
	}
	s.audit(c, &user, "project_create", "project", strconv.FormatInt(project.ID, 10), "Registered "+project.WorkingDir, true, "")
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/projects/%d?success=%s", project.ID, url.QueryEscape("Project registered.")))
}

func (s *Server) importDockerCompose(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	user := s.mustUser(c)
	project, err := s.docker.Import(c.Request.Context(), name)
	if err != nil {
		s.audit(c, &user, "docker_compose_import", "docker_compose", name, "", false, err.Error())
		s.redirectError(c, "/projects", err)
		return
	}
	snapshot := s.docker.Snapshot(c.Request.Context(), true)
	s.docker.SyncRegistered(c.Request.Context(), snapshot)
	s.ports.Invalidate()
	s.audit(c, &user, "docker_compose_import", "project", strconv.FormatInt(project.ID, 10), "Imported Docker Compose project "+name, true, "")
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/projects/%d?success=%s", project.ID, url.QueryEscape("Docker Compose project imported.")))
}

func (s *Server) projectPage(c *gin.Context) {
	dockerSnapshot := s.docker.Snapshot(c.Request.Context(), false)
	s.docker.SyncRegistered(c.Request.Context(), dockerSnapshot)
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	project, err := s.projects.Get(c.Request.Context(), id)
	if database.IsNotFound(err) {
		c.Status(http.StatusNotFound)
		return
	}
	if err != nil {
		s.logger.Error("get project", "error", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	s.renderProtected(c, http.StatusOK, "project_detail.html", project.Name, "projects", project, c.Query("success"), "")
}

func (s *Server) deleteProject(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	project, err := s.projects.Get(c.Request.Context(), id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	user := s.mustUser(c)
	if err := s.projects.Delete(c.Request.Context(), id); err != nil {
		s.audit(c, &user, "project_delete", "project", strconv.FormatInt(id, 10), project.Name, false, err.Error())
		s.redirectError(c, "/projects", err)
		return
	}
	s.audit(c, &user, "project_delete", "project", strconv.FormatInt(id, 10), "Unregistered "+project.Name+"; files preserved", true, "")
	c.Redirect(http.StatusSeeOther, "/projects?success="+url.QueryEscape("Project unregistered. Its files were not deleted."))
}

func (s *Server) filesPage(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	requestedPath := c.Query("path")
	entries, currentPath, err := s.files.List(project, requestedPath)
	if err != nil {
		s.redirectError(c, fmt.Sprintf("/projects/%d", project.ID), err)
		return
	}
	parent := ""
	if currentPath != "." && currentPath != "" {
		parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentPath)))
		if parent == "." {
			parent = ""
		}
	}
	data := FilePageData{
		Project:     project,
		Entries:     entries,
		CurrentPath: currentPath,
		ParentPath:  parent,
		Breadcrumbs: breadcrumbs(currentPath),
	}
	s.renderProtected(c, http.StatusOK, "files.html", project.Name+" files", "files", data, c.Query("success"), c.Query("error"))
}

func (s *Server) uploadFile(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		s.fileActionError(c, project, "file_upload", c.PostForm("path"), err)
		return
	}
	source, err := header.Open()
	if err != nil {
		s.fileActionError(c, project, "file_upload", c.PostForm("path"), err)
		return
	}
	defer source.Close()
	currentPath := c.PostForm("path")
	err = s.files.Upload(project, currentPath, header.Filename, source)
	user := s.mustUser(c)
	if err != nil {
		s.audit(c, &user, "file_upload", "project", strconv.FormatInt(project.ID, 10), header.Filename, false, err.Error())
		s.redirectError(c, filesURL(project.ID, currentPath), err)
		return
	}
	s.audit(c, &user, "file_upload", "project", strconv.FormatInt(project.ID, 10), filepath.Join(currentPath, filepath.Base(header.Filename)), true, "")
	c.Redirect(http.StatusSeeOther, filesURL(project.ID, currentPath)+"&success="+url.QueryEscape("File uploaded."))
}

func (s *Server) createFolder(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	currentPath := c.PostForm("path")
	name := c.PostForm("name")
	err := s.files.CreateFolder(project, currentPath, name)
	user := s.mustUser(c)
	if err != nil {
		s.audit(c, &user, "folder_create", "project", strconv.FormatInt(project.ID, 10), name, false, err.Error())
		s.redirectError(c, filesURL(project.ID, currentPath), err)
		return
	}
	s.audit(c, &user, "folder_create", "project", strconv.FormatInt(project.ID, 10), filepath.Join(currentPath, name), true, "")
	c.Redirect(http.StatusSeeOther, filesURL(project.ID, currentPath)+"&success="+url.QueryEscape("Folder created."))
}

func (s *Server) deleteFile(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	target := c.PostForm("target")
	currentPath := c.PostForm("path")
	err := s.files.Delete(project, target)
	user := s.mustUser(c)
	if err != nil {
		s.audit(c, &user, "file_delete", "project", strconv.FormatInt(project.ID, 10), target, false, err.Error())
		s.redirectError(c, filesURL(project.ID, currentPath), err)
		return
	}
	s.audit(c, &user, "file_delete", "project", strconv.FormatInt(project.ID, 10), target, true, "")
	c.Redirect(http.StatusSeeOther, filesURL(project.ID, currentPath)+"&success="+url.QueryEscape("Item deleted."))
}

func (s *Server) editFilePage(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	path := c.Query("path")
	content, err := s.files.ReadText(project, path)
	if err != nil {
		s.redirectError(c, filesURL(project.ID, filepath.Dir(filepath.FromSlash(path))), err)
		return
	}
	data := FileEditData{Project: project, Path: filepath.ToSlash(path), Content: content}
	s.renderProtected(c, http.StatusOK, "file_edit.html", "Edit "+filepath.Base(path), "projects", data, c.Query("success"), c.Query("error"))
}

func (s *Server) saveFile(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	path := c.PostForm("path")
	err := s.files.SaveText(project, path, c.PostForm("content"))
	user := s.mustUser(c)
	if err != nil {
		s.audit(c, &user, "file_edit", "project", strconv.FormatInt(project.ID, 10), path, false, err.Error())
		s.redirectError(c, fmt.Sprintf("/projects/%d/files/edit?path=%s", project.ID, url.QueryEscape(path)), err)
		return
	}
	s.audit(c, &user, "file_edit", "project", strconv.FormatInt(project.ID, 10), path, true, "")
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/projects/%d/files/edit?path=%s&success=%s", project.ID, url.QueryEscape(path), url.QueryEscape("File saved. A backup was created.")))
}

func (s *Server) downloadFile(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	path, name, err := s.files.DownloadPath(project, c.Query("path"))
	if err != nil {
		s.redirectError(c, filesURL(project.ID, ""), err)
		return
	}
	c.FileAttachment(path, name)
}

func (s *Server) envPage(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	content, err := s.files.ReadOptionalText(project, ".env")
	if err != nil {
		s.redirectError(c, fmt.Sprintf("/projects/%d", project.ID), err)
		return
	}
	data := EnvPageData{
		Project: project,
		Entries: parseEnv(content),
		Exists:  content != "",
	}
	s.renderProtected(c, http.StatusOK, "env.html", project.Name+" environment", "projects", data, c.Query("success"), c.Query("error"))
}

func (s *Server) saveEnv(c *gin.Context) {
	project, ok := s.projectForRequest(c)
	if !ok {
		return
	}
	content, err := formatEnv(c.PostFormArray("key"), c.PostFormArray("value"))
	user := s.mustUser(c)
	if err == nil {
		err = s.files.SaveText(project, ".env", content)
	}
	if err != nil {
		s.audit(c, &user, "env_edit", "project", strconv.FormatInt(project.ID, 10), ".env", false, err.Error())
		s.redirectError(c, fmt.Sprintf("/projects/%d/env", project.ID), err)
		return
	}
	s.audit(c, &user, "env_edit", "project", strconv.FormatInt(project.ID, 10), ".env updated; values redacted", true, "")
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/projects/%d/env?success=%s", project.ID, url.QueryEscape("Environment saved. Previous content was backed up when present.")))
}

func (s *Server) auditPage(c *gin.Context) {
	entries, err := s.db.ListAudit(c.Request.Context(), 200)
	if err != nil {
		s.logger.Error("list audit entries", "error", err)
		s.renderProtected(c, http.StatusInternalServerError, "audit.html", "Audit log", "audit", nil, "", "Could not load audit history.")
		return
	}
	s.renderProtected(c, http.StatusOK, "audit.html", "Audit log", "audit", entries, "", "")
}

func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := s.currentUser(c)
		if err != nil {
			s.clearSessionCookie(c)
			c.Redirect(http.StatusSeeOther, "/login?error="+url.QueryEscape("Please sign in to continue."))
			c.Abort()
			return
		}
		c.Set(userKey, user)
		c.Next()
	}
}

func (s *Server) requireAPIAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := s.currentUser(c)
		if err != nil {
			s.clearSessionCookie(c)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}
		c.Set(userKey, user)
		c.Next()
	}
}

func (s *Server) csrfRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookieToken, _ := c.Cookie(csrfCookie)
		requestToken := c.GetHeader("X-CSRF-Token")
		if requestToken == "" {
			requestToken = c.PostForm("csrf_token")
		}
		if !auth.VerifyCSRF(cookieToken, requestToken) {
			c.String(http.StatusForbidden, "Invalid or expired form token. Refresh the page and try again.")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) limitBody(bytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, bytes)
		c.Next()
	}
}

func (s *Server) currentUser(c *gin.Context) (database.User, error) {
	token, err := c.Cookie(sessionCookie)
	if err != nil {
		return database.User{}, err
	}
	return s.auth.UserFromSession(c.Request.Context(), token)
}

func (s *Server) mustUser(c *gin.Context) database.User {
	value, _ := c.Get(userKey)
	return value.(database.User)
}

func (s *Server) renderProtected(c *gin.Context, status int, templateName, title, active string, data any, success, errorMessage string) {
	s.render(c, status, templateName, PageData{
		Title:     title,
		Active:    active,
		User:      s.mustUser(c),
		CSRFToken: s.ensureCSRF(c),
		Success:   success,
		Error:     errorMessage,
		Data:      data,
	})
}

func (s *Server) render(c *gin.Context, status int, templateName string, data PageData) {
	c.HTML(status, templateName, data)
}

func (s *Server) ensureCSRF(c *gin.Context) string {
	if token, err := c.Cookie(csrfCookie); err == nil && token != "" {
		return token
	}
	token, err := auth.NewCSRFToken()
	if err != nil {
		s.logger.Error("generate CSRF token", "error", err)
		return ""
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   12 * 60 * 60,
		Secure:   s.cfg.Security.CookieSecure,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

func (s *Server) setSessionCookie(c *gin.Context, token string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		Secure:   s.cfg.Security.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   s.cfg.Security.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) audit(c *gin.Context, user *database.User, action, targetType, targetID, details string, success bool, errorMessage string) {
	entry := database.AuditEntry{
		IPAddress:  c.ClientIP(),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Details:    details,
		Success:    success,
		Error:      errorMessage,
	}
	if user != nil {
		entry.UserID = sql.NullInt64{Int64: user.ID, Valid: true}
	}
	if err := s.db.WriteAudit(c.Request.Context(), entry); err != nil {
		s.logger.Warn("write audit entry", "action", action, "error", err)
	}
}

func (s *Server) redirectError(c *gin.Context, destination string, err error) {
	c.Redirect(http.StatusSeeOther, destination+"?error="+url.QueryEscape(err.Error()))
}

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		s.logger.Info("HTTP request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}

func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self'; img-src 'self' data:; script-src 'self'; connect-src 'self'")
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid identifier")
	}
	return id, nil
}

func optionalPort(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("port must be between 1 and 65535")
	}
	return port, nil
}

func humanBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := uint64(unit), 0
	for quotient := value / unit; quotient >= unit; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}

func humanDuration(value time.Duration) string {
	days := value / (24 * time.Hour)
	value %= 24 * time.Hour
	hours := value / time.Hour
	minutes := (value % time.Hour) / time.Minute
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

func timeAgo(value time.Time) string {
	delta := time.Since(value)
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(delta.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(delta.Hours()/24))
	}
}

func initial(value string) string {
	for _, character := range value {
		return string(character)
	}
	return "?"
}

func (s *Server) projectForRequest(c *gin.Context) (database.Project, bool) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return database.Project{}, false
	}
	project, err := s.projects.Get(c.Request.Context(), id)
	if err != nil {
		if !database.IsNotFound(err) {
			s.logger.Error("get project", "id", id, "error", err)
		}
		c.Status(http.StatusNotFound)
		return database.Project{}, false
	}
	return project, true
}

func (s *Server) fileActionError(c *gin.Context, project database.Project, action, currentPath string, err error) {
	user := s.mustUser(c)
	s.audit(c, &user, action, "project", strconv.FormatInt(project.ID, 10), currentPath, false, err.Error())
	s.redirectError(c, filesURL(project.ID, currentPath), err)
}

func filesURL(projectID int64, path string) string {
	return fmt.Sprintf("/projects/%d/files?path=%s", projectID, url.QueryEscape(filepath.ToSlash(path)))
}

func breadcrumbs(path string) []Breadcrumb {
	result := []Breadcrumb{{Name: "Project root", Path: ""}}
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "." || path == "" {
		return result
	}
	var current string
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.ToSlash(filepath.Join(current, part))
		result = append(result, Breadcrumb{Name: part, Path: current})
	}
	return result
}

func parseEnv(content string) []EnvEntry {
	var entries []EnvEntry
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "export "), "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if !envKeyPattern.MatchString(key) {
			continue
		}
		entries = append(entries, EnvEntry{
			Key:    key,
			Value:  parts[1],
			Secret: isSecretKey(key),
		})
	}
	return entries
}

func formatEnv(keys, values []string) (string, error) {
	if len(keys) != len(values) {
		return "", errors.New("environment form is incomplete")
	}
	seen := make(map[string]bool)
	var builder strings.Builder
	for index, rawKey := range keys {
		key := strings.TrimSpace(rawKey)
		value := values[index]
		if key == "" && value == "" {
			continue
		}
		if !envKeyPattern.MatchString(key) {
			return "", fmt.Errorf("invalid environment key %q", key)
		}
		if seen[key] {
			return "", fmt.Errorf("environment key %q is duplicated", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("environment value for %s cannot contain a new line", key)
		}
		seen[key] = true
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

func isSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"PASSWORD", "SECRET", "TOKEN", "API_KEY", "PRIVATE_KEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
