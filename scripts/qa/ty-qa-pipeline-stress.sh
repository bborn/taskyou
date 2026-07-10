#!/usr/bin/env bash
# Regression test for the class of bug that silently dropped pipeline steps:
# the daemon's auto-complete sweep marking a step 'done' before its agent ever ran.
#
# It reproduces the two conditions our earlier QA missed, and which every real project
# has:
#   1. SLOW WORKTREE INIT — a bin/worktree-setup that takes longer than one sweep tick
#      (a Rails project's clone + bundle + migrate is 15-20s; the sweep ticks every 16s).
#   2. CONCURRENCY — several pipelines fanning out at once, widening the init window.
#
# THE INVARIANT (timing-robust): a step cannot be completed before its session started.
# A 'done' step whose completed_at precedes its session-start log — or which has no
# session at all — was never run. That is a zombie, and it means the DAG advanced past
# work that never happened.
#
# Usage:
#   ty-qa-pipeline-stress.sh [--pipelines N] [--init-delay S] [--binary PATH] [--watch S]
#
# Exits non-zero if any zombie step is found. Tear down with ty-qa-down.sh --purge.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PIPELINES=3
INIT_DELAY=30   # > the 16s sweep tick, so a tick always lands inside worktree init
WATCH=420
BINARY=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pipelines)  PIPELINES="$2"; shift 2 ;;
    --init-delay) INIT_DELAY="$2"; shift 2 ;;
    --binary)     BINARY="$2"; shift 2 ;;
    --watch)      WATCH="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Isolate the workflow definitions too — this test must not depend on (or be broken by)
# whatever the developer happens to have in ~/.config/task/workflows.
export TY_WORKFLOWS_DIR="$TY_QA_ROOT/workflows"
mkdir -p "$TY_WORKFLOWS_DIR"

PROJECT=stress
PROJECT_PATH="$TY_QA_PROJECTS/$PROJECT"

echo "==> Building ty -> $TY_BIN"
mkdir -p "$TY_QA_ROOT"
if [[ -n "$BINARY" ]]; then
  cp "$BINARY" "$TY_BIN"
  echo "    (using supplied binary: $BINARY)"
else
  ( cd "$TY_REPO_ROOT" && go build -buildvcs=false -o "$TY_BIN" ./cmd/task )
fi

echo "==> Fresh isolated DB"
rm -f "$WORKTREE_DB_PATH"
mkdir -p "$TY_QA_PROJECTS"
ty settings set projects_dir "$TY_QA_PROJECTS" >/dev/null

echo "==> Project '$PROJECT' with a ${INIT_DELAY}s worktree init script"
rm -rf "$PROJECT_PATH" "$TY_QA_ROOT/remote.git"
mkdir -p "$PROJECT_PATH/bin"
git -C "$PROJECT_PATH" init -q
git -C "$PROJECT_PATH" config user.email qa@ty.local
git -C "$PROJECT_PATH" config user.name "ty qa"
echo "# $PROJECT" > "$PROJECT_PATH/README.md"
# This is the whole point: worktree setup that outlasts a sweep tick.
cat > "$PROJECT_PATH/bin/worktree-setup" <<EOF
#!/usr/bin/env bash
echo "worktree-setup: simulating clone + bundle + migrate (${INIT_DELAY}s)"
sleep ${INIT_DELAY}
echo "worktree-setup: done"
EOF
chmod +x "$PROJECT_PATH/bin/worktree-setup"
git -C "$PROJECT_PATH" add -A
git -C "$PROJECT_PATH" commit -qm "init + slow worktree-setup"
git init --bare -q "$TY_QA_ROOT/remote.git"
git -C "$PROJECT_PATH" remote add origin "$TY_QA_ROOT/remote.git"
git -C "$PROJECT_PATH" push -q -u origin HEAD

# NOTE: do NOT pass --claude-config-dir. ty writes its trust/MCP config to
# ClaudeConfigFilePath(dir) == "<dir>.json", but launching claude with
# CLAUDE_CONFIG_DIR=<dir> makes it read "<dir>/.claude.json". Pinning even the default
# dir splits the two, and the step then hangs forever on the folder-trust prompt.
ty projects create "$PROJECT" --path "$PROJECT_PATH" >/dev/null 2>&1 || true

echo "==> Stress workflow (root -> A||B -> sink), cheap model, trivial work"
cat > "$TY_WORKFLOWS_DIR/stress.yaml" <<'EOF'
name: stress
description: Slow-init + concurrency regression workflow. Trivial work; the point is the DAG.
steps:
  - name: Root
    model: haiku
    prompt: Create a file root.txt at the repo root containing the single word "root". Nothing else.
  - name: A
    model: haiku
    deps: [Root]
    prompt: Create a file a.txt at the repo root containing the single word "a". Nothing else.
  - name: B
    model: haiku
    deps: [Root]
    prompt: Create a file b.txt at the repo root containing the single word "b". Nothing else.
  - name: Sink
    model: haiku
    deps: [A, B]
    prompt: Create a file sink.txt at the repo root containing the single word "sink". Nothing else.
