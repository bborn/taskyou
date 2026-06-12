#!/bin/bash
# TaskYou Desktop (macOS) Installation Script
# Usage: curl -fsSL taskyou.dev/install-macos.sh | bash
#
# Downloads the TaskYou desktop app DMG from the latest GitHub release,
# verifies it, and installs TaskYou.app. Because curl downloads never get
# the com.apple.quarantine flag, this skips Gatekeeper's "unidentified
# developer" prompt entirely — no right-click → Open dance needed.
#
# Installs to ~/Applications by default (no sudo required).
#
# Environment variables:
#   TASKYOU_INSTALL_SYSTEM=1   Install to /Applications for all users (uses sudo)
#   TASKYOU_NO_LAUNCH=1        Don't launch the app after installing
#   TASKYOU_GITHUB_REPO        Override the GitHub repo (default: bborn/taskyou)

set -euo pipefail

REPO="${TASKYOU_GITHUB_REPO:-bborn/taskyou}"
APP_NAME="TaskYou.app"
SYSTEM_APP="/Applications/${APP_NAME}"
USER_APP="${HOME}/Applications/${APP_NAME}"

# Colors for output (only when attached to a terminal)
RED=""
GREEN=""
YELLOW=""
BOLD=""
NC=""
if [ -t 1 ]; then
    RED=$'\033[0;31m'
    GREEN=$'\033[0;32m'
    YELLOW=$'\033[1;33m'
    BOLD=$'\033[1m'
    NC=$'\033[0m'
fi

banner() {
    printf '%s' "${BOLD}"
    cat <<'EOF'

 ████████╗ █████╗ ███████╗██╗  ██╗██╗   ██╗ ██████╗ ██╗   ██╗
 ╚══██╔══╝██╔══██╗██╔════╝██║ ██╔╝╚██╗ ██╔╝██╔═══██╗██║   ██║
    ██║   ███████║███████╗█████╔╝  ╚████╔╝ ██║   ██║██║   ██║
    ██║   ██╔══██║╚════██║██╔═██╗   ╚██╔╝  ██║   ██║██║   ██║
    ██║   ██║  ██║███████║██║  ██╗   ██║   ╚██████╔╝╚██████╔╝
    ╚═╝   ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝   ╚═╝    ╚═════╝  ╚═════╝

EOF
    printf '%s' "${NC}"
    printf '%sTaskYou · macOS desktop installer%s\n\n' "${BOLD}" "${NC}"
}

step() {
    printf '%s✓%s %s\n' "${GREEN}" "${NC}" "$1"
}

info() {
    printf '%s==>%s %s\n' "${GREEN}" "${NC}" "$1"
}

warn() {
    printf '%sWarning:%s %s\n' "${YELLOW}" "${NC}" "$1" >&2
}

error() {
    printf '%sError:%s %s\n' "${RED}" "${NC}" "$1" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || error "Required command not found: $1"
}

# Map the machine architecture to a release DMG asset.
# TaskYou Desktop is Apple Silicon only.
detect_asset() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        arm64) echo "TaskYou-macos-arm64.dmg" ;;
        x86_64) error "TaskYou Desktop is Apple Silicon only — there is no Intel (x86_64) build. Run the CLI instead: curl -fsSL taskyou.dev/install.sh | bash" ;;
        *) error "Unsupported architecture: ${arch}. TaskYou Desktop requires an Apple Silicon Mac." ;;
    esac
}

# Globals for the EXIT trap (locals would be gone by the time it fires)
TMP_DIR=""
MOUNT_POINT=""
cleanup() {
    if [ -n "${MOUNT_POINT}" ] && [ -d "${MOUNT_POINT}" ]; then
        hdiutil detach "${MOUNT_POINT}" >/dev/null 2>&1 || true
    fi
    if [ -n "${TMP_DIR}" ]; then
        rm -rf "${TMP_DIR}"
    fi
}
trap cleanup EXIT INT TERM

