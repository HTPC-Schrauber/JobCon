# Blueprint: JobCon – Lightweight Talend Job Deployment & Execution Engine

## 1. Executive Summary & Zielsetzung

**JobCon** ist ein leichtgewichtiger, wartungsarmer und performanter Ersatz für den Talend Administration Center (TAC) / Job Conductor. Die Kernphilosophie besteht in der **strikten Entkopplung von Deployment (Bereitstellung/Versionierung) und Execution (Ausführung)** sowie in einer minimalistischen Single-Binary-Architektur für **Linux-Umgebungen**.

### Zentrale Anforderungen & Leitprinzipien
* **Single-Binary & Linux-Native:** Entwickelt in **Go (Golang)** ohne CGO- oder externe Runtime-Abhängigkeiten (kein Java, kein Python, kein externer Webserver nötig). Betrieb von JobCon und allen Execution Servern erfolgt zu 100 % unter **Linux**.
* **Entkopplung von Deployment & Execution:**
  * **Deployment:** Herunterladen aus Nexus, Entpacken und atomare Versionierung auf dem Zielserver (angestoßen durch Jenkins CI/CD oder JobCon UI/API).
  * **Execution:** Lokale oder remote Ausführung der installierten Version. Ermöglicht **Zero-Overhead-Starts** für externe Scheduler wie **JS7 / SOS JobScheduler**.
* **Benutzerauthentifizierung & Rollen:**
  * Native Authentifizierung via **HTTP BasicAuth** und Session-Cookies.
  * Lokale Benutzerverwaltung mit gehashten Passwörtern (`bcrypt`).
  * Modulare Architektur zur späteren Anbindung von **Active Directory / LDAP**.
  * Rollenbasiertes Rechtesystem (RBAC: `admin`, `operator`, `viewer`).
* **Sicherheit & Verschlüsselung:**
  * Optionale native **TLS/SSL-Verschlüsselung** im Go-Server.
  * Nahtlose Unterstützung für vorgeschaltete **Reverse Proxies** (z. B. Nginx, Traefik, Caddy) inklusive Auswertung von `X-Forwarded-*`-Headern.
* **Integrierter Einstellungsdialog (Web-UI):**
  * Verwaltung von Benutzern (Anlage, Rollen, Passwörter).
  * Verwaltung von Execution Servern (SSH-Ziele, Verbindungstests).
  * Globale Einstellungen (Nexus, Aufbewahrungsfristen, LDAP-Konfiguration).
* **Echtzeit-Transparenz:** Live-Streaming von `stdout` und `stderr` via Server-Sent Events (SSE) in eine moderne HTMX-Webkonsole.
* **Automatisierung:** Schlanke REST-API mit API-Bearer-Tokens für CI/CD (Jenkins).
* **Minimalistischer Footprint & saubere Speicherung:** 
  * Metadaten in lokaler SQLite-Datenbank im WAL-Modus (`modernc.org/sqlite`).
  * Ausführungslogs dateibasiert im Dateisystem mit automatischer Gzip-Kompression und konfigurierbarer Retention Policy ($n$ letzte Läufe).

---

## 2. Systemarchitektur & Gesamtübersicht

