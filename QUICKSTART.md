# Quick Start: GitHub Task Queue

Get running in 15 minutes.

---

## Step 1: Create the repo (2 min)

```bash
# Create the task queue repo
gh repo create task-queue --private --clone
cd task-queue
```

---

## Step 2: Create labels (2 min)

```bash
# Run this once to set up all labels
gh label create "project:offerlab" --color 0366d6
gh label create "project:influencekit" --color 28a745
gh label create "project:personal" --color 6f42c1

gh label create "type:code" --color fbca04
gh label create "type:writing" --color d93f0b
gh label create "type:thinking" --color 0e8a16

gh label create "status:queued" --color ededed
gh label create "status:processing" --color 1d76db
gh label create "status:ready" --color 0e8a16
gh label create "status:blocked" --color b60205

gh label create "priority:high" --color b60205
gh label create "priority:low" --color c5def5
```

---

## Step 3: Add the CLI (2 min)

Add to your `~/.zshrc`:

```bash
# Task queue command
export TASK_REPO="YOUR_USERNAME/task-queue"

task() {
  if [[ -z "$1" ]]; then
    echo "Usage: task \"description\" [-p project] [-t type] [-P priority]"
    return 1
  fi
  
  local title="$1"
  shift
  
  local labels=("--label" "status:queued")
  
  while [[ $# -gt 0 ]]; do
    case $1 in
      -p|--project)
        labels+=("--label" "project:$2")
        shift 2
        ;;
      -t|--type)
        labels+=("--label" "type:$2")
        shift 2
        ;;
      -P|--priority)
        labels+=("--label" "priority:$2")
        shift 2
        ;;
      -b|--body)
        labels+=("--body" "$2")
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  
  gh issue create --repo "$TASK_REPO" --title "$title" "${labels[@]}"
}

# Shortcuts
alias tq='task'
alias tqo='task -p offerlab'      # Offerlab tasks
alias tqi='task -p influencekit'  # InfluenceKit tasks
alias tqc='task -t code'          # Code tasks
alias tqw='task -t writing'       # Writing tasks
alias tqt='task -t thinking'      # Thinking tasks

# View tasks
alias tasks='gh issue list --repo "$TASK_REPO" --label "status:queued"'
alias tasks-ready='gh issue list --repo "$TASK_REPO" --label "status:ready"'
alias tasks-all='gh issue list --repo "$TASK_REPO" --state all'
```

Then reload:
```bash
source ~/.zshrc
```

---

## Step 4: Test it (1 min)

```bash
# Create a test task
task "Test task from CLI"

# Create with labels
task "Add user avatars" -p influencekit -t code

# Create a writing task
task "Draft weekly update email" -t writing

# View your queue
tasks
```

---

## Step 5: Mobile access

1. Install **GitHub** app on your phone
2. Open your `task-queue` repo
3. Tap **Issues** → **New Issue**
4. Type your task, add labels

That's it for mobile. No shortcuts needed—GitHub app works great.

---

## Step 6: Claude access (already done!)

You have the GitHub MCP connected. Just ask Claude:

- "What's in my task queue?"
- "Show me my ready-for-review tasks"
- "Create a task: Add pagination to user list, project influencekit, type code"

---

## Done! Start using it.

### Daily workflow:

**Add tasks** (from anywhere):
```bash
task "Fix the login bug" -p offerlab -t code
task "Write investor update" -t writing  
task "Analyze pricing options" -t thinking -P high
```

**Check queue**:
```bash
tasks          # Queued tasks
tasks-ready    # Completed, need review
```

**Review** (web or CLI):
```bash
# Open in browser
gh issue view 123 --web

# Or view in terminal
gh issue view 123
```

---

## Optional: Add automation

The `orchestrator.yml` workflow automates processing. To enable:

1. Copy `.github/workflows/orchestrator.yml` to your repo
2. Add secrets in repo settings:
   - `ANTHROPIC_API_KEY` — for writing/thinking tasks
   - `REPO_ACCESS_TOKEN` — PAT with repo access for code tasks
3. For code tasks: Set up a self-hosted runner with Claude Code

But you can use the queue manually first—just process tasks yourself or with Conductor, and update labels when done.

---

## Label cheatsheet

| Flag | Label | Use |
|------|-------|-----|
| `-p offerlab` | `project:offerlab` | Offerlab work |
| `-p influencekit` | `project:influencekit` | InfluenceKit work |
| `-p personal` | `project:personal` | Personal stuff |
| `-t code` | `type:code` | Creates PRs |
| `-t writing` | `type:writing` | Content/emails |
| `-t thinking` | `type:thinking` | Analysis |
| `-P high` | `priority:high` | Do soon |

---

## Filtering in GitHub web

Useful searches to bookmark:

```
# Queued tasks
is:issue is:open label:status:queued

# Ready for review
is:issue is:open label:status:ready

# Blocked (need your input)
is:issue is:open label:status:blocked

# Offerlab code tasks
is:issue is:open label:project:offerlab label:type:code

# High priority
is:issue is:open label:priority:high
```
