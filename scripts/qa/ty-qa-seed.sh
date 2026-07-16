#!/usr/bin/env bash
# Seed the isolated ty instance with realistic, curated content.
#
# WHY THIS EXISTS
# ---------------
# QA screenshots double as marketing/documentation assets, so every board and
# detail view we render must look like a real team's real board — never "test 1",
# "hello", "foo". This script populates the throwaway DB with a hand-written set
# of tasks that read like genuine engineering work: real-sounding titles, a
# sentence or two of body, sensible tags, a spread across every column, and a few
# pinned items. Screenshot flows (ty-qa-shoot.sh with TY_QA_SHOT_KEEP_DB=1) build
# on top of this so the shots are presentation-quality by default.
#
# Keep the content here believable and evergreen: no lorem ipsum, no internal
# secrets, no real customer data, no dated references. Treat edits like copy that
# could end up on the website.
#
# Usage:
#   scripts/qa/ty-qa-up.sh          # build binary + isolated instance
#   scripts/qa/ty-qa-seed.sh        # populate it with realistic data
#   TY_QA_SHOT_KEEP_DB=1 scripts/qa/ty-qa-shoot.sh <cwd> out.png ...   # shoot with data
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ty_qa_require_built

# Realistic-looking repos for a small product team. Each is a throwaway git repo
# under the isolated projects dir so the board shows several coloured project tags.
PROJECTS=(storefront payments-api mobile-ios data-pipeline)

echo "==> Registering projects: ${PROJECTS[*]}"
for p in "${PROJECTS[@]}"; do
  path="$TY_QA_PROJECTS/$p"
  if [[ ! -d "$path/.git" ]]; then
    mkdir -p "$path"
    git -C "$path" init -q
    git -C "$path" config user.email dev@example.com
    git -C "$path" config user.name "Dev"
    printf '# %s\n' "$p" > "$path/README.md"
    git -C "$path" add -A
    git -C "$path" commit -qm "Initial commit"
  fi
  ty projects create "$p" --path "$path" --claude-config-dir "$HOME/.claude" >/dev/null 2>&1 || true
done

# seed <project> <status> <pinned:0|1> <tags> <title> <body>
# Creates the task, then moves it to the target column. Pinned/tags/body are set
# at create time. Order of calls == order tasks appear (by id) within a column.
seed() {
  local project="$1" status="$2" pinned="$3" tags="$4" title="$5" body="$6"
  local args=(create "$title" --project "$project" --tags "$tags" --body "$body" --json)
  [[ "$pinned" == "1" ]] && args+=(--pinned)
  local id
  id="$(ty "${args[@]}" | jq -r '.id')"
  if [[ -z "$id" || "$id" == "null" ]]; then
    echo "ty-qa-seed: failed to create task: $title" >&2
    exit 1
  fi
  ty status "$id" "$status" >/dev/null
  printf '    #%-3s %-13s %-9s %s\n' "$id" "$project" "$status" "$title"
}

echo "==> Seeding realistic tasks"

# Columns map to statuses: Backlog=backlog, "In Progress"=queued, Blocked=blocked,
# Done=done. We deliberately avoid the `processing` status: it implies a live
# agent session, and without one ty's reconcile demotes it (and renders a
# half-started "Starting task…" spinner that reads as broken in a screenshot).
# For a "live agent" hero shot, stand up a real pane with ty-qa-agent.sh instead.

# --- Backlog: shaped-but-not-started work ------------------------------------
seed storefront    backlog 0 "ui,enhancement" \
  "Add a dark mode toggle to account settings" \
  "Respect the OS preference by default and let signed-in users override it. Persist the choice per account so it follows them across devices."
seed storefront    backlog 0 "performance" \
  "Speed up product search on large catalogs" \
  "Search latency climbs past 800ms once a store has more than ~50k SKUs. Profile the query path and add an index or a search cache."
seed payments-api  backlog 0 "docs" \
  "Write getting-started docs for the public API" \
  "Cover auth, the three core endpoints, pagination, and rate limits, with copy-paste curl examples for each."
seed mobile-ios    backlog 0 "feature" \
  "Support Apple Pay at checkout" \
  "Add Apple Pay as an express checkout option on the cart and product pages, gated behind device capability checks."

# --- In Progress (queued): pin the flagship item so it leads its column -------
seed storefront    queued 1 "auth" \
  "Add single sign-on with Google and GitHub" \
  "Implement OAuth2 for Google and GitHub, link providers to existing accounts by verified email, and keep password login working alongside."
seed payments-api  queued 0 "feature" \
  "Build CSV export for order history" \
  "Let merchants export filtered orders to CSV from the dashboard. Stream the file for large ranges instead of buffering it in memory."
seed payments-api  queued 0 "security" \
  "Add rate limiting to the public API" \
  "Introduce per-key token-bucket limits with clear 429 responses and Retry-After headers. Ship sensible defaults per plan tier."
seed payments-api  queued 0 "infra" \
  "Move the session store from Redis to Postgres" \
  "Consolidate onto Postgres to drop a moving part from the stack. Migrate live sessions without forcing everyone to log out."

# --- Blocked: waiting on something -------------------------------------------
seed mobile-ios    blocked 1 "feature" \
  "Enable push notifications for order updates" \
  "Notify customers on shipment and delivery. Blocked on the new APNs auth key from the platform team before we can test end to end."
seed storefront    blocked 0 "chore" \
  "Upgrade the web app to React 19" \
  "Blocked on two dependencies that still pin React 18. Track their releases and revisit once both publish compatible versions."
seed storefront    blocked 0 "ui,bug" \
  "Fix layout shift when product images load" \
  "Reserve space with an aspect-ratio box so cards stop jumping as images stream in. Cumulative Layout Shift should drop below 0.1."

# --- Done: recently shipped ---------------------------------------------------
seed payments-api  done 0 "testing" \
  "Fix the flaky checkout integration test" \
  "The test raced the webhook callback and failed ~1 in 20 runs. Await the recorded event instead of sleeping a fixed interval."
seed payments-api  done 0 "reliability" \
  "Retry webhook delivery with exponential backoff" \
  "Failed deliveries now retry with jittered backoff up to six times, then land in a dead-letter queue for manual replay."
seed storefront    done 0 "ui" \
  "Redesign the empty state for the orders page" \
  "Replace the blank table with an illustration, a one-line explainer, and a primary call to action to place a first order."
seed data-pipeline done 0 "performance" \
  "Cache geocoding responses for 24 hours" \
  "Address lookups repeated the same provider calls all day. Caching cut geocoding spend by roughly 70% with no accuracy loss."

cat <<EOF

==> Seeded $(sqlite3 "$WORKTREE_DB_PATH" "select count(*) from tasks where status != 'archived'" 2>/dev/null || echo '?') realistic tasks across ${#PROJECTS[@]} projects.

Screenshot the seeded board/detail (keep the DB so data survives the shot):
    mkdir -p $TY_QA_ROOT/shots
    TY_QA_SHOT_KEEP_DB=1 scripts/qa/ty-qa-shoot.sh $TY_QA_PROJECTS/storefront $TY_QA_ROOT/shots/board.png "Sleep 3s"
EOF
