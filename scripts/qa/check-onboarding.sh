#!/usr/bin/env bash
# Exercise the documented CLI path without agents or the user's database.
set -euo pipefail
TY_CHECK_BIN="${1:?Usage: check-onboarding.sh /absolute/path/to/ty}"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK_ROOT="$(mktemp -d /tmp/ty-onboarding.XXXXXX)"
trap 'rm -rf "$CHECK_ROOT"' EXIT
export WORKTREE_DB_PATH="$CHECK_ROOT/tasks.db"
export WORKTREE_SESSION_ID="onboarding-$$"
export TY_WORKFLOWS_DIR="$CHECK_ROOT/workflows"
export TY_PLUGINS_DIR="$CHECK_ROOT/plugins"
export TMUX_TMPDIR="$CHECK_ROOT/tmux"
unset TMUX
mkdir -p "$TY_WORKFLOWS_DIR" "$TY_PLUGINS_DIR" "$TMUX_TMPDIR"
# CLI commands normally check GitHub for updates. Seed that cache with the
# binary under test so this smoke check stays deterministic and offline.
TY_CHECK_VERSION="$("$TY_CHECK_BIN" --version)"
python3 - "$CHECK_ROOT/version-check.json" "$TY_CHECK_VERSION" <<'PY'
import datetime, json, pathlib, sys
path = pathlib.Path(sys.argv[1])
path.write_text(json.dumps({
    "version": sys.argv[2],
    "url": "",
    "checked_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
}))
PY
unzip -q "$REPO_ROOT/docs/downloads/taskyou-storefront.zip" -d "$CHECK_ROOT"
cd "$CHECK_ROOT/taskyou-storefront"
git init -q
git add .
git -c user.name='Onboarding check' -c user.email='qa@taskyou.local' commit -qm init
"$TY_CHECK_BIN" projects create storefront --path "$PWD" >/dev/null
"$TY_CHECK_BIN" create 'Add a size guide to product pages' --project storefront --executor codex --json > "$CHECK_ROOT/task.json"
TASK_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$CHECK_ROOT/task.json")"
"$TY_CHECK_BIN" show "$TASK_ID" --json > "$CHECK_ROOT/shown.json"
"$TY_CHECK_BIN" list --project storefront --json > "$CHECK_ROOT/list.json"
git init --bare -q "$CHECK_ROOT/origin.git"
git remote add origin "$CHECK_ROOT/origin.git"
git push -q -u origin HEAD
cp "$REPO_ROOT"/docs/workflows/*.yaml "$TY_WORKFLOWS_DIR/"
for recipe in fix-and-review feature-and-review refactor-and-review; do
  "$TY_CHECK_BIN" pipeline 'Add a size guide' -p storefront -d "$recipe" --no-execute --json > "$CHECK_ROOT/$recipe.json"
done
python3 - "$CHECK_ROOT" <<'PY'
import json, pathlib, sqlite3, sys
root=pathlib.Path(sys.argv[1])
task=json.loads((root/'shown.json').read_text())
assert task['title']=='Add a size guide to product pages', task
assert task['status']=='backlog', task
with sqlite3.connect(root/'tasks.db') as db:
    assert db.execute('select count(*) from tasks').fetchone()[0]==12
    assert db.execute("select count(*) from tasks where status in ('queued','processing')").fetchone()[0]==0
print('PASS: practice archive, project registration, create/show/list, three staged workflows (3/5/3 steps), no execution.')
PY
