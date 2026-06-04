#!/usr/bin/env bash
# Stand up an isolated ty instance: build the binary, create a throwaway DB,
# and register a git-backed project. Does NOT start the daemon (see README).
#
# Usage: ty-qa-up.sh [project-name]   (default: qa)
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PROJECT="${1:-qa}"
PROJECT_PATH="$TY_QA_PROJECTS/$PROJECT"

echo "==> Building ty -> $TY_BIN"
mkdir -p "$TY_QA_ROOT"
( cd "$TY_REPO_ROOT" && go build -o "$TY_BIN" ./cmd/task )

echo "==> Fresh isolated DB at $WORKTREE_DB_PATH"
rm -f "$WORKTREE_DB_PATH"

echo "==> projects_dir -> $TY_QA_PROJECTS"
mkdir -p "$TY_QA_PROJECTS"
ty settings set projects_dir "$TY_QA_PROJECTS" >/dev/null

if [[ ! -d "$PROJECT_PATH/.git" ]]; then
  echo "==> Creating git project '$PROJECT' at $PROJECT_PATH"
  mkdir -p "$PROJECT_PATH"
  git -C "$PROJECT_PATH" init -q
  git -C "$PROJECT_PATH" config user.email qa@ty.local
  git -C "$PROJECT_PATH" config user.name "ty qa"
  echo "# $PROJECT" > "$PROJECT_PATH/README.md"
  git -C "$PROJECT_PATH" add -A
  git -C "$PROJECT_PATH" commit -qm init
fi
# Reuse your authed Claude config so agents (if you spawn any) don't need re-login.
ty projects create "$PROJECT" --path "$PROJECT_PATH" --claude-config-dir "$HOME/.claude" >/dev/null 2>&1 \
  || echo "    (project '$PROJECT' already registered)"

cat <<EOF

==> Isolated ty instance ready
    binary   : $TY_BIN
    db       : $WORKTREE_DB_PATH
    project  : $PROJECT ($PROJECT_PATH)
    sessions : $TY_UI_SESSION (tui)  /  $TY_DAEMON_SESSION (agents)

Next:
    scripts/qa/ty-qa-tui.sh          # launch the TUI
    scripts/qa/ty-qa-key.sh n        # send keystrokes (here: open new-task form)
    scripts/qa/ty-qa-state.sh        # read TUI state (no screen-scraping)
    scripts/qa/ty-qa-down.sh         # tear down
EOF
