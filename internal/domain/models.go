package domain

import (
	"time"
)

type Server struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Host               string     `json:"host"`
	Port               int        `json:"port"`
	User               string     `json:"user"`
	SSHKeyPath         string     `json:"ssh_key_path"`
	HostKey            string     `json:"host_key"`
	HostKeyFingerprint string     `json:"host_key_fingerprint,omitempty"`
	Status             string     `json:"status"` // "online", "offline", "unknown"
	JobsDir            string     `json:"jobs_dir"`
	ScriptsDir         string     `json:"scripts_dir"`
	EnvFile            string     `json:"env_file"`
	KeepReleases       int        `json:"keep_releases"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
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
	Page       int    `json:"page"`
	PageSize   int    `json:"page_size"`
	SortBy     string `json:"sort_by"`    // "name", "group_id", "server", "version", "last_run"
	SortOrder  string `json:"sort_order"` // "asc", "desc"
	GroupBy    string `json:"group_by"`   // e.g. "group_id"
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
