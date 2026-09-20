#!/usr/bin/env bash
# ==============================================================================
# JobCon Fast Runner for JS7 / SOS JobScheduler (run_job.sh)
# Executes the currently active version of a Talend job directly without overhead.
# ==============================================================================

set -euo pipefail

BASE_DIR="${TALEND_BASE_DIR:-/opt/talend}"
JOBS_DIR="${TALEND_JOBS_DIR:-${BASE_DIR}/jobs}"

JOB_NAME="${1:-}"
if [[ -z "$JOB_NAME" ]]; then
    echo "ERROR: Kein Job-Name angegeben." >&2
    echo "Aufruf: $(basename "$0") <job_name> [args...]" >&2
    exit 1
fi
shift || true

CURRENT_DIR="${JOBS_DIR}/${JOB_NAME}/current"

if [[ ! -d "$CURRENT_DIR" ]]; then
    echo "ERROR: Job '${JOB_NAME}' ist nicht unter '${CURRENT_DIR}' installiert!" >&2
    echo "Bitte den Job zunächst via JobCon deployen." >&2
    exit 2
fi

# Locate the Talend run script (*_run.sh)
RUN_SCRIPT=""
if [[ -f "${CURRENT_DIR}/${JOB_NAME}/${JOB_NAME}_run.sh" ]]; then
    RUN_SCRIPT="${CURRENT_DIR}/${JOB_NAME}/${JOB_NAME}_run.sh"
elif [[ -f "${CURRENT_DIR}/${JOB_NAME}_run.sh" ]]; then
    RUN_SCRIPT="${CURRENT_DIR}/${JOB_NAME}_run.sh"
else
    RUN_SCRIPT=$(find "${CURRENT_DIR}" -maxdepth 3 -type f -name "*_run.sh" | head -n 1)
fi

if [[ -z "$RUN_SCRIPT" || ! -x "$RUN_SCRIPT" ]]; then
    echo "ERROR: Kein ausführbares Talend-Skript (*_run.sh) in '${CURRENT_DIR}' gefunden!" >&2
    exit 3
fi

# Execute directly, replacing current shell process
exec "$RUN_SCRIPT" "$@"
