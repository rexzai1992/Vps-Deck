package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/database"
	"github.com/vpsdeck/vpsdeck/internal/doctor"
)

// DoctorPageData is passed to doctor.html.
type DoctorPageData struct {
	Run        *database.DoctorRun
	Results    []database.DoctorResult
	Categories []DoctorCategory
	HasRun     bool
}

// DoctorCategory groups results for template rendering.
type DoctorCategory struct {
	Name    string
	Results []database.DoctorResult
}

// groupByCategory organises results into category buckets in display order.
func groupByCategory(results []database.DoctorResult) []DoctorCategory {
	order := []string{
		string(doctor.CategorySystem),
		string(doctor.CategoryService),
		string(doctor.CategoryNginx),
		string(doctor.CategoryDocker),
		string(doctor.CategoryProjects),
		string(doctor.CategoryFirewall),
	}
	labels := map[string]string{
		string(doctor.CategorySystem):   "System Resources",
		string(doctor.CategoryService):  "Services",
		string(doctor.CategoryNginx):    "Nginx",
		string(doctor.CategoryDocker):   "Docker",
		string(doctor.CategoryProjects): "Project Health",
		string(doctor.CategoryFirewall): "Firewall",
	}
	buckets := make(map[string][]database.DoctorResult)
	for _, r := range results {
		buckets[r.Category] = append(buckets[r.Category], r)
	}
	var cats []DoctorCategory
	for _, key := range order {
		if rows, ok := buckets[key]; ok {
			cats = append(cats, DoctorCategory{Name: labels[key], Results: rows})
			delete(buckets, key)
		}
	}
	// Append any unexpected categories.
	for key, rows := range buckets {
		cats = append(cats, DoctorCategory{Name: key, Results: rows})
	}
	return cats
}

func (s *Server) doctorPage(c *gin.Context) {
	if s.demoMode {
		data := fakeDoctorData()
		s.renderProtected(c, http.StatusOK, "doctor.html", "VPS Doctor", "doctor", data, c.Query("success"), c.Query("error"))
		return
	}

	run, results, err := s.db.LastDoctorRun(c.Request.Context())
	var data DoctorPageData
	if err == nil {
		data.Run = &run
		data.Results = results
		data.Categories = groupByCategory(results)
		data.HasRun = true
	}
	// sql.ErrNoRows means no runs yet — show empty state.

	s.renderProtected(c, http.StatusOK, "doctor.html", "VPS Doctor", "doctor", data, c.Query("success"), c.Query("error"))
}

func (s *Server) runDoctor(c *gin.Context) {
	if s.demoMode {
		c.Redirect(http.StatusSeeOther, "/doctor")
		return
	}

	user, _ := s.currentUser(c)

	ctx := c.Request.Context()
	snap, err := s.doctor.Run(ctx)
	if err != nil {
		s.logger.Error("doctor run", "error", err)
		s.audit(c, &user, "doctor_run", "doctor", "", "full health check", false, err.Error())
		c.Redirect(http.StatusSeeOther, "/doctor?error=Health+check+failed:+"+err.Error())
		return
	}

	// Persist the run.
	dbRun := database.DoctorRun{
		StartedAt:    snap.StartedAt,
		DurationMS:   snap.Duration.Milliseconds(),
		Total:        snap.Summary.Total,
		OKCount:      snap.Summary.OK,
		WarningCount: snap.Summary.Warning,
		FailedCount:  snap.Summary.Failed,
		SkippedCount: snap.Summary.Skipped,
	}
	var dbResults []database.DoctorResult
	for _, ch := range snap.Checks {
		dbResults = append(dbResults, database.DoctorResult{
			CheckID:  ch.ID,
			Name:     ch.Name,
			Category: string(ch.Category),
			Status:   string(ch.Status),
			Message:  ch.Message,
			Detail:   ch.Detail,
		})
	}
	if _, saveErr := s.db.SaveDoctorRun(ctx, dbRun, dbResults); saveErr != nil {
		s.logger.Warn("save doctor run", "error", saveErr)
	}

	s.audit(c, &user, "doctor_run", "doctor", "", "full health check", true, "")
	c.Redirect(http.StatusSeeOther, "/doctor?success=Health+check+complete")
}

func (s *Server) repairDoctor(c *gin.Context) {
	if s.demoMode {
		c.Redirect(http.StatusSeeOther, "/doctor?error=Repair+actions+are+disabled+in+demo+mode.")
		return
	}

	rt := doctor.RepairType(c.PostForm("type"))
	switch rt {
	case doctor.RepairNginxReload, doctor.RepairNginxRestart, doctor.RepairDockerRestart:
		// allowed
	default:
		c.Redirect(http.StatusSeeOther, "/doctor?error=Unknown+repair+type.")
		return
	}

	user, _ := s.currentUser(c)
	result := s.doctor.Repair(c.Request.Context(), rt)

	if result.Success {
		s.audit(c, &user, "doctor_repair", "doctor", string(rt), string(rt), true, "")
		c.Redirect(http.StatusSeeOther, "/doctor?success=Repair+action+complete.+Re-run+the+health+check+to+confirm.")
	} else {
		s.audit(c, &user, "doctor_repair", "doctor", string(rt), string(rt), false, result.Error)
		c.Redirect(http.StatusSeeOther, "/doctor?error=Repair+failed:+"+result.Error)
	}
}

// fakeDoctorData returns demo-mode health check results.
func fakeDoctorData() DoctorPageData {
	now := time.Now().Add(-2 * time.Minute)
	durationMS := int64(412)
	run := database.DoctorRun{
		ID:           1,
		StartedAt:    now,
		DurationMS:   durationMS,
		Total:        8,
		OKCount:      6,
		WarningCount: 1,
		FailedCount:  0,
		SkippedCount: 1,
		CreatedAt:    now,
	}
	results := []database.DoctorResult{
		{CheckID: "system.cpu", Name: "CPU usage", Category: "system", Status: "ok", Message: "14.2% — within normal range.", Detail: "14.2%"},
		{CheckID: "system.memory", Name: "Memory usage", Category: "system", Status: "ok", Message: "41.0% used — OK.", Detail: "1.6 / 4.0 GB (41.0%)"},
		{CheckID: "system.disk", Name: "Disk usage (/)", Category: "system", Status: "warning", Message: "Disk usage high at 81.3% — consider cleaning up.", Detail: "22.6 / 27.8 GB (81.3%)"},
		{CheckID: "service.panel", Name: "VPSDeck panel port", Category: "service", Status: "ok", Message: "Listening on 127.0.0.1:8080.", Detail: "127.0.0.1:8080"},
		{CheckID: "service.nginx", Name: "Nginx service", Category: "service", Status: "ok", Message: "Nginx is active.", Detail: "active"},
		{CheckID: "nginx.config", Name: "Nginx config syntax", Category: "nginx", Status: "ok", Message: "Configuration syntax OK.", Detail: "nginx: configuration file /etc/nginx/nginx.conf test is successful"},
		{CheckID: "service.docker", Name: "Docker service", Category: "docker", Status: "ok", Message: "Docker service is active.", Detail: "active"},
		{CheckID: "firewall.ufw", Name: "Firewall (ufw)", Category: "firewall", Status: "skipped", Message: "ufw not available on this host.", Detail: ""},
	}
	return DoctorPageData{
		Run:        &run,
		Results:    results,
		Categories: groupByCategory(results),
		HasRun:     true,
	}
}
