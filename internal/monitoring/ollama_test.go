package monitoring

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOllamaOnlineAndCached(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/version":
			response.Write([]byte(`{"version":"0.17.0"}`))
		case "/api/tags":
			response.Write([]byte(`{"models":[{"name":"qwen3","model":"qwen3","size":1234,"details":{"family":"qwen","parameter_size":"8B","quantization_level":"Q4"}}]}`))
		case "/api/ps":
			response.Write([]byte(`{"models":[{"name":"qwen3","model":"qwen3","size":1234,"size_vram":1000,"context_length":4096,"details":{"family":"qwen","parameter_size":"8B","quantization_level":"Q4"}}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	service := NewOllamaService(true, server.URL, time.Second, time.Minute)
	first := service.Snapshot(t.Context())
	second := service.Snapshot(t.Context())
	if first.Status != "online" || first.Version != "0.17.0" {
		t.Fatalf("unexpected online result: %#v", first)
	}
	if first.InstalledCount != 1 || first.RunningCount != 1 || first.TotalVRAM != 1000 {
		t.Fatalf("unexpected model counts: %#v", first)
	}
	if requests.Load() != 3 || second.CollectedAt != first.CollectedAt {
		t.Fatalf("cache not used: requests=%d", requests.Load())
	}
}

func TestOllamaDegradedAndOfflineMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/version" {
			response.Write([]byte(`{"version":"0.17.0"}`))
			return
		}
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	degraded := NewOllamaService(true, server.URL, time.Second, time.Second)
	if result := degraded.Snapshot(t.Context()); result.Status != "degraded" {
		t.Fatalf("expected degraded, got %#v", result)
	}

	local := NewOllamaService(true, "http://127.0.0.1:1", 100*time.Millisecond, time.Second)
	local.detectExecutable = func() bool { return false }
	result := local.Snapshot(t.Context())
	if result.Status != "offline" || !strings.Contains(result.Message, "not detected") {
		t.Fatalf("unexpected local offline result: %#v", result)
	}

	remote := NewOllamaService(true, "http://192.0.2.1:11434", 100*time.Millisecond, time.Second)
	result = remote.Snapshot(t.Context())
	if result.Status != "offline" || !strings.Contains(result.Message, "Remote") {
		t.Fatalf("unexpected remote offline result: %#v", result)
	}
}

func TestOllamaDisabled(t *testing.T) {
	service := NewOllamaService(false, "http://127.0.0.1:11434", time.Second, time.Second)
	result := service.Snapshot(t.Context())
	if result.Status != "disabled" || result.Enabled {
		t.Fatalf("unexpected disabled result: %#v", result)
	}
}
