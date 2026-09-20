#!/usr/bin/env bash
# ==============================================================================
# JobCon Universal Target Controller (jobcon_ctl.sh)
# Manages download, atomic deployment, version retention, and execution of Talend jobs.
# ==============================================================================

set -euo pipefail

BASE_DIR="/opt/talend"
COMMAND=""
JOB_NAME=""
JOB_VERSION=""
NEXUS_URL=""
NEXUS_USER=""
NEXUS_PASS=""
CONTEXT="Default"
KEEP_RELEASES=3
EXTRA_PARAMS=()

usage() {
    cat <<EOF
Usage: $(basename "$0") <deploy|run> [options]

Commands:
  deploy    Download and unpack Talend job version, update current symlink
  run       Execute Talend job (deploys first if version specified & missing)

Options:
  --job <name>             Name/Artifact-ID of the Talend job (required)
  --version <version>      Job version (e.g. 1.4.2)
  --nexus-url <url>        Full download URL of the job ZIP in Nexus
  --nexus-user <user>      Nexus username (optional)
  --nexus-pass <pass>      Nexus password (optional)
  --context <context>      Talend context to run (default: Default)
  --base-dir <dir>         Talend base directory (default: /opt/talend)
  --keep <num>             Number of release versions to retain (default: 3)
  --params <args...>       Parameters forwarded to Talend job (e.g. --context_param key=val)
  -h, --help               Show this help message
EOF
    exit 1
}

