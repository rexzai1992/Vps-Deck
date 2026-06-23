package demo

import (
	"database/sql"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/dashboard"
	"github.com/vpsdeck/vpsdeck/internal/database"
	dockerpkg "github.com/vpsdeck/vpsdeck/internal/docker"
	"github.com/vpsdeck/vpsdeck/internal/monitoring"
	"github.com/vpsdeck/vpsdeck/internal/selfupdate"
)

func DashboardSummary() dashboard.Summary {
	return dashboard.Summary{
		CPUPercent:    14.3,
		MemoryUsed:    1_677_721_600,
		MemoryTotal:   4_294_967_296,
		MemoryPercent: 39.1,
		DiskUsed:      18_253_611_008,
		DiskTotal:     85_899_345_920,
		DiskPercent:   21.3,
		Uptime:        14*24*time.Hour + 6*time.Hour + 32*time.Minute,
		OS:            "Ubuntu 24.04",
		Kernel:        "6.8.0-51-generic",
		Architecture:  "amd64",
		LocalIP:       "10.0.0.4",
		ProjectCounts: database.ProjectCounts{Total: 4, Running: 3, Stopped: 1},
		CollectedAt:   time.Now(),
	}
}

func Projects() []database.Project {
	now := time.Now()
	return []database.Project{
		{
			ID: 1, Name: "storefront", Type: "docker-compose",
			Domain: "shop.izzul.xyz", Port: 3000,
			WorkingDir: "/opt/apps/storefront", Status: "running",
			CreatedAt: now.Add(-30 * 24 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID: 2, Name: "api-gateway", Type: "docker-compose",
			Domain: "api.izzul.xyz", Port: 8080,
			WorkingDir: "/opt/apps/api-gateway", Status: "running",
			CreatedAt: now.Add(-45 * 24 * time.Hour), UpdatedAt: now.Add(-1 * time.Hour),
		},
		{
			ID: 3, Name: "blog", Type: "docker-compose",
			Domain: "blog.izzul.xyz", Port: 4000,
			WorkingDir: "/opt/apps/blog", Status: "running",
			CreatedAt: now.Add(-7 * 24 * time.Hour), UpdatedAt: now.Add(-30 * time.Minute),
		},
		{
			ID: 4, Name: "admin-panel", Type: "docker-compose",
			Domain: "admin.izzul.xyz", Port: 5000,
			WorkingDir: "/opt/apps/admin-panel", Status: "stopped",
			CreatedAt: now.Add(-14 * 24 * time.Hour), UpdatedAt: now.Add(-5 * 24 * time.Hour),
		},
	}
}

func ProjectByID(id int64) (database.Project, bool) {
	for _, p := range Projects() {
		if p.ID == id {
			return p, true
		}
	}
	return database.Project{}, false
}

func Deployments() []database.Deployment {
	now := time.Now()
	ts := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	return []database.Deployment{
		{
			ID: 5, ProjectID: 3, ProjectName: "blog", Action: "deploy", State: "success",
			CommitBefore: "a1b2c3d", CommitAfter: "e4f5g6h",
			Output:    "Pulling ghcr.io/izzul/blog:latest\nContainer restarted successfully.\nHealth check passed.",
			CreatedAt: now.Add(-1*time.Hour - 2*time.Minute), FinishedAt: ts(-1 * time.Hour),
		},
		{
			ID: 4, ProjectID: 1, ProjectName: "storefront", Action: "deploy", State: "success",
			CommitBefore: "f8e7d6c", CommitAfter: "a1b2c3d",
			Output:    "Pulling ghcr.io/izzul/storefront:latest\n3 containers restarted.\nHealth check passed.",
			CreatedAt: now.Add(-6*time.Hour - 3*time.Minute), FinishedAt: ts(-6 * time.Hour),
		},
		{
			ID: 3, ProjectID: 2, ProjectName: "api-gateway", Action: "deploy", State: "success",
			CommitBefore: "1a2b3c4", CommitAfter: "f8e7d6c",
			Output:    "Pulling ghcr.io/izzul/api:latest\n2 containers restarted.\nHealth check passed.",
			CreatedAt: now.Add(-24*time.Hour - 1*time.Minute), FinishedAt: ts(-24 * time.Hour),
		},
		{
			ID: 2, ProjectID: 4, ProjectName: "admin-panel", Action: "deploy", State: "failed",
			CommitBefore: "9z8y7x6", CommitAfter: "",
			Error:     "Build failed: missing environment variable DATABASE_URL",
			CreatedAt: now.Add(-3*24*time.Hour - 5*time.Minute), FinishedAt: ts(-3 * 24 * time.Hour),
		},
		{
			ID: 1, ProjectID: 1, ProjectName: "storefront", Action: "deploy", State: "success",
			CommitBefore: "0a9b8c7", CommitAfter: "1a2b3c4",
			Output:    "Pulling ghcr.io/izzul/storefront:latest\n3 containers restarted.",
			CreatedAt: now.Add(-7*24*time.Hour - 10*time.Minute), FinishedAt: ts(-7 * 24 * time.Hour),
		},
	}
}

func DeploymentsByProjectID(projectID int64) []database.Deployment {
	var result []database.Deployment
	for _, d := range Deployments() {
		if d.ProjectID == projectID {
			result = append(result, d)
		}
	}
	return result
}

func PortSnapshot() monitoring.PortSnapshot {
	return monitoring.PortSnapshot{
		Enabled:    true,
		Status:     "running",
		TCPCount:   8,
		UDPCount:   2,
		TotalCount: 10,
		Listeners: []monitoring.PortListener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 80, Exposure: "public", ExposureKey: "public", Process: "nginx"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 443, Exposure: "public", ExposureKey: "public", Process: "nginx"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 22, Exposure: "public", ExposureKey: "public", Process: "sshd"},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 8080, Exposure: "loopback", ExposureKey: "loopback", Process: "vpsdeck"},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 3000, Exposure: "loopback", ExposureKey: "loopback", Process: "node", Projects: []monitoring.PortProject{{ID: 1, Name: "storefront"}}},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 4000, Exposure: "loopback", ExposureKey: "loopback", Process: "node", Projects: []monitoring.PortProject{{ID: 3, Name: "blog"}}},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 5432, Exposure: "loopback", ExposureKey: "loopback", Process: "postgres"},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, Exposure: "loopback", ExposureKey: "loopback", Process: "redis-server"},
			{Protocol: "udp", Address: "0.0.0.0", Port: 53, Exposure: "public", ExposureKey: "public", Process: "systemd-resolved"},
			{Protocol: "udp", Address: "127.0.0.1", Port: 123, Exposure: "loopback", ExposureKey: "loopback", Process: "chronyd"},
		},
		CollectedAt: time.Now(),
	}
}

