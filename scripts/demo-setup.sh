#!/bin/bash
# Setup and run TaskYou demo on a fresh machine.
#
# Prerequisites: go, tmux
# Optional: vhs (brew install vhs) for automated recording
#
# Usage:
#   ./scripts/demo-setup.sh          # seed + launch demo
#   ./scripts/demo-setup.sh record   # seed + record with VHS

set -e

cd "$(dirname "$0")/.."

echo "==> Building TaskYou..."
make build

echo "==> Seeding demo database..."
go run ./cmd/demoseed

export WORKTREE_DB_PATH=~/.local/share/task/demo.db

if [ "$1" = "record" ]; then
    if ! command -v vhs &>/dev/null; then
        echo "Error: vhs not installed. Run: brew install vhs"
        exit 1
    fi
    echo "==> Recording demo with VHS..."
    vhs demo.tape
    echo "==> Done! Output: demo.gif, demo.mp4"
else
    echo "==> Launching TaskYou with demo database..."
    echo "    Database: $WORKTREE_DB_PATH"
    echo ""
    ./bin/task -l
fi
