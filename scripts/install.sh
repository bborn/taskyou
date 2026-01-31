#!/bin/bash
# TaskYou Installation Script
# Usage: curl -fsSL taskyou.dev/install.sh | bash
#
# This script downloads and installs the 'ty' CLI tool.
# It detects your OS and architecture automatically.

set -e

REPO="bborn/taskyou"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="ty"

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
    version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$version" ]; then
        error "Failed to fetch latest version. Check your internet connection or try again later."
    fi
    echo "$version"
}

# Download and install the binary
install_binary() {
    local os=$1
    local arch=$2
    local version=$3

    local filename="ty-${os}-${arch}"
    local url="https://github.com/${REPO}/releases/download/${version}/${filename}"

    info "Downloading ${BINARY_NAME} ${version} for ${os}/${arch}..."

    # Create temp directory
    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap "rm -rf $tmp_dir" EXIT

    # Download binary
    if ! curl -fsSL "$url" -o "${tmp_dir}/${BINARY_NAME}"; then
        error "Failed to download from ${url}"
    fi

    chmod +x "${tmp_dir}/${BINARY_NAME}"

    # Install binary
    info "Installing to ${INSTALL_DIR}/${BINARY_NAME}..."

    # Create directory if it doesn't exist
    if [ ! -d "$INSTALL_DIR" ]; then
        mkdir -p "$INSTALL_DIR" 2>/dev/null || sudo mkdir -p "$INSTALL_DIR"
    fi

    if [ -w "$INSTALL_DIR" ]; then
        mv "${tmp_dir}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        ln -sf "${BINARY_NAME}" "${INSTALL_DIR}/taskyou"
    else
        warn "${INSTALL_DIR} is not writable. Using sudo..."
        sudo mv "${tmp_dir}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        sudo ln -sf "${BINARY_NAME}" "${INSTALL_DIR}/taskyou"
    fi

    info "Successfully installed ${BINARY_NAME} ${version} to ${INSTALL_DIR}/${BINARY_NAME}"
    info "Also available as 'taskyou' (symlink)"
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

# Main installation
main() {
    echo ""
    echo "  TaskYou Installer"
    echo "  =================="
    echo ""

    local os arch version

    os=$(detect_os)
    arch=$(detect_arch)

    info "Detected: ${os}/${arch}"

    version=$(get_latest_version)
    info "Latest version: ${version}"

    install_binary "$os" "$arch" "$version"
    check_path

    echo ""
    info "Run 'ty --help' or 'taskyou --help' to get started!"
    echo ""
}

main
