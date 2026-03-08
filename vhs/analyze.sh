#!/usr/bin/env bash
# analyze.sh — Collect VHS screenshots and generate an analysis prompt for an LLM.
#
# This script:
# 1. Lists all generated screenshots
# 2. Creates a structured analysis prompt
# 3. Outputs a report that an LLM agent can use
#
# Usage:
#   ./vhs/analyze.sh                    # Print analysis prompt to stdout
#   ./vhs/analyze.sh > analysis.md      # Save to file
#   ./vhs/analyze.sh --list             # Just list screenshots

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SS_DIR="$SCRIPT_DIR/output/screenshots"

if [ ! -d "$SS_DIR" ]; then
    echo "No screenshots directory found. Run ./vhs/run-all.sh first."
    exit 1
fi

SCREENSHOTS=($(find "$SS_DIR" -name "*.png" | sort))

if [ ${#SCREENSHOTS[@]} -eq 0 ]; then
    echo "No screenshots found. Run ./vhs/run-all.sh first."
    exit 1
fi

if [ "${1:-}" = "--list" ]; then
    for ss in "${SCREENSHOTS[@]}"; do
        echo "$ss"
    done
    exit 0
fi

cat << 'PROMPT'
# TaskYou UX Analysis — VHS Recording Results

You are analyzing screenshots from automated VHS recordings of the TaskYou TUI application.
Each screenshot captures a specific moment in a user interaction flow.

## Analysis Framework

For each screenshot, evaluate:

### 1. Visual Clarity
- Is the information hierarchy clear?
- Can the user understand what they're looking at?
- Are labels, colors, and spacing effective?

### 2. Discoverability
- Are available actions visible or hinted at?
- Would a new user know what to do next?
- Are keyboard shortcuts communicated?

### 3. Feedback & State
- Does the UI clearly show the current state?
- Are transitions between states obvious?
- Is there adequate feedback for user actions?

### 4. Efficiency
- Can experienced users accomplish tasks quickly?
- Are there unnecessary steps or delays?
- Do shortcuts feel natural?

### 5. Edge Cases & Issues
- Any rendering glitches or misalignment?
- Text overflow or truncation problems?
- Missing or confusing UI elements?

## Screenshots to Analyze

PROMPT

for ss in "${SCREENSHOTS[@]}"; do
    name=$(basename "$ss" .png)

    # Determine context from filename
    context=""
    case "$name" in
        00-*) context="Smoke Test — Basic launch verification" ;;
        01-*) context="First Launch — New user seeing the dashboard for the first time" ;;
        02-*) context="Task Creation — User creating a new task" ;;
        03-*) context="Kanban Navigation — User navigating the board" ;;
        04-*) context="Filter & Search — User finding specific tasks" ;;
        05-*) context="Settings — User customizing the application" ;;
        06-*) context="Power User — Experienced keyboard-driven workflow" ;;
        persona-newcomer-*) context="Newcomer Persona — First-time user journey" ;;
        persona-power-*) context="Power User Persona — Experienced daily workflow" ;;
        persona-pm-*) context="Project Manager Persona — Multi-project oversight" ;;
        persona-dev-*) context="Developer Persona — Daily development workflow" ;;
        *) context="General interaction" ;;
    esac

    echo "### $name"
    echo "Context: $context"
    echo "File: $ss"
    echo ""
done

cat << 'FOOTER'
## Output Format

Provide your analysis as:

1. **Executive Summary** — Top 3-5 findings across all screenshots
2. **Per-Screenshot Notes** — Specific observations for each screenshot
3. **Prioritized Recommendations** — Actionable improvements ranked by impact
4. **Quick Wins** — Small changes that would meaningfully improve UX
5. **Deep Issues** — Structural problems that need design rethinking

Focus on actionable product insights, not just aesthetic preferences.
FOOTER