```text
                               +-------------------------------------------------+
                               |             Reverse Proxy (Optional)            |
                               |             Nginx / Traefik (HTTPS / SSL)       |
                               +------------------------+------------------------+
                                                        | Proxy Pass (HTTP)
                                                        v
+-----------------------+              +-------------------------------------+
|   CI/CD (Jenkins)     |              |              JobCon Host            |
|  - Build Talend Job   |              |             (Linux Server)          |
|  - Push to Nexus      |              |                                     |
+-----------+-----------+              |  +-------------------------------+  |
            |                          |  | Auth Layer                    |  |
            | POST /api/.../deploy     |  | - BasicAuth / Session Cookie  |  |
            | (Bearer Token)           |  | - Local (bcrypt) / LDAP (AD)  |  |
            v                          |  +---------------+---------------+  |
+--------------------------------------+                  |                  |
|                                                         v                  |
|  +--------------------+   +-----------------------+   +-----------------+  |
|  |   Web UI (HTMX)    |   |   REST-API            |   | Settings Dialog |  |
|  |  - Dashboard       |   |  - Token Auth         |   | - Users & Roles |  |
|  |  - SSE Live-Console|   |  - Deploy / Run Hooks |   | - Server Setup  |  |
|  +---------^----------+   +-----------^-----------+   +--------^--------+  |
|            |                          |                        |           |
|            +--------------------+-----+------------------------+           |
|                                 |                                          |
|                      +----------v----------+                               |
|                      |     Core Engine     |                               |
|                      |  - SSH Runner       |                               |
|                      |  - Multi-Writer     |                               |
|                      |  - Concurrency Lock |                               |
|                      |  - Retention Policy |                               |
|                      +----+-----------+----+                               |
|                           |           |                                    |
|         +-----------------+           +----+                               |
|         v                                  v                               |
|  +---------------+                 +---------------+                       |
|  | SQLite (WAL)  |                 | Log Storage   |                       |
|  | (Metadata DB) |                 | (Files / Gzip)|                       |
|  +---------------+                 +---------------+                       |
+-------------------|--------------------------------------------------------+
                    | SSH (Port 22, Key-Based)
                    | CLI-Parameter (Push-Prinzip)
                    v
+----------------------------------------------------------------------------+
|                        Zielserver (Execution Node - Linux)                 |
|                                                                            |
|  /opt/talend/scripts/jobcon_ctl.sh                                         |
|    - deploy: Download Nexus ZIP & Unzip <----+  Nexus Repository           |
|    - run:    Ausführung über current-Symlink |  (Talend Job Archive)       |
|                                              |                             |
|  +----------------------------------------+  +--------------------------+  |
|  | Verzeichnisstruktur:                   |                             |  |
|  | /opt/talend/jobs/{job_name}/           |                             |  |
|  |  ├── releases/1.0.0/                   |                             |  |
|  |  ├── releases/1.1.0/                   |                             |  |
|  |  └── current -> releases/1.1.0/        |                             |  |
|  +----------------------------------------+                             |  |
|                         ^                                               |  |
|                         | Lokale Ausführung                             |  |
|  /opt/talend/scripts/run_job.sh                                         |  |
|                         ^                                               |  |
+-------------------------|-----------------------------------------------+--+
                          |
             +------------+------------+
             |   JS7 / SOS JobScheduler|
             |   (Geplante Ausführung) |
             +-------------------------+
```

---

## 3. Zielserver-Spezifikation (Linux Dateisystem & Skripte)

Auf den Linux-Zielservern liegt die Ausführungslogik in zwei standardisierten Bash-Skripten.

### 3.1 Dateisystem-Layout
```text
/opt/talend/
├── scripts/
│   ├── jobcon_ctl.sh          # Universal-Skript für JobCon (Deploy, Run, Retention)
│   └── run_job.sh             # Schlanker Starter für JS7 / lokale Linux-Scheduler
└── jobs/
    └── {job_name}/
        ├── releases/
        │   ├── 1.0.0/
        │   │   └── {job_name}/
        │   │       ├── {job_name}_run.sh
        │   │       └── ...
        │   └── 1.1.0/
        └── current -> releases/1.1.0/
```

### 3.2 Das Universal-Skript: `/opt/talend/scripts/jobcon_ctl.sh`
Nimmt alle Parameter via CLI-Argumente entgegen (kein Rückkanal zu JobCon nötig).

