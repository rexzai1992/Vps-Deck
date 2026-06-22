package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

type Publisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

type Service struct {
	Name       string      `json:"name"`
	Container  string      `json:"container"`
	Image      string      `json:"image"`
	State      string      `json:"state"`
	Health     string      `json:"health"`
	Status     string      `json:"status"`
	Publishers []Publisher `json:"publishers"`
}

type ComposeProject struct {
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	ConfigFile     string    `json:"config_file"`
	WorkingDir     string    `json:"working_dir"`
	PrimaryPort    int       `json:"primary_port"`
	Services       []Service `json:"services"`
	ContainerCount int       `json:"container_count"`
	RunningCount   int       `json:"running_count"`
	HealthyCount   int       `json:"healthy_count"`
	Imported       bool      `json:"imported"`
	ProjectID      int64     `json:"project_id,omitempty"`
}

type Snapshot struct {
	Enabled     bool             `json:"enabled"`
	Status      string           `json:"status"`
	Message     string           `json:"message"`
	Projects    []ComposeProject `json:"projects"`
	Running     int              `json:"running"`
	Stopped     int              `json:"stopped"`
	CollectedAt time.Time        `json:"collected_at"`
}

type commandRunner interface {
	Output(context.Context, ...string) ([]byte, error)
}

type execRunner struct {
	command string
	timeout time.Duration
}

func (r execRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, r.command, args...)
	output, err := command.Output()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("Docker command timed out")
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			message := strings.TrimSpace(string(exitError.Stderr))
			if message != "" {
				return nil, errors.New(message)
			}
		}
		return nil, err
	}
	return output, nil
}

type Discovery struct {
	db      *database.DB
	enabled bool
	ttl     time.Duration
	runner  commandRunner

	mu        sync.Mutex
	cached    Snapshot
	hasCache  bool
	expiresAt time.Time
}

func NewDiscovery(db *database.DB, enabled bool, command string, timeout, ttl time.Duration) *Discovery {
	return &Discovery{
		db:      db,
		enabled: enabled,
		ttl:     ttl,
		runner:  execRunner{command: command, timeout: timeout},
	}
}

func (d *Discovery) Snapshot(ctx context.Context, force bool) Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !force && d.hasCache && time.Now().Before(d.expiresAt) {
		return d.cached
	}
	d.cached = d.collect(ctx)
	d.hasCache = true
	d.expiresAt = time.Now().Add(d.ttl)
	return d.cached
}

func (d *Discovery) Import(ctx context.Context, name string) (database.Project, error) {
	snapshot := d.Snapshot(ctx, true)
	for _, discovered := range snapshot.Projects {
		if discovered.Name != name {
			continue
		}
		if discovered.WorkingDir == "" || discovered.ConfigFile == "" {
			return database.Project{}, errors.New("Docker Compose project location is unavailable")
		}
		if existing, err := d.db.ProjectByWorkingDir(ctx, discovered.WorkingDir); err == nil {
			return existing, nil
		}
		project := database.Project{
			Name:       discovered.Name,
			Type:       "docker-compose",
			Port:       discovered.PrimaryPort,
			WorkingDir: discovered.WorkingDir,
			Status:     discovered.Status,
		}
		id, err := d.db.CreateProject(ctx, project)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return database.Project{}, errors.New("this Docker Compose project is already registered")
			}
			return database.Project{}, err
		}
		return d.db.ProjectByID(ctx, id)
	}
	return database.Project{}, errors.New("Docker Compose project was not found")
}

func (d *Discovery) SyncRegistered(ctx context.Context, snapshot Snapshot) {
	for _, item := range snapshot.Projects {
		if !item.Imported || item.ProjectID == 0 {
			continue
		}
		_ = d.db.UpdateProjectRuntime(ctx, item.ProjectID, item.Status, item.PrimaryPort)
	}
}

