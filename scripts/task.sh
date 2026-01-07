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
    task c "description"                    # Create a task
    task create "description"               # Create a task (alias)
    task list [options]                     # List tasks

CREATE OPTIONS:
    -p, --project NAME    Project: offerlab (o), influencekit (i), personal (p)
    -t, --type TYPE       Type: code (c), writing (w), thinking (t)
    -P, --priority LEVEL  Priority: high (h), low (l)
    -b, --body TEXT       Additional details
    -x, --execute         Queue task for immediate execution
    -                     Read from stdin (first line = title, rest = body)

QUEUE OPTIONS:
    task queue NUMBER     Queue a task for Claude to execute
    task q NUMBER         Alias for queue

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

REVIEW OPTIONS:
    task review           List tasks ready for review
    task review -p NAME   Filter by project
    task review --open    Open all ready tasks in browser
    task review NUMBER    Open specific task in browser

WATCH OPTIONS:
    task watch            Watch Claude working on Hetzner (parsed output)
    task watch --raw      Show raw JSON stream
    task w                Alias for watch

LOGS OPTIONS:
    task logs             List recent task logs
    task logs NUMBER      View log for specific task

REQUEUE OPTIONS:
    task requeue NUMBER              Requeue a blocked task
    task requeue NUMBER -m "info"    Requeue with additional context
    task rq NUMBER "more details"    Shorthand

EXAMPLES:
    task c "Fix login redirect bug"
    task c "Add Stripe webhook" -p offerlab -t code
    task create - (then paste multiline, Ctrl+D)
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

# Detect project from current directory
detect_project() {
    local cwd="$(pwd)"
    case "$cwd" in
        */offerlab*) echo "offerlab" ;;
        */influencekit*) echo "influencekit" ;;
        */workflow*) echo "personal" ;;
        *) echo "" ;;
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
        --json number,title,labels,state,url \
        2>&1) || { echo -e "${RED}Failed to fetch tasks${NC}"; exit 1; }

    # Check if empty
    if [[ "$ISSUES" == "[]" ]]; then
        echo -e "${DIM}No tasks found${NC}"
        exit 0
    fi

    # Parse and display
    echo "$ISSUES" | jq -r '.[] | "\(.number)|\(.title)|\(.labels | map(.name) | join(","))|\(.state)|\(.url)"' | while IFS='|' read -r num title labels state url; do
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

        # Make issue number a clickable link (OSC 8 hyperlink)
        # Format: ESC ] 8 ; ; URL ESC \ TEXT ESC ] 8 ; ; ESC \
        printf "%b%s%b %b\033]8;;%s\033\\\\#%s\033]8;;\033\\\\%b  %b%b%s %b\n" \
            "$STATUS_COLOR" "$STATUS_ICON" "$NC" "$DIM" "$url" "$num" "$NC" "$PRIORITY_IND" "$PROJECT_TAG" "$title" "$TYPE_TAG"
    done

    echo ""
    echo -e "${DIM}$(echo "$ISSUES" | jq length) tasks${NC}"
}