#### Befehle:
1. **`deploy`** – Lädt Release herunter, entpackt versioniert und aktualisiert den Symlink:
   ```bash
   /opt/talend/scripts/jobcon_ctl.sh deploy \
       --job "sync_sap_kunden" \
       --version "1.4.2" \
       --nexus-url "https://nexus.intern/repo/.../sync_sap_kunden-1.4.2.zip" \
       [--keep 3]
   ```
   * *Ablauf:*
     1. Prüft, ob `/opt/talend/jobs/{job}/releases/{version}` bereits existiert (falls ja: Download überspringen).
     2. Download via `curl` in temporären Ordner (`releases/.tmp_{version}`).
     3. Entpacken des ZIP-Archivs und Setzen der Dateirechte (`chmod +x`).
     4. Atomares Umschalten des Symlinks: `ln -sfn "releases/${version}" "/opt/talend/jobs/${job}/current"`.
     5. Version-Retention: Löscht die ältesten Releases und behält die letzten `--keep` Versionen.

2. **`run`** – Führt den Job aus:
   ```bash
   /opt/talend/scripts/jobcon_ctl.sh run \
       --job "sync_sap_kunden" \
       [--version "1.4.2"] \
       [--nexus-url "..."] \
       [--context "Production"] \
       [--params "--context_param date=2026-09-18"]
   ```
   * *Ablauf:*
     1. Falls `--version` übergeben wurde und noch nicht als `current` aktiv ist: Führt intern erst `deploy` aus.
     2. Startet den Job in einer eigenen Linux-Prozessgruppe (`setsid`):
        ```bash
        exec "/opt/talend/jobs/${job}/current/${job}/${job}_run.sh" --context="${context}" ${params}
        ```
     3. Gibt Exit-Code des Talend-Prozesses transparent an die aufrufende SSH-Session zurück.

### 3.3 Der JS7-Starter: `/opt/talend/scripts/run_job.sh`
Für JS7 / SOS JobScheduler optimiert: **Zero Network Hop, Zero Database Overhead**.
```bash
#!/usr/bin/env bash
set -euo pipefail

JOB_NAME="${1:-}"
if [[ -z "$JOB_NAME" ]]; then
    echo "ERROR: Kein Job-Name angegeben. Aufruf: run_job.sh <job_name> [args...]" >&2
    exit 1
fi
shift || true

JOB_RUN_SCRIPT="/opt/talend/jobs/${JOB_NAME}/current/${JOB_NAME}/${JOB_NAME}_run.sh"

if [[ ! -x "$JOB_RUN_SCRIPT" ]]; then
    echo "ERROR: Ausführbares Skript nicht gefunden: ${JOB_RUN_SCRIPT}" >&2
    exit 2
fi

# Direkte Ausführung im current-Release
exec "$JOB_RUN_SCRIPT" "$@"
```

---

## 4. Authentifizierung, Autorisierung & Security

### 4.1 Authentifizierungs-Architektur (Pluggable Provider)
In Go wird ein modulares Interface für Authentifizierungs-Provider definiert:
```go
type User struct {
    ID          string
    Username    string
    DisplayName string
    Role        string // "admin", "operator", "viewer"
    Source      string // "local" oder "ldap"
}

type Authenticator interface {
    Authenticate(ctx context.Context, username, password string) (*User, error)
}
```

#### Provider 1: Local Auth (Standard / Phase 1)
* Passwörter werden mit **`bcrypt`** gehasht (`golang.org/x/crypto/bcrypt`).
* Benutzerdaten liegen in der SQLite-Tabelle `users`.
* Beim ersten Start von JobCon wird automatisch ein initialer `admin`-Benutzer angelegt (Initialpasswort wird sicher generiert und im Start-Log ausgegeben oder über CLI gesetzt).

#### Provider 2: Active Directory / LDAP (Vorbereitet / Phase 2)
* Konfigurierbar in `config.yaml` oder über den Einstellungsdialog.
* Nutzt `go-ldap/ldap/v3` für TLS-gesichertes LDAP (LDAPS auf Port 636 oder StartTLS auf 389).
* Bind & Search Mechanismus mit frei konfigurierbarem User-Filter (z. B. `(&(objectClass=user)(sAMAccountName=%s))`).
* LDAP-Gruppen können Rollen (`admin`, `operator`, `viewer`) in JobCon zugeordnet werden.

