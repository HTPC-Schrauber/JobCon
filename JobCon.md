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
  * Globale Einstellungen (Nexus, Synchronisations-Intervall, Parallelitätsgrenzen, Aufbewahrungsfristen, LDAP-Konfiguration).
* **Lokaler Nexus-Cache & Hintergrund-Synchronisation:**
  * Caching aller Nexus-Artefakte in der lokalen SQLite-Datenbank.
  * Periodische Hintergrund-Synchronisation in einstellbarem Zeitintervall und manuell per Knopfdruck / API.
  * Blitzschnelle (< 5ms) und ausfallsichere Suche ohne Last auf großen Nexus-Servern.
* **Warteschlange & Parallelitätssteuerung (Queue & Concurrency):**
  * Schutz vor Server-Überlastung bei Massenstarts durch globale Obergrenze (`max_concurrent_jobs`).
  * Automatische Warteschlange: Beendete Jobs machen unmittelbar Platz für den nächsten anstehenden Job.
  * Ad-hoc-Übersteuerung der Parallelität direkt neben dem Start-Button in der UI.
* **Modernes High-Density Telemetrie-UI:**
  * Kompaktes Status-Band (Operational High-Density Status Strip) anstelle sperriger Kacheln.
  * Warmes bernsteinfarbenes Farbschema (Warm Amber `#f59e0b`).
  * Fixierte Spaltenüberschriften (Sticky Headers) beim vertikalen Scrollen.
  * Blinkender bernsteinfarbener Punkt (`animate-ping`) vor laufenden Jobs mit direktem Live-Log-Link.
  * Komfortable Zeilenauswahl ohne Checkboxen (Klick toggelt, Strg+Klick Mehrfachauswahl, Shift+Klick Bereich, Esc leert).
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
        │   ├── 1.1.0/
        │   └── 1.2.0/
        ├── current   -> releases/1.2.0/   # Aktive Version
        ├── current-1 -> releases/1.1.0/   # Vorherige Version (Retention)
        └── current-2 -> releases/1.0.0/   # Vor-Vorherige Version (Retention)
```

### 3.2 Das Universal-Skript: `/opt/talend/scripts/jobcon_ctl.sh`
Nimmt alle Parameter via CLI-Argumente entgegen (kein Rückkanal zu JobCon nötig).
Unterstützt automatisches Einbinden von `.env`-Dateien (entweder über `--env-file <path>` oder standardmäßig aus `/opt/talend/jobs/{job}/.env` bzw. `/opt/talend/jobs/.env`).

#### Befehle:
1. **`deploy`** – Lädt Release herunter, entpackt versioniert, rotiert die Generations-Symlinks und bereinigt unverlinkte Versionen:
   ```bash
   /opt/talend/scripts/jobcon_ctl.sh deploy \
       --job "sync_sap_kunden" \
       --version "1.4.2" \
       --nexus-url "https://nexus.intern/repo/.../sync_sap_kunden-1.4.2.zip" \
       [--keep 3] \
       [--env-file "/opt/talend/jobs/.env"]
   ```
   * *Ablauf:*
     1. Lädt bei Vorhandensein Umgebungsvariablen aus der konfigurierten `.env`-Datei.
     2. Prüft, ob `/opt/talend/jobs/{job}/releases/{version}` bereits existiert (falls ja: Download überspringen).
     3. Download via `curl` in temporären Ordner (`releases/.tmp_{version}`).
     4. Entpacken des ZIP-Archivs und Setzen der Dateirechte (`chmod +x`).
     5. Generations-Symlink-Rotation: Aktualisiert die Symlink-Kette (`current`, `current-1`, `current-2`, ...) bis zu `--keep` Versionen ohne Duplikate.
     6. Version-Retention: Löscht alle Release-Ordner in `releases/`, auf die kein aktiver Symlink zeigt (`rm -rf`). Überzählige `current-*` Symlinks jenseits `--keep` werden entfernt.

2. **`undeploy`** – Entfernt einen bereitgestellten Job und dessen Versionen vollständig vom Zielserver:
   ```bash
   /opt/talend/scripts/jobcon_ctl.sh undeploy \
       --job "sync_sap_kunden" \
       [--jobs-dir "/opt/talend/jobs"]
   ```
   * *Ablauf:*
     1. Entfernt alle aktiven Symlinks (`current`, `current-*`).
     2. Löscht alle installierten Releases unter `releases/`.
     3. Entfernt das Job-Verzeichnis, falls keine weiteren Daten vorhanden sind.

3. **`run`** – Führt den Job aus:
   ```bash
   /opt/talend/scripts/jobcon_ctl.sh run \
       --job "sync_sap_kunden" \
       [--version "1.4.2"] \
       [--nexus-url "..."] \
       [--context "Production"] \
       [--params "--context_param date=2026-09-18"] \
       [--env-file "/opt/talend/jobs/.env"]
   ```
   * *Ablauf:*
     1. Lädt Umgebungsvariablen aus der `.env`-Datei.
     2. Falls `--version` übergeben wurde und noch nicht als `current` aktiv ist: Führt intern erst `deploy` aus.
     3. Startet den Job in einer eigenen Linux-Prozessgruppe (`setsid`):
        ```bash
        exec "/opt/talend/jobs/${job}/current/${job}/${job}_run.sh" --context="${context}" ${params}
        ```
     4. Gibt Exit-Code des Talend-Prozesses transparent an die aufrufende SSH-Session zurück.

### 3.3 Der JS7-Starter: `/opt/talend/scripts/run_job.sh`
Für JS7 / SOS JobScheduler optimiert: **Zero Network Hop, Zero Database Overhead**.
Lädt automatisch Umgebungsvariablen aus `.env` (falls am Job oder im übergeordneten Jobs-Verzeichnis vorhanden oder via `TALEND_ENV_FILE` gesetzt).
```bash
#!/usr/bin/env bash
set -euo pipefail

