package files

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

const maxEditableSize = 2 << 20

var editableExtensions = map[string]bool{
	".css": true, ".env": true, ".go": true, ".html": true, ".js": true,
	".json": true, ".md": true, ".py": true, ".service": true, ".sh": true,
	".sql": true, ".toml": true, ".ts": true, ".txt": true, ".yaml": true,
	".yml": true, ".conf": true,
}

type Entry struct {
	Name         string
	RelativePath string
	IsDir        bool
	Size         uint64
	ModifiedAt   time.Time
	Editable     bool
}

type Service struct {
	backupDir string
}

func NewService(backupDir string) *Service {
	return &Service{backupDir: backupDir}
}

func (s *Service) List(project database.Project, relative string) ([]Entry, string, error) {
	path, cleanRelative, err := resolveExisting(project.WorkingDir, relative)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", errors.New("selected path is not a folder")
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, "", fmt.Errorf("read folder: %w", err)
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		itemInfo, err := item.Info()
		if err != nil {
			continue
		}
		itemRelative := filepath.Join(cleanRelative, item.Name())
		entries = append(entries, Entry{
			Name:         item.Name(),
			RelativePath: filepath.ToSlash(itemRelative),
			IsDir:        item.IsDir(),
			Size:         uint64(itemInfo.Size()),
			ModifiedAt:   itemInfo.ModTime(),
			Editable:     !item.IsDir() && isEditable(item.Name()),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, filepath.ToSlash(cleanRelative), nil
}

func (s *Service) Upload(project database.Project, directory, filename string, source io.Reader) error {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		return errors.New("invalid file name")
	}
	parent, _, err := resolveExisting(project.WorkingDir, directory)
	if err != nil {
		return err
	}
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return errors.New("upload destination is not a folder")
	}
	target := filepath.Join(parent, filename)
	if !within(project.WorkingDir, target) {
		return errors.New("upload path is outside the project")
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if errors.Is(err, fs.ErrExist) {
		return errors.New("a file with this name already exists")
	}
	if err != nil {
		return fmt.Errorf("create uploaded file: %w", err)
	}
	defer output.Close()
	if _, err := io.Copy(output, source); err != nil {
		os.Remove(target)
		return fmt.Errorf("save upload: %w", err)
	}
	return output.Sync()
}

func (s *Service) CreateFolder(project database.Project, parentRelative, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return errors.New("folder name must not contain a path")
	}
	parent, _, err := resolveExisting(project.WorkingDir, parentRelative)
	if err != nil {
		return err
	}
	target := filepath.Join(parent, name)
	if !within(project.WorkingDir, target) {
		return errors.New("folder path is outside the project")
	}
	if err := os.Mkdir(target, 0o750); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errors.New("a file or folder with this name already exists")
		}
		return fmt.Errorf("create folder: %w", err)
	}
	return nil
}

func (s *Service) Delete(project database.Project, relative string) error {
	path, cleanRelative, err := resolveExisting(project.WorkingDir, relative)
	if err != nil {
		return err
	}
	if cleanRelative == "." {
		return errors.New("the project root cannot be deleted")
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrInvalid) || strings.Contains(strings.ToLower(err.Error()), "not empty") {
			return errors.New("folders must be empty before they can be deleted")
		}
		return fmt.Errorf("delete item: %w", err)
	}
	return nil
}