### 4.2 Transport-Authentifizierung
* **HTTP BasicAuth:** Wird für alle Browser-Requests und API-Clients unterstützt (`Authorization: Basic base64(user:pass)`).
* **Session-Cookies:** Sichere HTTP-Only-Cookies für Web-UI-Logins.
* **API Bearer Tokens:** Für CI/CD-Systeme (Jenkins) unabhängig von Benutzerpasswörtern.

### 4.3 Rollen & Berechtigungen (RBAC)
| Aktion | Viewer | Operator | Admin |
| :--- | :---: | :---: | :---: |
| Dashboard & Job-Status einsehen | Ja | Ja | Ja |
| Live-Logs streamen & Historie lesen | Ja | Ja | Ja |
| Jobs starten (`run`) & abbrechen (`abort`) | Nein | Ja | Ja |
| Jobs deployen (`deploy`) | Nein | Ja | Ja |
| Execution Server anlegen & bearbeiten | Nein | Nein | Ja |
| Benutzerverwaltung & Rollenvergabe | Nein | Nein | Ja |
| Globale Systemeinstellungen bearbeiten | Nein | Nein | Ja |

### 4.4 SSL / TLS & Reverse Proxy Setup
* **Nativer Modus:** JobCon kann direkt mit Zertifikat und Key betrieben werden (`tls.enabled: true`).
* **Reverse Proxy Modus (Empfehlung für Linux):**
  * Nginx oder Traefik terminieren SSL/TLS und leiten via HTTP (`http://127.0.0.1:8080`) weiter.
  * JobCon liest `X-Forwarded-For`, `X-Forwarded-Proto` und `X-Forwarded-Host` aus.
  * Konfigurierbare CIDR-Whitelist (`trusted_proxies`) zur Vermeidung von Header-Spoofing.

---

## 5. JobCon Core Engine (Go)

### 5.1 SSH Runner & Prozesssteuerung
* **Bibliothek:** `golang.org/x/crypto/ssh` (reines Go, portabel).
* **Key-Handling:** SSH-Keys können zentral auf dem JobCon-Host liegen oder pro Server in der SQLite-Datenbank hinterlegt werden.
* **Keep-Alive:** Periodische Pings (`keepalive@openssh.com`) alle 30s verhindern Verbindungsabbrüche durch Linux-Firewalls / conntrack.
* **Prozess-Abbruch (Graceful Kill):**
  * Bei Klick auf "Abort" oder via API sendet JobCon remote `kill -TERM -$PGID`. Durch die beim Start gesetzte Process Group (`setsid`) werden auch alle von Talend gestarteten Kind-JVMs zuverlässig beendet.

### 5.2 Multi-Writer & Log-Streaming
* SSH `stdout`/`stderr` wird gebündelt:
  1. Direkt in die lokale Logdatei geschrieben (`data/logs/{execution_id}.log`).
  2. In den SSE-Broadcast-Hub eingespeist.
  3. Gepuffert und gethrottelt (50–100ms) an den Web-Browser gestreamt.

### 5.3 Concurrency Control
* Verhindert parallele Läufe desselben Jobs (`allow_concurrent: false`).
* Verhindert Überlastung des Zielservers durch einstellbare Worker-Limits (z. B. max. 4 parallele Jobs pro Zielserver).

---

## 6. Datenmodell (SQLite Schema)

CGO-freier Treiber **`modernc.org/sqlite`** im WAL-Modus (`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`).

