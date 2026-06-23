package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/auth"
)

const githubStateCookie = "vpsdeck_gh_state"

func (s *Server) githubConnect(c *gin.Context) {
	if !s.github.Enabled() {
		s.redirectError(c, "/projects/new", errors.New("the GitHub integration is not configured on this server"))
		return
	}
	state, err := auth.NewCSRFToken()
	if err != nil {
		s.redirectError(c, "/projects/new", errors.New("could not start the GitHub connection"))
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     githubStateCookie,
		Value:    state,
		Path:     "/integrations/github",
		MaxAge:   600,
		Secure:   s.cfg.Security.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	c.Redirect(http.StatusSeeOther, s.github.AuthorizeURL(state))
}

func (s *Server) githubCallback(c *gin.Context) {
	user := s.mustUser(c)
	s.clearGitHubState(c)

	if reason := c.Query("error"); reason != "" {
		s.audit(c, &user, "github_connect", "integration", "github", "", false, reason)
		s.redirectError(c, "/projects/new", errors.New("GitHub authorization was cancelled"))
		return
	}
	cookieState, _ := c.Cookie(githubStateCookie)
	if !auth.VerifyCSRF(cookieState, c.Query("state")) {
		s.audit(c, &user, "github_connect", "integration", "github", "", false, "state mismatch")
		s.redirectError(c, "/projects/new", errors.New("the GitHub connection could not be verified; try again"))
		return
	}
	account, err := s.github.HandleCallback(c.Request.Context(), user.ID, c.Query("code"))
	if err != nil {
		s.audit(c, &user, "github_connect", "integration", "github", "", false, err.Error())
		s.redirectError(c, "/projects/new", err)
		return
	}
	s.audit(c, &user, "github_connect", "integration", "github", "Connected as @"+account.Login, true, "")
	c.Redirect(http.StatusSeeOther, "/projects/new?success="+url.QueryEscape("Connected GitHub as @"+account.Login+"."))
}

func (s *Server) githubDisconnect(c *gin.Context) {
	user := s.mustUser(c)
	if err := s.github.Disconnect(c.Request.Context(), user.ID); err != nil {
		s.audit(c, &user, "github_disconnect", "integration", "github", "", false, err.Error())
		s.redirectError(c, "/projects/new", err)
		return
	}
	s.audit(c, &user, "github_disconnect", "integration", "github", "Disconnected GitHub", true, "")
	c.Redirect(http.StatusSeeOther, "/projects/new?success="+url.QueryEscape("GitHub account disconnected."))
}

func (s *Server) githubReposAPI(c *gin.Context) {
	if !s.github.Enabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the GitHub integration is disabled"})
		return
	}
	user := s.mustUser(c)
	repos, err := s.github.Repositories(c.Request.Context(), user.ID, c.Query("q"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"repos": repos})
}

func (s *Server) githubBranchesAPI(c *gin.Context) {
	if !s.github.Enabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the GitHub integration is disabled"})
		return
	}
	user := s.mustUser(c)
	branches, err := s.github.Branches(c.Request.Context(), user.ID, c.Query("owner"), c.Query("repo"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches})
}

func (s *Server) clearGitHubState(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     githubStateCookie,
		Value:    "",
		Path:     "/integrations/github",
		MaxAge:   -1,
		Secure:   s.cfg.Security.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// gitHubAccountFor returns the connected account login + avatar for the UI.
func (s *Server) gitHubAccountFor(c *gin.Context) (login, avatar string, connected bool) {
	user := s.mustUser(c)
	account, ok := s.github.Account(c.Request.Context(), user.ID)
	if !ok {
		return "", "", false
	}
	return account.Login, account.AvatarURL, true
}

// importGitHubToken returns the caller's stored token when a picker selection or
// a private repository requires authenticated cloning.
func (s *Server) importGitHubToken(c *gin.Context, needed bool) string {
	if !needed || !s.github.Enabled() {
		return ""
	}
	user := s.mustUser(c)
	token, err := s.github.TokenForUser(c.Request.Context(), user.ID)
	if err != nil {
		s.logger.Warn("github token lookup", "user", strconv.FormatInt(user.ID, 10), "error", err)
		return ""
	}
	return token
}
