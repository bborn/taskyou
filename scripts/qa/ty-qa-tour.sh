#!/usr/bin/env bash
# Record the real TUI and executor in an isolated, disposable storefront. Never publishes.
set -euo pipefail
export TY_QA_ROOT="${TY_QA_ROOT:-/tmp/ty-product-tour}"
export TY_QA_SID="${TY_QA_SID:-product-tour}"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
OUT="${1:-$TY_QA_ROOT/tour.mp4}"
mkdir -p "$(dirname "$OUT")"
OUT="$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")"
command -v vhs >/dev/null
"$TY_QA_DIR/ty-qa-up.sh" storefront
"$TY_QA_DIR/ty-qa-seed.sh"
export TY_PLUGINS_DIR="$TY_QA_ROOT/plugins"
export TY_ROUTINES_DIR="$TY_QA_ROOT/routines"
export TY_ROUTINES_STATE_DIR="$TY_QA_ROOT/routine-state"
export TY_ROUTINES_LAUNCHD_DIR="$TY_QA_ROOT/launchd"
mkdir -p "$TY_PLUGINS_DIR" "$TY_ROUTINES_DIR" "$TY_ROUTINES_STATE_DIR" "$TY_ROUTINES_LAUNCHD_DIR"
trap '"$TY_QA_DIR/ty-qa-down.sh" >/dev/null' EXIT
# Only the task created on camera may execute.
sqlite3 "$WORKTREE_DB_PATH" <<'SQL'
UPDATE tasks SET status='backlog' WHERE status='queued';
UPDATE tasks SET permission_mode='auto' WHERE project='storefront';
UPDATE projects SET instructions='Work only in this disposable storefront repository. Do not publish, push, open PRs, or send messages. Implement the requested feature and run node --test. No dependencies are needed.' WHERE name='storefront';
UPDATE tasks SET created_at=datetime('now','-2 hours'), updated_at=datetime('now','-25 minutes');
UPDATE tasks SET completed_at=datetime('now','-40 minutes') WHERE status='done';
UPDATE tasks SET body='Use a client-supplied Idempotency-Key header with a 24-hour replay window and a stored response body.' WHERE title LIKE '[research] %';
INSERT OR REPLACE INTO settings(key,value) VALUES('show_advanced','true');
INSERT OR REPLACE INTO settings(key,value) VALUES('autocomplete_enabled','false');
SQL
cp -R "$TY_REPO_ROOT/examples/storefront/." "$TY_QA_PROJECTS/storefront/"
git -C "$TY_QA_PROJECTS/storefront" add .
git -C "$TY_QA_PROJECTS/storefront" commit -qm "Add storefront product page" || true
TY_QA_COLS=150 TY_QA_ROWS=45 "$TY_QA_DIR/ty-qa-tui.sh"
tmux set-option -t "$TY_UI_SESSION" status off
TAPE="$TY_QA_ROOT/tour.tape"
cat > "$TAPE" <<TAPE
Output "$OUT"
Set Shell "bash"
Set Width 3840
Set Height 2160
Set FontSize 40
Set FontFamily "Menlo"
Set Padding 32
Set Theme "Dracula"
Set TypingSpeed 35ms
Set Framerate 30
Env TMUX_TMPDIR "$TMUX_TMPDIR"
Env WORKTREE_DB_PATH "$WORKTREE_DB_PATH"
Env WORKTREE_SESSION_ID "$WORKTREE_SESSION_ID"
Env TY_PLUGINS_DIR "$TY_PLUGINS_DIR"
Env TY_ROUTINES_DIR "$TY_ROUTINES_DIR"
Env TY_ROUTINES_STATE_DIR "$TY_ROUTINES_STATE_DIR"
Env TY_ROUTINES_LAUNCHD_DIR "$TY_ROUTINES_LAUNCHD_DIR"
Hide
Type "tmux attach-session -t $TY_UI_SESSION"
Enter
Sleep 3s
Show
Sleep 5s
Type "/"
Sleep 500ms
Type "storefront"
Sleep 4s
Enter
Sleep 1s
Left 4
Sleep 500ms
Enter
Sleep 6s
Escape
Sleep 1s
Type "/"
Sleep 500ms
Escape
Sleep 1s
Type "n"
Sleep 2s
Type "storefront"
Sleep 1s
Tab
Sleep 500ms
Type "Add a size guide to product pages"
Sleep 1s
Tab
Type "Show measurements in cm and inches. Keep it usable on mobile."
Sleep 3s
Ctrl+S
Sleep 2s
Enter
Sleep 2s
Type "/"
Type "size guide"
Sleep 1s
Enter
Left 4
Sleep 1s
Type "x"
Sleep ${TY_QA_TOUR_EXECUTOR_SECONDS:-240}s

TAPE
vhs validate "$TAPE"
vhs "$TAPE"
python3 - "$WORKTREE_DB_PATH" "$TY_QA_STATE" <<'CHECK'
import json, sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    task = db.execute("select title, body, project, status from tasks order by id desc limit 1").fetchone()
    assert task == ("Add a size guide to product pages", "Show measurements in cm and inches. Keep it usable on mobile.", "storefront", task[3]), task
    assert task[3] in ("processing", "done", "blocked"), task
    assert json.load(open(sys.argv[2]))["detail"]["has_panes"], "Executor panel never became visible"
    assert db.execute("select count(*) from tasks").fetchone()[0] == 23
print("Tour assertions passed: task created correctly and executor pane allocated.")
CHECK
printf 'Recorded %s\nTape: %s\n' "$OUT" "$TAPE"