# Parse command
if [[ $# -eq 0 ]]; then
    usage
fi

COMMAND="$1"
shift

# Parse options
while [[ $# -gt 0 ]]; do
    case "$1" in
        --job)
            JOB_NAME="$2"
            shift 2
            ;;
        --version)
            JOB_VERSION="$2"
            shift 2
            ;;
        --nexus-url)
            NEXUS_URL="$2"
            shift 2
            ;;
        --nexus-user)
            NEXUS_USER="$2"
            shift 2
            ;;
        --nexus-pass)
            NEXUS_PASS="$2"
            shift 2
            ;;
        --context)
            CONTEXT="$2"
            shift 2
            ;;
        --base-dir)
            BASE_DIR="$2"
            shift 2
            ;;
        --keep)
            KEEP_RELEASES="$2"
            shift 2
            ;;
        --params)
            shift
            while [[ $# -gt 0 ]]; do
                EXTRA_PARAMS+=("$1")
                shift
            done
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage
            ;;
    esac
done

if [[ -z "$JOB_NAME" ]]; then
    echo "ERROR: --job is required." >&2
    exit 1
fi

JOB_ROOT="${BASE_DIR}/jobs/${JOB_NAME}"
RELEASES_DIR="${JOB_ROOT}/releases"
CURRENT_LINK="${JOB_ROOT}/current"

# Helper function to find the Talend run script in a directory
find_run_script() {
    local search_dir="$1"
    # Case 1: search_dir/job_name/job_name_run.sh
    if [[ -f "${search_dir}/${JOB_NAME}/${JOB_NAME}_run.sh" ]]; then
        echo "${search_dir}/${JOB_NAME}/${JOB_NAME}_run.sh"
        return 0
    fi
    # Case 2: search_dir/job_name_run.sh
    if [[ -f "${search_dir}/${JOB_NAME}_run.sh" ]]; then
        echo "${search_dir}/${JOB_NAME}_run.sh"
        return 0
    fi
    # Case 3: Any *_run.sh inside search_dir
    local found
    found=$(find "${search_dir}" -maxdepth 3 -type f -name "*_run.sh" | head -n 1)
    if [[ -n "$found" && -f "$found" ]]; then
        echo "$found"
        return 0
    fi
    return 1
}

# ------------------------------------------------------------------------------
# DEPLOY ACTION
# ------------------------------------------------------------------------------
do_deploy() {
    if [[ -z "$JOB_VERSION" ]]; then
        echo "ERROR: --version is required for deploy." >&2
        exit 1
    fi

    local target_version_dir="${RELEASES_DIR}/${JOB_VERSION}"

    mkdir -p "${RELEASES_DIR}"

    # Check if already installed
    if [[ -d "${target_version_dir}" ]] && find_run_script "${target_version_dir}" >/dev/null 2>&1; then
        echo "[JobCon] Release ${JOB_VERSION} is already installed at ${target_version_dir}."
    else
        if [[ -z "$NEXUS_URL" ]]; then
            echo "ERROR: --nexus-url is required to download missing version ${JOB_VERSION}." >&2
            exit 1
        fi

        local tmp_dir="${RELEASES_DIR}/.tmp_${JOB_VERSION}_$$"
        local zip_file="${tmp_dir}/artifact.zip"

        rm -rf "${tmp_dir}"
        mkdir -p "${tmp_dir}"

        echo "[JobCon] Downloading ${JOB_NAME} v${JOB_VERSION} from ${NEXUS_URL}..."
        local curl_opts=(-fsSL)
        if [[ -n "$NEXUS_USER" && -n "$NEXUS_PASS" ]]; then
            curl_opts+=(-u "${NEXUS_USER}:${NEXUS_PASS}")
        fi

        if ! curl "${curl_opts[@]}" -o "${zip_file}" "${NEXUS_URL}"; then
            echo "ERROR: Failed to download artifact from ${NEXUS_URL}." >&2
            rm -rf "${tmp_dir}"
            exit 10
        fi

        echo "[JobCon] Unpacking artifact..."
        if ! unzip -q -o "${zip_file}" -d "${tmp_dir}"; then
            echo "ERROR: Failed to unzip artifact." >&2
            rm -rf "${tmp_dir}"
            exit 11
        fi
        rm -f "${zip_file}"

        # Ensure all shell scripts are executable
        find "${tmp_dir}" -type f -name "*.sh" -exec chmod +x {} +

        # Validate that a run script exists
        if ! find_run_script "${tmp_dir}" >/dev/null 2>&1; then
            echo "ERROR: No executable Talend run script (*_run.sh) found in unpacked archive." >&2
            rm -rf "${tmp_dir}"
            exit 12
        fi

        # Atomic move to version directory
        rm -rf "${target_version_dir}"
        mv "${tmp_dir}" "${target_version_dir}"
        echo "[JobCon] Version ${JOB_VERSION} successfully installed."
    fi

    # Update current symlink atomically
    echo "[JobCon] Updating symlink: current -> releases/${JOB_VERSION}"
    ln -sfn "releases/${JOB_VERSION}" "${CURRENT_LINK}"

    # Clean up older releases if KEEP_RELEASES > 0
    if [[ "$KEEP_RELEASES" -gt 0 ]]; then
        local installed_count
        installed_count=$(find "${RELEASES_DIR}" -mindepth 1 -maxdepth 1 -type d ! -name ".*" | wc -l)
        if [[ "$installed_count" -gt "$KEEP_RELEASES" ]]; then
            echo "[JobCon] Retaining latest ${KEEP_RELEASES} releases (found ${installed_count})..."
            # Sort directories by modification time (oldest first)
            find "${RELEASES_DIR}" -mindepth 1 -maxdepth 1 -type d ! -name ".*" -printf '%T+ %p\n' \
                | sort \
                | head -n -"${KEEP_RELEASES}" \
                | cut -d' ' -f2- \
                | while read -r old_release; do
                    if [[ "$old_release" != "${target_version_dir}" ]]; then
                        echo "[JobCon] Purging old release: $(basename "$old_release")"
                        rm -rf "$old_release"
                    fi
                done
        fi
    fi

    echo "[JobCon] Deployment of ${JOB_NAME} (${JOB_VERSION}) completed successfully."
}

# ------------------------------------------------------------------------------
# RUN ACTION
# ------------------------------------------------------------------------------
do_run() {
    # If a version is explicitly requested, check if it is active
    if [[ -n "$JOB_VERSION" ]]; then
        local target_version_dir="${RELEASES_DIR}/${JOB_VERSION}"
        local active_link_target=""
        if [[ -L "${CURRENT_LINK}" ]]; then
            active_link_target=$(readlink "${CURRENT_LINK}" || true)
        fi

        if [[ ! -d "${target_version_dir}" || "$active_link_target" != *"releases/${JOB_VERSION}"* ]]; then
            echo "[JobCon] Version ${JOB_VERSION} is not active. Running deploy first..."
            do_deploy
        fi
    fi

    if [[ ! -L "${CURRENT_LINK}" && ! -d "${CURRENT_LINK}" ]]; then
        echo "ERROR: Job '${JOB_NAME}' is not deployed at ${CURRENT_LINK}. Please deploy first." >&2
        exit 20
    fi

    local run_script
    if ! run_script=$(find_run_script "${CURRENT_LINK}"); then
        echo "ERROR: Could not locate runnable Talend script under ${CURRENT_LINK}." >&2
        exit 21
    fi

    echo "[JobCon] ========================================================"
    echo "[JobCon] Starting Talend Job: ${JOB_NAME}"
    echo "[JobCon] Script: ${run_script}"
    echo "[JobCon] Context: ${CONTEXT}"
    if [[ ${#EXTRA_PARAMS[@]} -gt 0 ]]; then
        echo "[JobCon] Parameters: ${EXTRA_PARAMS[*]}"
    fi
    echo "[JobCon] Timestamp: $(date -Iseconds)"
    echo "[JobCon] ========================================================"

    # Execute in its own process group via setsid so child Java processes can be killed as a group
    # Talend expects context as: --context=<name>
    local cmd_args=("--context=${CONTEXT}")
    if [[ ${#EXTRA_PARAMS[@]} -gt 0 ]]; then
        cmd_args+=("${EXTRA_PARAMS[@]}")
    fi

    # Run and forward exit code
    setsid "$run_script" "${cmd_args[@]}"
    local exit_code=$?

    echo "[JobCon] ========================================================"
    echo "[JobCon] Job '${JOB_NAME}' finished with exit code ${exit_code} at $(date -Iseconds)"
    echo "[JobCon] ========================================================"

    exit $exit_code
}

case "$COMMAND" in
    deploy)
        do_deploy
        ;;
    run)
        do_run
        ;;
    *)
        echo "ERROR: Unknown command '${COMMAND}'. Use 'deploy' or 'run'." >&2
        usage
        ;;
esac
