package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/demo"
	"github.com/vpsdeck/vpsdeck/internal/selfupdate"
)

type UpdatesPageData struct {
	Update         selfupdate.Snapshot
	RefreshSeconds int
}

func (s *Server) updatesPage(c *gin.Context) {
	if s.demoMode {
		data := UpdatesPageData{
			Update:         demo.UpdateSnapshot(),
			RefreshSeconds: s.cfg.Monitoring.RefreshSeconds,
		}
		s.renderProtected(c, http.StatusOK, "updates.html", "Updates", "updates", data, c.Query("success"), c.Query("error"))
		return
	}
	data := UpdatesPageData{
		Update:         s.update.Snapshot(c.Request.Context(), false),
		RefreshSeconds: s.cfg.Monitoring.RefreshSeconds,
	}
	s.renderProtected(c, http.StatusOK, "updates.html", "Updates", "updates", data, c.Query("success"), c.Query("error"))
}

func (s *Server) updateStatusAPI(c *gin.Context) {
	c.JSON(http.StatusOK, s.update.Snapshot(c.Request.Context(), false))
}

func (s *Server) checkUpdate(c *gin.Context) {
	user := s.mustUser(c)
	snapshot := s.update.Snapshot(c.Request.Context(), true)
	s.audit(c, &user, "update_check", "system", "", snapshot.RemoteShort, snapshot.Error == "", snapshot.Error)
	c.Redirect(http.StatusSeeOther, "/system/updates")
}

func (s *Server) applyUpdate(c *gin.Context) {
	user := s.mustUser(c)
	if err := s.update.RequestUpdate(); err != nil {
		s.audit(c, &user, "update_apply", "system", "", "", false, err.Error())
		s.redirectError(c, "/system/updates", err)
		return
	}
	s.audit(c, &user, "update_apply", "system", "", "Update requested", true, "")
	c.Redirect(http.StatusSeeOther, "/system/updates?success=Update+requested.+VPSDeck+will+rebuild+and+restart+shortly.")
}
