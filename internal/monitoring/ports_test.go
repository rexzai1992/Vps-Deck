package monitoring

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

type fakeConnectionSource struct {
	mu    sync.Mutex
	calls int
}

func (s *fakeConnectionSource) Connections(_ context.Context, kind string) ([]gnet.ConnectionStat, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if kind == "tcp" {
		return []gnet.ConnectionStat{
			{Laddr: gnet.Addr{IP: "127.0.0.1", Port: 8080}, Status: "LISTEN", Pid: 42},
			{Laddr: gnet.Addr{IP: "10.0.0.2", Port: 5432}, Status: "ESTABLISHED", Pid: 99},
		}, nil
	}
	return []gnet.ConnectionStat{
		{Laddr: gnet.Addr{IP: "0.0.0.0", Port: 53}, Pid: 7},
	}, nil
}

func TestPortSnapshotFiltersMatchesProjectsAndCaches(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, project := range []database.Project{
		{Name: "Panel", Type: "golang", WorkingDir: "/tmp/panel", Port: 8080},
		{Name: "Missing", Type: "nodejs", WorkingDir: "/tmp/missing", Port: 9090},
	} {
		if _, err := db.CreateProject(t.Context(), project); err != nil {
			t.Fatal(err)
		}
	}

	source := &fakeConnectionSource{}
	service := NewPortService(db, true, time.Minute)
	service.source = source
	service.processName = func(_ context.Context, pid int32) string {
		if pid == 42 {
			return "vpsdeck"
		}
		return ""
	}

	first := service.Snapshot(t.Context())
	second := service.Snapshot(t.Context())
	if first.TotalCount != 2 || first.TCPCount != 1 || first.UDPCount != 1 {
		t.Fatalf("unexpected counts: %#v", first)
	}
	if len(first.Listeners[1].Projects) != 1 || first.Listeners[1].Projects[0].Name != "Panel" {
		t.Fatalf("project was not matched: %#v", first.Listeners)
	}
	if len(first.MissingProjectPorts) != 1 || first.MissingProjectPorts[0].Port != 9090 {
		t.Fatalf("missing project port not reported: %#v", first.MissingProjectPorts)
	}
	if first.Listeners[0].ExposureKey != "all" || first.Listeners[1].ExposureKey != "loopback" {
		t.Fatalf("unexpected exposure classification: %#v", first.Listeners)
	}
	if source.calls != 2 {
		t.Fatalf("cache did not prevent recollection: calls=%d", source.calls)
	}
	if second.CollectedAt != first.CollectedAt {
		t.Fatal("cached snapshot changed")
	}
}
