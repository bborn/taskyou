#!/bin/bash
# Install the TaskYou skill for Claude Code
#
# Two modes:
#   1. Local: Run from within the TaskYou repo to symlink the skill globally
#   2. Remote: Download the skill directly from GitHub (no repo needed)
#
# Usage:
#   From repo:  ./scripts/install-skill.sh
#   Remote:     curl -fsSL raw.githubusercontent.com/bborn/taskyou/main/scripts/install-skill.sh | bash

set -e

REPO="bborn/taskyou"
BRANCH="main"
SKILL_TARGET="$HOME/.claude/skills/taskyou"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info() { echo -e "${GREEN}==>${NC} $1"; }
warn() { echo -e "${YELLOW}Warning:${NC} $1"; }
error() { echo -e "${RED}Error:${NC} $1" >&2; exit 1; }

echo ""
echo "  TaskYou Skill Installer"
echo "  ========================"
echo ""

# Create skills directory
mkdir -p "$HOME/.claude/skills"

# Remove existing
if [ -L "$SKILL_TARGET" ]; then
    rm "$SKILL_TARGET"
elif [ -d "$SKILL_TARGET" ]; then
    rm -rf "$SKILL_TARGET"
fi

# Check if running from within the repo
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}" 2>/dev/null)" && pwd 2>/dev/null)" || SCRIPT_DIR=""
PROJECT_DIR="$(dirname "$SCRIPT_DIR" 2>/dev/null)" || PROJECT_DIR=""
SKILL_SOURCE="$PROJECT_DIR/skills/taskyou"

if [ -n "$SCRIPT_DIR" ] && [ -f "$SKILL_SOURCE/SKILL.md" ]; then
    # Local mode: symlink from the repo
    info "Installing from local repo..."
    ln -s "$SKILL_SOURCE" "$SKILL_TARGET"
    info "Symlinked $SKILL_TARGET -> $SKILL_SOURCE"
else
    # Remote mode: download from GitHub
    info "Downloading skill from GitHub..."
    mkdir -p "$SKILL_TARGET"

    SKILL_URL="https://raw.githubusercontent.com/${REPO}/${BRANCH}/skills/taskyou/SKILL.md"
    if ! curl -fsSL "$SKILL_URL" -o "$SKILL_TARGET/SKILL.md"; then
        rm -rf "$SKILL_TARGET"
        error "Failed to download skill from $SKILL_URL"
    fi

    info "Downloaded to $SKILL_TARGET/SKILL.md"
fi

echo ""
echo -e "${GREEN}Success!${NC} TaskYou skill installed for Claude Code."
echo ""
echo -e "${BLUE}Usage:${NC}"
echo "  Type /taskyou in Claude Code to invoke the skill"
echo "  Or ask Claude to 'manage my task queue' — it will use the skill automatically"
echo ""

# Check if ty is installed
if command -v ty &>/dev/null; then
    echo -e "${BLUE}TaskYou CLI:${NC} $(ty --version 2>/dev/null || echo 'installed')"
else
    echo -e "${YELLOW}TaskYou CLI not found.${NC} The skill will guide installation when invoked."
    echo "  Or install now: curl -fsSL taskyou.dev/install.sh | bash"
fi
echo ""
