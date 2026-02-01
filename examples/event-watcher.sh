#!/bin/bash
# Event Watcher Example
# Demonstrates real-time event streaming from TaskYou

set -euo pipefail

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}TaskYou Event Watcher${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Watching for task events (Ctrl+C to stop)..."
echo ""

# Stream events and format them
ty events watch | while IFS= read -r line; do
  # Skip connection messages
  if [[ "$line" =~ ^# ]]; then
    echo -e "${BLUE}$line${NC}"
    continue
  fi
  
  # Parse JSON event
  event_type=$(echo "$line" | jq -r '.type // empty')
  task_id=$(echo "$line" | jq -r '.task_id // empty')
  message=$(echo "$line" | jq -r '.message // empty')
  timestamp=$(echo "$line" | jq -r '.timestamp // empty' | cut -d'T' -f2 | cut -d'.' -f1)
  
  # Skip if not a valid event
  if [ -z "$event_type" ]; then
    continue
  fi
  
  # Color-code by event type
  case "$event_type" in
    task.created)
      color="$GREEN"
      icon="✨"
      ;;
    task.queued|task.started|task.processing)
      color="$BLUE"
      icon="▶️"
      ;;
    task.completed)
      color="$GREEN"
      icon="✅"
      ;;
    task.blocked)
      color="$YELLOW"
      icon="⏸️"
      ;;
    task.failed)
      color="$RED"
      icon="❌"
      ;;
    task.status.changed)
      color="$BLUE"
      icon="🔄"
      ;;
    task.updated)
      color="$BLUE"
      icon="📝"
      ;;
    task.deleted)
      color="$RED"
      icon="🗑️"
      ;;
    *)
      color="$NC"
      icon="•"
      ;;
  esac
  
  # Format and print
  printf "${color}%s [%s] %s #%s: %s${NC}\n" \
    "$icon" \
    "$timestamp" \
    "$event_type" \
    "$task_id" \
    "$message"
done