# Sourcing .env falls vorhanden
TALEND_ENV_FILE="${TALEND_ENV_FILE:-}"
if [[ -n "$TALEND_ENV_FILE" && -f "$TALEND_ENV_FILE" ]]; then
    set -a; source "$TALEND_ENV_FILE"; set +a
elif [[ -f "/opt/talend/jobs/.env" ]]; then
    set -a; source "/opt/talend/jobs/.env"; set +a
fi

JOB_NAME="${1:-}"
if [[ -z "$JOB_NAME" ]]; then
    echo "ERROR: Kein Job-Name angegeben. Aufruf: run_job.sh <job_name> [args...]" >&2
    exit 1
fi
shift || true

# Job-spezifische .env nachladen
if [[ -f "/opt/talend/jobs/${JOB_NAME}/.env" ]]; then
    set -a; source "/opt/talend/jobs/${JOB_NAME}/.env"; set +a
fi

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

#### Provider 2: Active Directory / LDAP (Vollständig integriert)
* **Gleichzeitige Koexistenz:** Lokale Konten (`bcrypt`) und Verzeichnisdienst-Konten (Active Directory / OpenLDAP) existieren parallel nebeneinander. Bei der Benutzeranlage wird die Authentifizierungsquelle (`local` oder `ldap`) festgelegt.
* **Protokolle & Ports:** Volle Unterstützung für LDAP (Port 389) und LDAPS (Port 636, SSL/TLS). Die Portnummer wird je nach gewähltem Protokoll automatisch vorbelegt, bleibt jedoch jederzeit frei editierbar.
* **Betrieb ohne extra Bind-User (Direct User Bind):**
  * LDAP/AD funktioniert standardmäßig ohne separaten Service-Account.
  * Benutzer binden sich direkt mit ihren Zugangsdaten am Verzeichnis (z. B. via UPN-Muster `%s@intern.firma.de` oder automatisch aus der Suchbasis abgeleitet).
  * Optional kann weiterhin ein klassischer Service-Account (`bind_dn` und `bind_password`) hinterlegt werden.
* **Konfigurierbare Attribut-Herkunft (Grundeinstellungen):**
  * Loginname-Attribut (Default: `sAMAccountName`, alternativ `uid`, `userPrincipalName`).
  * Vorname-Attribut (Default: `givenName`).
  * Nachname-Attribut (Default: `sn`).
  * E-Mail-Attribut (Default: `mail`).
  * Automatische Befüllung/Synchronisation von Vorname + Nachname (als Anzeigename) und E-Mail bei der Anmeldung oder via Lookup.
