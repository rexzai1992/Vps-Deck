// Package selfupdate lets VPSDeck detect when a newer commit is available on
// its own Git source checkout and request an update.
//
// The panel runs unprivileged, so it cannot rebuild and restart itself. Detection
// is read-only (git rev-parse HEAD vs git ls-remote origin <branch>). Applying an
// update is delegated: RequestUpdate writes a flag file that a root-owned,
// path-activated systemd unit watches; that unit runs scripts/update.sh, which
// clears the flag and reports progress back through the status file this package
// reads. The panel never gains privilege.
package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ApplyStatus mirrors the JSON document written by scripts/update.sh.
type ApplyStatus struct {
	State        string `json:"state"` // running | success | failed
	Message      string `json:"message"`
	FromRevision string `json:"from_revision"`
	ToRevision   string `json:"to_revision"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
}

// Snapshot is the full view the UI renders and polls.
type Snapshot struct {
	Enabled         bool         `json:"enabled"`
	Available       bool         `json:"available"`
	CurrentRevision string       `json:"current_revision"`
	CurrentShort    string       `json:"current_short"`
	RemoteRevision  string       `json:"remote_revision"`
	RemoteShort     string       `json:"remote_short"`
	Branch          string       `json:"branch"`
	CheckedAt       time.Time    `json:"checked_at"`
	Error           string       `json:"error"`
	Requested       bool         `json:"requested"`
	Apply           *ApplyStatus `json:"apply"`
}

type checkResult struct {
	current string
	remote  string
	err     string
	at      time.Time
}

type Service struct {
	enabled     bool
	sourceDir   string
	branch      string
	gitCommand  string
	requestPath string
	statusPath  string
	timeout     time.Duration
	ttl         time.Duration

	mu     sync.Mutex
	cached *checkResult
}

func NewService(enabled bool, sourceDir, branch, gitCommand, requestPath, statusPath string, timeout, ttl time.Duration) *Service {
	return &Service{
		enabled:     enabled,
		sourceDir:   sourceDir,
		branch:      strings.TrimSpace(branch),
		gitCommand:  strings.TrimSpace(gitCommand),
		requestPath: requestPath,
		statusPath:  statusPath,
		timeout:     timeout,
		ttl:         ttl,
	}
}

func (s *Service) Enabled() bool { return s.enabled }

// Snapshot returns the cached commit comparison plus a fresh read of the request
// and status files. Passing force=true re-runs the git comparison immediately.
func (s *Service) Snapshot(ctx context.Context, force bool) Snapshot {
	snapshot := Snapshot{Enabled: s.enabled, Branch: s.branch}
	if !s.enabled {
		return snapshot
	}

	result := s.compare(ctx, force)
	snapshot.CurrentRevision = result.current
	snapshot.CurrentShort = short(result.current)
	snapshot.RemoteRevision = result.remote
	snapshot.RemoteShort = short(result.remote)
	snapshot.Error = result.err
	snapshot.CheckedAt = result.at
	snapshot.Available = result.err == "" && result.current != "" && result.remote != "" && result.current != result.remote

	if _, err := os.Stat(s.requestPath); err == nil {
		snapshot.Requested = true
	}
	if status := s.readStatus(); status != nil {
		snapshot.Apply = status
	}
	return snapshot
}

func (s *Service) compare(ctx context.Context, force bool) checkResult {
	s.mu.Lock()
	if !force && s.cached != nil && time.Since(s.cached.at) < s.ttl {
		cached := *s.cached
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	result := checkResult{at: time.Now().UTC()}
	current, err := s.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		result.err = "Could not read the installed revision. Self-update may not be configured on this host."
		s.store(result)
		return result
	}
	result.current = strings.TrimSpace(current)

	remote, err := s.run(ctx, "ls-remote", "origin", "refs/heads/"+s.branch)
	if err != nil {
		result.err = "Could not reach the Git remote to check for updates."
		s.store(result)
		return result
	}
	result.remote = parseLsRemote(remote)
	if result.remote == "" {
		result.err = fmt.Sprintf("Branch %q was not found on the remote.", s.branch)
	}
	s.store(result)
	return result
}

func (s *Service) store(result checkResult) {
	s.mu.Lock()
	s.cached = &result
	s.mu.Unlock()
}

// Invalidate drops the cached comparison so the next Snapshot re-checks.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// RequestUpdate writes the flag file that triggers the privileged updater. It
// refuses if updates are disabled or an apply is already in progress.
func (s *Service) RequestUpdate() error {
	if !s.enabled {
		return errors.New("self-update is disabled")
	}
	if status := s.readStatus(); status != nil && status.State == "running" {
		return errors.New("an update is already in progress")
	}
	if err := os.MkdirAll(filepath.Dir(s.requestPath), 0o750); err != nil {
		return fmt.Errorf("prepare update request: %w", err)
	}
	content := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	temp, err := os.CreateTemp(filepath.Dir(s.requestPath), ".update-request-*")
	if err != nil {
		return fmt.Errorf("create update request: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("write update request: %w", err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, s.requestPath); err != nil {
		return fmt.Errorf("submit update request: %w", err)
	}
	s.Invalidate()
	return nil
}

func (s *Service) readStatus() *ApplyStatus {
	content, err := os.ReadFile(s.statusPath)
	if err != nil {
		return nil
	}
	var status ApplyStatus
	if err := json.Unmarshal(content, &status); err != nil {
		return nil
	}
	if strings.TrimSpace(status.State) == "" {
		return nil
	}
	return &status
}

func (s *Service) run(ctx context.Context, arguments ...string) (string, error) {
	if strings.TrimSpace(s.gitCommand) == "" {
		return "", errors.New("git command is not configured")
	}
	commandCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	args := append([]string{"-C", s.sourceDir}, arguments...)
	command := exec.CommandContext(commandCtx, s.gitCommand, args...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false")
	var buffer bytes.Buffer
	command.Stdout = &buffer
	command.Stderr = &buffer
	if err := command.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return "", errors.New("git command timed out")
		}
		return "", err
	}
	return buffer.String(), nil
}

func parseLsRemote(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 && len(fields[0]) >= 7 {
			return fields[0]
		}
	}
	return ""
}

func short(revision string) string {
	revision = strings.TrimSpace(revision)
	if len(revision) > 8 {
		return revision[:8]
	}
	return revision
}
