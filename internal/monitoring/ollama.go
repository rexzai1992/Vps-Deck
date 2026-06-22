package monitoring

import (
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
	"strings"
	"sync"
	"time"
)

const maxOllamaResponseBytes = 4 << 20

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
	enabled bool
	baseURL string
	ttl     time.Duration
	client  *http.Client
	cache   snapshotCache[OllamaSnapshot]

	mu               sync.Mutex
	lastSuccessful   *time.Time
	detectExecutable func() bool
}

func NewOllamaService(enabled bool, baseURL string, timeout, ttl time.Duration) *OllamaService {
	return &OllamaService{
		enabled: enabled,
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     ttl,
		client: &http.Client{
			Timeout: timeout,
		},
		detectExecutable: ollamaExecutablePresent,
	}
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
