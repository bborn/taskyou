#!/bin/bash
# task - Manage your GitHub Issues task queue
#
# Usage:
#   task "Fix the login bug"                    # Create task
#   task list                                   # List all tasks
#   task list -p offerlab -s queued             # Filter tasks

set -e

# Configuration
TASK_REPO="${TASK_REPO:-bborn/workflow}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
DIM='\033[2m'
NC='\033[0m'

show_help() {
    cat << 'EOF'
task - Manage your GitHub Issues task queue

USAGE:
    task "description"                      # Create a task
    task list [options]                     # List tasks

CREATE OPTIONS:
    -p, --project NAME    Project: offerlab (o), influencekit (i), personal (p)
    -t, --type TYPE       Type: code (c), writing (w), thinking (t)
    -P, --priority LEVEL  Priority: high (h), low (l)
    -b, --body TEXT       Additional details

LIST OPTIONS:
    -p, --project NAME    Filter by project
    -t, --type TYPE       Filter by type
    -s, --status STATUS   Filter by status: queued, processing, ready, blocked
    -P, --priority LEVEL  Filter by priority
    -a, --all             Include closed tasks
    -l, --limit NUM       Max results (default: 30)

CLOSE OPTIONS:
    task close NUMBER     Close task by issue number
    task done NUMBER      Alias for close

EXAMPLES:
    task "Fix login redirect bug"
    task "Add Stripe webhook" -p offerlab -t code
    task list
    task list -p offerlab -s queued
    task close 42
    task done 15

SHORTCUTS:
    alias tq='task'
    alias tql='task list'
    alias tqo='task -p offerlab'
    alias tqi='task -p influencekit'

EOF
}

# Normalize project name
normalize_project() {
    case "$1" in
        offerlab|ol|o) echo "project:offerlab" ;;
        influencekit|ik|i) echo "project:influencekit" ;;
        personal|p) echo "project:personal" ;;
        *) echo "project:$1" ;;
    esac
}

# Normalize type
normalize_type() {
    case "$1" in
        code|c) echo "type:code" ;;
        writing|write|w) echo "type:writing" ;;
        thinking|think|t) echo "type:thinking" ;;
        *) echo "type:$1" ;;
    esac
}

# Normalize status
normalize_status() {
    case "$1" in
        queued|q) echo "status:queued" ;;
        processing|proc|p) echo "status:processing" ;;
        ready|r|done|d) echo "status:ready" ;;
        blocked|b) echo "status:blocked" ;;
        *) echo "status:$1" ;;
    esac
}

# Normalize priority
normalize_priority() {
    case "$1" in
        high|h|1) echo "priority:high" ;;
        low|l|3) echo "priority:low" ;;
        *) echo "priority:$1" ;;
    esac
}

# List tasks
list_tasks() {
    local PROJECT="" TYPE="" STATUS="" PRIORITY="" SHOW_ALL="" LIMIT="30"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -p|--project) PROJECT="$2"; shift 2 ;;
            -t|--type) TYPE="$2"; shift 2 ;;
            -s|--status) STATUS="$2"; shift 2 ;;
            -P|--priority) PRIORITY="$2"; shift 2 ;;
            -a|--all) SHOW_ALL="1"; shift ;;
            -l|--limit) LIMIT="$2"; shift 2 ;;
            -h|--help) show_help; exit 0 ;;
            *) shift ;;
        esac
    done

    # Build label filters
    LABEL_ARGS=()
    [[ -n "$PROJECT" ]] && LABEL_ARGS+=("--label" "$(normalize_project "$PROJECT")")
    [[ -n "$TYPE" ]] && LABEL_ARGS+=("--label" "$(normalize_type "$TYPE")")
    [[ -n "$STATUS" ]] && LABEL_ARGS+=("--label" "$(normalize_status "$STATUS")")
    [[ -n "$PRIORITY" ]] && LABEL_ARGS+=("--label" "$(normalize_priority "$PRIORITY")")

    # State filter
    STATE_ARG="--state open"
    [[ -n "$SHOW_ALL" ]] && STATE_ARG="--state all"

    echo -e "${BLUE}Tasks in ${TASK_REPO}${NC}"
    echo ""

    # Get issues with JSON output for better formatting
    ISSUES=$(gh issue list \
        --repo "$TASK_REPO" \
        ${STATE_ARG} \
        --limit "$LIMIT" \
        "${LABEL_ARGS[@]}" \
        --json number,title,labels,state,createdAt \
        2>&1) || { echo -e "${RED}Failed to fetch tasks${NC}"; exit 1; }

    # Check if empty
    if [[ "$ISSUES" == "[]" ]]; then
        echo -e "${DIM}No tasks found${NC}"
        exit 0
    fi

    # Parse and display
    echo "$ISSUES" | jq -r '.[] | "\(.number)|\(.title)|\(.labels | map(.name) | join(","))|\(.state)"' | while IFS='|' read -r num title labels state; do
        # Color based on status
        if [[ "$labels" == *"status:ready"* ]]; then
            STATUS_COLOR="$GREEN"
            STATUS_ICON="✓"
        elif [[ "$labels" == *"status:blocked"* ]]; then
            STATUS_COLOR="$RED"
            STATUS_ICON="!"
        elif [[ "$labels" == *"status:processing"* ]]; then
            STATUS_COLOR="$YELLOW"
            STATUS_ICON="⋯"
        else
            STATUS_COLOR="$DIM"
            STATUS_ICON="○"
        fi

        # Priority indicator
        PRIORITY_IND=""
        [[ "$labels" == *"priority:high"* ]] && PRIORITY_IND="${RED}↑${NC} "

        # Extract project
        PROJECT_TAG=""
        if [[ "$labels" == *"project:offerlab"* ]]; then
            PROJECT_TAG="${CYAN}[offerlab]${NC} "
        elif [[ "$labels" == *"project:influencekit"* ]]; then
            PROJECT_TAG="${CYAN}[ik]${NC} "
        elif [[ "$labels" == *"project:personal"* ]]; then
            PROJECT_TAG="${CYAN}[personal]${NC} "
        fi

        # Type indicator
        TYPE_TAG=""
        if [[ "$labels" == *"type:code"* ]]; then
            TYPE_TAG="${DIM}code${NC}"
        elif [[ "$labels" == *"type:writing"* ]]; then
            TYPE_TAG="${DIM}write${NC}"
        elif [[ "$labels" == *"type:thinking"* ]]; then
            TYPE_TAG="${DIM}think${NC}"
        fi

        printf "${STATUS_COLOR}${STATUS_ICON}${NC} ${DIM}#%-4s${NC} ${PRIORITY_IND}${PROJECT_TAG}%s ${TYPE_TAG}\n" "$num" "$title"
    done

    echo ""
    echo -e "${DIM}$(echo "$ISSUES" | jq length) tasks${NC}"
}

