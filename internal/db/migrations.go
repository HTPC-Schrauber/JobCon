package db

const schemaSQL = `
CREATE TABLE IF NOT EXISTS servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    user TEXT NOT NULL DEFAULT 'talend',
    ssh_key_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    jobs_dir TEXT NOT NULL DEFAULT '/opt/talend/jobs',
    scripts_dir TEXT NOT NULL DEFAULT '/opt/talend/scripts',
    env_file TEXT NOT NULL DEFAULT '',
    keep_releases INTEGER NOT NULL DEFAULT 3,
    last_checked_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    email TEXT,
    role TEXT NOT NULL DEFAULT 'viewer',
    auth_source TEXT NOT NULL DEFAULT 'local',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at DATETIME
);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE RESTRICT,
    group_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    active_version TEXT NOT NULL,
    nexus_repo TEXT NOT NULL,
    default_context TEXT NOT NULL DEFAULT 'Default',
    allow_concurrent INTEGER NOT NULL DEFAULT 0,
    retention_runs INTEGER NOT NULL DEFAULT 10,
    env_file TEXT NOT NULL DEFAULT '',
    is_deployed INTEGER NOT NULL DEFAULT 0,
    deployed_version TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS executions (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    status TEXT NOT NULL,
    version TEXT NOT NULL,
    context TEXT,
    exit_code INTEGER,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME,
    duration_ms INTEGER,
    log_path TEXT,
    triggered_by TEXT NOT NULL,
    purged INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'operator',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, key)
);

CREATE INDEX IF NOT EXISTS idx_executions_job_id ON executions(job_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_executions_status ON executions(status);
CREATE INDEX IF NOT EXISTS idx_jobs_group_id ON jobs(group_id);
`

func (db *DB) Migrate() error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return err
	}
	// Safe additions for existing tables
	_, _ = db.Exec("ALTER TABLE servers ADD COLUMN jobs_dir TEXT NOT NULL DEFAULT '/opt/talend/jobs'")
	_, _ = db.Exec("ALTER TABLE servers ADD COLUMN scripts_dir TEXT NOT NULL DEFAULT '/opt/talend/scripts'")
	_, _ = db.Exec("ALTER TABLE servers ADD COLUMN env_file TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE servers ADD COLUMN keep_releases INTEGER NOT NULL DEFAULT 3")
	_, _ = db.Exec("ALTER TABLE jobs ADD COLUMN env_file TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE jobs ADD COLUMN is_deployed INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE jobs ADD COLUMN deployed_version TEXT NOT NULL DEFAULT ''")
	return nil
}
