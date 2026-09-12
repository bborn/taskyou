#!/bin/bash
# TaskYou Installation Script
# Usage: curl -fsSL taskyou.dev/install.sh | bash
#
# This script downloads and installs the 'ty' CLI tool and optionally the 'taskd' SSH server.
# It detects your OS and architecture automatically.
#
# Options:
#   --no-ssh-server    Skip installing taskd (the SSH server daemon)
#   --no-restart       Leave a running ty on its old version (see restart_running_ty)
#
# Environment variables:
#   INSTALL_DIR        Installation directory (default: ~/.local/bin)

set -e

REPO="bborn/taskyou"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
INSTALL_TASKD=true
RESTART_TY=true

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    echo -e "${GREEN}==>${NC} $1"
}

warn() {
    echo -e "${YELLOW}Warning:${NC} $1"
}

error() {
    echo -e "${RED}Error:${NC} $1" >&2
    exit 1
}

# Parse arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --no-ssh-server)
                INSTALL_TASKD=false
                shift
                ;;
            --no-restart)
                RESTART_TY=false
                shift
                ;;
            --help|-h)
                echo "TaskYou Installation Script"
                echo ""
                echo "Usage: curl -fsSL taskyou.dev/install.sh | bash [-s -- OPTIONS]"
                echo "   or: ./install.sh [OPTIONS]"
                echo ""
                echo "Options:"
                echo "  --no-ssh-server    Skip installing taskd (the SSH server daemon)"
                echo "  --no-restart       Leave a running ty on its old version"
                echo "  --help, -h         Show this help message"
                echo ""
                echo "Environment variables:"
                echo "  INSTALL_DIR        Installation directory (default: ~/.local/bin)"
                exit 0
                ;;
            *)
                warn "Unknown option: $1"
                shift
                ;;
        esac
    done
}

# Detect OS
detect_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        darwin) echo "darwin" ;;
        linux) echo "linux" ;;
        *) error "Unsupported operating system: $os" ;;
    esac
}

# Detect architecture
detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) error "Unsupported architecture: $arch" ;;
    esac
}

# Get the latest release version from GitHub
get_latest_version() {
    local version
    # Extract version from the /releases/latest redirect URL (no API rate limits)
    version=$(curl -fsSI "https://github.com/${REPO}/releases/latest" 2>/dev/null | grep -i '^location:' | sed -E 's|.*/tag/||' | tr -d '[:space:]')
    if [ -z "$version" ]; then
        error "Failed to fetch latest version. Check your internet connection or try again later."
    fi
    echo "$version"
}

# Create install directory if needed
ensure_install_dir() {
    if [ ! -d "$INSTALL_DIR" ]; then
        mkdir -p "$INSTALL_DIR" 2>/dev/null || sudo mkdir -p "$INSTALL_DIR"
    fi
}

# Install a file to INSTALL_DIR (handles sudo if needed)
install_file() {
    local src=$1
    local dest_name=$2

    if [ -w "$INSTALL_DIR" ]; then
        mv "$src" "${INSTALL_DIR}/${dest_name}"
    else
        warn "${INSTALL_DIR} is not writable. Using sudo..."
        sudo mv "$src" "${INSTALL_DIR}/${dest_name}"
    fi
}

# Create a symlink in INSTALL_DIR (handles sudo if needed)
create_symlink() {
    local target=$1
    local link_name=$2

    if [ -w "$INSTALL_DIR" ]; then
        ln -sf "$target" "${INSTALL_DIR}/${link_name}"
    else
        sudo ln -sf "$target" "${INSTALL_DIR}/${link_name}"
    fi
}

# Download and install the ty CLI
install_ty() {
    local os=$1
    local arch=$2
    local version=$3
    local tmp_dir=$4

    local filename="ty_${version#v}_${os}_${arch}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${version}/${filename}"

    info "Downloading ty ${version} for ${os}/${arch}..."

    # Download and extract tarball
    if ! curl -fsSL "$url" -o "${tmp_dir}/ty.tar.gz"; then
        error "Failed to download from ${url}"
    fi

    tar -xzf "${tmp_dir}/ty.tar.gz" -C "${tmp_dir}"
    chmod +x "${tmp_dir}/ty"

    # Install binary
    info "Installing to ${INSTALL_DIR}/ty..."
    install_file "${tmp_dir}/ty" "ty"
    create_symlink "ty" "taskyou"

    info "Successfully installed ty ${version}"
    info "Also available as 'taskyou' (symlink)"
}

