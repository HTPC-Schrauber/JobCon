# JobCon – Lightweight Talend Job Deployment & Execution Engine

JobCon ist ein leichtgewichtiger, Linux-nativer Ersatz für den Talend Administration Center (TAC) / Job Conductor.

## Kernmerkmale

* **Single-Binary-Architektur:** Entwickelt in reiner Go-Standardbibliothek + CGO-freiem SQLite (`modernc.org/sqlite`). Keine Java-, Node- oder Python-Laufzeitabhängigkeiten.
* **Entkopplung von Deployment & Execution:**
  * **Deployment:** Automatisierter Download aus Nexus & atomare Versionierung (`releases/{version}` und Symlink `current`).
  * **Execution:** Lokaler Start ohne TAC-Overhead via `run_job.sh` für **JS7 / SOS JobScheduler** oder über JobCon SSH-Runner.
* **Authentifizierung & Sicherheit:**
  * HTTP BasicAuth und Session-Cookies.
  * Benutzerverwaltung mit `bcrypt`-Passwort-Hashing.
  * Rollenbasiertes Rechtesystem (`admin`, `operator`, `viewer`).
  * Vorbereitet für LDAP / Active Directory.
  * Native TLS-Unterstützung oder Betrieb hinter Nginx/Traefik Reverse-Proxy.
* **Echtzeit-Transparenz:** Live-Streaming von `stdout` und `stderr` via Server-Sent Events (SSE) in ein schlankes Web-Terminal.
* **CI/CD Integration (Jenkins):** REST-API mit API-Bearer-Tokens und blockierender Ausführung (`?wait=true`) als moderner MetaServlet-Ersatz.
* **Einstellungsdialog:** Integrierte Web-UI zur Verwaltung von Benutzern, Execution-Servern (inkl. Live-SSH-Verbindungstest) und Systemparametern.

---

## Schnellstart

### 1. Kompilieren
```bash
go build -o jobcon ./cmd/jobcon
```

### 2. Konfiguration
Kopieren Sie die Beispielkonfiguration:
```bash
cp config.example.yaml config.yaml
```

Passen Sie die Pfade und Zugangsdaten nach Bedarf an.

### 3. Starten
```bash
./jobcon --config config.yaml
```

Beim Erststart wird automatisch ein initialer Administrator angelegt:
* **Benutzer:** `admin`
* **Passwort:** Wird im Konsolen-Log generiert oder kann über die Umgebungsvariable `JOBCON_ADMIN_PASSWORD` vorgegeben werden.

Öffnen Sie anschließend `http://localhost:8080` im Browser.

---

## Zielserver-Skripte

Auf den Execution-Hosts werden die beiden Skripte aus dem Ordner `scripts/` unter `/opt/talend/scripts/` abgelegt:

1. **`jobcon_ctl.sh`** (Universal-Skript für JobCon):
   * Deployt Releases aus Nexus nach `/opt/talend/jobs/{job_name}/releases/{version}`.
   * Schaltet atomar den Symlink `current` um.
   * Bereinigt alte Releases gemäß Retention-Vorgabe.
   * Startet den Job in einer eigenen Prozessgruppe (`setsid`).

2. **`run_job.sh`** (Schlanker Starter für JS7 / Cron):
   * Führt das jeweils aktive Release direkt lokal aus:
     ```bash
     /opt/talend/scripts/run_job.sh <job_name> [optionale talend params...]
     ```

---

## REST-API Übersicht

Alle API-Aufrufe erfordern Authentifizierung via `Authorization: Bearer <TOKEN>` oder HTTP BasicAuth.

| Methode | Endpunkt | Beschreibung |
| :--- | :--- | :--- |
| `GET` | `/healthz` | Health-Check (ohne Auth) |
| `GET` | `/api/v1/jobs` | Liste aller Jobs |
| `POST` | `/api/v1/jobs` | Neuen Job anlegen (Admin) |
| `POST` | `/api/v1/jobs/{id}/deploy` | Version auf Zielserver installieren |
| `POST` | `/api/v1/jobs/{id}/run` | Job starten (optional mit `?wait=true`) |
| `GET` | `/api/v1/jobs/{id}/artifact` | Nexus Download-URL und Metadaten |
| `GET` | `/api/v1/executions` | Ausführungshistorie |
| `GET` | `/api/v1/executions/{id}/logs` | Logs als Plain-Text oder SSE |
| `POST` | `/api/v1/executions/{id}/abort` | Laufende Ausführung abbrechen |
| `POST` | `/api/v1/servers/{id}/test` | SSH-Verbindung zum Zielserver prüfen |

### Beispiel: Jenkins Pipeline Aufruf (Blocking)
```bash
curl -X POST "http://jobcon:8080/api/v1/jobs/sync_sap/run?wait=true" \
     -H "Authorization: Bearer ${JOBCON_TOKEN}" \
     -H "Content-Type: application/json" \
     -d '{"context": "Production", "params": {"batchSize": "1000"}}'
```

---

## Linux Systemd-Installation

1. Binary kopieren:
   ```bash
   sudo cp jobcon /usr/local/bin/
   sudo chmod +x /usr/local/bin/jobcon
   ```
2. Konfiguration anlegen:
   ```bash
   sudo mkdir -p /etc/jobcon /var/lib/jobcon/data /var/lib/jobcon/logs
   sudo cp config.example.yaml /etc/jobcon/config.yaml
   ```
3. Benutzer und Rechte:
   ```bash
   sudo useradd -r -s /bin/false jobcon
   sudo chown -R jobcon:jobcon /var/lib/jobcon /etc/jobcon
   ```
4. Service-Unit installieren:
   ```bash
   sudo cp packaging/jobcon.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now jobcon
   ```