main() {
    banner

    if [ "$(uname -s)" != "Darwin" ]; then
        error "This installer is macOS-only. For the CLI/TUI (macOS and Linux): curl -fsSL taskyou.dev/install.sh | bash"
    fi

    require_command curl
    require_command hdiutil
    require_command ditto
    require_command xattr
    require_command open

    local asset
    asset=$(detect_asset)
    step "Detected macOS $(uname -m) → ${asset}"

    # Pick the install destination
    local dest other_copy sudo_cmd=""
    if [ -n "${TASKYOU_INSTALL_SYSTEM:-}" ]; then
        dest="${SYSTEM_APP}"
        other_copy="${USER_APP}"
        if [ "$(id -u)" -ne 0 ]; then
            command -v sudo >/dev/null 2>&1 || error "sudo is required to install into /Applications. Unset TASKYOU_INSTALL_SYSTEM to install to ~/Applications without a password."
            sudo_cmd="sudo"
        fi
    else
        dest="${USER_APP}"
        other_copy="${SYSTEM_APP}"
    fi

    # Warn if a copy already lives in the *other* location — macOS may keep
    # launching that one instead of the copy we are about to install.
    if [ -e "${other_copy}" ]; then
        echo "" >&2
        warn "TaskYou is already installed at: ${other_copy}"
        printf '  Remove that copy to avoid two installs (macOS may launch it instead of this one):\n' >&2
        if [ "${other_copy}" = "${SYSTEM_APP}" ]; then
            printf "    sudo rm -rf '%s'\n\n" "${other_copy}" >&2
        else
            printf "    rm -rf '%s'\n\n" "${other_copy}" >&2
        fi
    fi

    TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/taskyou-macos-install.XXXXXX")
    MOUNT_POINT="${TMP_DIR}/volume"
    local dmg_path="${TMP_DIR}/${asset}"
    local url="https://github.com/${REPO}/releases/latest/download/${asset}"

    info "Downloading ${asset}..."
    if ! curl -fL --retry 3 --retry-delay 2 --progress-bar -o "${dmg_path}" "${url}"; then
        error "Download failed. ${asset} may not be attached to the latest release for this architecture — check https://github.com/${REPO}/releases/latest"
    fi
    step "Download complete"

    info "Verifying disk image..."
    hdiutil verify -quiet "${dmg_path}" || error "The downloaded DMG failed verification (corrupt or incomplete download). Please retry."
    step "Disk image verified"

    info "Mounting disk image..."
    mkdir -p "${MOUNT_POINT}"
    hdiutil attach -nobrowse -readonly -mountpoint "${MOUNT_POINT}" "${dmg_path}" >/dev/null || error "Could not mount the disk image."
    step "Disk image mounted"

    local app_src
    app_src=$(find "${MOUNT_POINT}" -maxdepth 3 -name "${APP_NAME}" -type d | head -n 1)
    if [ -z "${app_src}" ]; then
        error "${APP_NAME} was not found inside the disk image (unexpected layout)."
    fi
    step "Found ${APP_NAME} in disk image"

    if [ "${dest}" = "${SYSTEM_APP}" ]; then
        info "Installing to /Applications (admin password may be required)..."
    else
        info "Installing to ~/Applications..."
        mkdir -p "${HOME}/Applications"
    fi
    if [ -e "${dest}" ]; then
        ${sudo_cmd} rm -rf "${dest}"
    fi
    ${sudo_cmd} ditto "${app_src}" "${dest}" || error "Failed to copy ${APP_NAME} to ${dest}"
    step "Installed to ${dest}"

    hdiutil detach "${MOUNT_POINT}" >/dev/null 2>&1 || true
    MOUNT_POINT=""
    step "Disk image ejected"

    # curl downloads are never quarantined, but clear the flag just in case
    # (e.g. the script itself was saved via a browser first).
    ${sudo_cmd} xattr -dr com.apple.quarantine "${dest}" 2>/dev/null || true
    step "Gatekeeper quarantine cleared"

    if [ -n "${TASKYOU_NO_LAUNCH:-}" ]; then
        step "Skipping launch (TASKYOU_NO_LAUNCH is set)"
    else
        info "Launching TaskYou..."
        open "${dest}" || error "TaskYou is installed at ${dest} but failed to launch — try opening it from your Applications folder."
        step "TaskYou started"
    fi

    echo ""
    info "All set! Reminder: TaskYou needs tmux and at least one executor CLI (e.g. Claude Code) installed to run tasks."
    echo ""
}

main "$@"
