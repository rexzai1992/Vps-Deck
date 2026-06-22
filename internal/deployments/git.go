package deployments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
	"github.com/vpsdeck/vpsdeck/internal/projects"
)

const maxCommandOutput = 1 << 20

var (
	githubPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	branchPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	directoryPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

var ErrDeploymentInProgress = errors.New("a deployment is already running for this project")

type ImportInput struct {
	Name           string
	RepositoryURL  string
	Branch         string
	Directory      string
	DeployMode     string
	Domain         string
	Port           int
	HealthcheckURL string
}

type Service struct {
	db          *database.DB
	projects    *projects.Service
	appsDir     string
	enabled     bool
	gitCommand  string
	dockerCmd   string
	timeout     time.Duration
	lockMu      sync.Mutex
	activeLocks map[int64]bool
}

func NewService(
	db *database.DB,
	projectService *projects.Service,
	appsDir string,
	enabled bool,
	gitCommand string,
	dockerCommand string,
	timeout time.Duration,
) *Service {
	return &Service{
		db:          db,
		projects:    projectService,
		appsDir:     filepath.Clean(appsDir),
		enabled:     enabled,
		gitCommand:  strings.TrimSpace(gitCommand),
		dockerCmd:   strings.TrimSpace(dockerCommand),
		timeout:     timeout,
		activeLocks: make(map[int64]bool),
	}
}

func (s *Service) Enabled() bool {
	return s.enabled
}

func (s *Service) Source(ctx context.Context, projectID int64) (database.ProjectSource, error) {
	return s.db.ProjectSourceByProjectID(ctx, projectID)
}

func (s *Service) ImportGitHub(ctx context.Context, input ImportInput) (database.Project, error) {
	if !s.enabled {
		return database.Project{}, errors.New("GitHub deployments are disabled")
	}
	repositoryURL, repositoryName, err := normalizeGitHubURL(input.RepositoryURL)
	if err != nil {
		return database.Project{}, err
	}
	branch, err := normalizeBranch(input.Branch)
	if err != nil {
		return database.Project{}, err
	}
	deployMode, err := normalizeDeployMode(input.DeployMode)
	if err != nil {
		return database.Project{}, err
	}
	directory, err := normalizeDirectory(input.Directory, repositoryName)
	if err != nil {
		return database.Project{}, err
	}
	destination := filepath.Join(s.appsDir, directory)
	if !pathWithin(s.appsDir, destination) {
		return database.Project{}, errors.New("project destination is outside the configured apps directory")
	}
	if _, err := os.Stat(destination); err == nil {
		return database.Project{}, errors.New("the destination folder already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return database.Project{}, errors.New("the destination folder cannot be inspected")
	}
	if err := os.MkdirAll(s.appsDir, 0o750); err != nil {
		return database.Project{}, fmt.Errorf("prepare apps directory: %w", err)
	}

	commandCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	output, err := s.run(commandCtx, "", s.gitCommand,
		"clone", "--depth", "1", "--branch", branch, "--single-branch", "--", repositoryURL, destination)
	if err != nil {
		_ = os.RemoveAll(destination)
		return database.Project{}, fmt.Errorf("clone GitHub repository: %w", commandError(err, output))
	}

	projectType := projects.DetectType(destination)
	if deployMode == "docker-compose" && projectType != "docker-compose" {
		_ = os.RemoveAll(destination)
		return database.Project{}, errors.New("Docker Compose deployment requires a compose.yaml, compose.yml, or docker-compose.yml file")
	}
	project, err := s.projects.CreateExisting(ctx, projects.CreateInput{
		Name:           input.Name,
		Type:           projectType,
		Domain:         input.Domain,
		Port:           input.Port,
		WorkingDir:     destination,
		HealthcheckURL: input.HealthcheckURL,
	})
	if err != nil {
		_ = os.RemoveAll(destination)
		return database.Project{}, err
	}

	commit, err := s.run(commandCtx, destination, s.gitCommand, "rev-parse", "HEAD")
	if err != nil {
		_ = s.projects.Delete(ctx, project.ID)
		_ = os.RemoveAll(destination)
		return database.Project{}, fmt.Errorf("read cloned revision: %w", commandError(err, commit))
	}
	commit = strings.TrimSpace(commit)
	now := time.Now().UTC()
	source := database.ProjectSource{
		ProjectID:      project.ID,
		Provider:       "github",
		RepositoryURL:  repositoryURL,
		Branch:         branch,
		DeployMode:     deployMode,
		LastCommit:     commit,
		LastDeployedAt: &now,
	}
	if err := s.db.CreateProjectSource(ctx, source); err != nil {
		_ = s.projects.Delete(ctx, project.ID)
		_ = os.RemoveAll(destination)
		return database.Project{}, fmt.Errorf("save GitHub project settings: %w", err)
	}
	deploymentID, err := s.db.CreateDeployment(ctx, database.Deployment{
		ProjectID:    project.ID,
		Action:       "clone",
		State:        "running",
		CommitAfter:  commit,
		CommitBefore: "",
	})
	if err == nil {
		_ = s.db.FinishDeployment(ctx, deploymentID, "success", "", commit, cleanOutput(output), "", now)
	}
	return project, nil
}

func (s *Service) Deploy(ctx context.Context, project database.Project) (database.Deployment, error) {
	if !s.enabled {
		return database.Deployment{}, errors.New("GitHub deployments are disabled")
	}
	if !s.acquire(project.ID) {
		return database.Deployment{}, ErrDeploymentInProgress
	}
	defer s.release(project.ID)

	source, err := s.db.ProjectSourceByProjectID(ctx, project.ID)
	if err != nil {
		if database.IsNotFound(err) {
			return database.Deployment{}, errors.New("this project is not connected to a GitHub repository")
		}
		return database.Deployment{}, err
	}
	deployment := database.Deployment{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		Action:      "deploy",
		State:       "running",
	}
	deployment.ID, err = s.db.CreateDeployment(ctx, deployment)
	if err != nil {
		return database.Deployment{}, fmt.Errorf("create deployment record: %w", err)
	}

	commandCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var log strings.Builder
	finishFailure := func(cause error) (database.Deployment, error) {
		deployment.State = "failed"
		deployment.Error = cause.Error()
		deployment.Output = cleanOutput(log.String())
		finishedAt := time.Now().UTC()
		deployment.FinishedAt = &finishedAt
		_ = s.db.FinishDeployment(ctx, deployment.ID, deployment.State, deployment.CommitBefore,
			deployment.CommitAfter, deployment.Output, deployment.Error, finishedAt)
		return deployment, cause
	}

	before, err := s.run(commandCtx, project.WorkingDir, s.gitCommand, "rev-parse", "HEAD")
	output := before
	logCommand(&log, "Read current revision", output)
	if err != nil {
		return finishFailure(fmt.Errorf("read current Git revision: %w", commandError(err, output)))
	}
	deployment.CommitBefore = strings.TrimSpace(before)

	dirty, err := s.run(commandCtx, project.WorkingDir, s.gitCommand, "status", "--porcelain", "--untracked-files=no")
	output = dirty
	logCommand(&log, "Check tracked files", output)
	if err != nil {
		return finishFailure(fmt.Errorf("check Git working tree: %w", commandError(err, output)))
	}
	if strings.TrimSpace(dirty) != "" {
		return finishFailure(errors.New("deployment stopped because tracked project files have local changes"))
	}

	output, err = s.run(commandCtx, project.WorkingDir, s.gitCommand, "fetch", "--prune", "origin", source.Branch)
	logCommand(&log, "Fetch origin/"+source.Branch, output)
	if err != nil {
		return finishFailure(fmt.Errorf("fetch GitHub branch: %w", commandError(err, output)))
	}
	output, err = s.run(commandCtx, project.WorkingDir, s.gitCommand, "merge", "--ff-only", "origin/"+source.Branch)
	logCommand(&log, "Fast-forward working tree", output)
	if err != nil {
		return finishFailure(fmt.Errorf("fast-forward GitHub branch: %w", commandError(err, output)))
	}

	after, err := s.run(commandCtx, project.WorkingDir, s.gitCommand, "rev-parse", "HEAD")
	output = after
	logCommand(&log, "Read deployed revision", output)
	if err != nil {
		return finishFailure(fmt.Errorf("read deployed Git revision: %w", commandError(err, output)))
	}
	deployment.CommitAfter = strings.TrimSpace(after)

	if source.DeployMode == "docker-compose" {
		output, err = s.run(commandCtx, project.WorkingDir, s.dockerCmd, "compose", "config", "--quiet")
		logCommand(&log, "Validate Docker Compose", output)
		if err != nil {
			return finishFailure(fmt.Errorf("validate Docker Compose project: %w", commandError(err, output)))
		}
		output, err = s.run(commandCtx, project.WorkingDir, s.dockerCmd, "compose", "up", "-d", "--build")
		logCommand(&log, "Build and start Docker Compose", output)
		if err != nil {
			return finishFailure(fmt.Errorf("deploy Docker Compose project: %w", commandError(err, output)))
		}
		_ = s.db.UpdateProjectRuntime(ctx, project.ID, "running", 0)
	}

	finishedAt := time.Now().UTC()
	deployment.State = "success"
	deployment.Output = cleanOutput(log.String())
	deployment.FinishedAt = &finishedAt
	if err := s.db.FinishDeployment(ctx, deployment.ID, deployment.State, deployment.CommitBefore,
		deployment.CommitAfter, deployment.Output, "", finishedAt); err != nil {
		return deployment, fmt.Errorf("finish deployment record: %w", err)
	}
	if err := s.db.UpdateProjectSourceDeployment(ctx, project.ID, deployment.CommitAfter, finishedAt); err != nil {
		return deployment, fmt.Errorf("update project source revision: %w", err)
	}
	return deployment, nil
}

func (s *Service) acquire(projectID int64) bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.activeLocks[projectID] {
		return false
	}
	s.activeLocks[projectID] = true
	return true
}

func (s *Service) release(projectID int64) {
	s.lockMu.Lock()
	delete(s.activeLocks, projectID)
	s.lockMu.Unlock()
}

func (s *Service) run(ctx context.Context, directory, command string, arguments ...string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("required command is not configured")
	}
	item := exec.CommandContext(ctx, command, arguments...)
	if directory != "" {
		item.Dir = directory
	}
	item.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
	)
	buffer := &limitedBuffer{limit: maxCommandOutput}
	item.Stdout = buffer
	item.Stderr = buffer
	err := item.Run()
	content := buffer.String()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return content, errors.New("command timed out")
	}
	return content, err
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(content []byte) (int, error) {
	originalLength := len(content)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(content) > remaining {
			content = content[:remaining]
		}
		_, _ = b.buffer.Write(content)
	}
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func normalizeGitHubURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("GitHub repository URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", "", errors.New("use an HTTPS GitHub repository URL such as https://github.com/owner/repository")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return "", "", errors.New("GitHub repository URL must not contain credentials, query parameters, or fragments")
	}
	path := strings.TrimPrefix(parsed.EscapedPath(), "/")
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !githubPartPattern.MatchString(parts[0]) || !githubPartPattern.MatchString(parts[1]) {
		return "", "", errors.New("GitHub repository URL must contain exactly an owner and repository")
	}
	if parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", "", errors.New("invalid GitHub owner or repository")
	}
	return "https://github.com/" + parts[0] + "/" + parts[1] + ".git", parts[1], nil
}

