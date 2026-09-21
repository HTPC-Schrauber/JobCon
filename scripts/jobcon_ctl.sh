#!/usr/bin/env bash
# ==============================================================================
# JobCon Universal Target Controller (jobcon_ctl.sh)
# Manages download, atomic deployment, version retention, and execution of Talend jobs.
# ==============================================================================

set -euo pipefail

BASE_DIR="/opt/talend"
JOBS_DIR=""
COMMAND=""
JOB_NAME=""
JOB_VERSION=""
NEXUS_URL=""
NEXUS_USER=""
NEXUS_PASS=""
CONTEXT="Default"
KEEP_RELEASES=3
ENV_FILE=""
EXTRA_PARAMS=()

usage() {
    cat <<EOF
Usage: $(basename "$0") <deploy|run|undeploy> [options]

Commands:
  deploy    Download and unpack Talend job version, rotate generation symlinks (current, current-1, ...), purge unlinked releases
  run       Execute Talend job (deploys first if version specified & missing)
  undeploy  Remove job and its releases from target system

Options:
  --job <name>             Name/Artifact-ID of the Talend job (required)
  --version <version>      Job version (e.g. 1.4.2)
  --nexus-url <url>        Full download URL of the job ZIP in Nexus
  --nexus-user <user>      Nexus username (optional)
  --nexus-pass <pass>      Nexus password (optional)
  --context <context>      Talend context to run (default: Default)
  --base-dir <dir>         Talend base directory (default: /opt/talend)
  --jobs-dir <dir>         Talend jobs directory (default: <base-dir>/jobs)
  --env-file <path>        Path to environment file to source before running (.env)
  --keep <num>             Number of release versions to retain via symlinks (default: 3)
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
        --jobs-dir)
            JOBS_DIR="$2"
            shift 2
            ;;
        --keep)
            KEEP_RELEASES="$2"
            shift 2
            ;;
        --env-file)
            ENV_FILE="$2"
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

if [[ -n "$JOBS_DIR" ]]; then
    JOB_ROOT="${JOBS_DIR}/${JOB_NAME}"
else
    JOB_ROOT="${BASE_DIR}/jobs/${JOB_NAME}"
fi
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

    # Gather previously linked releases (excluding the version being deployed now)
    local active_targets=("releases/${JOB_VERSION}")

    for ((i=0; i<KEEP_RELEASES + 5; i++)); do
        local check_link
        if [[ $i -eq 0 ]]; then
            check_link="${CURRENT_LINK}"
        else
            check_link="${JOB_ROOT}/current-${i}"
        fi

        if [[ -L "$check_link" ]]; then
            local raw_target
            raw_target=$(readlink "$check_link" || true)
            local target_basename
            target_basename=$(basename "$raw_target")
            if [[ -n "$target_basename" && -d "${RELEASES_DIR}/${target_basename}" ]]; then
                local rel_target="releases/${target_basename}"
                local duplicate=0
                for t in "${active_targets[@]}"; do
                    if [[ "$t" == "$rel_target" ]]; then
                        duplicate=1
                        break
                    fi
                done
                if [[ $duplicate -eq 0 ]]; then
                    active_targets+=("$rel_target")
                fi
            fi
        fi
    done

    # 1. Update primary 'current' symlink
    echo "[JobCon] Updating symlink: current -> ${active_targets[0]}"
    ln -sfn "${active_targets[0]}" "${CURRENT_LINK}"

    # 2. Update secondary symlinks (current-1, current-2, ...) up to KEEP_RELEASES - 1
    if [[ "$KEEP_RELEASES" -gt 1 ]]; then
        for ((idx=1; idx<KEEP_RELEASES; idx++)); do
            local sec_link="${JOB_ROOT}/current-${idx}"
            if [[ $idx -lt ${#active_targets[@]} ]]; then
                echo "[JobCon] Updating symlink: current-${idx} -> ${active_targets[idx]}"
                ln -sfn "${active_targets[idx]}" "$sec_link"
            else
                rm -f "$sec_link"
            fi
        done
    fi

    # 3. Clean up any stale current-* symlinks beyond KEEP_RELEASES - 1
    for extra_link in "${JOB_ROOT}"/current-*; do
        if [[ -L "$extra_link" || -e "$extra_link" ]]; then
            local suffix="${extra_link##*-}"
            if [[ "$suffix" =~ ^[0-9]+$ ]] && [[ "$suffix" -ge "$KEEP_RELEASES" ]]; then
                echo "[JobCon] Removing stale symlink: $(basename "$extra_link")"
                rm -f "$extra_link"
            elif [[ "$KEEP_RELEASES" -le 1 ]]; then
                echo "[JobCon] Removing stale symlink: $(basename "$extra_link")"
                rm -f "$extra_link"
            fi
        fi
    done

    # 4. Clean up unlinked releases in releases/
    local retained_versions=()
    local max_retained=$(( KEEP_RELEASES < ${#active_targets[@]} ? KEEP_RELEASES : ${#active_targets[@]} ))
    for ((idx=0; idx<max_retained; idx++)); do
        retained_versions+=("$(basename "${active_targets[idx]}")")
    done

    for dir in "${RELEASES_DIR}"/*; do
        if [[ -d "$dir" && ! -L "$dir" ]]; then
            local ver_name
            ver_name=$(basename "$dir")
            if [[ "$ver_name" == .* ]]; then
                continue
            fi
            local keep=0
            for rv in "${retained_versions[@]}"; do
                if [[ "$ver_name" == "$rv" ]]; then
                    keep=1
                    break
                fi
            done
            if [[ $keep -eq 0 ]]; then
                echo "[JobCon] Purging unlinked release: ${ver_name}"
                rm -rf "$dir"
            fi
        fi
    done

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

    # Source environment file if available
    local active_env="$ENV_FILE"
    if [[ -z "$active_env" ]]; then
        if [[ -f "${JOB_ROOT}/.env" ]]; then
            active_env="${JOB_ROOT}/.env"
        elif [[ -f "${BASE_DIR}/.env" ]]; then
            active_env="${BASE_DIR}/.env"
        fi
    fi

    if [[ -n "$active_env" && -f "$active_env" ]]; then
        echo "[JobCon] Sourcing environment file: ${active_env}"
        set -a
        # shellcheck source=/dev/null
        source "$active_env"
        set +a
    fi

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

# ------------------------------------------------------------------------------
# UNDEPLOY ACTION
# ------------------------------------------------------------------------------
do_undeploy() {
    echo "[JobCon] ========================================================"
    echo "[JobCon] Undeploying Talend Job: ${JOB_NAME}"
    echo "[JobCon] Job Root: ${JOB_ROOT}"
    echo "[JobCon] Timestamp: $(date -Iseconds)"
    echo "[JobCon] ========================================================"

    if [[ -d "${JOB_ROOT}" || -L "${JOB_ROOT}" ]]; then
        echo "[JobCon] Removing job directory and all installed releases: ${JOB_ROOT}"
        rm -rf "${JOB_ROOT}"
        echo "[JobCon] Job '${JOB_NAME}' successfully undeployed from execution host."
    else
        echo "[JobCon] Job '${JOB_NAME}' is not installed under ${JOB_ROOT} (already undeployed)."
    fi
}

case "$COMMAND" in
    deploy)
        do_deploy
        ;;
    run)
        do_run
        ;;
    undeploy)
        do_undeploy
        ;;
    *)
        echo "ERROR: Unknown command '${COMMAND}'. Use 'deploy', 'run', or 'undeploy'." >&2
        usage
        ;;
esac