```sql
-- Execution Server (Zielsysteme)
CREATE TABLE servers (
    id TEXT PRIMARY KEY,                   -- z.B. 'srv-talend-prod-01'
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    user TEXT NOT NULL DEFAULT 'talend',
    ssh_key_path TEXT NOT NULL,            -- Pfad zum Private Key auf JobCon-Host
    status TEXT NOT NULL DEFAULT 'unknown',-- 'online', 'offline', 'unknown'
    last_checked_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Benutzer & Authentifizierung
CREATE TABLE users (
    id TEXT PRIMARY KEY,                   -- UUID
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,          -- bcrypt Hash (leer bei reinen LDAP-Users)
    display_name TEXT NOT NULL,
    email TEXT,
    role TEXT NOT NULL DEFAULT 'viewer',   -- 'admin', 'operator', 'viewer'
    auth_source TEXT NOT NULL DEFAULT 'local', -- 'local' oder 'ldap'
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at DATETIME
);

-- Job-Definitionen
CREATE TABLE jobs (
    id TEXT PRIMARY KEY,                   -- z.B. 'sync_sap_kunden'
    name TEXT NOT NULL,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE RESTRICT,
    group_id TEXT NOT NULL,                -- Maven GroupId (z.B. 'de.firma.talend')
    artifact_id TEXT NOT NULL,             -- Maven ArtifactId
    active_version TEXT NOT NULL,          -- Aktuell produktive Version (z.B. '1.4.2')
    nexus_repo TEXT NOT NULL,              -- z.B. 'talend-releases'
    default_context TEXT NOT NULL DEFAULT 'Default',
    allow_concurrent BOOLEAN NOT NULL DEFAULT 0,
    retention_runs INTEGER NOT NULL DEFAULT 10,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Ausführungshistorie
CREATE TABLE executions (
    id TEXT PRIMARY KEY,                   -- NanoID (z.B. 'exec_01J8...')
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    action TEXT NOT NULL,                  -- 'run' oder 'deploy'
    status TEXT NOT NULL,                  -- 'pending', 'running', 'success', 'failed', 'aborted'
    version TEXT NOT NULL,
    context TEXT,
    exit_code INTEGER,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME,
    duration_ms INTEGER,
    log_path TEXT,
    triggered_by TEXT NOT NULL             -- 'jenkins', 'ui:admin', 'js7', etc.
);

-- API-Tokens für CI/CD (Jenkins)
CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,                    -- z.B. 'jenkins-deploy-token'
    token_hash TEXT NOT NULL,              -- SHA-256 Hash
    role TEXT NOT NULL DEFAULT 'operator',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME
);

-- Globale Einstellungen (Key-Value)
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Benutzereinstellungen (Seitengröße, Sortierung, Filter)
CREATE TABLE user_preferences (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, key)
);

-- Performance-Indizes für Skalierung auf 1.400+ Jobs
CREATE INDEX idx_jobs_group_id ON jobs(group_id);
CREATE INDEX idx_executions_job_id ON executions(job_id);
CREATE INDEX idx_executions_started_at ON executions(started_at DESC);
```

---

## 7. Web UI & Einstellungsdialog (HTMX & Dark-Design)

Das UI wird via Go `embed.FS` vollständig in das Single-Binary kompiliert und benötigt keine externen Assets oder CDNs.

### 7.1 Hauptbereiche der Web-Oberfläche
1. **Jobs Dashboard (`/`):**
   * **Skalierbar für 1.400+ Jobs:** Vollständig serverseitige Paginierung (10, 25, 50, 100, 1.000 Einträge), Volltextsuche und Filterung nach `group_id` und Server direkt über integrierte Spaltenfilter (`.table-filter-row`).
   * **Serverseitige Sortierung:** Sortierbar nach Name, Gruppe, Zielserver, aktiver Version und Zeitpunkt des letzten Laufs.
   * **Gruppierungsansicht:** Checkbox „Nach Gruppen bündeln“ zur optischen und logischen Clusterung nach Maven `GroupId` (mit Beibehaltung der internen Sortierung innerhalb der Gruppen).
   * **Klickbare Zeilen (`.job-row`):** Klick auf eine beliebige Stelle der Zeile öffnet direkt das Slide-Out-Sidepanel. Redundante Details-Buttons wurden entfernt.
   * **Letzter Lauf:** Status-Badge, Startzeitpunkt, Dauer und Direkt-Icon 📄 zum Öffnen des Logs.
   * **Aktions-Buttons:** Schnellstart (`▶ Start`) und Deployment (`🚀 Deploy`).
