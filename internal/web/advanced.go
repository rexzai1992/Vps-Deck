package web

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

// advancedProject is the synthetic project the full-filesystem explorer operates
// on. Its working directory is the configured Advanced Mode root (default "/"),
// so the file service's containment checks resolve against the whole filesystem.
func (s *Server) advancedProject() database.Project {
	return database.Project{ID: 0, Name: "System", WorkingDir: s.cfg.Security.AdvancedMode.Root}
}

type AdvancedPageData struct {
	Enabled           bool
	Active            bool
	Until             time.Time
	Root              string
	TimeoutMinutes    int
	HasSecondPassword bool
}

type AdvancedFilesData struct {
	Root        string
	CurrentPath string
	Base        string
	APIBase     string
}

func (s *Server) requireAdvancedPage() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.cfg.Security.AdvancedMode.Enabled {
			s.renderProtected(c, http.StatusForbidden, "advanced.html", "Advanced Mode", "advanced", s.advancedData(c), "", "Advanced Mode is disabled in the VPSDeck configuration.")
			c.Abort()
			return
		}
		if !s.advancedActive(c) {
			c.Redirect(http.StatusSeeOther, "/advanced?error="+url.QueryEscape("Enter Advanced Mode to use system tools."))
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) requireAdvancedAPI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.cfg.Security.AdvancedMode.Enabled || !s.advancedActive(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Advanced Mode is required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) advancedData(c *gin.Context) AdvancedPageData {
	data := AdvancedPageData{
		Enabled:           s.cfg.Security.AdvancedMode.Enabled,
		Root:              s.cfg.Security.AdvancedMode.Root,
		TimeoutMinutes:    s.cfg.Security.AdvancedMode.TimeoutMinutes,
		HasSecondPassword: s.cfg.Security.AdvancedMode.SecondPassword != "",
	}
	if token, err := c.Cookie(sessionCookie); err == nil {
		data.Active, data.Until = s.auth.AdvancedStatus(c.Request.Context(), token)
	}
	return data
}

func (s *Server) advancedPage(c *gin.Context) {
	s.renderProtected(c, http.StatusOK, "advanced.html", "Advanced Mode", "advanced", s.advancedData(c), c.Query("success"), c.Query("error"))
}

func (s *Server) enableAdvanced(c *gin.Context) {
	user := s.mustUser(c)
	if !s.cfg.Security.AdvancedMode.Enabled {
		s.redirectError(c, "/advanced", errors.New("Advanced Mode is disabled"))
		return
	}
	if _, err := s.auth.Authenticate(c.Request.Context(), user.Username, c.PostForm("password")); err != nil {
		s.audit(c, &user, "advanced_enable", "session", "", "", false, "password confirmation failed")
		s.redirectError(c, "/advanced", errors.New("incorrect password"))
		return
	}
	if expected := s.cfg.Security.AdvancedMode.SecondPassword; expected != "" {
		if c.PostForm("second_password") != expected {
			s.audit(c, &user, "advanced_enable", "session", "", "", false, "second password failed")
			s.redirectError(c, "/advanced", errors.New("incorrect second password"))
			return
		}
	}
	token, _ := c.Cookie(sessionCookie)
	timeout := time.Duration(s.cfg.Security.AdvancedMode.TimeoutMinutes) * time.Minute
	if _, err := s.auth.EnableAdvanced(c.Request.Context(), token, timeout); err != nil {
		s.redirectError(c, "/advanced", errors.New("could not enable Advanced Mode"))
		return
	}
	s.audit(c, &user, "advanced_enable", "session", "", "Advanced Mode enabled", true, "")
	c.Redirect(http.StatusSeeOther, "/system/files?success="+url.QueryEscape("Advanced Mode is active."))
}

func (s *Server) disableAdvanced(c *gin.Context) {
	user := s.mustUser(c)
	token, _ := c.Cookie(sessionCookie)
	if err := s.auth.DisableAdvanced(c.Request.Context(), token); err != nil {
		s.logger.Warn("disable advanced", "error", err)
	}
	s.audit(c, &user, "advanced_disable", "session", "", "Advanced Mode disabled", true, "")
	c.Redirect(http.StatusSeeOther, "/advanced?success="+url.QueryEscape("Advanced Mode disabled."))
}

// --- full-filesystem explorer handlers (gated by requireAdvanced*) -----------

func (s *Server) advancedFilesPage(c *gin.Context) {
	data := AdvancedFilesData{
		Root:        s.cfg.Security.AdvancedMode.Root,
		CurrentPath: c.Query("path"),
		Base:        "/system/files",
		APIBase:     "/api/system/files",
	}
	s.renderProtected(c, http.StatusOK, "advanced_files.html", "System files", "system-files", data, c.Query("success"), c.Query("error"))
}

func (s *Server) advancedListAPI(c *gin.Context)   { s.respondFileList(c, s.advancedProject()) }
func (s *Server) advancedMoveAPI(c *gin.Context)   { s.transferFiles(c, s.advancedProject(), false) }
func (s *Server) advancedCopyAPI(c *gin.Context)   { s.transferFiles(c, s.advancedProject(), true) }
func (s *Server) advancedRenameAPI(c *gin.Context) { s.renameFileFor(c, s.advancedProject()) }
func (s *Server) advancedDeleteAPI(c *gin.Context) { s.deleteFilesFor(c, s.advancedProject()) }
func (s *Server) advancedNewFileAPI(c *gin.Context) {
	s.newFileFor(c, s.advancedProject())
}
func (s *Server) advancedNewFolderAPI(c *gin.Context) {
	s.newFolderFor(c, s.advancedProject())
}
func (s *Server) advancedUploadAPI(c *gin.Context) { s.uploadFilesFor(c, s.advancedProject()) }

func (s *Server) advancedDownloadZip(c *gin.Context) {
	s.streamFolderZip(c, s.advancedProject(), c.Query("path"))
}

func (s *Server) advancedDownloadFile(c *gin.Context) {
	path, name, err := s.files.DownloadPath(s.advancedProject(), c.Query("path"))
	if err != nil {
		s.redirectError(c, "/system/files", err)
		return
	}
	c.FileAttachment(path, name)
}

func (s *Server) advancedEditPage(c *gin.Context) {
	project := s.advancedProject()
	path := c.Query("path")
	content, err := s.files.ReadText(project, path)
	if err != nil {
		s.redirectError(c, "/system/files?path="+url.QueryEscape(filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))), err)
		return
	}
	data := FileEditData{Project: project, Path: filepath.ToSlash(path), Content: content, Base: "/system/files"}
	s.renderProtected(c, http.StatusOK, "file_edit.html", "Edit "+filepath.Base(path), "system-files", data, c.Query("success"), c.Query("error"))
}

func (s *Server) advancedSaveFile(c *gin.Context) {
	project := s.advancedProject()
	user := s.mustUser(c)
	path := c.PostForm("path")
	if err := s.files.SaveText(project, path, c.PostForm("content")); err != nil {
		s.audit(c, &user, "file_edit", "system", "0", path, false, err.Error())
		s.redirectError(c, "/system/files/edit?path="+url.QueryEscape(path), err)
		return
	}
	s.audit(c, &user, "file_edit", "system", "0", path, true, "")
	c.Redirect(http.StatusSeeOther, "/system/files/edit?path="+url.QueryEscape(path)+"&success="+url.QueryEscape("File saved. A backup was created."))
}