* **Schutz vor Aussperren (Last Local Admin Protection):**
  * Es muss zu jedem Zeitpunkt mindestens **ein aktiver lokaler Administrator** (`role = 'admin'`, `auth_source = 'local'`, `is_active = 1`) im System verbleiben.
  * Das Löschen, Deaktivieren, Herabstufen auf eine niedrigere Rolle oder Umstellen auf LDAP des letzten lokalen Administrators wird von der Datenbank (`ErrLastAdminProtection`), der API und der Web-UI strikt unterbunden.
* **Verbindungs- und Benutzer-Lookup-Tests:**
  * Direkte interaktive Tests im Einstellungsdialog (`/settings/ldap`) zur Überprüfung von Konnektivität, TLS und Attribut-Auflösung.

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
    jobs_dir TEXT NOT NULL DEFAULT '/opt/talend/jobs',       -- Konfigurierbares Jobs-Verzeichnis
    scripts_dir TEXT NOT NULL DEFAULT '/opt/talend/scripts', -- Konfigurierbares Scripte-Verzeichnis
    env_file TEXT NOT NULL DEFAULT '',                      -- Optionaler Pfad zu einer .env Datei auf dem Zielserver
    keep_releases INTEGER NOT NULL DEFAULT 3,               -- Vorgehaltene Release-Versionen (Default: 3)
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
    active_version TEXT NOT NULL,          -- Aktuell konfigurierte Version (z.B. '1.4.2')
    nexus_repo TEXT NOT NULL,              -- z.B. 'talend-releases'
    default_context TEXT NOT NULL DEFAULT 'Default',
    allow_concurrent BOOLEAN NOT NULL DEFAULT 0,
    retention_runs INTEGER NOT NULL DEFAULT 10,
    env_file TEXT NOT NULL DEFAULT '',         -- Optionaler Job-spezifischer Pfad zu .env
    is_deployed BOOLEAN NOT NULL DEFAULT 0,    -- 1 wenn aktuell auf Zielserver deployed
    deployed_version TEXT NOT NULL DEFAULT '', -- Aktuell auf Zielserver installierte Version
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

-- Nexus Artefakte (Lokaler Cache für blitzschnelle Suche)
CREATE TABLE nexus_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    version TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repository_id, group_id, artifact_id, version)
);

-- Performance-Indizes für Skalierung auf 1.400+ Jobs & Nexus Cache
CREATE INDEX idx_jobs_group_id ON jobs(group_id);
CREATE INDEX idx_executions_job_id ON executions(job_id);
CREATE INDEX idx_executions_started_at ON executions(started_at DESC);
CREATE INDEX idx_nexus_repo_group ON nexus_artifacts(repository_id, group_id);
CREATE INDEX idx_nexus_repo_artifact ON nexus_artifacts(repository_id, artifact_id);
CREATE INDEX idx_nexus_search ON nexus_artifacts(repository_id, group_id, artifact_id);
```

---

## 7. Web UI & Einstellungsdialog (HTMX & Dark-Design)

Das UI wird via Go `embed.FS` vollständig in das Single-Binary kompiliert und benötigt keine externen Assets oder CDNs.

### 7.1 Hauptbereiche der Web-Oberfläche
1. **Umgebungs-Kennzeichnung & Farbkonzept (Warm Amber Theme):**
   * **Prominenter Indikator:** Eindeutige Anzeige der aktuellen Umgebung (z. B. `DEV`, `TEST`, `PROD`) in der Navigationsleiste und auf der Login-Seite.
   * **Farbkodierung:** Konfigurierbar in `config.yaml` (`name`, `color`, `text_color`). Ein farblicher Akzentstreifen am oberen Rand (`top-accent-line`) signalisiert sofort, auf welcher Instanz gearbeitet wird.
   * **Farbschema:** Modernes, augenschonendes High-Density-Design im Pro-Audio-Stil mit warmen Bernstein-Akzenten (`#f59e0b`, `#0d1117`, `#161b22`, `#1c2128`, `#30363d`).
   * **Benutzer-Avatar:** Initialen-Badge (z. B. `MO`) direkt neben Benutzername und Rolle.
