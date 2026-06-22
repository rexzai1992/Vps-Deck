package projects

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/vpsdeck/vpsdeck/internal/database"
)

var allowedTypes = map[string]bool{
	"auto":           true,
	"docker":         true,
	"docker-compose": true,
	"nodejs":         true,
	"python":         true,
	"golang":         true,
	"static":         true,
	"pm2":            true,
	"systemd":        true,
	"custom":         true,
}

type CreateInput struct {
	Name           string
	Type           string
	Domain         string
	Port           int
	WorkingDir     string
	HealthcheckURL string
}

type FolderEntry struct {
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
}

type FolderBreadcrumb struct {
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
}

type FolderListing struct {
	RootIndex    int                `json:"root_index"`
	RootName     string             `json:"root_name"`
	RootPath     string             `json:"root_path"`
	CurrentPath  string             `json:"current_path"`
	AbsolutePath string             `json:"absolute_path"`
	ParentPath   string             `json:"parent_path"`
	Folders      []FolderEntry      `json:"folders"`
	Breadcrumbs  []FolderBreadcrumb `json:"breadcrumbs"`
	Warning      string             `json:"warning,omitempty"`
	ShowHidden   bool               `json:"show_hidden"`
}

type Service struct {
	db           *database.DB
	allowedRoots []string
}

func NewService(db *database.DB, roots []string) (*Service, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one Simple Mode project root is required")
	}
	canonicalRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		canonical, err := canonicalDirectory(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("prepare allowed root %q: %w", root, err)
		}
		canonicalRoots = append(canonicalRoots, canonical)
	}
	if len(canonicalRoots) == 0 {
		return nil, errors.New("none of the configured Simple Mode project roots exist")
	}
	return &Service{db: db, allowedRoots: canonicalRoots}, nil
}

func (s *Service) AllowedRoots() []string {
	return append([]string(nil), s.allowedRoots...)
}

func (s *Service) BrowseFolders(rootIndex int, relative string, showHidden bool) (FolderListing, error) {
	if rootIndex < 0 || rootIndex >= len(s.allowedRoots) {
		return FolderListing{}, errors.New("invalid project root")
	}
	root := s.allowedRoots[rootIndex]
	cleanRelative, err := cleanBrowsePath(relative)
	if err != nil {
		return FolderListing{}, err
	}
	candidate := filepath.Join(root, cleanRelative)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return FolderListing{}, errors.New("folder does not exist or cannot be accessed")
	}
	if !pathWithin(root, resolved) {
		return FolderListing{}, errors.New("folder is outside the approved project root")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return FolderListing{}, errors.New("selected path is not an accessible folder")
	}
	actualRelative, err := filepath.Rel(root, resolved)
	if err != nil {
		return FolderListing{}, errors.New("could not resolve folder location")
	}
	actualRelative = filepath.Clean(actualRelative)

	items, err := os.ReadDir(resolved)
	if err != nil {
		return FolderListing{}, errors.New("folder cannot be read")
	}
	folders := make([]FolderEntry, 0, len(items))
	skipped := 0
	for _, item := range items {
		if !showHidden && strings.HasPrefix(item.Name(), ".") {
			continue
		}
		if item.Type()&os.ModeSymlink != 0 {
			skipped++
			continue
		}
		childPath := filepath.Join(resolved, item.Name())
		childInfo, err := item.Info()
		if err != nil || !childInfo.IsDir() {
			if err != nil {
				skipped++
			}
			continue
		}
		childResolved, err := filepath.EvalSymlinks(childPath)
		if err != nil || !pathWithin(root, childResolved) {
			skipped++
			continue
		}
		folders = append(folders, FolderEntry{
			Name:         item.Name(),
			RelativePath: filepath.ToSlash(filepath.Join(actualRelative, item.Name())),
		})
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name)
	})

	parent := ""
	if actualRelative != "." {
		parent = filepath.ToSlash(filepath.Dir(actualRelative))
		if parent == "." {
			parent = ""
		}
	}
	listing := FolderListing{
		RootIndex:    rootIndex,
		RootName:     filepath.Base(root),
		RootPath:     root,
		CurrentPath:  filepath.ToSlash(actualRelative),
		AbsolutePath: resolved,
		ParentPath:   parent,
		Folders:      folders,
		Breadcrumbs:  folderBreadcrumbs(actualRelative),
		ShowHidden:   showHidden,
	}
	if skipped > 0 {
		listing.Warning = fmt.Sprintf("%d inaccessible or linked folder(s) were hidden", skipped)
	}
	return listing, nil
}