func (s *Service) ReadText(project database.Project, relative string) (string, error) {
	path, cleanRelative, err := resolveExisting(project.WorkingDir, relative)
	if err != nil {
		return "", err
	}
	if !isEditable(cleanRelative) {
		return "", errors.New("this file type is not editable in the browser")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("folders cannot be edited as text")
	}
	if info.Size() > maxEditableSize {
		return "", errors.New("file is larger than the 2 MiB editor limit")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(content), nil
}

func (s *Service) ReadOptionalText(project database.Project, relative string) (string, error) {
	content, err := s.ReadText(project, relative)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return content, err
}

func (s *Service) SaveText(project database.Project, relative, content string) error {
	if int64(len(content)) > maxEditableSize {
		return errors.New("file is larger than the 2 MiB editor limit")
	}
	target, cleanRelative, exists, err := resolveForWrite(project.WorkingDir, relative)
	if err != nil {
		return err
	}
	if !isEditable(cleanRelative) {
		return errors.New("this file type is not editable in the browser")
	}

	mode := fs.FileMode(0o640)
	if exists {
		info, err := os.Stat(target)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return errors.New("folders cannot be edited as text")
		}
		mode = info.Mode().Perm()
		if err := s.backup(project, cleanRelative, target); err != nil {
			return err
		}
	}

	temp, err := os.CreateTemp(filepath.Dir(target), ".vpsdeck-save-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

func (s *Service) DownloadPath(project database.Project, relative string) (string, string, error) {
	path, _, err := resolveExisting(project.WorkingDir, relative)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", errors.New("use the folder ZIP download for folders")
	}
	return path, info.Name(), nil
}

// Move relocates a file or folder into destDirRelative. It refuses to move the
// project root or to move a folder into itself or one of its descendants, and
// auto-renames on a name collision so nothing is overwritten.
func (s *Service) Move(project database.Project, sourceRelative, destDirRelative string) (string, error) {
	sourcePath, sourceClean, err := resolveExisting(project.WorkingDir, sourceRelative)
	if err != nil {
		return "", err
	}
	if sourceClean == "." {
		return "", errors.New("the project root cannot be moved")
	}
	destPath, err := resolveDir(project.WorkingDir, destDirRelative)
	if err != nil {
		return "", err
	}
	if destPath == sourcePath || isDescendant(sourcePath, destPath) {
		return "", errors.New("a folder cannot be moved into itself")
	}
	if filepath.Dir(sourcePath) == destPath {
		return "", errors.New("the item is already in this folder")
	}
	target := uniqueTarget(destPath, filepath.Base(sourcePath))
	if !within(project.WorkingDir, target) {
		return "", errors.New("destination is outside the project")
	}
	if err := os.Rename(sourcePath, target); err != nil {
		return "", fmt.Errorf("move item: %w", err)
	}
	return relativeTo(project.WorkingDir, target)
}

// Copy duplicates a file or folder (recursively) into destDirRelative, with the
// same containment guards as Move and auto-rename on collision.
func (s *Service) Copy(project database.Project, sourceRelative, destDirRelative string) (string, error) {
	sourcePath, sourceClean, err := resolveExisting(project.WorkingDir, sourceRelative)
	if err != nil {
		return "", err
	}
	if sourceClean == "." {
		return "", errors.New("the project root cannot be copied")
	}
	destPath, err := resolveDir(project.WorkingDir, destDirRelative)
	if err != nil {
		return "", err
	}
	if destPath == sourcePath || isDescendant(sourcePath, destPath) {
		return "", errors.New("a folder cannot be copied into itself")
	}
	target := uniqueTarget(destPath, filepath.Base(sourcePath))
	if !within(project.WorkingDir, target) {
		return "", errors.New("destination is outside the project")
	}
	if err := copyTree(sourcePath, target); err != nil {
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("copy item: %w", err)
	}
	return relativeTo(project.WorkingDir, target)
}

// Rename changes the name of a file or folder in place. newName must be a bare
// name without any path separators.
func (s *Service) Rename(project database.Project, sourceRelative, newName string) (string, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" || filepath.Base(newName) != newName || newName == "." || newName == ".." {
		return "", errors.New("name must not contain a path")
	}
	sourcePath, sourceClean, err := resolveExisting(project.WorkingDir, sourceRelative)
	if err != nil {
		return "", err
	}
	if sourceClean == "." {
		return "", errors.New("the project root cannot be renamed")
	}
	target := filepath.Join(filepath.Dir(sourcePath), newName)
	if !within(project.WorkingDir, target) {
		return "", errors.New("destination is outside the project")
	}
	if target == sourcePath {
		return relativeTo(project.WorkingDir, target)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", errors.New("an item with this name already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(sourcePath, target); err != nil {
		return "", fmt.Errorf("rename item: %w", err)
	}
	return relativeTo(project.WorkingDir, target)
}

// DeleteRecursive removes a file or a folder and all of its contents. It is the
// confirmed, audited counterpart to Delete, which only removes empty folders.
func (s *Service) DeleteRecursive(project database.Project, relative string) error {
	path, cleanRelative, err := resolveExisting(project.WorkingDir, relative)
	if err != nil {
		return err
	}
	if cleanRelative == "." {
		return errors.New("the project root cannot be deleted")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	return nil
}

// NewFile creates an empty file inside parentRelative.
func (s *Service) NewFile(project database.Project, parentRelative, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", errors.New("file name must not contain a path")
	}
	parent, _, err := resolveExisting(project.WorkingDir, parentRelative)
	if err != nil {
		return "", err
	}
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return "", errors.New("the destination is not a folder")
	}
	target := filepath.Join(parent, name)
	if !within(project.WorkingDir, target) {
		return "", errors.New("file path is outside the project")
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if errors.Is(err, fs.ErrExist) {
		return "", errors.New("a file or folder with this name already exists")
	}
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	_ = file.Close()
	return relativeTo(project.WorkingDir, target)
}

// ZipFolder streams a folder and its regular-file contents as a ZIP archive and
// returns the suggested download file name.
func (s *Service) ZipFolder(project database.Project, relative string, w io.Writer) (string, error) {
	path, cleanRelative, err := resolveExisting(project.WorkingDir, relative)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("only folders can be downloaded as a ZIP")
	}
	downloadName := filepath.Base(path)
	if cleanRelative == "." {
		downloadName = project.Name
	}
	if strings.TrimSpace(downloadName) == "" {
		downloadName = "folder"
	}

	archive := zip.NewWriter(w)
	walkErr := filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relInZip, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		if relInZip == "." {
			return nil
		}
		name := filepath.ToSlash(relInZip)
		if entry.IsDir() {
			_, err := archive.Create(name + "/")
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		source, err := os.Open(current)
		if err != nil {
			return err
		}
		defer source.Close()
		_, err = io.Copy(writer, source)
		return err
	})
	if walkErr != nil {
		_ = archive.Close()
		return "", fmt.Errorf("build ZIP archive: %w", walkErr)
	}
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("finish ZIP archive: %w", err)
	}
	return downloadName + ".zip", nil
}

