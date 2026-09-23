#!/usr/bin/env bash
# ==============================================================================
# JobCon Build & Distribution Packaging Script
# ==============================================================================
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_DIR="${REPO_DIR}/target"
VERSION=$(git describe --tags --always 2>/dev/null || echo "1.0.0")
ARCH="${GOARCH:-amd64}"
OS="${GOOS:-linux}"

echo "================================================================="
echo "⚡ Building JobCon Release (OS: ${OS}, Arch: ${ARCH})"
echo "================================================================="

# 1. Clean target directory
rm -rf "${TARGET_DIR}"
mkdir -p "${TARGET_DIR}/scripts"
mkdir -p "${TARGET_DIR}/data/logs"

# 2. Compile static binary
echo "[1/5] Compiling static Go binary..."
CGO_ENABLED=0 GOOS="${OS}" GOARCH="${ARCH}" go build \
    -ldflags="-s -w" \
    -o "${TARGET_DIR}/jobcon" \
    "${REPO_DIR}/cmd/jobcon"

chmod 0755 "${TARGET_DIR}/jobcon"

# 3. Copy target server scripts
echo "[2/5] Copying target server scripts to target/scripts/..."
if ls "${REPO_DIR}/scripts/"*.sh 1> /dev/null 2>&1; then
    cp "${REPO_DIR}/scripts/"*.sh "${TARGET_DIR}/scripts/"
    chmod 0755 "${TARGET_DIR}/scripts/"*.sh
else
    echo "ERROR: Keine .sh-Skripte in ${REPO_DIR}/scripts/ gefunden!" >&2
    exit 1
fi

# 4. Copy configuration example and systemd service
echo "[3/5] Copying config.example.yaml and jobcon.service..."
cp "${REPO_DIR}/config.example.yaml" "${TARGET_DIR}/config.example.yaml"
cp "${REPO_DIR}/jobcon.service" "${TARGET_DIR}/jobcon.service"
touch "${TARGET_DIR}/data/.gitkeep"
touch "${TARGET_DIR}/data/logs/.gitkeep"

# 5. Create deployment guide
echo "[4/5] Generating deployment readme..."
cat << 'EOF' > "${TARGET_DIR}/README-DEPLOY.md"
# JobCon Deployment Anleitung (Debian / Linux)

Dieses Verzeichnis enthält die vollständige Distributionsstruktur von JobCon.

## Schnell-Installation auf Zielserver:

1. Verzeichnis nach `/opt/jobcon` kopieren:
   ```bash
   sudo mkdir -p /opt/jobcon
   sudo cp -r ./* /opt/jobcon/
   ```

2. Benutzer anlegen & Berechtigungen setzen:
   ```bash
   sudo useradd -r -s /bin/false -d /opt/jobcon jobcon || true
   sudo chown -R jobcon:jobcon /opt/jobcon
   sudo chmod 0755 /opt/jobcon/jobcon
   sudo chmod 0755 /opt/jobcon/scripts/*.sh
   ```

3. Konfiguration anlegen:
   ```bash
   sudo cp /opt/jobcon/config.example.yaml /opt/jobcon/config.yaml
   sudo nano /opt/jobcon/config.yaml
   ```

4. Systemd-Service aktivieren und starten:
   ```bash
   sudo cp /opt/jobcon/jobcon.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now jobcon
   sudo systemctl status jobcon
   ```
EOF

# 6. Optional package archive (tar.gz & zip)
echo "[5/5] Creating release archives..."
(
    cd "${TARGET_DIR}"
    tar -czf "jobcon-${OS}-${ARCH}.tar.gz" \
        jobcon \
        config.example.yaml \
        jobcon.service \
        scripts \
        data \
        README-DEPLOY.md
)

if command -v zip &> /dev/null; then
    (
        cd "${TARGET_DIR}"
        zip -q -r "jobcon-${OS}-${ARCH}.zip" \
            jobcon \
            config.example.yaml \
            jobcon.service \
            scripts \
            data \
            README-DEPLOY.md
    )
fi

echo "================================================================="
echo "✅ Build completed successfully!"
echo "   Output directory: ${TARGET_DIR}"
echo "   Binary:           ${TARGET_DIR}/jobcon"
echo "   Server Scripts:   ${TARGET_DIR}/scripts/"
echo "   Archive:          ${TARGET_DIR}/jobcon-${OS}-${ARCH}.tar.gz"
echo "================================================================="