func normalizeBranch(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "main"
	}
	if len(value) > 200 || !branchPattern.MatchString(value) ||
		strings.Contains(value, "..") || strings.Contains(value, "//") ||
		strings.Contains(value, "@{") || strings.HasSuffix(value, ".") ||
		strings.HasSuffix(value, ".lock") {
		return "", errors.New("invalid Git branch name")
	}
	return value, nil
}

func normalizeDirectory(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > 100 || !directoryPattern.MatchString(value) ||
		value == "." || value == ".." || strings.HasSuffix(value, ".") {
		return "", errors.New("destination folder may contain only letters, numbers, dot, dash, and underscore")
	}
	return value, nil
}

func normalizeDeployMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "git"
	}
	if value != "git" && value != "docker-compose" {
		return "", errors.New("unsupported deployment mode")
	}
	return value, nil
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func commandError(err error, output string) error {
	message := strings.TrimSpace(cleanOutput(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%s", message)
}

func logCommand(log *strings.Builder, label, output string) {
	log.WriteString(label)
	log.WriteString("\n")
	output = strings.TrimSpace(cleanOutput(output))
	if output == "" {
		log.WriteString("OK\n\n")
		return
	}
	log.WriteString(output)
	log.WriteString("\n\n")
}

func cleanOutput(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if len(value) > maxCommandOutput {
		value = value[:maxCommandOutput]
	}
	return strings.TrimSpace(value)
}
