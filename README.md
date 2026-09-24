# JobCon – Lightweight Job Deployment & Execution Engine

**English** | [Deutsch](README.de.md)

JobCon is a lightweight, high-performance, and Linux-native deployment and execution engine for versioned jobs on target servers.

Originally conceived as a modern, streamlined replacement for the **Talend Administration Center (TAC) / Job Conductor**, JobCon continuously evolves into a universal control hub for batch, ETL, and background jobs (e.g. Talend, Python, Bash scripts, or native Linux binaries). Sonatype Nexus (Maven ZIP archives) serves as the primary artifact repository, with a modular design facilitating future repository integrations such as Git Releases.

![JobCon Dashboard](docs/screenshots/dashboard.png)

## Key Features

* **Single-Binary & Container-Ready:** Built purely with the Go standard library and CGO-free SQLite (`modernc.org/sqlite`). Zero Java, Node, or Python runtime dependencies required on the JobCon host.
* **Decoupled Deployment & Execution:**
  * **Deployment & Undeployment:** Direct artifact download from Nexus, versioned release storage (`releases/{version}`), atomic switching of the `current` symlink, and clean undeployment/removal from target hosts.
  * **Execution:** Local startup without TAC overhead via lean starter scripts for **JS7 / SOS JobScheduler**, cron, or via JobCon's integrated SSH runner.
  * **Visual Deployment Status:** Immediate status indication (green/gray dot) showing whether jobs are deployed on target servers.
* **Security & Secret Protection:**
  * **AES-256-GCM Database Encryption:** Sensitive credentials (Nexus repository password, LDAP bind password) are transparently encrypted with AES-256-GCM before being stored in SQLite.
  * **Master Key & File Permissions (0600):** The cryptographic master key is configured in `config.yaml` under `security.encryption_key` (generate with `openssl rand -hex 32`). JobCon automatically enforces strict file permissions (`0600`, readable/writable by owner only) on `config.yaml` upon startup.
  * **Zero Passwords in SSH Commands:** Passwords are never transmitted in SSH command lines or exposed in process tables (`ps aux`). During server setup, JobCon deploys a hidden file `<scripts_dir>/.nexus_auth` with `0600` permissions on the target server, where `jobcon_ctl.sh` reads credentials locally.
  * **SSH Host-Key Pinning (TOFU):** Protection against Man-in-the-Middle attacks via automatic Trust-On-First-Use pinning of target server host keys in SQLite.
  * **Role-Based Access Control (RBAC):** User management with `bcrypt` hashing, user roles (`admin`, `operator`, `viewer`), and protection ensuring the last active local administrator cannot be deleted or deactivated.
  * **Authentication:** HTTP BasicAuth, session cookies, and built-in LDAP / Active Directory support.
* **External Target Server Scripts:** Execution scripts (`jobcon_ctl.sh`, `run_job.sh`) are **fully external** in `./scripts/` and not embedded into the binary. They can be modified or extended ad-hoc for custom job types without recompiling.
* **Multi-Job Control (Bulk Actions):**
  * Seamless multi-selection via Click, Shift+Click (range), and Ctrl/Cmd+Click (toggle) without checkboxes.
  * Bulk action toolbar for direct start (`▶ Start` without modal dialogs), deployment (`🚀 Deploy`), and undeployment (`🗑️ Undeploy`).
  * Dedicated "Details" button to open the slide-out sidepanel.
* **High-Density UI & Nexus Cache:**
  * Compact operational status strip (jobs, online servers with pulse dot, groups, active runs, Nexus sync status).
  * Local SQLite cache for Nexus artifacts with fast in-memory search (< 5ms) and scheduled background synchronization.
  * Queue worker pool to enforce global concurrency limits (`max_concurrent_jobs`).
* **Environment Badge:** Configurable environment badge (e.g. `DEV`, `TEST`, `PROD`) with matching top accent color strip in the UI.
* **Multilingual (i18n):** Native English and German localization, switchable directly in the header and persisted per user.
* **Environment Variables (.env):** Automatic sourcing of `.env` files on target systems per server or individually per job.
* **Real-Time Transparency:** Live streaming of `stdout` and `stderr` via Server-Sent Events (SSE) into a web terminal.
* **CI/CD Integration (Jenkins):** REST API with API bearer tokens and blocking execution (`?wait=true`) as a drop-in replacement for TAC MetaServlet calls.

---

## Deployment & Quick Start

JobCon can be run via **Docker Compose**, as a **Linux Systemd Service**, or directly as a **Single Binary**.

### Option A: Docker Compose (Recommended)