2. **Slide-Out Sidepanel & Log-Großansicht:**
   * Fährt bei Zeilenklick von rechts über den Bildschirm.
   * Zeigt alle Job-Metadaten (Server, Maven-Koordinaten, Repository, Context, Concurrency, Retention).
   * Ausführungshistorie der letzten Läufe für diesen spezifischen Job.
   * Konsolen-Log-Vorschau mit Umschaltung zwischen Läufen.
   * **Button „⛶ Großansicht“:** Öffnet das vollständige Log in einem großen modalen Dialog mit Kopierfunktion („📋 Kopieren“) und Link zur Vollbild-Live-Konsole.
   * Schnellaktionen im Header: [Starten], [Deployen], [Bearbeiten], [Löschen].
3. **Modale Dialoge & Z-Index-Hierarchie:**
   * Modale Hauptdialoge besitzen einen Z-Index von `2000`, verschachtelte Modale wie der Nexus-Picker besitzen `z-index: 2200`. Damit liegt auch bei Auswahldialogen und Fehlermeldungen (z. B. wenn Nexus nicht erreichbar ist) alles immer im absoluten Vordergrund vor dem aufrufenden Dialog und dem Sidepanel (`z-index: 1050`).
   * Beim Öffnen von Formulardialogen (wie „Bearbeiten“) aus dem Sidepanel wird das Sidepanel automatisch geschlossen, um den Fokus auf die Eingabemaske zu lenken.
4. **Nexus 3 Tree-View Artefakt-Browser:**
   * Beim Anlegen eines neuen Jobs: Button „📦 Aus Nexus auswählen“.
   * Fragt die Nexus 3 REST-API `/service/rest/v1/components` über das JobCon-Backend ab (umgeht CORS & Auth-Probleme).
   * Hierarchische Baumansicht (`GroupId` -> `ArtifactId` -> `Versions-Badges`).
   * Übernimmt gewählte Koordinaten direkt in das Anlageformular unter Berücksichtigung bereits eingegebener Daten.
5. **Globale Ausführungshistorie (`/executions`):**
   * Dedizierte Übersichtsseite aller vergangenen und laufenden Ausführungen.
   * Schnellfilter nach Status (`Alle`, `Läuft`, `Erfolg`, `Fehler`, `Abgebrochen`).
6. **Live-Konsole (`/executions/{id}`):**
   * SSE-Streaming von Konsolen-Logs in Echtzeit.
   * Suchfeld, Auto-Scroll-Pausierung und „Abort Job“-Button.
7. **Einstellungsdialog (`/settings` - nur für Rolle `admin`):**
   * **Reiter „Execution Server“ (`/settings/servers`):**
     * Server anlegen/bearbeiten (Host, Port, User, SSH-Key-Pfad).
     * **Button „Verbindung testen“:** Prüft SSH-Erreichbarkeit, Latenz und Talend-Pfade direkt via HTMX.
   * **Reiter „Benutzerverwaltung“ (`/settings/users`):**
     * Benutzer anlegen, Passwörter ändern (`bcrypt`), Rollen zuweisen (`admin`, `operator`, `viewer`).
   * **Reiter „System & Tokens“ (`/settings/system`):**
     * **Nexus 3 Anbindung:** Basis-URL, Benutzername und Passwort frei bearbeitbar und geschützt gespeichert.
     * **Button „⚡ Verbindung testen“:** Prüft Erreichbarkeit und Authentifizierung gegen die Nexus-API.
     * **Multi-Repository-Verwaltung:** Beliebig viele Repositories (z. B. `releases`, `snapshots`) anlegen, umbenennen oder löschen.
     * **Log-Aufbewahrung:** Manuelle Speicherbereinigung („🗑️ Alte Logs jetzt bereinigen“) mit Ein-Klick-Löschung überzähliger Logdateien.
     * **CI/CD API Tokens:** Erstellen und Widerrufen von Bearer-Tokens für Jenkins.