2. **Operational High-Density Status Strip:**
   * Kompaktes Statusband im Kopfbereich anstelle sperriger Kacheln:
     * **Jobs:** Gesamtzahl aller konfigurierten Talend Jobs.
     * **JobServer:** Anzahl aktiver Knoten (`online / total`) mit grün pulsierendem Statuspunkt.
     * **Gruppen:** Anzahl unterschiedlicher Gruppen (GroupID).
     * **Laufend:** Anzahl aktiver Läufe, auffällig in Amber hervorgehoben.
     * **Tipp:** Tastatur-Hinweis (`Strg` für Mehrfachauswahl, `Shift` für Bereich).
     * **Nexus Sync:** Statusanzeige („vor X Min.“ / „Sync läuft...“) mit Sofort-Aktualisierungsbutton (drehendes Icon bei aktivem Lauf).
3. **Jobs Dashboard (`/`):**
   * **Skalierbar für 1.400+ Jobs:** Vollständig serverseitige Paginierung (10, 25, 50, 100, 1.000 Einträge), Volltextsuche und Filterung nach `group_id` und Server direkt über integrierte Spaltenfilter (`.table-filter-row`).
   * **Serverseitige Sortierung:** Sortierbar nach Name, Gruppe, Zielserver, aktiver Version und Zeitpunkt des letzten Laufs.
   * **Fixierte Spaltenüberschriften (Sticky Headers):**
     * Beim vertikalen Scrollen bleiben Spaltenköpfe (`thead th`) und Filterzeile oben fixiert (`.table-scroll-container`), sodass Orientierung und Filterung bei großen Listen jederzeit erhalten bleiben.
   * **Pulsierender Indikator für laufende Jobs:**
     * Bei laufenden Jobs (`status: running`) pulsiert links neben dem Jobnamen ein animierter bernsteinfarbener Punkt mit Ring-Animation (`animate-ping`).
     * In der Spalte „Letzter Lauf“ signalisiert ein amberfarbenes `RUNNING`-Badge mit rotierendem Spinner den Lauf, ergänzt durch einen direkten Link `Live-Log` zum sofortigen Öffnen der Konsolenausgabe.
   * **Optimierte Zeilenauswahl & Deselektierung (ohne Checkboxen):**
     * **Klick auf eine Zeile:** Wählt den Job exklusiv aus.
     * **Erneuter Klick auf denselben Job:** Hebt die Auswahl wieder auf (Deselektion).
     * **Strg / Cmd + Klick:** Schaltet den angeklickten Job gezielt an oder aus (Toggle).
     * **Shift + Klick:** Wählt einen zusammenhängenden Zeilenbereich aus.
     * **Esc-Taste oder „Auswahl aufheben“:** Setzt die gesamte Zeilenauswahl zurück.
     * Klicks auf Aktions-Buttons (Details, Log, Live-Log) lösen keine Selektion aus (`.action-btn`).
   * **Bulk-Aktionsleiste mit Inline-Parallelitätssteuerung:**
     * Erscheint dynamisch bei selektierten Jobs über der Tabelle:
       * `Parallel: [ N ]`: Eingabefeld zur temporären Übersteuerung des Parallelitätslimits für diesen spezifischen Startlauf.
       * `▶ Starten`: Startet alle markierten Jobs über die Semaphore-Warteschlange.
       * `🚀 Deployen`: Stößt das Deployment der markierten Jobs an.
       * `🗑️ Undeployen`: Entfernt die markierten Jobs von den Zielservern.
       * `Auswahl aufheben (Esc)`: Setzt die Selektion zurück.
   * **Deployment-Statusindikator:** Farbiger Punkt links neben dem Jobnamen (grün = auf Zielserver deployed, grau = nicht deployed) basierend auf `is_deployed`.
   * **Letzter Lauf:** Status-Badge, Startzeitpunkt, Dauer und Direkt-Icon 📄 zum Öffnen des Logs.
4. **Slide-Out Sidepanel & Log-Großansicht:**
   * Fährt bei Klick auf „Details“ von rechts über den Bildschirm.
   * Zeigt alle Job-Metadaten (Server, Maven-Koordinaten, Repository, Context, Concurrency, Retention, Deployment-Status, `.env`-Pfad).
   * Ausführungshistorie der letzten Läufe für diesen spezifischen Job.
   * Konsolen-Log-Vorschau mit Umschaltung zwischen Läufen.
   * **Button „⛶ Großansicht“:** Öffnet das vollständige Log in einem großen modalen Dialog mit Kopierfunktion („📋 Kopieren“) und Link zur Vollbild-Live-Konsole.
   * Schnellaktionen im Header: [Starten] (öffnet modalen Parameter-Dialog), [Deployen], [Undeployen], [Bearbeiten], [Löschen].
   * **Lösch-Sicherheitsabfrage:** Ist ein Job noch deployed, fragt JobCon beim Löschen, ob die Artefakte auch vom Zielserver entfernt werden sollen (`undeploy_server=true`).