func (s *Service) List(ctx context.Context) ([]database.Project, error) {
	return s.db.ListProjects(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (database.Project, error) {
	return s.db.ProjectByID(ctx, id)
}

func (s *Service) CreateExisting(ctx context.Context, input CreateInput) (database.Project, error) {
	input.Name = strings.TrimSpace(input.Name)
	if err := validateName(input.Name); err != nil {
		return database.Project{}, err
	}

	canonical, err := canonicalDirectory(input.WorkingDir)
	if err != nil {
		return database.Project{}, err
	}
	if !s.isAllowed(canonical) {
		return database.Project{}, errors.New("folder is outside the approved Simple Mode project roots")
	}

	projectType := strings.ToLower(strings.TrimSpace(input.Type))
	if !allowedTypes[projectType] {
		return database.Project{}, errors.New("unsupported project type")
	}
	if projectType == "" || projectType == "auto" {
		projectType = DetectType(canonical)
	}
	if input.Port < 0 || input.Port > 65535 {
		return database.Project{}, errors.New("port must be between 1 and 65535, or left empty")
	}
	input.Domain = strings.ToLower(strings.TrimSpace(input.Domain))
	if input.Domain != "" && strings.ContainsAny(input.Domain, " /:") {
		return database.Project{}, errors.New("domain must be a hostname without protocol or path")
	}
	input.HealthcheckURL = strings.TrimSpace(input.HealthcheckURL)
	if input.HealthcheckURL != "" {
		parsed, err := url.ParseRequestURI(input.HealthcheckURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return database.Project{}, errors.New("health check must be a valid HTTP or HTTPS URL")
		}
	}

	project := database.Project{
		Name:           input.Name,
		Type:           projectType,
		Domain:         input.Domain,
		Port:           input.Port,
		WorkingDir:     canonical,
		Status:         "unknown",
		HealthcheckURL: input.HealthcheckURL,
	}
	id, err := s.db.CreateProject(ctx, project)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return database.Project{}, errors.New("a project with this name or folder is already registered")
		}
		return database.Project{}, err
	}
	return s.db.ProjectByID(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.db.DeleteProject(ctx, id)
}

func (s *Service) isAllowed(path string) bool {
	for _, root := range s.allowedRoots {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func cleanBrowsePath(relative string) (string, error) {
	relative = filepath.FromSlash(strings.TrimSpace(relative))
	if relative == "" {
		return ".", nil
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("absolute folder paths are not accepted")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("folder traversal is not allowed")
	}
	return clean, nil
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func folderBreadcrumbs(relative string) []FolderBreadcrumb {
	result := []FolderBreadcrumb{{Name: "Root", RelativePath: ""}}
	relative = filepath.ToSlash(filepath.Clean(relative))
	if relative == "." || relative == "" {
		return result
	}
	current := ""
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.ToSlash(filepath.Join(current, part))
		result = append(result, FolderBreadcrumb{Name: part, RelativePath: current})
	}
	return result
}

func canonicalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("working directory is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("folder does not exist or cannot be accessed: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("working directory must be a folder")
	}
	return filepath.Clean(resolved), nil
}

func validateName(name string) error {
	if len(name) < 2 || len(name) > 100 {
		return errors.New("project name must contain 2 to 100 characters")
	}
	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == ' ' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return errors.New("project name contains unsupported characters")
	}
	return nil
}

func DetectType(path string) string {
	checks := []struct {
		files       []string
		projectType string
	}{
		{[]string{"compose.yaml", "compose.yml", "docker-compose.yml"}, "docker-compose"},
		{[]string{"Dockerfile"}, "docker"},
		{[]string{"package.json"}, "nodejs"},
		{[]string{"requirements.txt", "pyproject.toml"}, "python"},
		{[]string{"go.mod"}, "golang"},
		{[]string{"index.html"}, "static"},
	}
	for _, check := range checks {
		for _, name := range check.files {
			if info, err := os.Stat(filepath.Join(path, name)); err == nil && !info.IsDir() {
				return check.projectType
			}
		}
	}
	return "custom"
}
