package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

type fakeRunner struct {
	listOutput []byte
	psOutput   []byte
	calls      []string
}

func (r *fakeRunner) Output(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	if len(args) >= 2 && args[0] == "compose" && args[1] == "ls" {
		return r.listOutput, nil
	}
	if len(args) >= 2 && args[0] == "compose" && args[1] == "--project-name" {
		return r.psOutput, nil
	}
	return nil, fmt.Errorf("unexpected command")
}

func TestDiscoverImportAndSyncComposeProject(t *testing.T) {
	base := t.TempDir()
	workingDir := filepath.Join(base, "app")
	if err := os.MkdirAll(workingDir, 0o750); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(workingDir, "compose.yaml")
	if err := os.WriteFile(configFile, []byte("services: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	runner := &fakeRunner{
		listOutput: []byte(fmt.Sprintf(
			`[{"Name":"example","Status":"running(2)","ConfigFiles":%q}]`,
			configFile,
		)),
		psOutput: []byte(
			`{"Name":"example-web","Image":"nginx","State":"running","Health":"healthy","Status":"Up","Service":"web","Publishers":[{"URL":"127.0.0.1","TargetPort":80,"PublishedPort":8088,"Protocol":"tcp"}]}` + "\n" +
				`{"Name":"example-db","Image":"postgres","State":"running","Health":"healthy","Status":"Up","Service":"db","Publishers":[]}` + "\n",
		),
	}
	discovery := NewDiscovery(db, true, "docker", time.Second, time.Minute)
	discovery.runner = runner

	snapshot := discovery.Snapshot(t.Context(), false)
	if snapshot.Status != "online" || snapshot.Running != 1 || len(snapshot.Projects) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	detected := snapshot.Projects[0]
	if detected.PrimaryPort != 8088 || detected.Status != "running" || detected.ContainerCount != 2 {
		t.Fatalf("unexpected detected project: %#v", detected)
	}

	project, err := discovery.Import(t.Context(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if project.Type != "docker-compose" || project.Port != 8088 || project.Status != "running" {
		t.Fatalf("unexpected imported project: %#v", project)
	}
	snapshot = discovery.Snapshot(t.Context(), true)
	if !snapshot.Projects[0].Imported || snapshot.Projects[0].ProjectID != project.ID {
		t.Fatalf("import state missing: %#v", snapshot.Projects[0])
	}

	runner.psOutput = []byte(
		`{"Name":"example-web","Image":"nginx","State":"exited","Health":"","Status":"Exited","Service":"web","Publishers":[{"URL":"127.0.0.1","TargetPort":80,"PublishedPort":8088,"Protocol":"tcp"}]}` + "\n",
	)
	snapshot = discovery.Snapshot(t.Context(), true)
	discovery.SyncRegistered(t.Context(), snapshot)
	updated, err := db.ProjectByID(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "stopped" {
		t.Fatalf("runtime status was not synchronized: %#v", updated)
	}
}

func TestDockerUnavailableIsSafe(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	discovery := NewDiscovery(db, true, "missing-docker", time.Second, time.Second)
	discovery.runner = errorRunner{}
	result := discovery.Snapshot(t.Context(), false)
	if result.Status != "unavailable" || len(result.Projects) != 0 {
		t.Fatalf("unexpected unavailable result: %#v", result)
	}
}

type errorRunner struct{}

func (errorRunner) Output(context.Context, ...string) ([]byte, error) {
	return nil, fmt.Errorf("not found")
}