5. **Mehrsprachigkeit (i18n):**
   * Vollständige Internationalisierung (Englisch als Default, Deutsch integriert).
   * Sprachumschaltung direkt im Header-Menü für jeden Benutzer.
   * Gespeichert in der Datenbank (`user_preferences`) und im Fallback-Cookie (`jobcon_lang`).
   * Erweiterbar über externe Übersetzungsdateien (`locales_dir`).
   * Integrierte JavaScript-Hilfsfunktion `window.t(key, ...args)`.
6. **Nexus 3 Tree-View Artefakt-Browser (Lokaler Cache):**
   * Beim Anlegen eines neuen Jobs: Button „📦 Aus Nexus auswählen“.
   * Greift transparent auf die lokale SQLite-Tabelle `nexus_artifacts` zu – extrem performant (< 5ms) und unabhängig von der Latenz oder Auslastung des Nexus-Servers.
   * Getrennte Eingabefelder für `GroupId` und `ArtifactId` zur präzisen Suche.
   * Hierarchische Baumansicht (`GroupId` -> `ArtifactId` -> `Versions-Badges`).
   * Übernimmt gewählte Koordinaten direkt in das Anlageformular.
7. **Globale Ausführungshistorie (`/executions`):**
   * Dedizierte Übersichtsseite aller vergangenen und laufenden Ausführungen.
   * Schnellfilter nach Status (`Alle`, `Läuft`, `Erfolg`, `Fehler`, `Abgebrochen`).
8. **Live-Konsole (`/executions/{id}`):**
   * SSE-Streaming von Konsolen-Logs in Echtzeit.
   * Suchfeld, Auto-Scroll-Pausierung und „Abort Job“-Button.
9. **Einstellungsdialog (`/settings` - nur für Rolle `admin`):**
   * **Reiter „Execution Server“ (`/settings/servers`):**
     * **Server-Sidepanel:** Statusdiagnose, Verzeichnisse, installierte Scripte, `.env`-Pfad und Job-Liste.
     * **Konfigurierbare Verzeichnisse & Release-Retention:** `jobs_dir`, `scripts_dir`, `env_file`, `keep_releases`.
     * **Automatisches SSH-Setup („Scripte bereitstellen“):** Legt Zielverzeichnisse per SSH an und installiert `jobcon_ctl.sh` und `run_job.sh` mit 0755.
     * **Sicheres Löschen:** Migriert verknüpfte Jobs atomar auf alternative Zielserver.
     * **Button „Verbindung testen“:** Prüft SSH-Erreichbarkeit, Latenz und Scripte.
   * **Reiter „Benutzerverwaltung“ (`/settings/users`):**
     * Benutzer anlegen, Passwörter ändern (`bcrypt`), Rollen zuweisen (`admin`, `operator`, `viewer`).
   * **Reiter „System & Tokens“ (`/settings/system`):**
     * **Nexus 3 Grundkonfiguration:** Basis-URL, Benutzername und Passwort mit Verbindungstest.
     * **Multi-Repository-Verwaltung:** Repositories anlegen, umbenennen oder löschen.
     * **Nexus Synchronisation & Lokaler Cache:**
       * Anzeige des aktuellen Sync-Status (Zuletzt synchronisiert, Anzahl gecachter Artefakte).
       * Button „Nexus jetzt synchronisieren“ zum sofortigen manuellen Abgleich.
       * Einstellbares Synchronisationsintervall in Minuten (Default: 60 Minuten).
     * **Job-Ausführung & Warteschlange:**
       * Konfigurierbare Standard-Parallelität (`max_concurrent_jobs`, Default: 2) für Bulk-Starts.
     * **Log-Aufbewahrung:** Manuelle Speicherbereinigung („🗑️ Alte Logs jetzt bereinigen“).
     * **CI/CD API Tokens:** Erstellen und Widerrufen von Bearer-Tokens für Jenkins.

---

## 8. REST-API Spezifikation

Authentifizierung via `Authorization: Bearer <API_TOKEN>` oder HTTP BasicAuth.