# Create task
create_task() {
    local PROJECT="" TYPE="" PRIORITY="" BODY="" TITLE=""

    while [[ $# -gt 0 ]]; do
        case $1 in
            -p|--project) PROJECT="$2"; shift 2 ;;
            -t|--type) TYPE="$2"; shift 2 ;;
            -P|--priority) PRIORITY="$2"; shift 2 ;;
            -b|--body) BODY="$2"; shift 2 ;;
            -h|--help) show_help; exit 0 ;;
            -*)
                echo -e "${RED}Unknown option: $1${NC}"
                show_help
                exit 1
                ;;
            *)
                if [[ -z "$TITLE" ]]; then
                    TITLE="$1"
                else
                    TITLE="$TITLE $1"
                fi
                shift
                ;;
        esac
    done

    # Validate
    if [[ -z "$TITLE" ]]; then
        echo -e "${RED}Error: Task description required${NC}"
        echo ""
        show_help
        exit 1
    fi

    # Build label arguments
    LABELS=()
    [[ -n "$PROJECT" ]] && LABELS+=("--label" "$(normalize_project "$PROJECT")")
    [[ -n "$TYPE" ]] && LABELS+=("--label" "$(normalize_type "$TYPE")")
    [[ -n "$PRIORITY" ]] && LABELS+=("--label" "$(normalize_priority "$PRIORITY")")

    # Always add queued status
    LABELS+=("--label" "status:queued")

    # Body is required for non-interactive mode
    [[ -z "$BODY" ]] && BODY=" "

    # Create the issue
    echo -e "${BLUE}Creating task...${NC}"

    RESULT=$(gh issue create \
        --repo "$TASK_REPO" \
        --title "$TITLE" \
        --body "$BODY" \
        "${LABELS[@]}" \
        2>&1)

    if [[ $? -eq 0 ]]; then
        ISSUE_URL=$(echo "$RESULT" | grep -o 'https://github.com/[^ ]*')
        ISSUE_NUM=$(echo "$ISSUE_URL" | grep -o '[0-9]*$')

        echo -e "${GREEN}✓ Created task #${ISSUE_NUM}${NC}"
        echo -e "  ${BLUE}${ISSUE_URL}${NC}"

        # Show labels applied
        if [[ ${#LABELS[@]} -gt 2 ]]; then
            echo -e "  ${YELLOW}Labels: ${LABELS[*]/--label /}${NC}"
        fi

        # Copy to clipboard on macOS
        if command -v pbcopy &> /dev/null; then
            echo -n "$ISSUE_URL" | pbcopy
            echo -e "  ${DIM}(URL copied to clipboard)${NC}"
        fi
    else
        echo -e "${RED}✗ Failed to create task${NC}"
        echo "$RESULT"
        exit 1
    fi
}

# Close task
close_task() {
    local ISSUE_NUM="$1"

    if [[ -z "$ISSUE_NUM" ]]; then
        echo -e "${RED}Error: Issue number required${NC}"
        echo "Usage: task close NUMBER"
        exit 1
    fi

    # Validate it's a number
    if ! [[ "$ISSUE_NUM" =~ ^[0-9]+$ ]]; then
        echo -e "${RED}Error: Invalid issue number${NC}"
        exit 1
    fi

    echo -e "${BLUE}Closing task #${ISSUE_NUM}...${NC}"

    RESULT=$(gh issue close "$ISSUE_NUM" --repo "$TASK_REPO" 2>&1)

    if [[ $? -eq 0 ]]; then
        echo -e "${GREEN}✓ Closed task #${ISSUE_NUM}${NC}"
    else
        echo -e "${RED}✗ Failed to close task${NC}"
        echo "$RESULT"
        exit 1
    fi
}

# Main routing
case "${1:-}" in
    list|ls|l)
        shift
        list_tasks "$@"
        ;;
    close|done|c|d)
        shift
        close_task "$@"
        ;;
    help|--help|-h)
        show_help
        ;;
    "")
        show_help
        ;;
    *)
        create_task "$@"
        ;;
esac