func (s *Service) backup(project database.Project, relative, source string) error {
	destination := filepath.Join(
		s.backupDir,
		"file-edits",
		fmt.Sprintf("project-%d", project.ID),
		time.Now().UTC().Format("20060102-150405.000000000"),
		filepath.FromSlash(relative),
	)
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create edit backup directory: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open file for backup: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create edit backup: %w", err)
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("write edit backup: %w", err)
	}
	return output.Sync()
}

func resolveExisting(root, relative string) (string, string, error) {
	cleanRelative, err := cleanRelativePath(relative)
	if err != nil {
		return "", "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("project root cannot be accessed: %w", err)
	}
	candidate := filepath.Join(canonicalRoot, cleanRelative)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", fmt.Errorf("path does not exist or cannot be accessed: %w", err)
	}
	if !within(canonicalRoot, resolved) {
		return "", "", errors.New("path is outside the project")
	}
	actualRelative, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, filepath.Clean(actualRelative), nil
}

func resolveForWrite(root, relative string) (string, string, bool, error) {
	cleanRelative, err := cleanRelativePath(relative)
	if err != nil || cleanRelative == "." {
		return "", "", false, errors.New("a file path is required")
	}
	candidate := filepath.Join(root, cleanRelative)
	if _, err := os.Lstat(candidate); err == nil {
		resolved, actualRelative, resolveErr := resolveExisting(root, cleanRelative)
		return resolved, actualRelative, true, resolveErr
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", "", false, err
	}
	parent, _, err := resolveExisting(root, filepath.Dir(cleanRelative))
	if err != nil {
		return "", "", false, err
	}
	target := filepath.Join(parent, filepath.Base(cleanRelative))
	if !within(root, target) {
		return "", "", false, errors.New("path is outside the project")
	}
	return target, cleanRelative, false, nil
}

func cleanRelativePath(relative string) (string, error) {
	relative = filepath.FromSlash(strings.TrimSpace(relative))
	if relative == "" {
		return ".", nil
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("absolute paths are not accepted")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path traversal is not allowed")
	}
	return clean, nil
}

func resolveDir(root, relative string) (string, error) {
	path, _, err := resolveExisting(root, relative)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("the destination is not a folder")
	}
	return path, nil
}

// relativeTo returns the slash-form path of target relative to the canonical
// project root. target's parent is already canonical, so target need not exist.
func relativeTo(root, target string) (string, error) {
	if canonicalRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = canonicalRoot
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

// isDescendant reports whether child sits inside parent (and is not parent).
func isDescendant(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// uniqueTarget returns dir/name, or dir/name (2), dir/name (3)… if that path is
// already taken, so move/copy never overwrites an existing item.
func uniqueTarget(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = name
		ext = ""
	}
	for index := 2; index < 100000; index++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, index, ext))
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
	return filepath.Join(dir, stem+" (copy)"+ext)
}

// copyTree recursively copies a regular file or a directory. Symlinks and other
// special files are skipped so a copy can never escape the project root.
func copyTree(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.MkdirAll(target, 0o750); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if err := copyTree(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		return copyFile(source, target, info.Mode().Perm())
	default:
		return nil
	}
}

func copyFile(source, target string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Sync()
}

func within(root, path string) bool {
	if canonicalRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = canonicalRoot
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func isEditable(name string) bool {
	if filepath.Base(name) == ".env" {
		return true
	}
	return editableExtensions[strings.ToLower(filepath.Ext(name))]
}