### 8.1 Job-Steuerung & Metadaten
* **`GET /api/v1/jobs`** – Paginierte Liste der Jobs (Parameter: `page`, `page_size`, `search`, `group_id`, `server_id`, `sort_by`, `sort_order`).
* **`POST /api/v1/jobs`** – Neuen Job anlegen.
* **`GET /api/v1/jobs/{id}`** – Details eines Jobs.
* **`PUT /api/v1/jobs/{id}`** – Bestehenden Job bearbeiten (Metadaten, Version, Server, etc.).
* **`DELETE /api/v1/jobs/{id}`** – Job löschen (optional `?undeploy=true` zur Bereinigung des Zielservers).
* **`POST /api/v1/jobs/{id}/deploy`** – Deployt Version auf Zielserver (`"set_active": true`).
* **`POST /api/v1/jobs/{id}/undeploy`** – Entfernt Versionen und Symlinks vom Zielserver.
* **`POST /api/v1/jobs/{id}/run`** – Job ausführen. Parameter: `?wait=true` für synchrone Jenkins-Pipelines.
* **`POST /api/v1/jobs/bulk/run`** – Führt mehrere Jobs über die Semaphore-Warteschlange aus. Unterstützt optionalen Parameter `concurrency` (z. B. `{"job_ids": ["job1", "job2"], "concurrency": 2}`).
* **`POST /api/v1/jobs/bulk/deploy`** – Deployt mehrere Jobs gleichzeitig.
* **`POST /api/v1/jobs/bulk/undeploy`** – Entfernt mehrere Jobs gleichzeitig von Zielservern.
* **`POST /api/v1/executions/{id}/abort`** – Bricht laufende Ausführung per SSH-Prozessgruppen-Signal ab.
* **`GET /api/v1/executions/{id}/logs`** – Roh-Text (`?format=raw`) oder SSE-Stream.

### 8.2 Nexus Integration & Lokaler Cache
* **`GET /api/v1/nexus/repositories`** – Liste konfigurierter Repositories aus Settings oder YAML.
* **`POST /api/v1/nexus/test`** – Prüft Verbindung und Anmeldedaten gegen die Nexus 3 REST-API.
* **`POST /api/v1/nexus/sync`** – Startet die asynchrone Hintergrund-Synchronisation aller konfigurierten Repositories in die lokale SQLite-Datenbank.
* **`GET /api/v1/nexus/sync/status`** – Liefert aktuellen Sync-Status (`status`, `is_syncing`, `last_synced_at`, `item_count`, `last_error`, `interval_minutes`).
* **`GET /api/v1/nexus/search`** – Durchsucht blitzschnell den lokalen SQLite-Cache nach Gruppen, Komponenten und Versionen (`repo`, `group`, `query`).

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

environment:
  name: "PROD"                # z.B. DEV, TEST, STAGING, PROD (leer = kein Badge)
  color: "#dc2626"            # Hintergrundfarbe für Badge und oberen Farbakzentstreifen
  text_color: "#ffffff"       # Textfarbe des Badges

i18n:
  default_language: "en"      # Standard: "en" (English) oder "de" (Deutsch)
  locales_dir: ""             # Optional: Pfad zu benutzerdefinierten JSON-Locales

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
    port: 636                   # Vorbelegt: 636 für LDAPS, 389 für LDAP (frei änderbar)
    use_ssl: true               # true = LDAPS, false = LDAP
    insecure_skip_verify: false
    # Optionaler Bind-Account (wenn leer, greift Direct User Bind ohne extra Service-Account):
    bind_dn: ""
    bind_password_env: "JOBCON_LDAP_PASSWORD"
    user_bind_template: "%s@intern.firma.de" # Direct Bind Muster (z.B. %s@domain oder uid=%s,ou=users,...)
    base_dn: "OU=Mitarbeiter,DC=intern,DC=firma,DC=de"
    user_filter: ""             # Optionaler Filter, Standard: (<attr_username>=%s)
    attr_username: "sAMAccountName" # Loginname (sAMAccountName, uid, userPrincipalName)
    attr_first_name: "givenName"    # Vorname
    attr_last_name: "sn"            # Nachname
    attr_email: "mail"              # E-Mail

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
6. **Phase 5b: Erweitertes Execution-Server Management [Abgeschlossen]**
   * Interaktives Sidepanel für Execution Server mit Statusdiagnose und Job-Zugehörigkeiten.
   * Server-spezifische Jobs- (`jobs_dir`) und Scripte-Verzeichnisse (`scripts_dir`) in DB und UI.
   * Automatisches Zielserver-Setup via SSH: Einbetten (`//go:embed`) und Verteilen von `jobcon_ctl.sh` und `run_job.sh` mit Rechten 0755.
   * Sicheres Löschen von Servern mit Abhängigkeitsprüfung und atomarer Job-Migration auf alternative Zielserver.
