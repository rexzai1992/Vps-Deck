package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/database"
	"github.com/vpsdeck/vpsdeck/internal/domains"
	"github.com/vpsdeck/vpsdeck/internal/nginxscanner"
)

// ScannedRouteView wraps a ScannedRoute with DB-lookup state.
type ScannedRouteView struct {
	nginxscanner.ScannedRoute
	AlreadyImported bool // hostname already exists in proxy_routes
}

type DomainRouteView struct {
	ID              int64
	Hostname        string
	TargetType      string
	ProjectID       int64
	ProjectName     string
	TargetHost      string
	TargetPort      int
	TargetScheme    string
	SSLStatus       string
	Enabled         bool
	LastError       string
	TargetListening bool
	IsManaged       bool
	SourceType      string
	SourceConfigPath string
	CertPath        string
	CertExpiry      *time.Time
	ImportedAt      *time.Time
}

type DomainsPageData struct {
	Routes            []DomainRouteView
	Projects          []database.Project
	PanelTarget       string
	InternalPortRange string
	NginxAvailable    string
	NginxEnabled      string
	NginxBridge       string
	// Scanner results (Sections A and C).
	PanelRoute    *ScannedRouteView
	ScannedRoutes []ScannedRouteView
	ScanErrors    []string
	ScanTime      time.Time
}

// defaultScanOptions returns the standard nginx directories to scan on a
// VPSDeck-managed VPS. Directories that do not exist are silently skipped.
func defaultScanOptions(panelPort int) nginxscanner.ScanOptions {
	return nginxscanner.ScanOptions{
		SitesAvailDirs: []string{
			"/etc/nginx/sites-available",
			"/etc/nginx/vpsdeck/sites-available",
		},
		SitesEnabledDirs: []string{
			"/etc/nginx/sites-enabled",
			"/etc/nginx/vpsdeck/sites-enabled",
		},
		PanelPort: panelPort,
	}
}

func (s *Server) domainsPage(c *gin.Context) {
	if s.demoMode {
		s.renderProtected(c, http.StatusOK, "domains.html", "Domains", "domains", DomainsPageData{}, "", "Domain management is disabled in demo mode.")
		return
	}
	routes, err := s.domains.List(c.Request.Context())
	if err != nil {
		s.logger.Error("list proxy routes", "error", err)
		s.renderProtected(c, http.StatusInternalServerError, "domains.html", "Domains", "domains", DomainsPageData{}, "", "Could not load proxy routes.")
		return
	}
	projects, err := s.projects.List(c.Request.Context())
	if err != nil {
		s.logger.Error("list projects for domains", "error", err)
		s.renderProtected(c, http.StatusInternalServerError, "domains.html", "Domains", "domains", DomainsPageData{}, "", "Could not load projects.")
		return
	}
	names := make(map[int64]string, len(projects))
	for _, project := range projects {
		names[project.ID] = project.Name
	}

	// Build hostname → DB-route map for import dedup.
	importedHostnames := make(map[string]bool, len(routes))
	for _, r := range routes {
		importedHostnames[r.Hostname] = true
	}

	views := make([]DomainRouteView, 0, len(routes))
	for _, route := range routes {
		view := DomainRouteView{
			ID:               route.ID,
			Hostname:         route.Hostname,
			TargetType:       route.TargetType,
			TargetHost:       route.TargetHost,
			TargetPort:       route.TargetPort,
			TargetScheme:     route.TargetScheme,
			SSLStatus:        route.SSLStatus,
			Enabled:          route.Enabled,
			LastError:        route.LastError,
			TargetListening:  s.domains.TargetListening(route),
			IsManaged:        route.IsManaged,
			SourceType:       route.SourceType,
			SourceConfigPath: route.SourceConfigPath,
			CertPath:         route.CertPath,
			CertExpiry:       route.CertExpiry,
			ImportedAt:       route.ImportedAt,
		}
		if route.ProjectID.Valid {
			view.ProjectID = route.ProjectID.Int64
			view.ProjectName = names[route.ProjectID.Int64]
		}
		views = append(views, view)
	}

	// Run nginx scanner (read-only, never modifies files).
	scanResult := nginxscanner.Scan(defaultScanOptions(s.cfg.ReverseProxy.PanelBindPort))

	var panelRoute *ScannedRouteView
	var scannedRoutes []ScannedRouteView
	for _, r := range scanResult.Routes {
		view := ScannedRouteView{
			ScannedRoute:    r,
			AlreadyImported: importedHostnames[r.Hostname],
		}
		if r.TargetType == "panel" {
			panelRoute = &view
		} else {
			scannedRoutes = append(scannedRoutes, view)
		}
	}

	data := DomainsPageData{
		Routes:            views,
		Projects:          projects,
		PanelTarget:       fmt.Sprintf("%s:%d", s.cfg.ReverseProxy.PanelBindHost, s.cfg.ReverseProxy.PanelBindPort),
		InternalPortRange: fmt.Sprintf("%d-%d", s.cfg.ReverseProxy.InternalPortStart, s.cfg.ReverseProxy.InternalPortEnd),
		NginxAvailable:    s.cfg.ReverseProxy.NginxSitesAvailable,
		NginxEnabled:      s.cfg.ReverseProxy.NginxSitesEnabled,
		NginxBridge:       s.cfg.ReverseProxy.NginxBridgeInclude,
		PanelRoute:        panelRoute,
		ScannedRoutes:     scannedRoutes,
		ScanErrors:        scanResult.Errors,
		ScanTime:          scanResult.ScannedAt,
	}
	s.renderProtected(c, http.StatusOK, "domains.html", "Domains", "domains", data, c.Query("success"), c.Query("error"))
}

