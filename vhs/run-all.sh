#!/usr/bin/env bash
# run-all.sh — Execute VHS tapes and generate recordings + screenshots.
#
# Usage:
#   ./vhs/run-all.sh              # Run all tapes (feature + persona)
#   ./vhs/run-all.sh tapes        # Run only feature tapes
#   ./vhs/run-all.sh personas     # Run only persona tapes
#   ./vhs/run-all.sh tapes/01*    # Run a specific tape
#
# SAFETY: Seeds an isolated database first. Never touches your real data.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

# Ensure binary is built
if [ ! -x "bin/ty" ]; then
    echo "Building ty..."
    make build-no-restart
fi

# Seed isolated test database
echo "=== Seeding test database ==="
bash "$SCRIPT_DIR/seed-data.sh"
echo ""

# Determine which tapes to run
MODE="${1:-all}"
TAPES=()

case "$MODE" in
    tapes)
        TAPES=("$SCRIPT_DIR"/tapes/*.tape)
        ;;
    personas)
        TAPES=("$SCRIPT_DIR"/personas/*.tape)
        ;;
    all)
        TAPES=("$SCRIPT_DIR"/tapes/*.tape "$SCRIPT_DIR"/personas/*.tape)
        ;;
    *)
        # Specific tape path
        TAPES=("$SCRIPT_DIR"/${MODE}*.tape "$MODE")
        # Filter to only existing files
        EXISTING=()
        for t in "${TAPES[@]}"; do
            [ -f "$t" ] && EXISTING+=("$t")
        done
        TAPES=("${EXISTING[@]}")
        ;;
esac

if [ ${#TAPES[@]} -eq 0 ]; then
    echo "No tapes found for: $MODE"
    exit 1
fi

echo "=== Running ${#TAPES[@]} VHS tape(s) ==="
echo ""

PASS=0
FAIL=0

for tape in "${TAPES[@]}"; do
    name=$(basename "$tape" .tape)
    echo "--- Recording: $name ---"

    if vhs "$tape" 2>&1; then
        echo "  OK: $name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $name"
        FAIL=$((FAIL + 1))
    fi
    echo ""
done

echo "=== Results ==="
echo "  Passed: $PASS"
echo "  Failed: $FAIL"
echo ""

# List generated outputs
if [ -d "$SCRIPT_DIR/output/screenshots" ]; then
    SCREENSHOTS=$(find "$SCRIPT_DIR/output/screenshots" -name "*.png" 2>/dev/null | wc -l | tr -d ' ')
    echo "Screenshots: $SCREENSHOTS files in vhs/output/screenshots/"
fi
if [ -d "$SCRIPT_DIR/output/gifs" ]; then
    GIFS=$(find "$SCRIPT_DIR/output/gifs" -name "*.gif" 2>/dev/null | wc -l | tr -d ' ')
    echo "GIFs: $GIFS files in vhs/output/gifs/"
fi
