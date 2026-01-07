#!/bin/bash
# task - Add tasks to your GitHub Issues queue
# 
# Usage:
#   task "Fix the login bug"
#   task "Add pagination" -p offerlab -t code
#   task "Write blog post" -t writing
#   task "Analyze pricing strategy" -t thinking -P high

set -e

# Configuration
TASK_REPO="${TASK_REPO:-bborn/workflow}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Defaults
PROJECT=""
TYPE=""
PRIORITY=""
BODY=""
TITLE=""

show_help() {
    cat << 'EOF'
task - Add tasks to your GitHub Issues queue

USAGE:
    task "description"                      # Quick task
    task "Add auth" -p offerlab -t code    # Code task for Offerlab
    task "Write email" -t writing           # Writing task
    task "Analyze X" -t thinking -P high    # High priority thinking

OPTIONS:
    -p, --project NAME    Project: offerlab, influencekit, personal
    -t, --type TYPE       Type: code, writing, thinking
    -P, --priority LEVEL  Priority: high, low
    -b, --body TEXT       Additional details
    -h, --help            Show this help

EXAMPLES:
    task "Fix login redirect bug"
    task "Add Stripe webhook handler" -p offerlab -t code
    task "Draft Q4 investor email" -t writing -b "Include metrics and roadmap"
    task "Research competitor pricing" -t thinking -P high
    task "Refactor auth module" -p influencekit -t code -P high

SHORTCUTS (add to ~/.zshrc):
    alias tq='task'
    alias tqo='task -p offerlab'
    alias tqi='task -p influencekit'
    alias tqc='task -t code'
    alias tqw='task -t writing'

EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -p|--project)
            PROJECT="$2"
            shift 2
            ;;
        -t|--type)
            TYPE="$2"
            shift 2
            ;;
        -P|--priority)
            PRIORITY="$2"
            shift 2
            ;;
        -b|--body)
            BODY="$2"
            shift 2
            ;;
        -h|--help)
            show_help
            exit 0
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

# Validate
if [[ -z "$TITLE" ]]; then
    echo -e "${RED}Error: Task description required${NC}"
    echo ""
    show_help
    exit 1
fi

# Build label arguments
LABELS=()

if [[ -n "$PROJECT" ]]; then
    case "$PROJECT" in
        offerlab|ol|o)
            LABELS+=("--label" "project:offerlab")
            ;;
        influencekit|ik|i)
            LABELS+=("--label" "project:influencekit")
            ;;
        personal|p)
            LABELS+=("--label" "project:personal")
            ;;
        *)
            LABELS+=("--label" "project:$PROJECT")
            ;;
    esac
fi

if [[ -n "$TYPE" ]]; then
    case "$TYPE" in
        code|c)
            LABELS+=("--label" "type:code")
            ;;
        writing|write|w)
            LABELS+=("--label" "type:writing")
            ;;
        thinking|think|t)
            LABELS+=("--label" "type:thinking")
            ;;
        *)
            LABELS+=("--label" "type:$TYPE")
            ;;
    esac
fi

if [[ -n "$PRIORITY" ]]; then
    case "$PRIORITY" in
        high|h|1)
            LABELS+=("--label" "priority:high")
            ;;
        low|l|3)
            LABELS+=("--label" "priority:low")
            ;;
    esac
fi

# Always add queued status
LABELS+=("--label" "status:queued")

# Build body argument
BODY_ARG=()
if [[ -n "$BODY" ]]; then
    BODY_ARG=("--body" "$BODY")
fi

# Create the issue
echo -e "${BLUE}Creating task...${NC}"

RESULT=$(gh issue create \
    --repo "$TASK_REPO" \
    --title "$TITLE" \
    "${LABELS[@]}" \
    "${BODY_ARG[@]}" \
    2>&1)

if [[ $? -eq 0 ]]; then
    # Extract issue number and URL from result
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
        echo -e "  ${YELLOW}(URL copied to clipboard)${NC}"
    fi
else
    echo -e "${RED}✗ Failed to create task${NC}"
    echo "$RESULT"
    exit 1
fi