func (s *Server) createDomainRoute(c *gin.Context) {
	user := s.mustUser(c)
	projectID, err := optionalInt64(c.PostForm("project_id"))
	if err != nil {
		s.audit(c, &user, "proxy_route_create", "proxy_route", "", c.PostForm("hostname"), false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	targetPort, err := optionalPort(c.PostForm("target_port"))
	if err != nil {
		s.audit(c, &user, "proxy_route_create", "proxy_route", "", c.PostForm("hostname"), false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	route, err := s.domains.Create(c.Request.Context(), domains.CreateInput{
		Hostname:   c.PostForm("hostname"),
		TargetType: c.PostForm("target_type"),
		ProjectID:  projectID,
		TargetPort: targetPort,
	})
	if err != nil {
		s.audit(c, &user, "proxy_route_create", "proxy_route", "", c.PostForm("hostname"), false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	s.audit(c, &user, "proxy_route_create", "proxy_route", strconv.FormatInt(route.ID, 10), route.Hostname, true, "")
	c.Redirect(http.StatusSeeOther, "/domains?success="+url.QueryEscape("Route created. Apply it when you are ready to update Nginx."))
}

// importDomainRoute handles POST /domains/import. It creates a DB record for
// an externally-managed nginx config. It never modifies nginx files or reloads nginx.
func (s *Server) importDomainRoute(c *gin.Context) {
	if s.demoMode {
		c.Redirect(http.StatusSeeOther, "/domains?error=Import+is+disabled+in+demo+mode.")
		return
	}

	hostname := strings.TrimSpace(c.PostForm("hostname"))
	configPath := strings.TrimSpace(c.PostForm("source_config_path"))

	if hostname == "" {
		c.Redirect(http.StatusSeeOther, "/domains?error=Hostname+is+required.")
		return
	}
	// Security: only read from nginx config directories.
	if !strings.HasPrefix(configPath, "/etc/nginx/") {
		c.Redirect(http.StatusSeeOther, "/domains?error=Invalid+config+path.")
		return
	}

	ctx := c.Request.Context()

	// Reject if hostname already tracked.
	_, lookupErr := s.db.ProxyRouteByHostname(ctx, hostname)
	if lookupErr == nil {
		c.Redirect(http.StatusSeeOther, "/domains?error="+url.QueryEscape(hostname+" is already tracked in VPSDeck."))
		return
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		s.logger.Error("hostname lookup for import", "error", lookupErr)
		c.Redirect(http.StatusSeeOther, "/domains?error=Database+error.")
		return
	}

	// Re-scan the config file to get fresh, server-side validated data.
	scanned, scanErr := nginxscanner.ScanSingleFile(configPath, s.cfg.ReverseProxy.PanelBindPort)
	if scanErr != nil {
		c.Redirect(http.StatusSeeOther, "/domains?error="+url.QueryEscape("Could not read config: "+scanErr.Error()))
		return
	}

	var match *nginxscanner.ScannedRoute
	for i, r := range scanned {
		if r.Hostname == hostname {
			match = &scanned[i]
			break
		}
	}
	if match == nil {
		c.Redirect(http.StatusSeeOther, "/domains?error="+url.QueryEscape("Hostname "+hostname+" not found in "+configPath))
		return
	}

	sslStatus := "inactive"
	if match.SSLEnabled {
		sslStatus = "active"
	}
	targetScheme := "http"
	if match.SSLEnabled && match.TargetHost != "" {
		// External target scheme stays http unless proxy_pass says https.
		if strings.HasPrefix(match.ProxyPass, "https://") {
			targetScheme = "https"
		}
	}

	route := database.ProxyRoute{
		Hostname:         hostname,
		TargetType:       match.TargetType,
		TargetHost:       match.TargetHost,
		TargetPort:       match.TargetPort,
		TargetScheme:     targetScheme,
		SSLStatus:        sslStatus,
		SourceConfigPath: match.ConfigFile,
		CertPath:         match.CertPath,
		CertExpiry:       match.CertExpiry,
	}

	user, _ := s.currentUser(c)
	_, err := s.db.ImportProxyRoute(ctx, route)
	if err != nil {
		s.audit(c, &user, "proxy_route_import", "proxy_route", "", hostname, false, err.Error())
		c.Redirect(http.StatusSeeOther, "/domains?error="+url.QueryEscape("Import failed: "+err.Error()))
		return
	}

	s.audit(c, &user, "proxy_route_import", "proxy_route", "", hostname, true, "imported from "+configPath)
	c.Redirect(http.StatusSeeOther, "/domains?success="+url.QueryEscape(hostname+" imported — now tracked in VPSDeck."))
}

func (s *Server) testDomainRoute(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	user := s.mustUser(c)
	s.audit(c, &user, "proxy_route_test_started", "proxy_route", strconv.FormatInt(id, 10), "nginx -t", true, "")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	route, err := s.domains.Test(ctx, id)
	if err != nil {
		s.audit(c, &user, "proxy_route_test", "proxy_route", strconv.FormatInt(id, 10), "nginx -t", false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	s.audit(c, &user, "proxy_route_test", "proxy_route", strconv.FormatInt(route.ID, 10), route.Hostname, true, "")
	c.Redirect(http.StatusSeeOther, "/domains?success="+url.QueryEscape("Nginx config test passed."))
}

func (s *Server) applyDomainRoute(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	user := s.mustUser(c)
	s.audit(c, &user, "proxy_route_apply_started", "proxy_route", strconv.FormatInt(id, 10), "nginx -t/reload", true, "")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	route, err := s.domains.Apply(ctx, id)
	if err != nil {
		s.audit(c, &user, "proxy_route_apply", "proxy_route", strconv.FormatInt(id, 10), "nginx -t/reload", false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	s.audit(c, &user, "proxy_route_apply", "proxy_route", strconv.FormatInt(route.ID, 10), route.Hostname, true, "")
	c.Redirect(http.StatusSeeOther, "/domains?success="+url.QueryEscape("Nginx route applied."))
}

func (s *Server) disableDomainRoute(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	user := s.mustUser(c)
	s.audit(c, &user, "proxy_route_disable_started", "proxy_route", strconv.FormatInt(id, 10), "remove enabled symlink", true, "")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	route, err := s.domains.Disable(ctx, id)
	if err != nil {
		s.audit(c, &user, "proxy_route_disable", "proxy_route", strconv.FormatInt(id, 10), "remove enabled symlink", false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	s.audit(c, &user, "proxy_route_disable", "proxy_route", strconv.FormatInt(route.ID, 10), route.Hostname, true, "")
	c.Redirect(http.StatusSeeOther, "/domains?success="+url.QueryEscape("Route disabled and Nginx reloaded."))
}

func (s *Server) deleteDomainRoute(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	user := s.mustUser(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	route, err := s.domains.Delete(ctx, id)
	if err != nil {
		s.audit(c, &user, "proxy_route_delete", "proxy_route", strconv.FormatInt(id, 10), "remove managed config", false, err.Error())
		s.redirectError(c, "/domains", err)
		return
	}
	s.audit(c, &user, "proxy_route_delete", "proxy_route", strconv.FormatInt(route.ID, 10), route.Hostname, true, "")
	c.Redirect(http.StatusSeeOther, "/domains?success="+url.QueryEscape("Route deleted and Nginx reloaded."))
}

func optionalInt64(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid identifier")
	}
	return id, nil
}
