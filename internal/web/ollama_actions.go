package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) ollamaGenerate(c *gin.Context) {
	model := strings.TrimSpace(c.PostForm("model"))
	prompt := c.PostForm("prompt")
	user := s.mustUser(c)

	// Clear the per-response write deadline so a slow model isn't cut off by the
	// server's 60s WriteTimeout, and bound the work with a context instead.
	if rc := http.NewResponseController(c.Writer); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	response, err := s.ollama.Generate(ctx, model, prompt)
	if err != nil {
		s.audit(c, &user, "ollama_generate", "ollama", model, "", false, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, &user, "ollama_generate", "ollama", model, "prompt run", true, "")
	c.JSON(http.StatusOK, gin.H{"response": response})
}

func (s *Server) ollamaPull(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	user := s.mustUser(c)
	if err := s.ollama.StartPull(name); err != nil {
		s.audit(c, &user, "ollama_pull", "ollama", name, "", false, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, &user, "ollama_pull", "ollama", name, "pull requested", true, "")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) ollamaPullStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"pulls": s.ollama.ActivePulls()})
}

func (s *Server) ollamaDelete(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	user := s.mustUser(c)
	if err := s.ollama.Delete(c.Request.Context(), name); err != nil {
		s.audit(c, &user, "ollama_delete", "ollama", name, "", false, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, &user, "ollama_delete", "ollama", name, "model deleted", true, "")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