# Create task
create_task() {
    local PROJECT="" TYPE="" PRIORITY="" BODY="" TITLE="" FROM_STDIN="" EXECUTE=""

    while [[ $# -gt 0 ]]; do
        case $1 in
            -p|--project) PROJECT="$2"; shift 2 ;;
            -t|--type) TYPE="$2"; shift 2 ;;
            -P|--priority) PRIORITY="$2"; shift 2 ;;
            -b|--body) BODY="$2"; shift 2 ;;
            -x|--execute) EXECUTE="1"; shift ;;
            -h|--help) show_help; exit 0 ;;
            -)
                FROM_STDIN="1"
                shift
                ;;
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

    # Read from stdin if - flag was passed
    if [[ -n "$FROM_STDIN" ]]; then
        echo -e "${DIM}Reading from stdin (first line = title, rest = body)...${NC}"
        echo -e "${DIM}Paste content, then Ctrl+D when done:${NC}"
        local INPUT=$(cat)
        TITLE=$(echo "$INPUT" | head -n 1)
        BODY=$(echo "$INPUT" | tail -n +2)
    fi

    # Validate
    if [[ -z "$TITLE" ]]; then
        echo -e "${RED}Error: Task description required${NC}"
        echo ""
        show_help
        exit 1
    fi

    # Auto-detect project from current directory if not specified
    if [[ -z "$PROJECT" ]]; then
        PROJECT=$(detect_project)
        if [[ -n "$PROJECT" ]]; then
            echo -e "${DIM}Auto-detected project: ${PROJECT}${NC}"
        fi
    fi

    # Build label arguments
    LABELS=()
    [[ -n "$PROJECT" ]] && LABELS+=("--label" "$(normalize_project "$PROJECT")")
    [[ -n "$TYPE" ]] && LABELS+=("--label" "$(normalize_type "$TYPE")")
    [[ -n "$PRIORITY" ]] && LABELS+=("--label" "$(normalize_priority "$PRIORITY")")

    # Only queue for execution if -x flag passed
    [[ -n "$EXECUTE" ]] && LABELS+=("--label" "status:queued")

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

        if [[ -n "$EXECUTE" ]]; then
            echo -e "${GREEN}✓ Created and queued task #${ISSUE_NUM}${NC}"
        else
            echo -e "${GREEN}✓ Created task #${ISSUE_NUM}${NC}"
            echo -e "  ${DIM}Run 'task queue ${ISSUE_NUM}' to start execution${NC}"
        fi
        echo -e "  ${BLUE}${ISSUE_URL}${NC}"

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

# Review ready tasks
review_tasks() {
    local PROJECT="" OPEN_ALL="" ISSUE_NUM=""

    while [[ $# -gt 0 ]]; do
        case $1 in
            -p|--project) PROJECT="$2"; shift 2 ;;
            -o|--open) OPEN_ALL="1"; shift ;;
            -h|--help) show_help; exit 0 ;;
            [0-9]*)
                ISSUE_NUM="$1"
                shift
                ;;
            *) shift ;;
        esac
    done

    # If a specific issue number given, open it directly
    if [[ -n "$ISSUE_NUM" ]]; then
        echo -e "${BLUE}Opening task #${ISSUE_NUM}...${NC}"
        gh issue view "$ISSUE_NUM" --repo "$TASK_REPO" --web
        exit 0
    fi

    # Build label filters
    LABEL_ARGS=("--label" "status:ready")
    [[ -n "$PROJECT" ]] && LABEL_ARGS+=("--label" "$(normalize_project "$PROJECT")")

    echo -e "${BLUE}Tasks ready for review${NC}"
    echo ""

    # Get ready issues
    ISSUES=$(gh issue list \
        --repo "$TASK_REPO" \
        --state open \
        --limit 30 \
        "${LABEL_ARGS[@]}" \
        --json number,title,labels,url \
        2>&1) || { echo -e "${RED}Failed to fetch tasks${NC}"; exit 1; }

    # Check if empty
    if [[ "$ISSUES" == "[]" ]]; then
        echo -e "${DIM}No tasks ready for review${NC}"
        exit 0
    fi

    COUNT=$(echo "$ISSUES" | jq length)

    # If --open flag, open all in browser
    if [[ -n "$OPEN_ALL" ]]; then
        echo -e "${YELLOW}Opening $COUNT tasks in browser...${NC}"
        echo "$ISSUES" | jq -r '.[].url' | while read -r url; do
            open "$url" 2>/dev/null || xdg-open "$url" 2>/dev/null || echo "$url"
            sleep 0.3  # Small delay to not overwhelm browser
        done
        exit 0
    fi

    # Display ready tasks
    echo "$ISSUES" | jq -r '.[] | "\(.number)|\(.title)|\(.labels | map(.name) | join(","))|\(.url)"' | while IFS='|' read -r num title labels url; do
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

        # Make issue number a clickable link (OSC 8 hyperlink)
        LINK="\033]8;;${url}\033\\#${num}\033]8;;\033\\"
        printf "${GREEN}✓${NC} ${DIM}${LINK}${NC}  ${PROJECT_TAG}%s ${TYPE_TAG}\n" "$title"
    done

    echo ""
    echo -e "${DIM}${COUNT} tasks ready for review${NC}"
    echo ""
    echo -e "${DIM}Commands:${NC}"
    echo -e "  task review NUMBER    ${DIM}# Open specific task${NC}"
    echo -e "  task review --open    ${DIM}# Open all in browser${NC}"
    echo -e "  task close NUMBER     ${DIM}# Mark as reviewed${NC}"
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