func OllamaSnapshot() monitoring.OllamaSnapshot {
	now := time.Now()
	successAt := now.Add(-30 * time.Second)
	expiresAt := now.Add(5 * time.Minute)
	return monitoring.OllamaSnapshot{
		Enabled:           true,
		Status:            "running",
		Version:           "0.3.12",
		BaseURL:           "http://127.0.0.1:11434",
		LocalEndpoint:     true,
		ExecutablePresent: true,
		InstalledCount:    2,
		RunningCount:      1,
		TotalVRAM:         8_589_934_592,
		InstalledModels: []monitoring.OllamaInstalledModel{
			{
				Name: "llama3.2:latest", Model: "llama3.2:latest",
				ModifiedAt: now.Add(-3 * 24 * time.Hour), Size: 2_019_393_024, Digest: "dde5aa3fc5ff",
				Details: monitoring.OllamaModelDetails{Family: "llama", ParameterSize: "3.2B", QuantizationLevel: "Q4_K_M"},
			},
			{
				Name: "mistral:7b", Model: "mistral:7b",
				ModifiedAt: now.Add(-7 * 24 * time.Hour), Size: 4_108_916_736, Digest: "f974a74358d6",
				Details: monitoring.OllamaModelDetails{Family: "llama", ParameterSize: "7B", QuantizationLevel: "Q4_0"},
			},
		},
		RunningModels: []monitoring.OllamaRunningModel{
			{
				Name: "llama3.2:latest", Model: "llama3.2:latest",
				Size: 2_019_393_024, Digest: "dde5aa3fc5ff", ExpiresAt: expiresAt,
				SizeVRAM: 2_019_393_024, ContextLength: 4096,
				Details: monitoring.OllamaModelDetails{Family: "llama", ParameterSize: "3.2B", QuantizationLevel: "Q4_K_M"},
			},
		},
		CollectedAt:   now,
		LastSuccessAt: &successAt,
	}
}

