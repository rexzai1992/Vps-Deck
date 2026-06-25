package doctor

import "time"

// Status represents the result of a single health check.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Category groups related health checks together in the UI.
type Category string

const (
	CategorySystem   Category = "system"
	CategoryService  Category = "service"
	CategoryNginx    Category = "nginx"
	CategoryDocker   Category = "docker"
	CategoryProjects Category = "projects"
	CategoryFirewall Category = "firewall"
)

// CheckResult holds the outcome of a single health check.
type CheckResult struct {
	ID       string
	Name     string
	Category Category
	Status   Status
	Message  string
	Detail   string
}

// RepairType identifies a safe, pre-approved repair action.
type RepairType string

const (
	RepairNginxReload   RepairType = "nginx_reload"
	RepairNginxRestart  RepairType = "nginx_restart"
	RepairDockerRestart RepairType = "docker_restart"
)

// RepairResult holds the outcome of a repair action.
type RepairResult struct {
	Type    RepairType
	Success bool
	Output  string
	Error   string
}

// Summary counts results by status.
type Summary struct {
	Total   int
	OK      int
	Warning int
	Failed  int
	Skipped int
}

// Snapshot is the complete output of a single doctor run.
type Snapshot struct {
	Checks    []CheckResult
	StartedAt time.Time
	Duration  time.Duration
	Summary   Summary
}

// newSummary tallies check results.
func newSummary(checks []CheckResult) Summary {
	s := Summary{Total: len(checks)}
	for _, c := range checks {
		switch c.Status {
		case StatusOK:
			s.OK++
		case StatusWarning:
			s.Warning++
		case StatusFailed:
			s.Failed++
		case StatusSkipped:
			s.Skipped++
		}
	}
	return s
}