# Requeue a blocked task
requeue_task() {
    local ISSUE_NUM="" COMMENT=""

    while [[ $# -gt 0 ]]; do
        case $1 in
            -m|--message) COMMENT="$2"; shift 2 ;;
            -h|--help)
                echo "Usage: task requeue NUMBER [-m \"additional context\"]"
                exit 0
                ;;
            [0-9]*)
                ISSUE_NUM="$1"
                shift
                ;;
            *)
                # Treat as comment if no flag
                if [[ -z "$COMMENT" ]]; then
                    COMMENT="$1"
                fi
                shift
                ;;
        esac
    done

    if [[ -z "$ISSUE_NUM" ]]; then
        echo -e "${RED}Error: Issue number required${NC}"
        echo "Usage: task requeue NUMBER [-m \"additional context\"]"
        exit 1
    fi

    # Validate it's a number
    if ! [[ "$ISSUE_NUM" =~ ^[0-9]+$ ]]; then
        echo -e "${RED}Error: Invalid issue number${NC}"
        exit 1
    fi

    echo -e "${BLUE}Requeuing task #${ISSUE_NUM}...${NC}"

    # Add comment if provided
    if [[ -n "$COMMENT" ]]; then
        gh issue comment "$ISSUE_NUM" --repo "$TASK_REPO" --body "$COMMENT" > /dev/null 2>&1
        echo -e "${DIM}Added comment: ${COMMENT}${NC}"
    fi

    # Remove blocked/ready, add queued
    gh issue edit "$ISSUE_NUM" --repo "$TASK_REPO" \
        --remove-label "status:blocked" \
        --remove-label "status:ready" \
        --add-label "status:queued" \
        > /dev/null 2>&1

    if [[ $? -eq 0 ]]; then
        echo -e "${GREEN}✓ Task #${ISSUE_NUM} requeued${NC}"
    else
        echo -e "${RED}✗ Failed to requeue task${NC}"
        exit 1
    fi
}

# Queue a task for execution
queue_task() {
    local ISSUE_NUM="$1"

    if [[ -z "$ISSUE_NUM" ]]; then
        echo -e "${RED}Error: Issue number required${NC}"
        echo "Usage: task queue NUMBER"
        exit 1
    fi

    # Validate it's a number
    if ! [[ "$ISSUE_NUM" =~ ^[0-9]+$ ]]; then
        echo -e "${RED}Error: Invalid issue number${NC}"
        exit 1
    fi

    echo -e "${BLUE}Queueing task #${ISSUE_NUM} for execution...${NC}"

    # Add queued label
    gh issue edit "$ISSUE_NUM" --repo "$TASK_REPO" \
        --add-label "status:queued" \
        > /dev/null 2>&1

    if [[ $? -eq 0 ]]; then
        echo -e "${GREEN}✓ Task #${ISSUE_NUM} queued${NC}"
        echo -e "${DIM}Claude will pick it up shortly. Run 'task w' to watch.${NC}"
    else
        echo -e "${RED}✗ Failed to queue task${NC}"
        exit 1
    fi
}

# View task logs
view_logs() {
    local RUNNER_HOST="${RUNNER_HOST:-root@cloud-claude}"
    local ISSUE_NUM="$1"

    if [[ -n "$ISSUE_NUM" ]]; then
        # View specific task log
        echo -e "${BLUE}Fetching log for task #${ISSUE_NUM}...${NC}"
        ssh "$RUNNER_HOST" "ls -t /home/runner/logs/*task-${ISSUE_NUM}-* /home/runner/logs/*triage-${ISSUE_NUM}-* 2>/dev/null | head -1 | xargs cat" 2>/dev/null || {
            echo -e "${RED}No log found for task #${ISSUE_NUM}${NC}"
            exit 1
        }
    else
        # List recent logs
        echo -e "${BLUE}Recent task logs on ${RUNNER_HOST}:${NC}"
        echo ""
        ssh "$RUNNER_HOST" "ls -lht /home/runner/logs/*.txt 2>/dev/null | head -20" || {
            echo -e "${DIM}No logs found${NC}"
        }
        echo ""
        echo -e "${DIM}View specific log: task logs NUMBER${NC}"
    fi
}

