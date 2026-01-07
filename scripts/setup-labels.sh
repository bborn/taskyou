#!/bin/bash
# Setup GitHub labels for the task queue
# Run once after creating the repo: ./scripts/setup-labels.sh

set -e

REPO="${1:-bborn/workflow}"

echo "Setting up labels for $REPO..."

# Delete default labels (optional - comment out if you want to keep them)
echo "Removing default labels..."
for label in bug documentation enhancement "good first issue" "help wanted" invalid question wontfix duplicate; do
    gh label delete "$label" --repo "$REPO" --yes 2>/dev/null || true
done

# Project labels (blue tones)
echo "Creating project labels..."
gh label create "project:offerlab" --color "0052CC" --description "Offerlab project" --repo "$REPO" --force
gh label create "project:influencekit" --color "0366D6" --description "InfluenceKit project" --repo "$REPO" --force
gh label create "project:personal" --color "1D76DB" --description "Personal project" --repo "$REPO" --force

# Type labels (purple tones)
echo "Creating type labels..."
gh label create "type:code" --color "5319E7" --description "Development work" --repo "$REPO" --force
gh label create "type:writing" --color "7057FF" --description "Content creation" --repo "$REPO" --force
gh label create "type:thinking" --color "8B5CF6" --description "Analysis & strategy" --repo "$REPO" --force

# Status labels (traffic light)
echo "Creating status labels..."
gh label create "status:queued" --color "FEF3C7" --description "Ready for processing" --repo "$REPO" --force
gh label create "status:processing" --color "FCD34D" --description "Currently being worked on" --repo "$REPO" --force
gh label create "status:ready" --color "10B981" --description "Complete - ready for review" --repo "$REPO" --force
gh label create "status:blocked" --color "EF4444" --description "Needs clarification" --repo "$REPO" --force

# Priority labels
echo "Creating priority labels..."
gh label create "priority:high" --color "DC2626" --description "High priority" --repo "$REPO" --force
gh label create "priority:low" --color "9CA3AF" --description "Low priority" --repo "$REPO" --force

echo "Done! Labels created for $REPO"
