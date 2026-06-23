package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxOllamaResponseBytes = 4 << 20
	maxOllamaGenerateBytes = 8 << 20
)

var ollamaModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,128}$`)

// PullProgress tracks a background model download.
type PullProgress struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Completed uint64    `json:"completed"`
	Total     uint64    `json:"total"`
	Done      bool      `json:"done"`
	Success   bool      `json:"success"`
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OllamaModelDetails struct {
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type OllamaInstalledModel struct {
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	ModifiedAt time.Time          `json:"modified_at"`
	Size       uint64             `json:"size"`
	Digest     string             `json:"digest"`
	Details    OllamaModelDetails `json:"details"`
}

type OllamaRunningModel struct {
	Name          string             `json:"name"`
	Model         string             `json:"model"`
	Size          uint64             `json:"size"`
	Digest        string             `json:"digest"`
	Details       OllamaModelDetails `json:"details"`
	ExpiresAt     time.Time          `json:"expires_at"`
	SizeVRAM      uint64             `json:"size_vram"`
	ContextLength int                `json:"context_length"`
}

type OllamaSnapshot struct {
	Enabled           bool                   `json:"enabled"`
	Status            string                 `json:"status"`
	Message           string                 `json:"message"`
	BaseURL           string                 `json:"base_url"`
	Version           string                 `json:"version,omitempty"`
	InstalledModels   []OllamaInstalledModel `json:"installed_models"`
	RunningModels     []OllamaRunningModel   `json:"running_models"`
	InstalledCount    int                    `json:"installed_count"`
	RunningCount      int                    `json:"running_count"`
	TotalVRAM         uint64                 `json:"total_vram"`
	LocalEndpoint     bool                   `json:"local_endpoint"`
	ExecutablePresent bool                   `json:"executable_present"`
	CollectedAt       time.Time              `json:"collected_at"`
	LastSuccessAt     *time.Time             `json:"last_success_at,omitempty"`
}

type OllamaService struct {
	enabled      bool
	baseURL      string
	ttl          time.Duration
	client       *http.Client
	actionClient *http.Client
	cache        snapshotCache[OllamaSnapshot]

	mu               sync.Mutex
	lastSuccessful   *time.Time
	detectExecutable func() bool

	pullsMu sync.Mutex
	pulls   map[string]*PullProgress
}

func NewOllamaService(enabled bool, baseURL string, timeout, ttl time.Duration) *OllamaService {
	return &OllamaService{
		enabled: enabled,
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     ttl,
		client: &http.Client{
			Timeout: timeout,
		},
		// actionClient has no client-level timeout; callers bound work with a
		// context (generate) or a long-lived background context (pull).
		actionClient:     &http.Client{},
		detectExecutable: ollamaExecutablePresent,
		pulls:            make(map[string]*PullProgress),
	}
}

func (s *OllamaService) Enabled() bool { return s.enabled }

func validOllamaModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("a model name is required")
	}
	if !ollamaModelPattern.MatchString(name) {
		return errors.New("invalid model name")
	}
	return nil
}

// Generate runs a single non-streaming completion against the configured Ollama
// server and returns the model's text response.
func (s *OllamaService) Generate(ctx context.Context, model, prompt string) (string, error) {
	if !s.enabled {
		return "", errors.New("Ollama monitoring is disabled")
	}
	if err := validOllamaModel(model); err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("a prompt is required")
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "prompt": prompt, "stream": false})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.actionClient.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("the model took too long to respond")
		}
		return "", errors.New("could not reach Ollama")
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxOllamaGenerateBytes))
	if err != nil {
		return "", errors.New("could not read the Ollama response")
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama returned status %d", response.StatusCode)
	}
	var out struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(content, &out); err != nil {
		return "", errors.New("invalid Ollama response")
	}
	if out.Error != "" {
		return "", errors.New(out.Error)
	}
	return out.Response, nil
}

// Delete removes an installed model from the Ollama server.
func (s *OllamaService) Delete(ctx context.Context, name string) error {
	if !s.enabled {
		return errors.New("Ollama monitoring is disabled")
	}
	if err := validOllamaModel(name); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"name": name})
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.baseURL+"/api/delete", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return errors.New("could not reach Ollama")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama returned status %d", response.StatusCode)
	}
	s.cache.invalidate()
	return nil
}

// StartPull begins downloading a model in the background. Progress is tracked in
// memory and read with ActivePulls. Re-requesting an in-flight model is a no-op.
func (s *OllamaService) StartPull(name string) error {
	if !s.enabled {
		return errors.New("Ollama monitoring is disabled")
	}
	if err := validOllamaModel(name); err != nil {
		return err
	}
	s.pullsMu.Lock()
	if existing, ok := s.pulls[name]; ok && !existing.Done {
		s.pullsMu.Unlock()
		return nil
	}
	s.pulls[name] = &PullProgress{Name: name, Status: "starting", UpdatedAt: time.Now().UTC()}
	s.pullsMu.Unlock()
	go s.runPull(name)
	return nil
}

func (s *OllamaService) runPull(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"name": name, "stream": true})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		s.finishPull(name, err.Error())
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.actionClient.Do(request)
	if err != nil {
		s.finishPull(name, "could not reach Ollama")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.finishPull(name, fmt.Sprintf("Ollama returned status %d", response.StatusCode))
		return
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var line struct {
			Status    string `json:"status"`
			Completed uint64 `json:"completed"`
			Total     uint64 `json:"total"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		if line.Error != "" {
			s.finishPull(name, line.Error)
			return
		}
		s.pullsMu.Lock()
		if progress := s.pulls[name]; progress != nil {
			progress.Status = line.Status
			progress.Completed = line.Completed
			progress.Total = line.Total
			progress.UpdatedAt = time.Now().UTC()
		}
		s.pullsMu.Unlock()
	}
	if err := scanner.Err(); err != nil {
		s.finishPull(name, "the download stream ended unexpectedly")
		return
	}
	s.finishPull(name, "")
	s.cache.invalidate()
}

