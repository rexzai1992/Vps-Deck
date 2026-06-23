package files

import (
	"archive/zip"
	"bytes"
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

func newProject(t *testing.T) (*Service, database.Project, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	return NewService(filepath.Join(base, "backups")), database.Project{ID: 3, Name: "demo", WorkingDir: root}, root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestMoveAndDescendantGuard(t *testing.T) {
	service, project, root := newProject(t)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dst"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "src", "note.txt"), "hi")

	newPath, err := service.Move(project, "src/note.txt", "dst")
	if err != nil {
		t.Fatalf("move file: %v", err)
	}
	if newPath != "dst/note.txt" {
		t.Fatalf("unexpected new path: %q", newPath)
	}
	if _, err := os.Stat(filepath.Join(root, "dst", "note.txt")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "note.txt")); !os.IsNotExist(err) {
		t.Fatal("source file should be gone after move")
	}

	if _, err := service.Move(project, "src", "src"); err == nil {
		t.Fatal("moving a folder into itself should fail")
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Move(project, "src", "src/child"); err == nil {
		t.Fatal("moving a folder into its descendant should fail")
	}
}

func TestMoveAutoRenamesOnConflict(t *testing.T) {
	service, project, root := newProject(t)
	if err := os.MkdirAll(filepath.Join(root, "dst"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.txt"), "one")
	writeFile(t, filepath.Join(root, "dst", "a.txt"), "existing")

	newPath, err := service.Move(project, "a.txt", "dst")
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if newPath != "dst/a (2).txt" {
		t.Fatalf("expected auto-rename, got %q", newPath)
	}
	content, err := os.ReadFile(filepath.Join(root, "dst", "a.txt"))
	if err != nil || string(content) != "existing" {
		t.Fatalf("existing file must not be overwritten: %v %q", err, content)
	}
}

func TestCopyRecursive(t *testing.T) {
	service, project, root := newProject(t)
	writeFile(t, filepath.Join(root, "tree", "inner", "deep.txt"), "deep")
	if err := os.MkdirAll(filepath.Join(root, "out"), 0o750); err != nil {
		t.Fatal(err)
	}

	newPath, err := service.Copy(project, "tree", "out")
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if newPath != "out/tree" {
		t.Fatalf("unexpected copy path: %q", newPath)
	}
	if content, err := os.ReadFile(filepath.Join(root, "out", "tree", "inner", "deep.txt")); err != nil || string(content) != "deep" {
		t.Fatalf("recursive copy failed: %v %q", err, content)
	}
	// original remains
	if _, err := os.Stat(filepath.Join(root, "tree", "inner", "deep.txt")); err != nil {
		t.Fatalf("original should remain after copy: %v", err)
	}
}

func TestRenameRejectsConflictAndPath(t *testing.T) {
	service, project, root := newProject(t)
	writeFile(t, filepath.Join(root, "a.txt"), "a")
	writeFile(t, filepath.Join(root, "b.txt"), "b")

	if _, err := service.Rename(project, "a.txt", "renamed.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := service.Rename(project, "b.txt", "renamed.txt"); err == nil {
		t.Fatal("rename onto an existing name should fail")
	}
	if _, err := service.Rename(project, "b.txt", "../escape.txt"); err == nil {
		t.Fatal("rename with a path should fail")
	}
}

func TestDeleteRecursive(t *testing.T) {
	service, project, root := newProject(t)
	writeFile(t, filepath.Join(root, "folder", "file.txt"), "x")

	if err := service.Delete(project, "folder"); err == nil {
		t.Fatal("non-recursive delete of a non-empty folder should fail")
	}
	if err := service.DeleteRecursive(project, "folder"); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "folder")); !os.IsNotExist(err) {
		t.Fatal("folder should be gone after recursive delete")
	}
	if err := service.DeleteRecursive(project, ""); err == nil {
		t.Fatal("recursive delete of the project root must be refused")
	}
}

func TestNewFileAndZipFolder(t *testing.T) {
	service, project, root := newProject(t)
	if _, err := service.NewFile(project, "", "fresh.txt"); err != nil {
		t.Fatalf("new file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.txt")); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
	if _, err := service.NewFile(project, "", "fresh.txt"); err == nil {
		t.Fatal("creating an existing file should fail")
	}
	if _, err := service.NewFile(project, "", "../escape.txt"); err == nil {
		t.Fatal("new file with a path should fail")
	}

	writeFile(t, filepath.Join(root, "bundle", "a.txt"), "aaa")
	var buffer bytes.Buffer
	name, err := service.ZipFolder(project, "bundle", &buffer)
	if err != nil {
		t.Fatalf("zip folder: %v", err)
	}
	if name != "bundle.zip" {
		t.Fatalf("unexpected zip name: %q", name)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	var found bool
	for _, file := range reader.File {
		if file.Name == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("zip did not contain the folder file")
	}
}
