#!/usr/bin/env bash
# seed-data.sh - Create an isolated test database with sample tasks for VHS recordings.
#
# Usage: source seed-data.sh  (sets WORKTREE_DB_PATH for the current shell)
#    or: ./seed-data.sh       (just creates the database and prints the path)
#
# SAFETY: Uses a completely isolated temp directory. Never touches your real TaskYou data.

set -euo pipefail

# Isolated test directory
VHS_TEST_DIR="${VHS_TEST_DIR:-/tmp/vhs-taskyou-test}"
VHS_DB_PATH="${VHS_TEST_DIR}/tasks.db"

# Clean previous test data
rm -rf "$VHS_TEST_DIR"
mkdir -p "$VHS_TEST_DIR"

export WORKTREE_DB_PATH="$VHS_DB_PATH"

# Find the ty binary - prefer local build, then PATH
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TY_BIN="${PROJECT_ROOT}/bin/ty"

if [ ! -x "$TY_BIN" ]; then
    TY_BIN="$(which ty 2>/dev/null || true)"
fi

if [ -z "$TY_BIN" ]; then
    echo "Error: ty binary not found. Run 'make build' first." >&2
    exit 1
fi

ty() {
    "$TY_BIN" "$@"
}

echo "Using ty binary: $TY_BIN"
echo "Test database: $VHS_DB_PATH"
echo ""

# Create project directories and projects
echo "Creating projects..."
for proj in backend frontend infrastructure product; do
    mkdir -p "${VHS_TEST_DIR}/projects/${proj}"
done
ty projects create "backend" --path "${VHS_TEST_DIR}/projects/backend" --color "#61AFEF" --no-git
ty projects create "frontend" --path "${VHS_TEST_DIR}/projects/frontend" --color "#E5C07B" --no-git
ty projects create "infrastructure" --path "${VHS_TEST_DIR}/projects/infrastructure" --color "#98C379" --no-git
ty projects create "product" --path "${VHS_TEST_DIR}/projects/product" --color "#C678DD" --no-git

echo ""
echo "Seeding test tasks..."

# Backlog tasks (diverse projects and types)
ty create "Set up CI/CD pipeline for staging" \
    --body "Configure GitHub Actions to deploy to staging on every push to main. Include build, test, and deploy stages." \
    --project "infrastructure" --type code

ty create "Write API documentation" \
    --body "Document all REST endpoints including request/response schemas, authentication, and rate limits." \
    --project "backend" --type writing

ty create "Design onboarding flow for new users" \
    --body "Create wireframes and implement the first-time user experience. Should guide users through key features." \
    --project "frontend" --type thinking

ty create "Add dark mode support" \
    --body "Implement theme switching between light and dark modes. Persist preference in local storage." \
    --project "frontend" --type code

ty create "Optimize database queries" \
    --body "Profile slow queries and add proper indexes. Focus on the task listing and search endpoints." \
    --project "backend" --type code

ty create "Set up error monitoring" \
    --body "Integrate Honeybadger for error tracking across all services." \
    --project "infrastructure" --type code

ty create "Research WebSocket alternatives" \
    --body "Evaluate SSE vs WebSockets vs long-polling for real-time task updates." \
    --project "backend" --type thinking

ty create "Create user feedback survey" \
    --body "Design and distribute a survey to gather feedback on the current UX." \
    --project "product" --type writing

ty create "Implement keyboard shortcut help overlay" \
    --body "Show a modal with all available keyboard shortcuts when user presses '?'." \
    --project "frontend" --type code

ty create "Add task dependency visualization" \
    --body "Show a graph view of task dependencies with blocking relationships." \
    --project "frontend" --type code

ty create "Migrate to structured logging" \
    --body "Replace fmt.Println with structured slog calls for better observability." \
    --project "backend" --type code

ty create "Write getting started guide" \
    --body "Create a beginner-friendly tutorial covering installation, first task, and basic workflows." \
    --project "product" --type writing

echo ""
echo "Database seeded at: $VHS_DB_PATH"
echo ""
echo "To use in VHS tapes, add this line:"
echo "  Env WORKTREE_DB_PATH \"$VHS_DB_PATH\""
echo ""
echo "To use in shell:"
echo "  export WORKTREE_DB_PATH=\"$VHS_DB_PATH\""