func DockerSnapshot() dockerpkg.Snapshot {
	return dockerpkg.Snapshot{
		Enabled: true,
		Status:  "running",
		Projects: []dockerpkg.ComposeProject{
			{Name: "storefront", Status: "running", WorkingDir: "/opt/apps/storefront", PrimaryPort: 3000, ContainerCount: 3, RunningCount: 3, Imported: true, ProjectID: 1},
			{Name: "api-gateway", Status: "running", WorkingDir: "/opt/apps/api-gateway", PrimaryPort: 8080, ContainerCount: 2, RunningCount: 2, Imported: true, ProjectID: 2},
			{Name: "blog", Status: "running", WorkingDir: "/opt/apps/blog", PrimaryPort: 4000, ContainerCount: 2, RunningCount: 2, Imported: true, ProjectID: 3},
			{Name: "admin-panel", Status: "stopped", WorkingDir: "/opt/apps/admin-panel", PrimaryPort: 5000, ContainerCount: 2, RunningCount: 0, Imported: true, ProjectID: 4},
		},
		Running:     3,
		Stopped:     1,
		CollectedAt: time.Now(),
	}
}

func UpdateSnapshot() selfupdate.Snapshot {
	return selfupdate.Snapshot{
		Enabled:         false,
		Available:       false,
		CurrentRevision: "e1ad9cec8cbc3406418d5f7c00a04comp",
		CurrentShort:    "e1ad9ce",
		Branch:          "main",
	}
}

func AuditEntries() []database.AuditEntry {
	now := time.Now()
	uid := sql.NullInt64{Int64: 1, Valid: true}
	return []database.AuditEntry{
		{ID: 8, UserID: uid, Username: "demo", IPAddress: "203.0.113.42", Action: "login", TargetType: "session", Success: true, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: 7, UserID: uid, Username: "demo", IPAddress: "203.0.113.42", Action: "project_deploy", TargetType: "project", TargetID: "3", Details: "Deployed a1b2c3d → e4f5g6h", Success: true, CreatedAt: now.Add(-1*time.Hour - 2*time.Minute)},
		{ID: 6, UserID: uid, Username: "demo", IPAddress: "198.51.100.77", Action: "project_deploy", TargetType: "project", TargetID: "1", Details: "Deployed f8e7d6c → a1b2c3d", Success: true, CreatedAt: now.Add(-6*time.Hour - 3*time.Minute)},
		{ID: 5, UserID: uid, Username: "demo", IPAddress: "198.51.100.77", Action: "project_deploy", TargetType: "project", TargetID: "2", Details: "Deployed 1a2b3c4 → f8e7d6c", Success: true, CreatedAt: now.Add(-24*time.Hour - 1*time.Minute)},
		{ID: 4, UserID: uid, Username: "demo", IPAddress: "203.0.113.42", Action: "project_deploy", TargetType: "project", TargetID: "4", Details: "admin-panel", Success: false, Error: "Build failed: missing environment variable DATABASE_URL", CreatedAt: now.Add(-3*24*time.Hour - 5*time.Minute)},
		{ID: 3, UserID: uid, Username: "demo", IPAddress: "203.0.113.42", Action: "project_deploy", TargetType: "project", TargetID: "1", Details: "Deployed 0a9b8c7 → 1a2b3c4", Success: true, CreatedAt: now.Add(-7*24*time.Hour - 10*time.Minute)},
		{ID: 2, UserID: uid, Username: "demo", IPAddress: "198.51.100.77", Action: "login", TargetType: "session", Success: true, CreatedAt: now.Add(-7*24*time.Hour - 15*time.Minute)},
		{ID: 1, UserID: sql.NullInt64{Valid: false}, Username: "", IPAddress: "192.168.1.100", Action: "login_failed", TargetType: "session", Success: false, Error: "invalid credentials", CreatedAt: now.Add(-8 * 24 * time.Hour)},
	}
}
