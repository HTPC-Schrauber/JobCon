package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound    = errors.New("record not found")
	ErrServerInUse = errors.New("server is still in use by configured jobs")
)

type Server struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Host          string     `json:"host"`
	Port          int        `json:"port"`
	User          string     `json:"user"`
	SSHKeyPath    string     `json:"ssh_key_path"`
	Status        string     `json:"status"` // "online", "offline", "unknown"
	JobsDir       string     `json:"jobs_dir"`
	ScriptsDir    string     `json:"scripts_dir"`
	EnvFile       string     `json:"env_file"`
	KeepReleases  int        `json:"keep_releases"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	DisplayName  string     `json:"display_name"`
	Email        string     `json:"email"`
	Role         string     `json:"role"`        // "admin", "operator", "viewer"
	AuthSource   string     `json:"auth_source"` // "local", "ldap"
	IsActive     bool       `json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

type Job struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	ServerID        string    `json:"server_id"`
	ServerName      string    `json:"server_name,omitempty"`
	GroupID         string    `json:"group_id"`
	ArtifactID      string    `json:"artifact_id"`
	ActiveVersion   string    `json:"active_version"`
	NexusRepo       string    `json:"nexus_repo"`
	DefaultContext  string    `json:"default_context"`
	AllowConcurrent bool      `json:"allow_concurrent"`
	RetentionRuns   int       `json:"retention_runs"`
	EnvFile         string    `json:"env_file"`
	IsDeployed      bool      `json:"is_deployed"`
	DeployedVersion string    `json:"deployed_version,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type JobWithRunInfo struct {
	Job
	LastRunID         string     `json:"last_run_id,omitempty"`
	LastRunAction     string     `json:"last_run_action,omitempty"`
	LastRunStatus     string     `json:"last_run_status,omitempty"`
	LastRunStartedAt  *time.Time `json:"last_run_started_at,omitempty"`
	LastRunExitCode   *int       `json:"last_run_exit_code,omitempty"`
	LastRunDurationMS *int64     `json:"last_run_duration_ms,omitempty"`
}

type JobFilter struct {
	Search     string `json:"search"`
	GroupID    string `json:"group_id"`
	ArtifactID string `json:"artifact_id"`
	ServerID   string `json:"server_id"`
	Status     string `json:"status"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
	SortBy    string `json:"sort_by"`    // "name", "group_id", "server", "version", "last_run"
	SortOrder string `json:"sort_order"` // "asc", "desc"
	GroupBy   string `json:"group_by"`   // e.g. "group_id"
}

type JobPageResult struct {
	Jobs           []JobWithRunInfo `json:"jobs"`
	TotalCount     int              `json:"total_count"`
	TotalPages     int              `json:"total_pages"`
	CurrentPage    int              `json:"current_page"`
	PageSize       int              `json:"page_size"`
	DistinctGroups []string         `json:"distinct_groups"`
}

