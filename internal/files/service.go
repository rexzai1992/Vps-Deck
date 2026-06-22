package files

import (
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
		return "", "", errors.New("folder downloads as ZIP are not implemented yet")
	}
	return path, info.Name(), nil
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