7. **Phase 5c: Nexus 3 Pfad-Normalisierung & Download-Robustheit [Abgeschlossen]**
   * Normalisierung von Nexus 3 Maven-Repository URLs: Trennung von REST-API-Aufrufen (`/service/rest/v1/...` an Host-Root) und Artefakt-Downloads (`/repository/{repo}/...`).
   * Robuste Unterstützung sowohl für Basis-URLs mit als auch ohne `/repository`-Suffix.
8. **Phase 5d: Multi-Job Steuerung, Undeploy, i18n & Umgebungs-Kennzeichnung [Abgeschlossen]**
   * Konfigurierbares Environment-Badge (`DEV`, `TEST`, `PROD`) mit Farbstreifen im UI.
   * Mausbasierte Mehrfachauswahl (Klick, Shift+Klick, Strg/Cmd+Klick) ohne Checkboxen; dedizierter Details-Button.
   * Bulk-Aktionen: Sofort-Start (`▶ Start`), Deployment (`🚀 Deploy`) und Undeployment (`🗑️ Undeploy`).
   * Undeploy-Unterstützung im Bash-Controller (`jobcon_ctl.sh undeploy`) und Runner.
   * Optischer Deployment-Statusindikator (grüner/grauer Punkt) und Abfrage zum Server-Cleanup beim Job-Löschen.
   * Dynamische `.env`-Sourcing auf Zielsystemen pro Server und pro Job.
   * Vollständige Mehrsprachigkeit (i18n, DE/EN) mit Header-Sprachwechsler.
   * Stabiler, racy-sicherer Nexus-Artefakt-Picker mit getrennten GroupId/ArtifactId-Feldern.
9. **Phase 5e: High-Density UI, Nexus Local DB Cache & Concurrency Queue [Abgeschlossen]**
   * **Operational High-Density Status Strip:** Kompaktes Statusband im Kopfbereich (Jobs, Online-Server mit Puls-Dot, Gruppen, laufende Läufe in Amber, Tastaturtipp und Nexus Sync-Status mit Sofort-Aktualisierungs-Button).
   * **Sticky Headers:** Fixierte Spaltenköpfe (`thead th`) und Filterzeile beim vertikalen Scrollen großer Joblisten (`.table-scroll-container`).
   * **Blinkender bernsteinfarbener Punkt:** Animierter Radar-Puls (`animate-ping`) vor Jobnamen bei Status `running`, amberfarbenes `RUNNING`-Badge und direkter `Live-Log`-Link.
   * **Verbesserte Zeilenauswahl & Deselektion:** Einzelauswahl mit Deselektion bei erneutem Klick auf denselben Job; Mehrfachauswahl via Strg/Cmd+Klick Toggle; Shift+Klick Bereichsauswahl; Esc-Taste oder Button zum Leeren; Klicks auf Aktions-Buttons lösen keine Zeilenauswahl aus.
   * **Nexus Local DB Cache & Background Syncer:** Lokale SQLite-Tabelle `nexus_artifacts` mit Indizes; periodischer Hintergrund-Syncer (`Syncer`) mit konfigurierbarem Intervall; manuelle Sofort-Synchronisation via UI und API (`POST /api/v1/nexus/sync`); Artefakt-Suche (< 5ms) ohne Last auf Nexus.
   * **Warteschlange & Parallelität:** Globale Standard-Parallelität (`max_concurrent_jobs`) in System-Einstellungen; Queue-Worker-Pool startet bis zu $N$ Jobs parallel und lässt folgende automatisch nachrücken; temporäre Übersteuerung direkt in der Bulk-Toolbar (`Parallel: [ N ]`).
10. **Phase 6: Optionale LDAP/AD-Anbindung & Härtung**
   * Implementierung des LDAP-Authenticators (`go-ldap/ldap/v3`).
   * Reverse Proxy & TLS-Verifikation, systemd Deployment.
