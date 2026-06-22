package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

type Project struct {
	ID             int64
	Name           string
	Type           string
	Domain         string
	Port           int
	WorkingDir     string
	Status         string
	HealthcheckURL string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ProjectSource struct {
	ProjectID      int64
	Provider       string
	RepositoryURL  string
	Branch         string
	DeployMode     string
	LastCommit     string
	LastDeployedAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Deployment struct {
	ID           int64
	ProjectID    int64
	ProjectName  string
	Action       string
	State        string
	CommitBefore string
	CommitAfter  string
	Output       string
	Error        string
	CreatedAt    time.Time
	FinishedAt   *time.Time
}

type ProjectCounts struct {
	Total   int
	Running int
	Stopped int
	Failed  int
}

type AuditEntry struct {
	ID         int64
	UserID     sql.NullInt64
	Username   string
	IPAddress  string
	Action     string
	TargetType string
	TargetID   string
	Details    string
	Success    bool
	Error      string
	CreatedAt  time.Time
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	raw.SetMaxOpenConns(1)

	db := &DB{DB: raw}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			raw.Close()
			return nil, fmt.Errorf("apply %s: %w", pragma, err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	migrations := []string{
		`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL,
			domain TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			working_dir TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'unknown',
			healthcheck_url TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER,
			ip_address TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			success BOOLEAN NOT NULL DEFAULT 1,
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
		);

		CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
		CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at DESC);
		`,
		`
		CREATE TABLE project_sources (
			project_id INTEGER PRIMARY KEY,
			provider TEXT NOT NULL,
			repository_url TEXT NOT NULL,
			branch TEXT NOT NULL DEFAULT 'main',
			deploy_mode TEXT NOT NULL DEFAULT 'git',
			last_commit TEXT NOT NULL DEFAULT '',
			last_deployed_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
		);

		CREATE TABLE deployments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			state TEXT NOT NULL,
			commit_before TEXT NOT NULL DEFAULT '',
			commit_after TEXT NOT NULL DEFAULT '',
			output TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
		);

		CREATE INDEX idx_deployments_project_created
			ON deployments(project_id, created_at DESC);
		`,
	}

	for i, statement := range migrations {
		version := i + 1
		var exists int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if exists > 0 {
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

func (db *DB) UserCount(ctx context.Context) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (db *DB) CreateUser(ctx context.Context, username, passwordHash, role string) (int64, error) {
	result, err := db.ExecContext(ctx,
		"INSERT INTO users(username, password_hash, role) VALUES (?, ?, ?)",
		username, passwordHash, role,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	err := db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, created_at
		FROM users WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt)
	return user, err
}

func (db *DB) UserBySession(ctx context.Context, sessionID string, now time.Time) (User, error) {
	var user User
	err := db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash, u.role, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > ?
	`, sessionID, now.UTC()).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt,
	)
	return user, err
}

func (db *DB) CreateSession(ctx context.Context, id string, userID int64, expiresAt time.Time) error {
	_, err := db.ExecContext(ctx,
		"INSERT INTO sessions(id, user_id, expires_at) VALUES (?, ?, ?)",
		id, userID, expiresAt.UTC(),
	)
	return err
}

func (db *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (db *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", now.UTC())
	return err
}

func (db *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, type, domain, port, working_dir, status, healthcheck_url, created_at, updated_at
		FROM projects ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(
			&project.ID, &project.Name, &project.Type, &project.Domain, &project.Port,
			&project.WorkingDir, &project.Status, &project.HealthcheckURL,
			&project.CreatedAt, &project.UpdatedAt,
		); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (db *DB) ProjectByID(ctx context.Context, id int64) (Project, error) {
	var project Project
	err := db.QueryRowContext(ctx, `
		SELECT id, name, type, domain, port, working_dir, status, healthcheck_url, created_at, updated_at
		FROM projects WHERE id = ?
	`, id).Scan(
		&project.ID, &project.Name, &project.Type, &project.Domain, &project.Port,
		&project.WorkingDir, &project.Status, &project.HealthcheckURL,
		&project.CreatedAt, &project.UpdatedAt,
	)
	return project, err
}

func (db *DB) ProjectByWorkingDir(ctx context.Context, workingDir string) (Project, error) {
	var project Project
	err := db.QueryRowContext(ctx, `
		SELECT id, name, type, domain, port, working_dir, status, healthcheck_url, created_at, updated_at
		FROM projects WHERE working_dir = ?
	`, workingDir).Scan(
		&project.ID, &project.Name, &project.Type, &project.Domain, &project.Port,
		&project.WorkingDir, &project.Status, &project.HealthcheckURL,
		&project.CreatedAt, &project.UpdatedAt,
	)
	return project, err
}

func (db *DB) CreateProject(ctx context.Context, project Project) (int64, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO projects(name, type, domain, port, working_dir, status, healthcheck_url)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, project.Name, project.Type, project.Domain, project.Port, project.WorkingDir, project.Status, project.HealthcheckURL)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) DeleteProject(ctx context.Context, id int64) error {
	result, err := db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (db *DB) UpdateProjectRuntime(ctx context.Context, id int64, status string, port int) error {
	_, err := db.ExecContext(ctx, `
		UPDATE projects
		SET status = ?, port = CASE WHEN ? > 0 THEN ? ELSE port END, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, port, port, id)
	return err
}

func (db *DB) CreateProjectSource(ctx context.Context, source ProjectSource) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO project_sources(
			project_id, provider, repository_url, branch, deploy_mode, last_commit, last_deployed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, source.ProjectID, source.Provider, source.RepositoryURL, source.Branch,
		source.DeployMode, source.LastCommit, source.LastDeployedAt)
	return err
}

func (db *DB) ProjectSourceByProjectID(ctx context.Context, projectID int64) (ProjectSource, error) {
	var source ProjectSource
	var deployedAt sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT project_id, provider, repository_url, branch, deploy_mode, last_commit,
		       last_deployed_at, created_at, updated_at
		FROM project_sources
		WHERE project_id = ?
	`, projectID).Scan(
		&source.ProjectID, &source.Provider, &source.RepositoryURL, &source.Branch,
		&source.DeployMode, &source.LastCommit, &deployedAt, &source.CreatedAt, &source.UpdatedAt,
	)
	if deployedAt.Valid {
		value := deployedAt.Time
		source.LastDeployedAt = &value
	}
	return source, err
}

func (db *DB) UpdateProjectSourceDeployment(ctx context.Context, projectID int64, commit string, deployedAt time.Time) error {
	_, err := db.ExecContext(ctx, `
		UPDATE project_sources
		SET last_commit = ?, last_deployed_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE project_id = ?
	`, commit, deployedAt.UTC(), projectID)
	return err
}

func (db *DB) CreateDeployment(ctx context.Context, deployment Deployment) (int64, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO deployments(project_id, action, state, commit_before, commit_after, output, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, deployment.ProjectID, deployment.Action, deployment.State, deployment.CommitBefore,
		deployment.CommitAfter, deployment.Output, deployment.Error)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) FinishDeployment(ctx context.Context, id int64, state, commitBefore, commitAfter, output, errorMessage string, finishedAt time.Time) error {
	_, err := db.ExecContext(ctx, `
		UPDATE deployments
		SET state = ?, commit_before = ?, commit_after = ?, output = ?, error = ?, finished_at = ?
		WHERE id = ?
	`, state, commitBefore, commitAfter, output, errorMessage, finishedAt.UTC(), id)
	return err
}

func (db *DB) ListProjectDeployments(ctx context.Context, projectID int64, limit int) ([]Deployment, error) {
	if limit < 1 || limit > 200 {
		limit = 20
	}
	rows, err := db.QueryContext(ctx, `
		SELECT d.id, d.project_id, p.name, d.action, d.state, d.commit_before,
		       d.commit_after, d.output, d.error, d.created_at, d.finished_at
		FROM deployments d
		JOIN projects p ON p.id = d.project_id
		WHERE d.project_id = ?
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT ?
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

func (db *DB) ListDeployments(ctx context.Context, limit int) ([]Deployment, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT d.id, d.project_id, p.name, d.action, d.state, d.commit_before,
		       d.commit_after, d.output, d.error, d.created_at, d.finished_at
		FROM deployments d
		JOIN projects p ON p.id = d.project_id
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

func scanDeployments(rows *sql.Rows) ([]Deployment, error) {
	var deployments []Deployment
	for rows.Next() {
		var deployment Deployment
		var finishedAt sql.NullTime
		if err := rows.Scan(
			&deployment.ID, &deployment.ProjectID, &deployment.ProjectName,
			&deployment.Action, &deployment.State, &deployment.CommitBefore,
			&deployment.CommitAfter, &deployment.Output, &deployment.Error,
			&deployment.CreatedAt, &finishedAt,
		); err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			value := finishedAt.Time
			deployment.FinishedAt = &value
		}
		deployments = append(deployments, deployment)
	}
	return deployments, rows.Err()
}

func (db *DB) ProjectCounts(ctx context.Context) (ProjectCounts, error) {
	var counts ProjectCounts
	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'stopped' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM projects
	`).Scan(&counts.Total, &counts.Running, &counts.Stopped, &counts.Failed)
	return counts, err
}

func (db *DB) WriteAudit(ctx context.Context, entry AuditEntry) error {
	var userID any
	if entry.UserID.Valid {
		userID = entry.UserID.Int64
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_logs(
			user_id, ip_address, action, target_type, target_id, details, success, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, entry.IPAddress, entry.Action, entry.TargetType, entry.TargetID,
		entry.Details, entry.Success, entry.Error)
	return err
}

func (db *DB) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.user_id, COALESCE(u.username, ''), a.ip_address, a.action,
		       a.target_type, a.target_id, a.details, a.success, a.error, a.created_at
		FROM audit_logs a
		LEFT JOIN users u ON u.id = a.user_id
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(
			&entry.ID, &entry.UserID, &entry.Username, &entry.IPAddress, &entry.Action,
			&entry.TargetType, &entry.TargetID, &entry.Details, &entry.Success,
			&entry.Error, &entry.CreatedAt,
		); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