EOF

echo "==> Starting isolated daemon"
bash "$TY_QA_DIR/ty-qa-daemon.sh" >/dev/null
sleep 1

# Claude shows a first-run prompt in every fresh worktree whose .claude/settings.local.json
# pre-approves a tool permission (ty's setupClaudeHooks writes Read(.claude/attachments/**)).
# hasTrustDialogAccepted does NOT suppress it, and an unattended step waits on it forever.
# Answer it, in the ISOLATED tmux server only.
autotrust() {
  local end=$(( $(date +%s) + WATCH + 120 ))
  while [[ $(date +%s) -lt $end ]]; do
    for w in $(tmux list-windows -a -F "#{session_name}:#{window_name}" 2>/dev/null | grep -E ":task-[0-9]+$" || true); do
      if tmux capture-pane -t "$w" -p 2>/dev/null | grep -q "Yes, I trust this folder"; then
        tmux send-keys -t "$w" "1" 2>/dev/null || true
        tmux send-keys -t "$w" Enter 2>/dev/null || true
      fi
    done
    sleep 3
  done
}
autotrust & AUTOTRUST_PID=$!
trap 'kill "$AUTOTRUST_PID" 2>/dev/null || true' EXIT

echo "==> Launching $PIPELINES pipelines concurrently"
for i in $(seq 1 "$PIPELINES"); do
  ty pipeline "stress run $i: add the files" --definition stress --project "$PROJECT" --dangerous >/dev/null 2>&1 &
done
wait
sleep 1
echo "    steps created: $(sqlite3 "$WORKTREE_DB_PATH" "SELECT COUNT(*) FROM tasks WHERE COALESCE(tags,'') LIKE '%pipeline%';")"

# A step marked done whose completed_at precedes its session start (or which never had a
# session) was completed before it ran.
zombie_sql() {
  cat <<'SQL'
SELECT t.id || ' | ' || substr(t.title,1,40) || ' | completed=' || COALESCE(t.completed_at,'?')
       || ' | session=' || COALESCE((SELECT MIN(l.created_at) FROM task_logs l WHERE l.task_id=t.id
              AND (l.content LIKE 'Starting new session%' OR l.content LIKE 'Resuming%')), 'NEVER')
FROM tasks t
WHERE COALESCE(t.tags,'') LIKE '%pipeline%' AND t.status='done'
  AND ( (SELECT MIN(l.created_at) FROM task_logs l WHERE l.task_id=t.id
           AND (l.content LIKE 'Starting new session%' OR l.content LIKE 'Resuming%')) IS NULL
     OR t.completed_at < (SELECT MIN(l.created_at) FROM task_logs l WHERE l.task_id=t.id
           AND (l.content LIKE 'Starting new session%' OR l.content LIKE 'Resuming%')) );
SQL
}

echo "==> Watching for zombies (up to ${WATCH}s)…"
deadline=$(( $(date +%s) + WATCH ))
zombies=""
while [[ $(date +%s) -lt $deadline ]]; do
  zombies="$(sqlite3 "$WORKTREE_DB_PATH" "$(zombie_sql)" 2>/dev/null || true)"
  [[ -n "$zombies" ]] && break
  states=$(sqlite3 "$WORKTREE_DB_PATH" "SELECT group_concat(status) FROM (SELECT status FROM tasks WHERE COALESCE(tags,'') LIKE '%pipeline%' ORDER BY id);" 2>/dev/null || true)
  echo "    [$(date +%H:%M:%S)] $states"
  # Settled: nothing queued or processing left.
  if [[ -n "$states" ]] && ! echo "$states" | grep -qE 'queued|processing'; then
    echo "    all steps settled"
    break
  fi
  sleep 10
done

zombies="$(sqlite3 "$WORKTREE_DB_PATH" "$(zombie_sql)" 2>/dev/null || true)"
echo
echo "================ RESULT ================"
if [[ -n "$zombies" ]]; then
  echo "FAIL — steps were completed before their session ever started:"
  echo "$zombies" | sed 's/^/  ZOMBIE /'
  echo
  echo "The sweep advanced the DAG past work that never happened."
  exit 1
fi
echo "PASS — every 'done' step started a session before it completed."
sqlite3 -header -column "$WORKTREE_DB_PATH" "
SELECT id, substr(title,1,26) AS step, status,
       CAST((julianday(COALESCE(completed_at,'now')) - julianday(started_at))*86400 AS INT) AS runtime_s,
       CASE WHEN COALESCE(base_commit,'')='' THEN '-' ELSE substr(base_commit,1,8) END AS base
FROM tasks WHERE COALESCE(tags,'') LIKE '%pipeline%' AND started_at IS NOT NULL ORDER BY id;" 2>/dev/null