type Execution struct {
	ID          string     `json:"id"`
	JobID       string     `json:"job_id"`
	JobName     string     `json:"job_name,omitempty"`
	Action      string     `json:"action"` // "run", "deploy"
	Status      string     `json:"status"` // "pending", "running", "success", "failed", "aborted"
	Version     string     `json:"version"`
	Context     string     `json:"context,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	DurationMS  *int64     `json:"duration_ms,omitempty"`
	LogPath     string     `json:"log_path"`
	TriggeredBy string     `json:"triggered_by"`
	Purged      bool       `json:"purged"`
}

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	Role       string     `json:"role"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// -----------------------------------------------------------------------------
// SERVERS CRUD
// -----------------------------------------------------------------------------

func (db *DB) CreateServer(s *Server) error {
	s.CreatedAt = time.Now()
	s.UpdatedAt = s.CreatedAt
	if s.Port == 0 {
		s.Port = 22
	}
	if s.User == "" {
		s.User = "talend"
	}
	if s.Status == "" {
		s.Status = "unknown"
	}
	if s.JobsDir == "" {
		s.JobsDir = "/opt/talend/jobs"
	}
	if s.ScriptsDir == "" {
		s.ScriptsDir = "/opt/talend/scripts"
	}
	if s.KeepReleases <= 0 {
		s.KeepReleases = 3
	}

	query := `INSERT INTO servers (id, name, host, port, user, ssh_key_path, status, jobs_dir, scripts_dir, env_file, keep_releases, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, s.ID, s.Name, s.Host, s.Port, s.User, s.SSHKeyPath, s.Status, s.JobsDir, s.ScriptsDir, s.EnvFile, s.KeepReleases, s.CreatedAt, s.UpdatedAt)
	return err
}

func (db *DB) GetServer(id string) (*Server, error) {
	row := db.QueryRow(`SELECT id, name, host, port, user, ssh_key_path, status, jobs_dir, scripts_dir, env_file, keep_releases, last_checked_at, created_at, updated_at FROM servers WHERE id = ?`, id)
	var s Server
	err := row.Scan(&s.ID, &s.Name, &s.Host, &s.Port, &s.User, &s.SSHKeyPath, &s.Status, &s.JobsDir, &s.ScriptsDir, &s.EnvFile, &s.KeepReleases, &s.LastCheckedAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

func (db *DB) ListServers() ([]Server, error) {
	rows, err := db.Query(`SELECT id, name, host, port, user, ssh_key_path, status, jobs_dir, scripts_dir, env_file, keep_releases, last_checked_at, created_at, updated_at FROM servers ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.Name, &s.Host, &s.Port, &s.User, &s.SSHKeyPath, &s.Status, &s.JobsDir, &s.ScriptsDir, &s.EnvFile, &s.KeepReleases, &s.LastCheckedAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

func (db *DB) UpdateServer(s *Server) error {
	s.UpdatedAt = time.Now()
	if s.JobsDir == "" {
		s.JobsDir = "/opt/talend/jobs"
	}
	if s.ScriptsDir == "" {
		s.ScriptsDir = "/opt/talend/scripts"
	}
	if s.KeepReleases <= 0 {
		s.KeepReleases = 3
	}
	res, err := db.Exec(`UPDATE servers SET name = ?, host = ?, port = ?, user = ?, ssh_key_path = ?, jobs_dir = ?, scripts_dir = ?, env_file = ?, keep_releases = ?, updated_at = ? WHERE id = ?`,
		s.Name, s.Host, s.Port, s.User, s.SSHKeyPath, s.JobsDir, s.ScriptsDir, s.EnvFile, s.KeepReleases, s.UpdatedAt, s.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UpdateServerStatus(id, status string) error {
	now := time.Now()
	_, err := db.Exec(`UPDATE servers SET status = ?, last_checked_at = ?, updated_at = ? WHERE id = ?`,
		status, now, now, id)
	return err
}

func (db *DB) GetJobsByServerID(serverID string) ([]Job, error) {
	rows, err := db.Query(`
		SELECT j.id, j.name, j.server_id, COALESCE(s.name, ''), j.group_id, j.artifact_id, j.active_version, j.nexus_repo, j.default_context, j.allow_concurrent, j.retention_runs, j.env_file, j.is_deployed, j.deployed_version, j.created_at, j.updated_at
		FROM jobs j
		LEFT JOIN servers s ON j.server_id = s.id
		WHERE j.server_id = ?
		ORDER BY j.name ASC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var allowConcurrent int
		var isDeployed int
		if err := rows.Scan(&j.ID, &j.Name, &j.ServerID, &j.ServerName, &j.GroupID, &j.ArtifactID, &j.ActiveVersion, &j.NexusRepo, &j.DefaultContext, &allowConcurrent, &j.RetentionRuns, &j.EnvFile, &isDeployed, &j.DeployedVersion, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.AllowConcurrent = allowConcurrent == 1
		j.IsDeployed = isDeployed == 1
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (db *DB) DeleteServer(id string) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE server_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrServerInUse
	}

	res, err := db.Exec(`DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteServerWithJobReassignment(serverID string, targetServerID string) error {
	if serverID == targetServerID {
		return errors.New("target server must be different from server being deleted")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if targetServerID != "" {
		var dummy string
		if err := tx.QueryRow(`SELECT id FROM servers WHERE id = ?`, targetServerID).Scan(&dummy); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("target server not found")
			}
			return err
		}
		now := time.Now()
		if _, err := tx.Exec(`UPDATE jobs SET server_id = ?, updated_at = ? WHERE server_id = ?`, targetServerID, now, serverID); err != nil {
			return err
		}
	} else {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM jobs WHERE server_id = ?`, serverID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrServerInUse
		}
	}

	res, err := tx.Exec(`DELETE FROM servers WHERE id = ?`, serverID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}

	return tx.Commit()
}

// -----------------------------------------------------------------------------
// USERS CRUD
// -----------------------------------------------------------------------------

func (db *DB) CountUsers() (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (db *DB) CreateUser(u *User) error {
	u.CreatedAt = time.Now()
	query := `INSERT INTO users (id, username, password_hash, display_name, email, role, auth_source, is_active, created_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, u.ID, u.Username, u.PasswordHash, u.DisplayName, u.Email, u.Role, u.AuthSource, u.IsActive, u.CreatedAt)
	return err
}

func (db *DB) GetUserByUsername(username string) (*User, error) {
	row := db.QueryRow(`SELECT id, username, password_hash, display_name, email, role, auth_source, is_active, created_at, last_login_at FROM users WHERE username = ?`, username)
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Email, &u.Role, &u.AuthSource, &u.IsActive, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

func (db *DB) GetUserByID(id string) (*User, error) {
	row := db.QueryRow(`SELECT id, username, password_hash, display_name, email, role, auth_source, is_active, created_at, last_login_at FROM users WHERE id = ?`, id)
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Email, &u.Role, &u.AuthSource, &u.IsActive, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

func (db *DB) ListUsers() ([]User, error) {
	rows, err := db.Query(`SELECT id, username, password_hash, display_name, email, role, auth_source, is_active, created_at, last_login_at FROM users ORDER BY username ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Email, &u.Role, &u.AuthSource, &u.IsActive, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) UpdateUser(u *User) error {
	res, err := db.Exec(`UPDATE users SET display_name = ?, email = ?, role = ?, is_active = ? WHERE id = ?`,
		u.DisplayName, u.Email, u.Role, u.IsActive, u.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UpdateUserPassword(id, passwordHash string) error {
	res, err := db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UpdateUserLastLogin(id string) error {
	now := time.Now()
	_, err := db.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, now, id)
	return err
}

func (db *DB) DeleteUser(id string) error {
	res, err := db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------------
// JOBS CRUD
// -----------------------------------------------------------------------------

func (db *DB) CreateJob(j *Job) error {
	j.CreatedAt = time.Now()
	j.UpdatedAt = j.CreatedAt
	if j.DefaultContext == "" {
		j.DefaultContext = "Default"
	}
	if j.RetentionRuns <= 0 {
		j.RetentionRuns = 10
	}

	allowConcurrent := 0
	if j.AllowConcurrent {
		allowConcurrent = 1
	}
	isDeployed := 0
	if j.IsDeployed {
		isDeployed = 1
	}

	query := `INSERT INTO jobs (id, name, server_id, group_id, artifact_id, active_version, nexus_repo, default_context, allow_concurrent, retention_runs, env_file, is_deployed, deployed_version, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, j.ID, j.Name, j.ServerID, j.GroupID, j.ArtifactID, j.ActiveVersion, j.NexusRepo, j.DefaultContext, allowConcurrent, j.RetentionRuns, j.EnvFile, isDeployed, j.DeployedVersion, j.CreatedAt, j.UpdatedAt)
	return err
}

func (db *DB) GetJob(id string) (*Job, error) {
	row := db.QueryRow(`
		SELECT j.id, j.name, j.server_id, COALESCE(s.name, ''), j.group_id, j.artifact_id, j.active_version, j.nexus_repo, j.default_context, j.allow_concurrent, j.retention_runs, j.env_file, j.is_deployed, j.deployed_version, j.created_at, j.updated_at
		FROM jobs j
		LEFT JOIN servers s ON j.server_id = s.id
		WHERE j.id = ?`, id)

	var j Job
	var allowConcurrent int
	var isDeployed int
	err := row.Scan(&j.ID, &j.Name, &j.ServerID, &j.ServerName, &j.GroupID, &j.ArtifactID, &j.ActiveVersion, &j.NexusRepo, &j.DefaultContext, &allowConcurrent, &j.RetentionRuns, &j.EnvFile, &isDeployed, &j.DeployedVersion, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	j.AllowConcurrent = allowConcurrent == 1
	j.IsDeployed = isDeployed == 1
	return &j, err
}

func (db *DB) ListJobs() ([]Job, error) {
	rows, err := db.Query(`
		SELECT j.id, j.name, j.server_id, COALESCE(s.name, ''), j.group_id, j.artifact_id, j.active_version, j.nexus_repo, j.default_context, j.allow_concurrent, j.retention_runs, j.env_file, j.is_deployed, j.deployed_version, j.created_at, j.updated_at
		FROM jobs j
		LEFT JOIN servers s ON j.server_id = s.id
		ORDER BY j.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var allowConcurrent int
		var isDeployed int
		if err := rows.Scan(&j.ID, &j.Name, &j.ServerID, &j.ServerName, &j.GroupID, &j.ArtifactID, &j.ActiveVersion, &j.NexusRepo, &j.DefaultContext, &allowConcurrent, &j.RetentionRuns, &j.EnvFile, &isDeployed, &j.DeployedVersion, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.AllowConcurrent = allowConcurrent == 1
		j.IsDeployed = isDeployed == 1
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (db *DB) GetJobsByArtifact(artifactID string) ([]Job, error) {
	rows, err := db.Query(`
		SELECT j.id, j.name, j.server_id, COALESCE(s.name, ''), j.group_id, j.artifact_id, j.active_version, j.nexus_repo, j.default_context, j.allow_concurrent, j.retention_runs, j.env_file, j.is_deployed, j.deployed_version, j.created_at, j.updated_at
		FROM jobs j
		LEFT JOIN servers s ON j.server_id = s.id
		WHERE j.artifact_id = ?
		ORDER BY j.name ASC`, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var allowConcurrent int
		var isDeployed int
		if err := rows.Scan(&j.ID, &j.Name, &j.ServerID, &j.ServerName, &j.GroupID, &j.ArtifactID, &j.ActiveVersion, &j.NexusRepo, &j.DefaultContext, &allowConcurrent, &j.RetentionRuns, &j.EnvFile, &isDeployed, &j.DeployedVersion, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.AllowConcurrent = allowConcurrent == 1
		j.IsDeployed = isDeployed == 1
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (db *DB) GetJobsByArtifactOnServer(artifactID, serverID string) ([]Job, error) {
	rows, err := db.Query(`
		SELECT j.id, j.name, j.server_id, COALESCE(s.name, ''), j.group_id, j.artifact_id, j.active_version, j.nexus_repo, j.default_context, j.allow_concurrent, j.retention_runs, j.env_file, j.is_deployed, j.deployed_version, j.created_at, j.updated_at
		FROM jobs j
		LEFT JOIN servers s ON j.server_id = s.id
		WHERE j.artifact_id = ? AND j.server_id = ?
		ORDER BY j.name ASC`, artifactID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var allowConcurrent int
		var isDeployed int
		if err := rows.Scan(&j.ID, &j.Name, &j.ServerID, &j.ServerName, &j.GroupID, &j.ArtifactID, &j.ActiveVersion, &j.NexusRepo, &j.DefaultContext, &allowConcurrent, &j.RetentionRuns, &j.EnvFile, &isDeployed, &j.DeployedVersion, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.AllowConcurrent = allowConcurrent == 1
		j.IsDeployed = isDeployed == 1
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (db *DB) UpdateJob(j *Job) error {
	j.UpdatedAt = time.Now()
	allowConcurrent := 0
	if j.AllowConcurrent {
		allowConcurrent = 1
	}

	res, err := db.Exec(`
		UPDATE jobs SET name = ?, server_id = ?, group_id = ?, artifact_id = ?, active_version = ?, nexus_repo = ?, default_context = ?, allow_concurrent = ?, retention_runs = ?, env_file = ?, updated_at = ?
		WHERE id = ?`,
		j.Name, j.ServerID, j.GroupID, j.ArtifactID, j.ActiveVersion, j.NexusRepo, j.DefaultContext, allowConcurrent, j.RetentionRuns, j.EnvFile, j.UpdatedAt, j.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) SetJobDeployed(id string, isDeployed bool, version string) error {
	now := time.Now()
	dep := 0
	if isDeployed {
		dep = 1
	}
	res, err := db.Exec(`UPDATE jobs SET is_deployed = ?, deployed_version = ?, updated_at = ? WHERE id = ?`, dep, version, now, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UpdateJobActiveVersion(id, version string) error {
	now := time.Now()
	res, err := db.Exec(`UPDATE jobs SET active_version = ?, updated_at = ? WHERE id = ?`, version, now, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteJob(id string) error {
	res, err := db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------------
// EXECUTIONS CRUD
// -----------------------------------------------------------------------------

func (db *DB) CreateExecution(e *Execution) error {
	e.StartedAt = time.Now()
	query := `INSERT INTO executions (id, job_id, action, status, version, context, started_at, log_path, triggered_by, purged)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, e.ID, e.JobID, e.Action, e.Status, e.Version, e.Context, e.StartedAt, e.LogPath, e.TriggeredBy, 0)
	return err
}

func (db *DB) GetExecution(id string) (*Execution, error) {
	row := db.QueryRow(`
		SELECT e.id, e.job_id, COALESCE(j.name, ''), e.action, e.status, e.version, COALESCE(e.context, ''), e.exit_code, e.started_at, e.finished_at, e.duration_ms, e.log_path, e.triggered_by, e.purged
		FROM executions e
		LEFT JOIN jobs j ON e.job_id = j.id
		WHERE e.id = ?`, id)

	var e Execution
	var purged int
	err := row.Scan(&e.ID, &e.JobID, &e.JobName, &e.Action, &e.Status, &e.Version, &e.Context, &e.ExitCode, &e.StartedAt, &e.FinishedAt, &e.DurationMS, &e.LogPath, &e.TriggeredBy, &purged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	e.Purged = purged == 1
	return &e, err
}

func (db *DB) ListExecutions(limit int) ([]Execution, error) {
	return db.ListExecutionsFiltered("", limit)
}

func (db *DB) ListExecutionsFiltered(status string, limit int) ([]Execution, error) {
	if limit <= 0 {
		limit = 50
	}
	whereClause := ""
	args := []any{}
	if status != "" {
		whereClause = "WHERE e.status = ?"
		args = append(args, status)
	}
	query := fmt.Sprintf(`
		SELECT e.id, e.job_id, COALESCE(j.name, ''), e.action, e.status, e.version, COALESCE(e.context, ''), e.exit_code, e.started_at, e.finished_at, e.duration_ms, e.log_path, e.triggered_by, e.purged
		FROM executions e
		LEFT JOIN jobs j ON e.job_id = j.id
		%s
		ORDER BY e.started_at DESC LIMIT ?`, whereClause)
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []Execution
	for rows.Next() {
		var e Execution
		var purged int
		if err := rows.Scan(&e.ID, &e.JobID, &e.JobName, &e.Action, &e.Status, &e.Version, &e.Context, &e.ExitCode, &e.StartedAt, &e.FinishedAt, &e.DurationMS, &e.LogPath, &e.TriggeredBy, &purged); err != nil {
			return nil, err
		}
		e.Purged = purged == 1
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (db *DB) ListExecutionsByJob(jobID string, limit int) ([]Execution, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(`
		SELECT e.id, e.job_id, COALESCE(j.name, ''), e.action, e.status, e.version, COALESCE(e.context, ''), e.exit_code, e.started_at, e.finished_at, e.duration_ms, e.log_path, e.triggered_by, e.purged
		FROM executions e
		LEFT JOIN jobs j ON e.job_id = j.id
		WHERE e.job_id = ?
		ORDER BY e.started_at DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []Execution
	for rows.Next() {
		var e Execution
		var purged int
		if err := rows.Scan(&e.ID, &e.JobID, &e.JobName, &e.Action, &e.Status, &e.Version, &e.Context, &e.ExitCode, &e.StartedAt, &e.FinishedAt, &e.DurationMS, &e.LogPath, &e.TriggeredBy, &purged); err != nil {
			return nil, err
		}
		e.Purged = purged == 1
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (db *DB) CountActiveExecutionsForJob(jobID string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM executions WHERE job_id = ? AND status IN ('pending', 'running')`, jobID).Scan(&count)
	return count, err
}

func (db *DB) UpdateExecutionStatus(id, status string, exitCode *int, durationMS *int64) error {
	now := time.Now()
	_, err := db.Exec(`
		UPDATE executions SET status = ?, exit_code = ?, finished_at = ?, duration_ms = ?
		WHERE id = ?`,
		status, exitCode, now, durationMS, id)
	return err
}

func (db *DB) GetOldExecutionsForRetention(jobID string, keepCount int) ([]Execution, error) {
	rows, err := db.Query(`
		SELECT id, job_id, action, status, version, log_path, started_at
		FROM executions
		WHERE job_id = ? AND purged = 0 AND status IN ('success', 'failed', 'aborted')
		ORDER BY started_at DESC
		LIMIT -1 OFFSET ?`, jobID, keepCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []Execution
	for rows.Next() {
		var e Execution
		if err := rows.Scan(&e.ID, &e.JobID, &e.Action, &e.Status, &e.Version, &e.LogPath, &e.StartedAt); err != nil {
			return nil, err
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (db *DB) MarkExecutionPurged(id string) error {
	_, err := db.Exec(`UPDATE executions SET purged = 1 WHERE id = ?`, id)
	return err
}

// -----------------------------------------------------------------------------
// API TOKENS CRUD
// -----------------------------------------------------------------------------

func (db *DB) CreateAPIToken(t *APIToken) error {
	t.CreatedAt = time.Now()
	if t.Role == "" {
		t.Role = "operator"
	}
	_, err := db.Exec(`INSERT INTO api_tokens (id, name, token_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.TokenHash, t.Role, t.CreatedAt)
	return err
}

func (db *DB) GetAPITokenByHash(hash string) (*APIToken, error) {
	row := db.QueryRow(`SELECT id, name, token_hash, role, created_at, last_used_at FROM api_tokens WHERE token_hash = ?`, hash)
	var t APIToken
	err := row.Scan(&t.ID, &t.Name, &t.TokenHash, &t.Role, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (db *DB) UpdateAPITokenLastUsed(id string) error {
	now := time.Now()
	_, err := db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now, id)
	return err
}

func (db *DB) ListAPITokens() ([]APIToken, error) {
	rows, err := db.Query(`SELECT id, name, role, created_at, last_used_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Role, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (db *DB) DeleteAPIToken(id string) error {
	res, err := db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------------
// SETTINGS
// -----------------------------------------------------------------------------

func (db *DB) GetSetting(key, defaultValue string) (string, error) {
	var val string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultValue, nil
	}
	if err != nil {
		return defaultValue, err
	}
	return val, nil
}

func (db *DB) SetSetting(key, value string) error {
	now := time.Now()
	_, err := db.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now)
	return err
}

func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

// -----------------------------------------------------------------------------
// ADVANCED JOB LISTING & PAGINATION (FOR 1000+ JOBS)
// -----------------------------------------------------------------------------

func (db *DB) GetDistinctGroups() ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT group_id FROM jobs WHERE group_id != '' ORDER BY group_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (db *DB) ListJobsPaged(filter JobFilter) (*JobPageResult, error) {
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}

	whereClauses := []string{"1=1"}
	args := []any{}

	if strings.TrimSpace(filter.Search) != "" {
		term := "%" + strings.TrimSpace(filter.Search) + "%"
		whereClauses = append(whereClauses, "(j.name LIKE ? OR j.id LIKE ? OR j.artifact_id LIKE ? OR j.group_id LIKE ?)")
		args = append(args, term, term, term, term)
	}

	if strings.TrimSpace(filter.GroupID) != "" {
		whereClauses = append(whereClauses, "j.group_id = ?")
		args = append(args, strings.TrimSpace(filter.GroupID))
	}

	if strings.TrimSpace(filter.ArtifactID) != "" {
		whereClauses = append(whereClauses, "j.artifact_id = ?")
		args = append(args, strings.TrimSpace(filter.ArtifactID))
	}

	if strings.TrimSpace(filter.ServerID) != "" {
		whereClauses = append(whereClauses, "j.server_id = ?")
		args = append(args, strings.TrimSpace(filter.ServerID))
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. Total Count query
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM jobs j WHERE %s", whereSQL)
	var totalCount int
	if err := db.QueryRow(countSQL, args...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("failed to count jobs: %w", err)
	}

	totalPages := (totalCount + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * pageSize

	// 2. Sorting
	orderCol := "j.name"
	switch strings.ToLower(filter.SortBy) {
	case "group_id", "group":
		orderCol = "j.group_id"
	case "server":
		orderCol = "s.name"
	case "version", "active_version":
		orderCol = "j.active_version"
	case "last_run":
		orderCol = "le.started_at"
	default:
		orderCol = "j.name"
	}

	orderDir := "ASC"
	if strings.EqualFold(filter.SortOrder, "desc") {
		orderDir = "DESC"
	}

	var orderBySQL string
	if strings.ToLower(filter.GroupBy) == "group_id" {
		if orderCol == "j.group_id" {
			orderBySQL = fmt.Sprintf("j.group_id %s, j.name ASC, j.id ASC", orderDir)
		} else {
			orderBySQL = fmt.Sprintf("j.group_id ASC, %s %s, j.id ASC", orderCol, orderDir)
		}
	} else {
		orderBySQL = fmt.Sprintf("%s %s, j.id ASC", orderCol, orderDir)
	}

	// 3. Query with Window Function to attach latest execution per job
	query := fmt.Sprintf(`
		WITH LatestExecs AS (
			SELECT id, job_id, action, status, started_at, exit_code, duration_ms,
			       ROW_NUMBER() OVER (PARTITION BY job_id ORDER BY started_at DESC) as rn
			FROM executions
		)
		SELECT 
			j.id, j.name, j.server_id, COALESCE(s.name, ''), j.group_id, j.artifact_id, 
			j.active_version, j.nexus_repo, j.default_context, j.allow_concurrent, 
			j.retention_runs, j.env_file, j.is_deployed, j.deployed_version, j.created_at, j.updated_at,
			COALESCE(le.id, ''), COALESCE(le.action, ''), COALESCE(le.status, ''), 
			le.started_at, le.exit_code, le.duration_ms
		FROM jobs j
		LEFT JOIN servers s ON j.server_id = s.id
		LEFT JOIN LatestExecs le ON j.id = le.job_id AND le.rn = 1
		WHERE %s
		ORDER BY %s
		LIMIT ? OFFSET ?`, whereSQL, orderBySQL)

	queryArgs := append(args, pageSize, offset)
	rows, err := db.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query paged jobs: %w", err)
	}
	defer rows.Close()

	var jobs []JobWithRunInfo
	for rows.Next() {
		var item JobWithRunInfo
		var allowConcurrent int
		var isDeployed int
		if err := rows.Scan(
			&item.ID, &item.Name, &item.ServerID, &item.ServerName, &item.GroupID, &item.ArtifactID,
			&item.ActiveVersion, &item.NexusRepo, &item.DefaultContext, &allowConcurrent,
			&item.RetentionRuns, &item.EnvFile, &isDeployed, &item.DeployedVersion, &item.CreatedAt, &item.UpdatedAt,
			&item.LastRunID, &item.LastRunAction, &item.LastRunStatus,
			&item.LastRunStartedAt, &item.LastRunExitCode, &item.LastRunDurationMS,
		); err != nil {
			return nil, fmt.Errorf("failed to scan paged job: %w", err)
		}
		item.AllowConcurrent = allowConcurrent == 1
		item.IsDeployed = isDeployed == 1
		jobs = append(jobs, item)
	}

	distinctGroups, _ := db.GetDistinctGroups()

	return &JobPageResult{
		Jobs:           jobs,
		TotalCount:     totalCount,
		TotalPages:     totalPages,
		CurrentPage:    page,
		PageSize:       pageSize,
		DistinctGroups: distinctGroups,
	}, nil
}

// -----------------------------------------------------------------------------
// USER PREFERENCES
// -----------------------------------------------------------------------------

func (db *DB) GetUserPreference(userID, key, defaultValue string) (string, error) {
	var val string
	err := db.QueryRow(`SELECT value FROM user_preferences WHERE user_id = ? AND key = ?`, userID, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultValue, nil
	}
	if err != nil {
		return defaultValue, err
	}
	return val, nil
}

func (db *DB) SetUserPreference(userID, key, value string) error {
	now := time.Now()
	_, err := db.Exec(`
		INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		userID, key, value, now)
	return err
}

func (db *DB) GetAllUserPreferences(userID string) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM user_preferences WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prefs := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		prefs[k] = v
	}
	return prefs, rows.Err()
}

