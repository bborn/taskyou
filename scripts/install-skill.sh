#!/bin/bash
# Install the Task You skill for Claude Code (personal/global installation)
#
# The skill is already available when working inside the Task You project
# (at skills/taskyou/SKILL.md). This script installs it globally
# so you can use /taskyou from any project.
#
# Usage:
#   ./scripts/install-skill.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
SKILL_SOURCE="$PROJECT_DIR/skills/taskyou"
SKILL_TARGET="$HOME/.claude/skills/taskyou"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo "Installing Task You skill for Claude Code..."

# Check if skill source exists
if [ ! -d "$SKILL_SOURCE" ]; then
    echo -e "${RED}Error: Skill source not found at $SKILL_SOURCE${NC}"
    exit 1
fi

# Create skills directory if it doesn't exist
mkdir -p "$HOME/.claude/skills"

# Remove existing symlink or directory
if [ -L "$SKILL_TARGET" ]; then
    echo -e "${YELLOW}Removing existing symlink...${NC}"
    rm "$SKILL_TARGET"
elif [ -d "$SKILL_TARGET" ]; then
    echo -e "${YELLOW}Removing existing directory...${NC}"
    rm -rf "$SKILL_TARGET"
fi

# Create symlink
ln -s "$SKILL_SOURCE" "$SKILL_TARGET"

echo -e "${GREEN}Success!${NC} Task You skill installed globally."
echo ""
echo -e "${BLUE}Usage:${NC}"
echo "  Type /taskyou in Claude Code to invoke the skill"
echo "  Or ask Claude to 'manage my task queue' and it will use the skill automatically"
echo ""
echo -e "${BLUE}CLI commands:${NC}"
echo "  Use 'ty' or 'taskyou' to interact with Task You from the command line"
echo ""
echo -e "${BLUE}Note:${NC}"
echo "  The skill is also available automatically when working inside the Task You project."
echo "  This global install lets you use /taskyou from any directory."