---

## 8. REST-API Spezifikation

Authentifizierung via `Authorization: Bearer <API_TOKEN>` oder HTTP BasicAuth.

### 8.1 Job-Steuerung & Metadaten
* **`GET /api/v1/jobs`** – Paginierte Liste der Jobs (Parameter: `page`, `page_size`, `search`, `group_id`, `server_id`, `sort_by`, `sort_order`).
* **`POST /api/v1/jobs`** – Neuen Job anlegen.
* **`GET /api/v1/jobs/{id}`** – Details eines Jobs.
* **`PUT /api/v1/jobs/{id}`** – Bestehenden Job bearbeiten (Metadaten, Version, Server, etc.).
* **`DELETE /api/v1/jobs/{id}`** – Job löschen.
* **`POST /api/v1/jobs/{id}/deploy`** – Deployt Version auf Zielserver (`"set_active": true`).
* **`POST /api/v1/jobs/{id}/run`** – Job ausführen. Parameter: `?wait=true` für synchrone Jenkins-Pipelines.
* **`POST /api/v1/executions/{id}/abort`** – Bricht laufende Ausführung per SSH-Prozessgruppen-Signal ab.
* **`GET /api/v1/executions/{id}/logs`** – Roh-Text (`?format=raw`) oder SSE-Stream.

### 8.2 Nexus Integration (Backend Proxy)
* **`GET /api/v1/nexus/repositories`** – Liste konfigurierter Repositories aus Settings oder YAML.
* **`POST /api/v1/nexus/test`** – Prüft Verbindung und Anmeldedaten gegen die Nexus 3 REST-API.
* **`GET /api/v1/nexus/search`** – Durchsucht Nexus nach Gruppen, Komponenten und Versionen (`repo`, `group`, `query`).

### 8.3 Administration, Storage & Benutzereinstellungen
* **`GET /api/v1/servers` & `POST /api/v1/servers`** (Server CRUD & Verbindungstest).
* **`GET /api/v1/users` & `POST /api/v1/users`** (Benutzer CRUD & Passwortänderung).
* **`POST /api/v1/storage/clean`** – Führt manuelle Retention-Bereinigung überzähliger Logs durch.
* **`GET /api/v1/user/preferences` & `POST /api/v1/user/preferences`** – Liest und speichert Benutzereinstellungen (Seitengröße, Sortierung, Gruppierung).

---

## 9. Konfigurationsdatei (`config.yaml`)

```yaml
server:
  bind: "0.0.0.0"
  port: 8080
  base_url: "https://jobcon.intern.firma.de"
  trusted_proxies:
    - "127.0.0.1/32"
    - "10.0.0.0/8"

tls:
  enabled: false              # Bei vorgeschaltetem Nginx/Traefik: false
  cert_file: "/etc/jobcon/tls/cert.pem"
  key_file: "/etc/jobcon/tls/key.pem"

auth:
  session_secret_env: "JOBCON_SESSION_SECRET"
  mode: "local"               # "local" oder "ldap"
  ldap:
    enabled: false
    host: "ad.intern.firma.de"
    port: 636
    use_ssl: true
    insecure_skip_verify: false
    bind_dn: "CN=jobcon_svc,OU=ServiceAccounts,DC=intern,DC=firma,DC=de"
    bind_password_env: "JOBCON_LDAP_PASSWORD"
    base_dn: "OU=Mitarbeiter,DC=intern,DC=firma,DC=de"
    user_filter: "(&(objectClass=user)(sAMAccountName=%s))"
    role_mappings:
      admin_group: "CN=Talend_Admins,OU=Groups,DC=intern,DC=firma,DC=de"
      operator_group: "CN=Talend_Operators,OU=Groups,DC=intern,DC=firma,DC=de"

database:
  path: "/var/lib/jobcon/data/jobcon.db"

storage:
  logs_dir: "/var/lib/jobcon/logs"
  compress_completed: true

nexus:
  base_url: "https://nexus.intern.firma.de/repository"
  auth:
    username: "nexus_talend_reader"
    password_env: "JOBCON_NEXUS_PASSWORD"

ssh_defaults:
  user: "talend"
  key_path: "/var/lib/jobcon/keys/id_ed25519"
  timeout_seconds: 30
  keepalive_interval_seconds: 30
```

