package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

func TestCreateExistingDetectsTypeAndEnforcesRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "apps")
	projectPath := filepath.Join(root, "api")
	outsidePath := filepath.Join(base, "outside")
	for _, path := range []string{projectPath, outsidePath} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectPath, "go.mod"), []byte("module example.test/api\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service, err := NewService(db, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.CreateExisting(context.Background(), CreateInput{
		Name:       "Example API",
		Type:       "auto",
		WorkingDir: projectPath,
		Port:       8081,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if project.Type != "golang" {
		t.Fatalf("expected golang detection, got %q", project.Type)
	}

	if _, err := service.CreateExisting(context.Background(), CreateInput{
		Name:       "Outside",
		Type:       "auto",
		WorkingDir: outsidePath,
	}); err == nil {
		t.Fatal("outside project root was accepted")
	}
}

func TestSymlinkCannotEscapeAllowedRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "apps")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escaped")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := NewService(db, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateExisting(context.Background(), CreateInput{
		Name:       "Escaped",
		Type:       "custom",
		WorkingDir: link,
	}); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestBrowseFoldersStaysInsideApprovedRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "apps")
	visible := filepath.Join(root, "visible")
	hidden := filepath.Join(root, ".hidden")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{visible, hidden, outside} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := NewService(db, []string{root})
	if err != nil {
		t.Fatal(err)
	}

	listing, err := service.BrowseFolders(0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if listing.AbsolutePath != canonicalRoot {
		t.Fatalf("unexpected root path: %q", listing.AbsolutePath)
	}
	if len(listing.Folders) != 1 || listing.Folders[0].Name != "visible" {
		t.Fatalf("unexpected visible folders: %#v", listing.Folders)
	}

	listing, err = service.BrowseFolders(0, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Folders) != 2 {
		t.Fatalf("expected hidden folder, got %#v", listing.Folders)
	}
	if _, err := service.BrowseFolders(0, "../outside", false); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := service.BrowseFolders(1, "", false); err == nil {
		t.Fatal("invalid root index was accepted")
	}
	if _, err := service.BrowseFolders(0, "escape", false); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}
