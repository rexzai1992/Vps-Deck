package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/database"
	"github.com/vpsdeck/vpsdeck/internal/domains"
)

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
}

type DomainsPageData struct {
	Routes            []DomainRouteView
	Projects          []database.Project
	PanelTarget       string
	InternalPortRange string
	NginxAvailable    string
	NginxEnabled      string
	NginxBridge       string
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
	views := make([]DomainRouteView, 0, len(routes))
	for _, route := range routes {
		view := DomainRouteView{
			ID:              route.ID,
			Hostname:        route.Hostname,
			TargetType:      route.TargetType,
			TargetHost:      route.TargetHost,
			TargetPort:      route.TargetPort,
			TargetScheme:    route.TargetScheme,
			SSLStatus:       route.SSLStatus,
			Enabled:         route.Enabled,
			LastError:       route.LastError,
			TargetListening: s.domains.TargetListening(route),
		}
		if route.ProjectID.Valid {
			view.ProjectID = route.ProjectID.Int64
			view.ProjectName = names[route.ProjectID.Int64]
		}
		views = append(views, view)
	}
	data := DomainsPageData{
		Routes:            views,
		Projects:          projects,
		PanelTarget:       fmt.Sprintf("%s:%d", s.cfg.ReverseProxy.PanelBindHost, s.cfg.ReverseProxy.PanelBindPort),
		InternalPortRange: fmt.Sprintf("%d-%d", s.cfg.ReverseProxy.InternalPortStart, s.cfg.ReverseProxy.InternalPortEnd),
		NginxAvailable:    s.cfg.ReverseProxy.NginxSitesAvailable,
		NginxEnabled:      s.cfg.ReverseProxy.NginxSitesEnabled,
		NginxBridge:       s.cfg.ReverseProxy.NginxBridgeInclude,
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
