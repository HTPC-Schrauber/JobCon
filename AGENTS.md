# AGENTS.md – JobCon Developer & AI Agent Guide

Welcome to **JobCon**! This document provides context, architectural principles, development conventions, and operational guidelines for AI agents and human developers working on this codebase.

---

## 1. Project Overview & Mission

**JobCon** is a lightweight, high-performance, Linux-native deployment and execution engine for versioned batch, ETL, and background jobs on target servers.

* **Origin & Purpose:** Conceived as a streamlined, low-overhead modern replacement for the **Talend Administration Center (TAC) / Job Conductor**, JobCon is evolving into a universal control hub for batch jobs (Talend, Python, Bash, native Linux binaries).
* **Core Philosophy:** **Strict decoupling of Deployment and Execution**:
  * **Deployment:** Download versioned artifacts from Sonatype Nexus (Maven ZIP archives) or repositories, unpack to target servers, atomically rotate generation symlinks (`current`, `current-1`), and prune obsolete releases.
  * **Execution:** Direct local execution with zero network hop or database overhead via lean starter scripts for **JS7 / SOS JobScheduler** or cron, OR remote execution triggered via JobCon's integrated SSH runner / REST API / Web UI.
* **Minimal Footprint:** Single Go binary, zero Java/Node/Python runtime dependencies on the JobCon host, CGO-free SQLite in WAL mode.

---

## 2. Technology Stack

| Layer | Technology | Details |
| :--- | :--- | :--- |
| **Backend Language** | Go 1.26+ | Pure Go, standard library preferred, `CGO_ENABLED=0` |
| **Database** | SQLite (`modernc.org/sqlite`) | CGO-free, WAL mode (`PRAGMA journal_mode=WAL`), busy timeout 5000ms |
| **Web UI** | Go `html/template` + HTMX | Embedded in binary via `embed.FS`, zero npm/webpack dependencies |
| **Realtime Logs** | Server-Sent Events (SSE) | MultiWriter streaming stdout/stderr live to browser & disk |
| **Styling** | Pure CSS (`style.css`) | Dark high-density UI, warm amber theme (`#f59e0b`), responsive |
| **Remote Transport** | SSH (`golang.org/x/crypto/ssh`) | Key-based authentication, TOFU host-key pinning, 30s keepalive |
| **Artifact Repository** | Sonatype Nexus 3 REST API | Maven ZIP archives, local SQLite cache (< 5ms queries), background sync |
| **Target Scripts** | Bash (`scripts/*.sh`) | External, non-embedded scripts deployed to target servers |
| **Container** | Docker / Compose | Debian 13 (Trixie Slim), non-root user `jobcon` (UID 1000) |

---

## 3. Directory Structure

```text
JobCon/
├── cmd/
│   └── jobcon/                 # Main entrypoint (main.go, flags, startup, graceful shutdown)
├── internal/
│   ├── api/                    # REST API handlers, routing (/api/v1/...), SSE endpoints
│   ├── auth/                   # Authentication (Local bcrypt, LDAP/AD, sessions, RBAC middleware)
│   ├── config/                 # Configuration loader (YAML, env vars, 0600 permission check)
│   ├── crypto/                 # AES-256-GCM symmetric encryption for database secrets
│   ├── db/                     # SQLite connection, schema migrations, models, data access
│   ├── domain/                 # Domain models, input validation regex barrier guards
│   ├── i18n/                   # Internationalization (EN default, DE, locales/*.json)
│   ├── nexus/                  # Sonatype Nexus 3 client, background syncer, SSRF validator
│   ├── runner/                 # SSH runner (TOFU, keepalives), concurrency queue, MultiWriter
│   ├── services/               # Application service layer (JobService, ServerService, etc.)
│   ├── storage/                # Log file storage (data/logs/*.log), gzip compression, retention
│   └── web/                    # Web UI handlers, embedded templates (templates/*.html), static/
├── scripts/                    # EXTERNAL target server scripts (NOT embedded into binary!)
│   ├── jobcon_ctl.sh           # Target controller (deploy, undeploy, run, symlinks, setsid)
│   ├── run_job.sh              # Lean starter script for JS7 / cron (zero overhead)
│   └── scripts_test.go         # Integration tests for bash scripts
├── packaging/
│   └── jobcon.service          # Systemd unit file for Linux/Debian
├── build.sh                    # Build & release packaging script (generates target/)
├── Makefile                    # Make targets (build, test, docker-build, etc.)
├── Dockerfile                  # Multi-stage Debian build
├── compose.yaml                # Docker Compose setup with persistent volumes
├── config.example.yaml         # Fully documented example configuration
└── JobCon.md                   # Detailed architecture blueprint (German)
```