func (s *OllamaService) finishPull(name, errMessage string) {
	s.pullsMu.Lock()
	defer s.pullsMu.Unlock()
	progress := s.pulls[name]
	if progress == nil {
		progress = &PullProgress{Name: name}
		s.pulls[name] = progress
	}
	progress.Done = true
	progress.UpdatedAt = time.Now().UTC()
	if errMessage == "" {
		progress.Success = true
		progress.Status = "success"
	} else {
		progress.Success = false
		progress.Status = "failed"
		progress.Error = errMessage
	}
}

// ActivePulls returns the current/recent pull progress entries.
func (s *OllamaService) ActivePulls() []PullProgress {
	s.pullsMu.Lock()
	defer s.pullsMu.Unlock()
	out := make([]PullProgress, 0, len(s.pulls))
	for _, progress := range s.pulls {
		out = append(out, *progress)
	}
	return out
}

func (s *OllamaService) Snapshot(ctx context.Context) OllamaSnapshot {
	return s.cache.get(ctx, s.ttl, s.collect)
}

func (s *OllamaService) collect(ctx context.Context) OllamaSnapshot {
	local := isLoopbackURL(s.baseURL)
	snapshot := OllamaSnapshot{
		Enabled:         s.enabled,
		Status:          "disabled",
		Message:         "Ollama monitoring is disabled.",
		BaseURL:         s.baseURL,
		InstalledModels: []OllamaInstalledModel{},
		RunningModels:   []OllamaRunningModel{},
		LocalEndpoint:   local,
		CollectedAt:     time.Now().UTC(),
	}
	if !s.enabled {
		return snapshot
	}
	if local {
		snapshot.ExecutablePresent = s.detectExecutable()
	}

	var version struct {
		Version string `json:"version"`
	}
	var tags struct {
		Models []OllamaInstalledModel `json:"models"`
	}
	var running struct {
		Models []OllamaRunningModel `json:"models"`
	}

	type endpointResult struct {
		name string
		err  error
	}
	results := make(chan endpointResult, 3)
	var wait sync.WaitGroup
	requests := []struct {
		name string
		path string
		out  any
	}{
		{"version", "/api/version", &version},
		{"models", "/api/tags", &tags},
		{"running models", "/api/ps", &running},
	}
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- endpointResult{name: request.name, err: s.getJSON(ctx, request.path, request.out)}
		}()
	}
	wait.Wait()
	close(results)

	successes := 0
	var failures []string
	for result := range results {
		if result.err != nil {
			failures = append(failures, result.name)
		} else {
			successes++
		}
	}

	snapshot.Version = version.Version
	snapshot.InstalledModels = tags.Models
	snapshot.RunningModels = running.Models
	snapshot.InstalledCount = len(tags.Models)
	snapshot.RunningCount = len(running.Models)
	for _, model := range running.Models {
		snapshot.TotalVRAM += model.SizeVRAM
	}

	if successes == 0 {
		snapshot.Status = "offline"
		switch {
		case !local:
			snapshot.Message = "Remote Ollama is unreachable."
		case snapshot.ExecutablePresent:
			snapshot.Message = "Ollama is installed but its API is not reachable."
		default:
			snapshot.Message = "Ollama is not detected on this server."
		}
	} else if len(failures) > 0 {
		snapshot.Status = "degraded"
		snapshot.Message = "Ollama is reachable, but some details are unavailable."
		s.markSuccess(snapshot.CollectedAt)
	} else {
		snapshot.Status = "online"
		snapshot.Message = "Ollama is online."
		s.markSuccess(snapshot.CollectedAt)
	}
	snapshot.LastSuccessAt = s.lastSuccess()
	return snapshot
}

func (s *OllamaService) getJSON(ctx context.Context, path string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxOllamaResponseBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(content) > maxOllamaResponseBytes {
		return errors.New("response is too large")
	}
	if err := json.Unmarshal(content, destination); err != nil {
		return errors.New("invalid JSON response")
	}
	return nil
}

func (s *OllamaService) markSuccess(value time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyValue := value
	s.lastSuccessful = &copyValue
}

func (s *OllamaService) lastSuccess() *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSuccessful == nil {
		return nil
	}
	copyValue := *s.lastSuccessful
	return &copyValue
}

func isLoopbackURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func ollamaExecutablePresent() bool {
	if _, err := exec.LookPath("ollama"); err == nil {
		return true
	}
	for _, path := range []string{
		"/usr/bin/ollama",
		"/usr/local/bin/ollama",
		"/opt/homebrew/bin/ollama",
		"/Applications/Ollama.app",
	} {
		if _, err := os.Stat(filepath.Clean(path)); err == nil {
			return true
		}
	}
	return false
}
