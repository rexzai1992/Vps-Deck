package web

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vpsdeck/vpsdeck/internal/database"
)

// fileEntryJSON is the explicit JSON shape consumed by the explorer UI.
type fileEntryJSON struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"is_dir"`
	Size       uint64 `json:"size"`
	ModifiedAt string `json:"modified_at"`
	Editable   bool   `json:"editable"`
}

type fileListJSON struct {
	ProjectID   int64           `json:"project_id"`
	ProjectName string          `json:"project_name"`
	CurrentPath string          `json:"current_path"`
	ParentPath  string          `json:"parent_path"`
	IsRoot      bool            `json:"is_root"`
	Breadcrumbs []Breadcrumb    `json:"breadcrumbs"`
	Entries     []fileEntryJSON `json:"entries"`
}

func (s *Server) projectForAPI(c *gin.Context) (database.Project, bool) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return database.Project{}, false
	}
	project, err := s.projects.Get(c.Request.Context(), id)
	if err != nil {
		if !database.IsNotFound(err) {
			s.logger.Error("get project", "id", id, "error", err)
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return database.Project{}, false
	}
	return project, true
}

// --- project-scoped JSON handlers (thin wrappers over the shared bodies) -----

func (s *Server) filesListAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.respondFileList(c, project)
	}
}

func (s *Server) moveFilesAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.transferFiles(c, project, false)
	}
}

func (s *Server) copyFilesAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.transferFiles(c, project, true)
	}
}

func (s *Server) renameFileAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.renameFileFor(c, project)
	}
}

func (s *Server) deleteFilesAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.deleteFilesFor(c, project)
	}
}

func (s *Server) newFileAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.newFileFor(c, project)
	}
}

func (s *Server) newFolderAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.newFolderFor(c, project)
	}
}

func (s *Server) uploadFilesAPI(c *gin.Context) {
	if project, ok := s.projectForAPI(c); ok {
		s.uploadFilesFor(c, project)
	}
}

func (s *Server) downloadFolderZip(c *gin.Context) {
	if project, ok := s.projectForRequest(c); ok {
		s.streamFolderZip(c, project, c.Query("path"))
	}
}

// --- shared action bodies (used by both project and advanced explorers) ------

func (s *Server) respondFileList(c *gin.Context, project database.Project) {
	entries, currentPath, err := s.files.List(project, c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	parent := ""
	if currentPath != "." && currentPath != "" {
		parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentPath)))
		if parent == "." {
			parent = ""
		}
	}
	items := make([]fileEntryJSON, 0, len(entries))
	for _, entry := range entries {
		items = append(items, fileEntryJSON{
			Name:       entry.Name,
			Path:       entry.RelativePath,
			IsDir:      entry.IsDir,
			Size:       entry.Size,
			ModifiedAt: entry.ModifiedAt.Format("2006-01-02 15:04"),
			Editable:   entry.Editable,
		})
	}
	c.JSON(http.StatusOK, fileListJSON{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		CurrentPath: currentPath,
		ParentPath:  parent,
		IsRoot:      currentPath == "." || currentPath == "",
		Breadcrumbs: breadcrumbs(currentPath),
		Entries:     items,
	})
}

func (s *Server) transferFiles(c *gin.Context, project database.Project, copyMode bool) {
	dest := c.PostForm("dest")
	targets := c.PostFormArray("target")
	if len(targets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no items were selected"})
		return
	}
	user := s.mustUser(c)
	action := "file_move"
	if copyMode {
		action = "file_copy"
	}
	var failures []string
	moved := 0
	for _, target := range targets {
		var err error
		if copyMode {
			_, err = s.files.Copy(project, target, dest)
		} else {
			_, err = s.files.Move(project, target, dest)
		}
		if err != nil {
			failures = append(failures, filepath.Base(target)+": "+err.Error())
			s.audit(c, &user, action, "project", strconv.FormatInt(project.ID, 10), target+" → "+dest, false, err.Error())
			continue
		}
		moved++
		s.audit(c, &user, action, "project", strconv.FormatInt(project.ID, 10), target+" → "+dest, true, "")
	}
	s.respondFileAction(c, moved, failures)
}

func (s *Server) renameFileFor(c *gin.Context, project database.Project) {
	target := c.PostForm("target")
	name := c.PostForm("name")
	user := s.mustUser(c)
	newPath, err := s.files.Rename(project, target, name)
	if err != nil {
		s.audit(c, &user, "file_rename", "project", strconv.FormatInt(project.ID, 10), target, false, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, &user, "file_rename", "project", strconv.FormatInt(project.ID, 10), target+" → "+newPath, true, "")
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": newPath})
}

func (s *Server) deleteFilesFor(c *gin.Context, project database.Project) {
	targets := c.PostFormArray("target")
	if len(targets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no items were selected"})
		return
	}
	recursive := c.PostForm("recursive") == "true"
	user := s.mustUser(c)
	detailSuffix := ""
	if recursive {
		detailSuffix = " (recursive)"
	}
	var failures []string
	deleted := 0
	for _, target := range targets {
		var err error
		if recursive {
			err = s.files.DeleteRecursive(project, target)
		} else {
			err = s.files.Delete(project, target)
		}
		if err != nil {
			failures = append(failures, filepath.Base(target)+": "+err.Error())
			s.audit(c, &user, "file_delete", "project", strconv.FormatInt(project.ID, 10), target+detailSuffix, false, err.Error())
			continue
		}
		deleted++
		s.audit(c, &user, "file_delete", "project", strconv.FormatInt(project.ID, 10), target+detailSuffix, true, "")
	}
	s.respondFileAction(c, deleted, failures)
}

func (s *Server) newFileFor(c *gin.Context, project database.Project) {
	dir := c.PostForm("path")
	name := c.PostForm("name")
	user := s.mustUser(c)
	newPath, err := s.files.NewFile(project, dir, name)
	if err != nil {
		s.audit(c, &user, "file_new", "project", strconv.FormatInt(project.ID, 10), name, false, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, &user, "file_new", "project", strconv.FormatInt(project.ID, 10), newPath, true, "")
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": newPath})
}

func (s *Server) newFolderFor(c *gin.Context, project database.Project) {
	dir := c.PostForm("path")
	name := c.PostForm("name")
	user := s.mustUser(c)
	if err := s.files.CreateFolder(project, dir, name); err != nil {
		s.audit(c, &user, "folder_create", "project", strconv.FormatInt(project.ID, 10), name, false, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, &user, "folder_create", "project", strconv.FormatInt(project.ID, 10), filepath.ToSlash(filepath.Join(dir, name)), true, "")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) uploadFilesFor(c *gin.Context, project database.Project) {
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the upload"})
		return
	}
	headers := form.File["files"]
	if len(headers) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no files were uploaded"})
		return
	}
	dir := c.PostForm("path")
	user := s.mustUser(c)
	var failures []string
	uploaded := 0
	for _, header := range headers {
		source, openErr := header.Open()
		if openErr != nil {
			failures = append(failures, header.Filename+": could not open upload")
			continue
		}
		uploadErr := s.files.Upload(project, dir, header.Filename, source)
		source.Close()
		if uploadErr != nil {
			failures = append(failures, header.Filename+": "+uploadErr.Error())
			s.audit(c, &user, "file_upload", "project", strconv.FormatInt(project.ID, 10), header.Filename, false, uploadErr.Error())
			continue
		}
		uploaded++
		s.audit(c, &user, "file_upload", "project", strconv.FormatInt(project.ID, 10), filepath.ToSlash(filepath.Join(dir, filepath.Base(header.Filename))), true, "")
	}
	s.respondFileAction(c, uploaded, failures)
}

func (s *Server) streamFolderZip(c *gin.Context, project database.Project, requestedPath string) {
	name := filepath.Base(filepath.Clean(filepath.FromSlash(requestedPath)))
	if requestedPath == "" || name == "." || name == string(filepath.Separator) {
		name = project.Name
	}
	if strings.TrimSpace(name) == "" {
		name = "folder"
	}
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+name+`.zip"`)
	if _, err := s.files.ZipFolder(project, requestedPath, c.Writer); err != nil {
		s.logger.Warn("zip folder download", "project_id", project.ID, "error", err)
	}
}

// respondFileAction reports the outcome of a multi-item operation.
func (s *Server) respondFileAction(c *gin.Context, succeeded int, failures []string) {
	if len(failures) > 0 {
		status := http.StatusBadRequest
		if succeeded > 0 {
			status = http.StatusMultiStatus
		}
		c.JSON(status, gin.H{"ok": succeeded > 0, "succeeded": succeeded, "error": strings.Join(failures, "; ")})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "succeeded": succeeded})
}
