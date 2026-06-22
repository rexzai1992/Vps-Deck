package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

func TestReadSaveAndTraversalProtection(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	backups := filepath.Join(base, "backups")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("old: true\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := database.Project{ID: 7, WorkingDir: root}
	service := NewService(backups)

	if err := service.SaveText(project, "config.yaml", "new: true\n"); err != nil {
		t.Fatal(err)
	}
	content, err := service.ReadText(project, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if content != "new: true\n" {
		t.Fatalf("unexpected content: %q", content)
	}
	var backupFound bool
	filepath.WalkDir(backups, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, "config.yaml") {
			backupFound = true
		}
		return nil
	})
	if !backupFound {
		t.Fatal("edit backup was not created")
	}
	if _, err := service.ReadText(project, "../secret.txt"); err == nil {
		t.Fatal("path traversal was accepted")
	}
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	service := NewService(filepath.Join(base, "backups"))
	project := database.Project{ID: 1, WorkingDir: root}
	if _, err := service.ReadText(project, "escape/secret.txt"); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}