# Download and install the taskd SSH server (Linux only)
install_taskd() {
    local os=$1
    local arch=$2
    local version=$3
    local tmp_dir=$4

    # taskd is only available on Linux
    if [ "$os" != "linux" ]; then
        info "Skipping taskd (SSH server) - only available on Linux"
        return 0
    fi

    local filename="taskd_${version#v}_${os}_${arch}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${version}/${filename}"

    info "Downloading taskd ${version} for ${os}/${arch}..."

    # Download and extract tarball
    if ! curl -fsSL "$url" -o "${tmp_dir}/taskd.tar.gz"; then
        error "Failed to download taskd from ${url}"
    fi

    tar -xzf "${tmp_dir}/taskd.tar.gz" -C "${tmp_dir}"
    chmod +x "${tmp_dir}/taskd"

    # Install binary
    info "Installing to ${INSTALL_DIR}/taskd..."
    install_file "${tmp_dir}/taskd" "taskd"

    info "Successfully installed taskd ${version}"
}

# Check if install directory is in PATH
check_path() {
    if [[ ":$PATH:" != *":${INSTALL_DIR}:"* ]]; then
        warn "${INSTALL_DIR} is not in your PATH"
        echo ""
        echo "Add it to your shell profile:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
        echo ""
    fi
}

# Switch a running ty to the version just installed. `ty restart` restarts the
# daemon and asks every open TUI to reload the new binary in place, with agent
# sessions left running. Without it, an upgrade leaves the old daemon and TUIs
# running the old code next to any new one until someone restarts by hand.
#
# Only when a ty daemon is running: `ty restart` starts one, and a fresh install
# (or a machine where ty is not in use) should not end up with a daemon it never
# asked for. Only a ty whose restart reloads TUIs in place (its help says
# "reload"); an older one re-launched the TUI itself, which would take over this
# installer's terminal.
restart_running_ty() {
    local version=$1
    local bin="${INSTALL_DIR}/ty"

    if [ "$RESTART_TY" != true ]; then
        info "Not restarting ty (--no-restart). Run 'ty restart' to switch a running ty to ${version}."
        return 0
    fi
    if ! "$bin" daemon status 2>/dev/null | grep -q "Daemon running"; then
        return 0
    fi
    if ! "$bin" restart --help 2>/dev/null | grep -qi "reload"; then
        warn "This ty cannot restart in place. Run 'ty restart' to switch to ${version}."
        return 0
    fi
    info "Restarting ty on ${version}: the daemon restarts and open TUIs reload in place. Agents keep running."
    if ! "$bin" restart; then
        warn "Restart failed. Run 'ty restart' to switch the running ty to ${version}."
    fi
}

# Main installation
main() {
    # Parse command line arguments
    parse_args "$@"

    echo ""
    echo "  TaskYou Installer"
    echo "  =================="
    echo ""

    local os arch version tmp_dir

    os=$(detect_os)
    arch=$(detect_arch)

    info "Detected: ${os}/${arch}"

    version=$(get_latest_version)
    info "Latest version: ${version}"

    # Create temp directory for downloads
    tmp_dir=$(mktemp -d)
    trap "rm -rf $tmp_dir" EXIT

    # Ensure install directory exists
    ensure_install_dir

    # Install ty CLI
    install_ty "$os" "$arch" "$version" "$tmp_dir"

    # Install taskd SSH server (unless --no-ssh-server was specified)
    if [ "$INSTALL_TASKD" = true ]; then
        install_taskd "$os" "$arch" "$version" "$tmp_dir"
    else
        info "Skipping taskd installation (--no-ssh-server)"
    fi

    restart_running_ty "$version"

    check_path

    echo ""
    info "Run 'ty' to get started!"
    if [ "$INSTALL_TASKD" = true ] && [ "$os" = "linux" ]; then
        info "Run 'taskd' to start the SSH server"
    fi
    echo ""
}

main "$@"