---

## 4. Architectural Rules & Non-Negotiable Constraints

### 4.1 External Target Scripts (Do Not Embed!)
* The scripts in `./scripts/` (`jobcon_ctl.sh`, `run_job.sh`) **MUST NEVER be embedded** into the Go binary via `embed.FS`.
* They reside on disk and are dynamically read and uploaded to target servers via SSH during server setup.
* Reason: Users must be able to modify or patch target scripts directly on the host without recompiling the binary.

### 4.2 Security & Credential Protection
* **AES-256-GCM Database Encryption:** Sensitive values in SQLite (`nexus_password`, `ldap_bind_password`) are encrypted using the master key from `security.encryption_key` in `config.yaml` (`enc:v1:<base64>`).
* **Zero Passwords in SSH CLI:** Never transmit passwords as command-line arguments to SSH sessions (prevents exposure in `ps aux`). Repository credentials are deployed once to a hidden file `<scripts_dir>/.nexus_auth` with `0600` permissions on target servers.
* **Strict Config File Permissions:** JobCon enforces `0600` file permissions on `config.yaml` upon startup.
* **SSH Host-Key Pinning (TOFU):** Target host keys are pinned on first connect in `servers.host_key`. Any key mismatch is rejected to prevent Man-in-the-Middle (MitM) attacks.
* **SSRF Prevention:** Nexus URLs are strictly validated (`SafeNexusURLRegex`). Cloud metadata IPs (`169.254.169.254`, `metadata.google.internal`) and link-local ranges are blocked.
* **Command Injection Barrier Guards:**
  * All identifiers, job IDs, versions, and context names must match `^[a-zA-Z0-9._-]+$`.
  * Parameter keys must match `^[a-zA-Z0-9._-]+$`, and values must not contain null bytes (`\0`).
  * File paths must not contain shell metacharacters (`;`, `&`, `|`, `` ` ``, `$`, etc.).
  * Shell parameters passed to SSH commands must always be quoted using `runner.ShellQuote()`.

### 4.3 Last Local Admin Protection
* The system enforces that at least **one active local administrator** (`role = 'admin'`, `auth_source = 'local'`, `is_active = 1`) exists at all times.
* Attempting to delete, deactivate, demote, or convert the last local admin to LDAP returns `ErrLastAdminProtection` in DB, API, and Web UI.

### 4.4 Process Management & Graceful Abort
* Jobs on target servers are started inside a new process group via `setsid` in `jobcon_ctl.sh`.
* When a user aborts an execution, JobCon issues `kill -TERM -$PGID` via SSH to terminate the entire process group, ensuring child JVMs or subprocesses are cleanly killed.

### 4.5 Concurrency & Bulk Operations
* Bulk starts and queue processing are bounded by a global concurrency semaphore (`max_concurrent_jobs`, default: 3, max limit: 100).
* Individual jobs can have `allow_concurrent: false` to prevent overlapping runs of the same job.

### 4.6 Internationalization (i18n)
* The UI natively supports English (`en`, default) and German (`de`).
* Locale strings reside in `internal/i18n/locales/{en,de}.json`.
* **Rule:** Whenever adding or altering UI text, update **both** `en.json` and `de.json`.
* Templates use `{{ t "key" }}` or `{{ t "key" .Arg }}`. Client-side JavaScript uses `window.t("key", ...args)`.

---

## 5. Target Server Layout & Symlink Rotation

On Linux target servers (default root `/opt/talend`), the structure is:
```text
/opt/talend/
├── scripts/
│   ├── .nexus_auth            # File permissions 0600 (NEXUS_USER / NEXUS_PASS)
│   ├── jobcon_ctl.sh          # Universal runner & deployment controller (0755)
│   └── run_job.sh             # Lean starter for JS7 / local cron (0755)
└── jobs/
    └── {job_name}/
        ├── .env               # Optional job-level environment variables
        ├── current -> releases/1.2.0/    # Active release
        ├── current-1 -> releases/1.1.0/  # Previous release
        ├── current-2 -> releases/1.0.0/  # Older release
        └── releases/
            ├── 1.0.0/
            ├── 1.1.0/
            └── 1.2.0/
```

* Symlink rotation is atomic (`ln -sfn`).
* Old releases beyond `keep_releases` that have no active symlink pointing to them are pruned automatically during `deploy`.
* `jobcon_ctl.sh undeploy` cleans up releases and symlinks cleanly.

---

## 6. Build, Test & Development Workflow

### Useful Commands
* **Run Tests:**
  ```bash
  go test ./...
  ```
  *(Note: `scripts/scripts_test.go` performs integration tests executing `jobcon_ctl.sh` and requires `unzip` and `bash` installed on the host).*
* **Build Single Binary:**
  ```bash
  go build -o jobcon ./cmd/jobcon
  ```
* **Build Full Distribution (`target/` directory):**
  ```bash
  make build
  # or: ./build.sh
  ```
  Generates `target/jobcon`, copies `scripts/`, `config.example.yaml`, `jobcon.service`, and builds `.tar.gz` and `.zip` packages.
* **Run with Custom Config:**
  ```bash
  ./jobcon --config config.yaml
  ```
* **Docker Build & Run:**
  ```bash
  make docker-build
  make docker-up
  ```

### First-Time Run
* If no admin password is provided via `JOBCON_ADMIN_PASSWORD` or config, JobCon generates a secure random password on the initial run and logs it prominently to stdout.

---

## 7. REST API Quick Reference

All endpoints (except `/healthz`) require `Authorization: Bearer <API_TOKEN>` or HTTP BasicAuth.

| Endpoint | Method | Role | Description |
| :--- | :--- | :--- | :--- |
| `/healthz` | GET | Public | Healthcheck endpoint |
| `/api/v1/jobs` | GET | Viewer+ | List / search jobs (paged, filtered) |
| `/api/v1/jobs` | POST | Admin | Create new job definition |
| `/api/v1/jobs/{id}` | GET | Viewer+ | Get job details |
| `/api/v1/jobs/{id}` | PUT | Admin | Update job configuration |
| `/api/v1/jobs/{id}` | DELETE | Admin | Delete job (`?undeploy=true` to clean target) |
| `/api/v1/jobs/{id}/deploy` | POST | Operator+ | Deploy version to target server |
| `/api/v1/jobs/{id}/undeploy`| POST | Operator+ | Remove deployed releases from target server |
| `/api/v1/jobs/{id}/run` | POST | Operator+ | Execute job (`?wait=true` for blocking CI/CD calls) |
| `/api/v1/jobs/bulk/run` | POST | Operator+ | Trigger bulk run with concurrency limit |
| `/api/v1/jobs/bulk/deploy` | POST | Operator+ | Bulk deploy multiple jobs |
| `/api/v1/jobs/bulk/undeploy`| POST | Operator+ | Bulk undeploy multiple jobs |
| `/api/v1/executions` | GET | Viewer+ | Query execution history |
| `/api/v1/executions/{id}/logs` | GET | Viewer+ | Stream logs (SSE or raw text) |
| `/api/v1/executions/{id}/abort`| POST | Operator+ | Abort a running execution |
| `/api/v1/nexus/sync` | POST | Operator+ | Trigger background artifact sync |
| `/api/v1/nexus/search` | GET | Viewer+ | Query cached artifacts (< 5ms response) |
| `/api/v1/servers/{id}/test` | POST | Admin | Test SSH connection and probe host |
| `/api/v1/servers/{id}/setup`| POST | Admin | Deploy external scripts to target server |

---

## 8. Coding Conventions & Style

1. **Standard Library First:** Avoid unnecessary third-party dependencies. If a feature can be cleanly implemented with the Go standard library, prefer it.
2. **Error Wrapping:** Always wrap errors with context: `fmt.Errorf("action description: %w", err)`.
3. **Frontend Simplicity:** The Web UI uses semantic HTML, HTMX attributes (`hx-get`, `hx-post`, `hx-target`, `hx-swap`), and pure CSS. Do not introduce npm packages, client-side frameworks, or external CDNs.
4. **Data Isolation:** Keep database schema changes idempotent and backwards-compatible inside `internal/db/migrations.go`.
5. **No Passwords in Logs:** Never log plaintext passwords or API token secrets.

---

## 9. Projekt-Richtlinien & Tooling

### Language Server & Werkzeuge
* **Go:** Nutze `gopls` für Typdefinitionen, Referenzen und Diagnosen.
* **HTML/CSS/JSON:** Nutze die installierten Language Server (`vscode-html-language-server`, `vscode-json-language-server`, etc.).
* **Testpflicht:** Führe vor Abschluss von Aufgaben immer `go test ./...` aus.