# Watch Claude working on Hetzner
watch_claude() {
    local RUNNER_HOST="${RUNNER_HOST:-root@cloud-claude}"
    local RAW_MODE=""

    while [[ $# -gt 0 ]]; do
        case $1 in
            -r|--raw) RAW_MODE="1"; shift ;;
            *) shift ;;
        esac
    done

    echo -e "${BLUE}Watching Claude on ${RUNNER_HOST}...${NC}"
    echo -e "${DIM}(Ctrl+C to exit)${NC}"
    echo ""

    # Show tasks currently processing
    PROCESSING=$(gh issue list \
        --repo "$TASK_REPO" \
        --state open \
        --label "status:processing" \
        --json number,title \
        --limit 5 \
        2>&1)

    if [[ "$PROCESSING" != "[]" ]]; then
        echo -e "${GREEN}Currently processing:${NC}"
        echo "$PROCESSING" | jq -r '.[] | "  #\(.number): \(.title)"'
        echo ""
    else
        echo -e "${DIM}No tasks currently processing - waiting...${NC}"
        echo ""
    fi

    # Raw mode - just tail the file directly
    if [[ -n "$RAW_MODE" ]]; then
        ssh "$RUNNER_HOST" "tail -F /tmp/claude_output.txt 2>/dev/null" || {
            echo -e "${YELLOW}Connection closed. Reconnecting in 2s...${NC}"
            sleep 2
            watch_claude --raw
        }
        return
    fi

    # SSH and watch for new log files, then tail them
    ssh "$RUNNER_HOST" 'LOG_DIR=/home/runner/logs
    LAST_FILE=""
    while true; do
      # Find the most recent log file
      CURRENT_FILE=$(ls -t "$LOG_DIR"/*.txt 2>/dev/null | head -1)

      # If new file appeared, tail it
      if [[ -n "$CURRENT_FILE" && "$CURRENT_FILE" != "$LAST_FILE" ]]; then
        LAST_FILE="$CURRENT_FILE"
        echo "━━━ Watching: $(basename "$CURRENT_FILE") ━━━"

        # Tail with parsing until file stops growing
        tail -f "$CURRENT_FILE" 2>/dev/null | while IFS= read -r line; do
          echo "$line" | jq -r "
            if .type == \"system\" and .subtype == \"init\" then
              \"[session start]\"
            elif .type == \"assistant\" then
              .message.content[]? |
              if .type == \"text\" then
                \"💬 \" + (.text | split(\"\n\")[0] | .[0:150])
              elif .type == \"tool_use\" then
                \"🔧 \" + .name
              else empty end
            else empty end
          " 2>/dev/null | grep -v "^$"
        done &
        TAIL_PID=$!
      fi
      sleep 1
    done' 2>/dev/null

    echo -e "${YELLOW}Connection closed. Reconnecting in 2s...${NC}"
    sleep 2
    watch_claude
}

# Main routing
case "${1:-}" in
    create|c|new|add)
        shift
        create_task "$@"
        ;;
    list|ls|l)
        shift
        list_tasks "$@"
        ;;
    review|rev|r)
        shift
        review_tasks "$@"
        ;;
    watch|w)
        shift
        watch_claude "$@"
        ;;
    logs|log)
        shift
        view_logs "$@"
        ;;
    requeue|rq)
        shift
        requeue_task "$@"
        ;;
    queue|q)
        shift
        queue_task "$@"
        ;;
    close|done)
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
        echo -e "${RED}Unknown command: $1${NC}"
        echo ""
        echo "Did you mean: task c \"$*\""
        echo ""
        echo "Run 'task help' for usage"
        exit 1
        ;;
esac