---

## 10. Linux Deployment & Systemd-Integration

JobCon wird auf Linux als systemd Service betrieben.

### 10.1 Systemd Service Unit (`/etc/systemd/system/jobcon.service`)
```ini
[Unit]
Description=JobCon - Talend Deployment & Execution Engine
After=network.target

[Service]
Type=simple
User=jobcon
Group=jobcon
WorkingDirectory=/var/lib/jobcon
ExecStart=/usr/local/bin/jobcon --config /etc/jobcon/config.yaml
Restart=on-failure
RestartSec=5s

# Security Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/jobcon

[Install]
WantedBy=multi-user.target
```

---

## 11. Implementierungs-Roadmap

```text
+-------------------+     +-------------------+     +-------------------+     +-------------------+
|  Phase 1: Shell   | --> |  Phase 2: Core    | --> |  Phase 3: Auth &  | --> |  Phase 4: UI &    |
|  & SSH Prototyp   |     |  Runner & DB      |     |  Settings Dialog  |     |  End-to-End Test  |
+-------------------+     +-------------------+     +-------------------+     +-------------------+
```

1. **Phase 1: Shell-Skripte (`jobcon_ctl.sh` & `run_job.sh`)**
   * Bereitstellung der Zielserver-Skripte für Download, Unzip, Symlink `current` und Execution via `setsid`.
2. **Phase 2: Go SSH Runner & SQLite Datenbank**
   * SSH-Client (`golang.org/x/crypto/ssh`) mit Multi-Writer-Logging und Kill-Signal.
   * SQLite-Metadatenbank (`modernc.org/sqlite`), Log-Kompression und Retention-Cleaner.
3. **Phase 3: Authentifizierung, REST-API & Einstellungsdialog**
   * BasicAuth, Session-Management und `bcrypt`-Passwort-Handling.
   * REST-Endpunkte für Jenkins (`/deploy`, `/run?wait=true`).
   * Einstellungsdialog für Benutzerverwaltung und Serververwaltung mit SSH-Verbindungstest.
4. **Phase 4: Web UI (HTMX & SSE-Konsole) [Abgeschlossen]**
   * Dashboard für Job-Übersicht, Deployment- und Run-Modals.
   * Echtzeit-Konsole mit Server-Sent Events und Abort-Steuerung.
5. **Phase 5: Skalierung (1.400+ Jobs), Nexus-Browser & UX-Feinschliff [Abgeschlossen]**
   * Serverseitige Paginierung, Volltextsuche und Sortierung in SQLite mit Window Functions.
   * Persistenz von Benutzereinstellungen in `user_preferences`.
   * Interaktiver Nexus 3 Baum-Browser (`GroupId` -> `ArtifactId` -> `Version`).
   * Slide-Out Sidepanel für Job-Details und Historie mit nahtloser Log-Großansicht.
   * Vollständig klickbare Tabellenzeilen (`.job-row`) und saubere Modalführung (`z-index: 2000`).
   * Dedizierte `/executions` Historienseite und manuelle Log-Retention-Bereinigung.
6. **Phase 6: Optionale LDAP/AD-Anbindung & Härtung**
   * Implementierung des LDAP-Authenticators (`go-ldap/ldap/v3`).
   * Reverse Proxy & TLS-Verifikation, systemd Deployment.
