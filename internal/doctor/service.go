package doctor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

// Service runs VPS health checks and optional repair actions.
type Service struct {
	db       *database.DB
	runner   CommandRunner
	repairer Repairer
	panelPort int
	httpClient *http.Client
}

// NewService creates a Service. runner must implement both CommandRunner and Repairer
// (RealRunner satisfies both; FakeRunner satisfies both in tests).
func NewService(db *database.DB, runner interface {
	CommandRunner
	Repairer
}, panelPort int) *Service {
	return &Service{
		db:        db,
		runner:    runner,
		repairer:  runner,
		panelPort: panelPort,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Run executes all health checks and returns a Snapshot.
func (s *Service) Run(ctx context.Context) (Snapshot, error) {
	start := time.Now()

	checks := []CheckResult{
		s.checkCPU(ctx),
		s.checkMemory(ctx),
		s.checkDisk(ctx),
		s.checkPanelPort(ctx),
		s.checkNginxService(ctx),
		s.checkNginxConfig(ctx),
		s.checkDockerService(ctx),
		s.checkFirewall(ctx),
	}

	// Add per-project health checks for projects with a configured URL.
	projects, err := s.db.ListProjects(ctx)
	if err == nil {
		for _, p := range projects {
			if p.HealthcheckURL != "" {
				checks = append(checks, s.checkProjectHealth(ctx, p))
			}
		}
	}

	snap := Snapshot{
		Checks:    checks,
		StartedAt: start,
		Duration:  time.Since(start),
		Summary:   newSummary(checks),
	}
	return snap, nil
}

// Repair executes a pre-approved repair action.
func (s *Service) Repair(ctx context.Context, rt RepairType) RepairResult {
	out, err := s.repairer.RunRepair(ctx, rt)
	if err != nil {
		return RepairResult{Type: rt, Success: false, Output: out, Error: err.Error()}
	}
	return RepairResult{Type: rt, Success: true, Output: out}
}

// ─── System checks (gopsutil, no subprocess) ────────────────────────────────

func (s *Service) checkCPU(ctx context.Context) CheckResult {
	r := CheckResult{ID: "system.cpu", Name: "CPU usage", Category: CategorySystem}
	percents, err := cpu.PercentWithContext(ctx, 150*time.Millisecond, false)
	if err != nil || len(percents) == 0 {
		r.Status = StatusSkipped
		r.Message = "Could not read CPU usage."
		return r
	}
	pct := percents[0]
	r.Detail = fmt.Sprintf("%.1f%%", pct)
	switch {
	case pct >= 95:
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("CPU is critically high at %.1f%%.", pct)
	case pct >= 80:
		r.Status = StatusWarning
		r.Message = fmt.Sprintf("CPU usage is elevated at %.1f%%.", pct)
	default:
		r.Status = StatusOK
		r.Message = fmt.Sprintf("%.1f%% — within normal range.", pct)
	}
	return r
}

func (s *Service) checkMemory(ctx context.Context) CheckResult {
	r := CheckResult{ID: "system.memory", Name: "Memory usage", Category: CategorySystem}
	v, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		r.Status = StatusSkipped
		r.Message = "Could not read memory statistics."
		return r
	}
	pct := v.UsedPercent
	usedGB := float64(v.Used) / (1 << 30)
	totalGB := float64(v.Total) / (1 << 30)
	r.Detail = fmt.Sprintf("%.1f / %.1f GB (%.1f%%)", usedGB, totalGB, pct)
	switch {
	case pct >= 95:
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Memory critically high: %.1f%% used.", pct)
	case pct >= 85:
		r.Status = StatusWarning
		r.Message = fmt.Sprintf("Memory usage elevated at %.1f%%.", pct)
	default:
		r.Status = StatusOK
		r.Message = fmt.Sprintf("%.1f%% used — OK.", pct)
	}
	return r
}

func (s *Service) checkDisk(ctx context.Context) CheckResult {
	r := CheckResult{ID: "system.disk", Name: "Disk usage (/)", Category: CategorySystem}
	u, err := disk.UsageWithContext(ctx, "/")
	if err != nil {
		r.Status = StatusSkipped
		r.Message = "Could not read disk usage."
		return r
	}
	pct := u.UsedPercent
	usedGB := float64(u.Used) / (1 << 30)
	totalGB := float64(u.Total) / (1 << 30)
	r.Detail = fmt.Sprintf("%.1f / %.1f GB (%.1f%%)", usedGB, totalGB, pct)
	switch {
	case pct >= 90:
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Disk critically full: %.1f%% used.", pct)
	case pct >= 80:
		r.Status = StatusWarning
		r.Message = fmt.Sprintf("Disk usage high at %.1f%% — consider cleaning up.", pct)
	default:
		r.Status = StatusOK
		r.Message = fmt.Sprintf("%.1f%% used — OK.", pct)
	}
	return r
}

// ─── TCP dial check ──────────────────────────────────────────────────────────

func (s *Service) checkPanelPort(ctx context.Context) CheckResult {
	r := CheckResult{ID: "service.panel", Name: "VPSDeck panel port", Category: CategoryService}
	addr := fmt.Sprintf("127.0.0.1:%d", s.panelPort)
	r.Detail = addr
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(tctx, "tcp", addr)
	if err != nil {
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Panel not reachable on %s.", addr)
		return r
	}
	conn.Close()
	r.Status = StatusOK
	r.Message = fmt.Sprintf("Listening on %s.", addr)
	return r
}

// ─── Service checks (subprocess via allowlist) ───────────────────────────────

func (s *Service) checkNginxService(ctx context.Context) CheckResult {
	r := CheckResult{ID: "service.nginx", Name: "Nginx service", Category: CategoryService}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, code, err := s.runner.Run(tctx, "systemctl", "is-active", "nginx")
	if err != nil {
		// systemctl not available on this host (macOS dev, container, etc.)
		r.Status = StatusSkipped
		r.Message = "systemctl not available on this host."
		return r
	}
	r.Detail = out
	if code == 0 && strings.TrimSpace(out) == "active" {
		r.Status = StatusOK
		r.Message = "Nginx is active."
	} else {
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Nginx is not active (state: %s).", strings.TrimSpace(out))
	}
	return r
}

func (s *Service) checkNginxConfig(ctx context.Context) CheckResult {
	r := CheckResult{ID: "nginx.config", Name: "Nginx config syntax", Category: CategoryNginx}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, code, err := s.runner.Run(tctx, "nginx", "-t")
	if err != nil {
		r.Status = StatusSkipped
		r.Message = "nginx binary not available on this host."
		return r
	}
	r.Detail = out
	if code == 0 {
		r.Status = StatusOK
		r.Message = "Configuration syntax OK."
	} else {
		r.Status = StatusFailed
		r.Message = "Nginx config has errors — check the detail."
	}
	return r
}

func (s *Service) checkDockerService(ctx context.Context) CheckResult {
	r := CheckResult{ID: "service.docker", Name: "Docker service", Category: CategoryDocker}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, code, err := s.runner.Run(tctx, "systemctl", "is-active", "docker")
	if err != nil {
		r.Status = StatusSkipped
		r.Message = "systemctl not available on this host."
		return r
	}
	r.Detail = out
	state := strings.TrimSpace(out)
	if code == 0 && state == "active" {
		r.Status = StatusOK
		r.Message = "Docker service is active."
	} else if state == "inactive" || state == "unknown" {
		// Docker is not installed — treat as warning, not failure.
		r.Status = StatusWarning
		r.Message = fmt.Sprintf("Docker service is not running (state: %s).", state)
	} else {
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Docker service problem (state: %s).", state)
	}
	return r
}

func (s *Service) checkFirewall(ctx context.Context) CheckResult {
	r := CheckResult{ID: "firewall.ufw", Name: "Firewall (ufw)", Category: CategoryFirewall}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, _, err := s.runner.Run(tctx, "ufw", "status")
	if err != nil {
		r.Status = StatusSkipped
		r.Message = "ufw not available on this host."
		return r
	}
	r.Detail = out
	if strings.Contains(out, "Status: active") {
		r.Status = StatusOK
		r.Message = "Firewall is active."
	} else if strings.Contains(out, "Status: inactive") {
		r.Status = StatusWarning
		r.Message = "Firewall (ufw) is installed but inactive."
	} else {
		r.Status = StatusSkipped
		r.Message = "Could not determine firewall status."
	}
	return r
}

// ─── Project health checks ───────────────────────────────────────────────────

func (s *Service) checkProjectHealth(ctx context.Context, p database.Project) CheckResult {
	r := CheckResult{
		ID:       fmt.Sprintf("project.health.%d", p.ID),
		Name:     p.Name + " health check",
		Category: CategoryProjects,
	}
	r.Detail = p.HealthcheckURL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.HealthcheckURL, nil)
	if err != nil {
		r.Status = StatusSkipped
		r.Message = "Invalid health check URL."
		return r
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Health check failed: %v", err)
		return r
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		r.Status = StatusOK
		r.Message = fmt.Sprintf("Responded %d.", resp.StatusCode)
	} else {
		r.Status = StatusFailed
		r.Message = fmt.Sprintf("Responded %d — may be unhealthy.", resp.StatusCode)
	}
	return r
}