JobCon runs in a hardened container based on **Debian 13 (Trixie Slim)** under the non-root user `jobcon` (UID 1000).

1. Prepare configuration:
   ```bash
   cp config.example.yaml config.yaml
   # Generate and insert 256-bit encryption master key:
   openssl rand -hex 32
   chmod 0600 config.yaml
   ```

2. Start the container:
   ```bash
   docker compose up -d
   ```

3. View live logs:
   ```bash
   docker compose logs -f
   ```

* **Persistence:** Database and execution logs are stored in `./data`.
* **External Scripts:** The `./scripts/` directory is mounted as a host volume. Modifications take effect immediately without rebuilding the image!

---

### Option B: Build & Deployment via `target/` Directory

The build script produces a distribution-ready release bundle:

```bash
make build
# or: ./build.sh
```

The generated `target/` directory contains:
* `jobcon`: Statically linked Linux binary.
* `scripts/`: External target server scripts (`jobcon_ctl.sh`, `run_job.sh`).
* `config.example.yaml`: Example configuration.
* `jobcon.service`: Systemd service unit for Linux/Debian.
* `jobcon-linux-amd64.tar.gz` / `.zip`: Release archives.

#### Installation as a Systemd Service:
```bash
sudo cp -r target/* /opt/jobcon/
sudo useradd -r -s /bin/false -d /opt/jobcon jobcon || true
sudo chown -R jobcon:jobcon /opt/jobcon
sudo chmod 0600 /opt/jobcon/config.yaml
sudo cp /opt/jobcon/jobcon.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now jobcon
```

---

## External Target Server Scripts

The scripts deployed to target servers reside in `./scripts/` and are not embedded into the binary:

1. **`jobcon_ctl.sh`** (Universal Target Controller):
   * Deploys releases to `/opt/talend/jobs/{job_name}/releases/{version}`.
   * Atomically rotates symlinks (`current`, `current-1`, etc.).
   * Purges unlinked releases according to the retention policy.
   * Securely reads repository credentials from `/opt/talend/scripts/.nexus_auth` (0600).
   * Sources `.env` files and starts jobs in dedicated process groups (`setsid`).
2. **`run_job.sh`** (Lean Starter for JS7 / SOS JobScheduler or Cron):
   * Executes the active release directly on the local machine:
     ```bash
     /opt/talend/scripts/run_job.sh <job_name> [optional params...]
     ```

When setting up a server through the Web UI ("Deploy Scripts") or API, JobCon automatically transfers all `.sh` scripts and the encrypted `.nexus_auth` file to the target server via SSH.

---

## REST API Overview

All API endpoints require authentication via `Authorization: Bearer <TOKEN>` or HTTP BasicAuth.

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/healthz` | Health check (unauthenticated) |
| `GET` | `/api/v1/jobs` | List configured jobs |
| `POST` | `/api/v1/jobs` | Create job (Admin) |
| `DELETE` | `/api/v1/jobs/{id}` | Delete job (optional `?undeploy=true`) |
| `POST` | `/api/v1/jobs/{id}/deploy` | Deploy version to target server |
| `POST` | `/api/v1/jobs/{id}/undeploy` | Remove job from target server |
| `POST` | `/api/v1/jobs/{id}/run` | Execute job (optional `?wait=true`) |
| `POST` | `/api/v1/jobs/bulk/run` | Execute multiple jobs simultaneously |
| `POST` | `/api/v1/jobs/bulk/deploy` | Deploy multiple jobs simultaneously |
| `POST` | `/api/v1/jobs/bulk/undeploy` | Undeploy multiple jobs simultaneously |
| `GET` | `/api/v1/nexus/artifacts` | Query cached Nexus artifacts |
| `POST` | `/api/v1/nexus/sync` | Trigger immediate background artifact sync |
| `GET` | `/api/v1/jobs/{id}/artifact` | Get Nexus download URL and metadata |
| `GET` | `/api/v1/executions` | Execution history |
| `GET` | `/api/v1/executions/{id}/logs` | Stream logs as plain text or SSE |
| `POST` | `/api/v1/executions/{id}/abort` | Abort a running execution |
| `POST` | `/api/v1/servers/{id}/test` | Test SSH connectivity to target server |

### Example: Jenkins Pipeline Call (Blocking)
```bash
curl -X POST "http://jobcon:8080/api/v1/jobs/sync_sap/run?wait=true" \
     -H "Authorization: Bearer ${JOBCON_TOKEN}" \
     -H "Content-Type: application/json" \
     -d '{"context": "Production", "params": {"batchSize": "1000"}}'
```

---

## Development Note

This project was developed with the assistance of Artificial Intelligence (AI).

---

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