func (d *Discovery) collect(ctx context.Context) Snapshot {
	snapshot := Snapshot{
		Enabled:     d.enabled,
		Status:      "disabled",
		Message:     "Docker Compose discovery is disabled.",
		Projects:    []ComposeProject{},
		CollectedAt: time.Now().UTC(),
	}
	if !d.enabled {
		return snapshot
	}

	output, err := d.runner.Output(ctx, "compose", "ls", "--all", "--format", "json")
	if err != nil {
		snapshot.Status = "unavailable"
		snapshot.Message = "Docker or Docker Compose is not available."
		return snapshot
	}
	var listed []struct {
		Name        string `json:"Name"`
		Status      string `json:"Status"`
		ConfigFiles string `json:"ConfigFiles"`
	}
	if err := json.Unmarshal(output, &listed); err != nil {
		snapshot.Status = "degraded"
		snapshot.Message = "Docker Compose returned an unreadable project list."
		return snapshot
	}

	registered, _ := d.db.ListProjects(ctx)
	byWorkingDir := make(map[string]database.Project)
	for _, project := range registered {
		byWorkingDir[filepath.Clean(project.WorkingDir)] = project
	}

	for _, item := range listed {
		configFile := firstConfigFile(item.ConfigFiles)
		workingDir := ""
		if configFile != "" {
			configFile, _ = filepath.Abs(filepath.Clean(configFile))
			if info, statErr := os.Stat(configFile); statErr == nil && !info.IsDir() {
				workingDir = filepath.Dir(configFile)
			}
		}
		project := ComposeProject{
			Name:       item.Name,
			ConfigFile: configFile,
			WorkingDir: workingDir,
			Status:     composeStatus(item.Status),
			Services:   []Service{},
		}
		if existing, ok := byWorkingDir[filepath.Clean(workingDir)]; ok && workingDir != "" {
			project.Imported = true
			project.ProjectID = existing.ID
		}
		if configFile != "" {
			if serviceOutput, serviceErr := d.runner.Output(ctx,
				"compose", "--project-name", item.Name, "--file", configFile,
				"ps", "--all", "--format", "json",
			); serviceErr == nil {
				project.Services = parseServices(serviceOutput)
			}
		}
		for _, service := range project.Services {
			project.ContainerCount++
			if service.State == "running" {
				project.RunningCount++
			}
			if service.Health == "healthy" {
				project.HealthyCount++
			}
			for _, publisher := range service.Publishers {
				if publisher.PublishedPort > 0 &&
					(project.PrimaryPort == 0 || publisher.PublishedPort < project.PrimaryPort) {
					project.PrimaryPort = publisher.PublishedPort
				}
			}
		}
		if project.ContainerCount > 0 {
			project.Status = statusFromServices(project.Services)
		}
		if project.Status == "running" {
			snapshot.Running++
		} else {
			snapshot.Stopped++
		}
		snapshot.Projects = append(snapshot.Projects, project)
	}
	sort.Slice(snapshot.Projects, func(i, j int) bool {
		return strings.ToLower(snapshot.Projects[i].Name) < strings.ToLower(snapshot.Projects[j].Name)
	})
	snapshot.Status = "online"
	snapshot.Message = fmt.Sprintf("%d Docker Compose project(s) detected.", len(snapshot.Projects))
	return snapshot
}

func parseServices(output []byte) []Service {
	var services []Service
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var row struct {
			Name       string      `json:"Name"`
			Image      string      `json:"Image"`
			State      string      `json:"State"`
			Health     string      `json:"Health"`
			Status     string      `json:"Status"`
			Service    string      `json:"Service"`
			Publishers []Publisher `json:"Publishers"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		services = append(services, Service{
			Name:       row.Service,
			Container:  row.Name,
			Image:      row.Image,
			State:      strings.ToLower(row.State),
			Health:     strings.ToLower(row.Health),
			Status:     row.Status,
			Publishers: row.Publishers,
		})
	}
	return services
}

func statusFromServices(services []Service) string {
	if len(services) == 0 {
		return "stopped"
	}
	running := 0
	for _, service := range services {
		if service.Health == "unhealthy" || service.State == "dead" || service.State == "restarting" {
			return "failed"
		}
		if service.State == "running" {
			running++
		}
	}
	if running == len(services) {
		return "running"
	}
	if running > 0 {
		return "warning"
	}
	return "stopped"
}

func composeStatus(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.HasPrefix(value, "running"):
		return "running"
	case strings.HasPrefix(value, "exited"):
		return "stopped"
	default:
		return "unknown"
	}
}

func firstConfigFile(value string) string {
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}
