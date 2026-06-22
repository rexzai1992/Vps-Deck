package deployments

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
	"github.com/vpsdeck/vpsdeck/internal/projects"
)

func TestNormalizeGitHubURLRejectsCredentialsAndUnexpectedHosts(t *testing.T) {
	valid, name, err := normalizeGitHubURL("https://github.com/rexzai1992/Vps-Deck")
	if err != nil {
		t.Fatal(err)
	}
	if valid != "https://github.com/rexzai1992/Vps-Deck.git" || name != "Vps-Deck" {
		t.Fatalf("unexpected normalized repository: %q %q", valid, name)
	}
	for _, value := range []string{
		"https://token@github.com/owner/repo",
		"https://github.com.evil.example/owner/repo",
		"https://github.com/owner/repo?token=secret",
		"https://github.com/owner/repo/extra",
		"git@github.com:owner/repo.git",
	} {
		if _, _, err := normalizeGitHubURL(value); err == nil {
			t.Fatalf("unsafe repository URL accepted: %q", value)
		}
	}
}

func TestDeployFastForwardsAndRefusesTrackedChanges(t *testing.T) {
	base := t.TempDir()
	apps := filepath.Join(base, "apps")
	seed := filepath.Join(base, "seed")
	remote := filepath.Join(base, "remote.git")
	projectPath := filepath.Join(apps, "app")
	for _, path := range []string{apps, seed} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	runGit(t, "", "init", "--bare", remote)
	runGit(t, seed, "init", "-b", "main")
	runGit(t, seed, "config", "user.name", "VPSDeck Test")
	runGit(t, seed, "config", "user.email", "vpsdeck@example.test")
	if err := os.WriteFile(filepath.Join(seed, "version.txt"), []byte("one\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "version.txt")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	runGit(t, "", "clone", "--branch", "main", remote, projectPath)

	db, err := database.Open(filepath.Join(base, "vpsdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	projectService, err := projects.NewService(db, []string{apps})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projectService.CreateExisting(context.Background(), projects.CreateInput{
		Name:       "Git App",
		Type:       "custom",
		WorkingDir: projectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := strings.TrimSpace(runGit(t, projectPath, "rev-parse", "HEAD"))
	now := time.Now().UTC()
	if err := db.CreateProjectSource(context.Background(), database.ProjectSource{
		ProjectID:      project.ID,
		Provider:       "github",
		RepositoryURL:  "https://github.com/example/app.git",
		Branch:         "main",
		DeployMode:     "git",
		LastCommit:     initial,
		LastDeployedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, projectService, apps, true, "git", "docker", 30*time.Second)

	if err := os.WriteFile(filepath.Join(seed, "version.txt"), []byte("two\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "version.txt")
	runGit(t, seed, "commit", "-m", "second")
	runGit(t, seed, "push", "origin", "main")

	deployment, err := service.Deploy(context.Background(), project)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if deployment.State != "success" || deployment.CommitBefore == deployment.CommitAfter {
		t.Fatalf("unexpected deployment: %#v", deployment)
	}
	content, err := os.ReadFile(filepath.Join(projectPath, "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "two\n" {
		t.Fatalf("working tree was not updated: %q", content)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "version.txt"), []byte("local edit\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Deploy(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("tracked local change was not rejected: deployment=%#v err=%v", failed, err)
	}
	if failed.State != "failed" {
		t.Fatalf("failed deployment was not recorded: %#v", failed)
	}
	history, err := db.ListProjectDeployments(context.Background(), project.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].State != "failed" || history[1].State != "success" {
		t.Fatalf("unexpected deployment history: %#v", history)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
	return string(output)
}
