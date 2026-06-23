package selfupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	command := exec.Command("git", full...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func newRepoWithRemote(t *testing.T) (work, dataDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	work = filepath.Join(base, "work")
	dataDir = filepath.Join(base, "data")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	git(t, base, "init", "--bare", "-b", "main", origin)
	git(t, work, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("one\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "first")
	git(t, work, "remote", "add", "origin", origin)
	git(t, work, "push", "-u", "origin", "main")
	return work, dataDir
}

func newService(work, dataDir string, enabled bool) *Service {
	return NewService(enabled, work, "main", "git",
		filepath.Join(dataDir, "update.request"),
		filepath.Join(dataDir, "update.status"),
		10*time.Second, 0)
}

func TestSnapshotNoUpdateWhenInSync(t *testing.T) {
	work, dataDir := newRepoWithRemote(t)
	service := newService(work, dataDir, true)
	snapshot := service.Snapshot(t.Context(), true)
	if snapshot.Error != "" {
		t.Fatalf("unexpected error: %s", snapshot.Error)
	}
	if snapshot.Available {
		t.Fatal("no update should be available when local matches remote")
	}
	if snapshot.CurrentRevision == "" || snapshot.CurrentRevision != snapshot.RemoteRevision {
		t.Fatalf("expected matching revisions, got %q vs %q", snapshot.CurrentRevision, snapshot.RemoteRevision)
	}
}

func TestSnapshotDetectsRemoteAhead(t *testing.T) {
	work, dataDir := newRepoWithRemote(t)
	// A second clone advances the remote without touching the working checkout.
	base := filepath.Dir(work)
	origin := filepath.Join(base, "origin.git")
	clone := filepath.Join(base, "clone2")
	git(t, base, "clone", origin, clone)
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("two\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "add", ".")
	git(t, clone, "commit", "-m", "second")
	git(t, clone, "push", "origin", "main")

	service := newService(work, dataDir, true)
	snapshot := service.Snapshot(t.Context(), true)
	if snapshot.Error != "" {
		t.Fatalf("unexpected error: %s", snapshot.Error)
	}
	if !snapshot.Available {
		t.Fatalf("expected an update to be available; current=%s remote=%s", snapshot.CurrentShort, snapshot.RemoteShort)
	}
}

func TestRequestUpdateAndStatus(t *testing.T) {
	work, dataDir := newRepoWithRemote(t)
	service := newService(work, dataDir, true)

	if err := service.RequestUpdate(); err != nil {
		t.Fatalf("request update: %v", err)
	}
	snapshot := service.Snapshot(t.Context(), true)
	if !snapshot.Requested {
		t.Fatal("request flag should be reported")
	}

	statusPath := filepath.Join(dataDir, "update.status")
	if err := os.WriteFile(statusPath, []byte(`{"state":"running","message":"building"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	snapshot = service.Snapshot(t.Context(), true)
	if snapshot.Apply == nil || snapshot.Apply.State != "running" {
		t.Fatalf("expected running apply status, got %+v", snapshot.Apply)
	}
	if err := service.RequestUpdate(); err == nil {
		t.Fatal("request should be refused while an update is running")
	}
}

func TestDisabledService(t *testing.T) {
	service := newService(t.TempDir(), t.TempDir(), false)
	if service.Snapshot(t.Context(), true).Enabled {
		t.Fatal("disabled service should report Enabled=false")
	}
	if err := service.RequestUpdate(); err == nil {
		t.Fatal("disabled service should refuse update requests")
	}
}
