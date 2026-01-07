# Setup Guide

Complete installation guide for the Task Queue system.

## Prerequisites

- GitHub CLI (`gh`) installed and authenticated
- GitHub account with repo access

## 1. Fork/Clone the Repository

```bash
# Option A: Fork it
gh repo fork bborn/workflow --clone

# Option B: Create your own
gh repo create my-task-queue --private --clone
cd my-task-queue
# Then copy the files from this repo
```

## 2. Configure Repository Settings

### Required: GitHub Secrets

Go to your repo **Settings > Secrets and variables > Actions** and add:

| Secret | Required For | How to Get |
|--------|--------------|------------|
| `ANTHROPIC_API_KEY` | Writing & Thinking tasks | [console.anthropic.com](https://console.anthropic.com) > API Keys |
| `REPO_ACCESS_TOKEN` | Code tasks (cross-repo PRs) | GitHub Settings > Developer settings > Personal access tokens > Generate new token (classic) with `repo` scope |

### How to add secrets:

1. Go to `https://github.com/YOUR_USERNAME/YOUR_REPO/settings/secrets/actions`
2. Click "New repository secret"
3. Add each secret with exact name shown above

### Anthropic API Key

1. Go to [console.anthropic.com](https://console.anthropic.com)
2. Sign in or create account
3. Navigate to **API Keys**
4. Click **Create Key**
5. Copy the key (starts with `sk-ant-...`)
6. Add as `ANTHROPIC_API_KEY` secret in GitHub

### GitHub Personal Access Token

Required only for code tasks that create PRs in other repositories.

1. Go to [github.com/settings/tokens](https://github.com/settings/tokens)
2. Click **Generate new token (classic)**
3. Name: `task-queue-access`
4. Expiration: Choose based on preference
5. Scopes: Check `repo` (full control of private repositories)
6. Click **Generate token**
7. Copy the token
8. Add as `REPO_ACCESS_TOKEN` secret in GitHub

## 3. Create Labels

Run the setup script or create manually:

```bash
# Using the included script
./scripts/setup-labels.sh

# Or manually
gh label create "project:offerlab" --color 0366d6 --description "Offerlab work"
gh label create "project:influencekit" --color 28a745 --description "InfluenceKit work"
gh label create "project:personal" --color 6f42c1 --description "Personal projects"

gh label create "type:code" --color fbca04 --description "Code task, creates PR"
gh label create "type:writing" --color d93f0b --description "Writing task"
gh label create "type:thinking" --color 0e8a16 --description "Analysis/research task"

gh label create "status:queued" --color ededed --description "Waiting for processing"
gh label create "status:processing" --color 1d76db --description "Agent working"
gh label create "status:ready" --color 0e8a16 --description "Ready for review"
gh label create "status:blocked" --color d93f0b --description "Needs clarification"

gh label create "priority:high" --color b60205 --description "High priority"
gh label create "priority:low" --color c5def5 --description "Low priority"
```

## 4. Install CLI Tool

### Option A: Add to PATH

```bash
# Add to ~/.zshrc or ~/.bashrc
export PATH="$PATH:/path/to/workflow/scripts"
export TASK_REPO="YOUR_USERNAME/workflow"  # Change this!
```

### Option B: Symlink

```bash
ln -s /path/to/workflow/scripts/task.sh /usr/local/bin/task
```

### Option C: Shell function

Add to `~/.zshrc`:

```bash
export TASK_REPO="YOUR_USERNAME/workflow"

task() {
  /path/to/workflow/scripts/task.sh "$@"
}
```

Then reload: `source ~/.zshrc`

## 5. Install Claude Code Commands (Optional)

For Claude Code integration, copy the commands to your global Claude config:

```bash
# Create commands directory if needed
mkdir -p ~/.claude/commands

# Copy task commands
cp /path/to/workflow/claude-commands/*.md ~/.claude/commands/
```

Or manually create:

**~/.claude/commands/task.md:**
```markdown
Create a task in the workflow queue (bborn/workflow GitHub issues).

Input: $ARGUMENTS

Parse the input for:
- Task description (the main text)
- `-p` or `--project`: Project name (offerlab/ol, influencekit/ik, personal)
- `-t` or `--type`: Task type (code/c, writing/w, thinking/t)
- `-P` or `--priority`: Priority level (high/h, low/l)
- `-b` or `--body`: Additional details for the issue body

Create a GitHub issue with:
- Title: the task description
- Labels: Always include `status:queued`, plus any specified project/type/priority labels

Use `gh issue create --repo YOUR_USERNAME/workflow` to create the issue.
```

**~/.claude/commands/tasks.md:**
```markdown
List tasks from the workflow queue (YOUR_USERNAME/workflow GitHub issues).

Input: $ARGUMENTS

Parse optional filters:
- `-p` or `--project`: Filter by project
- `-t` or `--type`: Filter by type
- `-s` or `--status`: Filter by status (default: queued)

Use `gh issue list --repo YOUR_USERNAME/workflow` with appropriate `--label` flags.
```

## 6. Self-Hosted Runner (For Code Tasks)

Code tasks require a self-hosted runner with Claude Code installed.

### Why?

- GitHub-hosted runners don't have Claude Code
- Code tasks need to checkout your private repos
- Claude Code needs local filesystem access

### Setup

1. Go to repo **Settings > Actions > Runners**
2. Click **New self-hosted runner**
3. Follow instructions for your OS
4. Install Claude Code on the runner machine:
   ```bash
   npm install -g @anthropic-ai/claude-code
   ```
5. Authenticate Claude Code:
   ```bash
   claude auth
   ```

### Alternative: Skip Code Tasks

If you don't need automated code tasks, you can:
- Remove or disable the `process-code` job in `orchestrator.yml`
- Use writing/thinking tasks only (work on GitHub-hosted runners)
- Process code tasks manually with Claude Code locally

## 7. Customize for Your Projects

Edit `orchestrator.yml` to match your project repos:

```yaml
- name: Determine target repo
  id: repo
  run: |
    PROJECT="${{ needs.should-process.outputs.project }}"
    case "$PROJECT" in
      offerlab)
        echo "repo=YOUR_USERNAME/offerlab" >> $GITHUB_OUTPUT
        ;;
      influencekit)
        echo "repo=YOUR_USERNAME/influencekit" >> $GITHUB_OUTPUT
        ;;
      myproject)  # Add your own
        echo "repo=YOUR_USERNAME/myproject" >> $GITHUB_OUTPUT
        ;;
      *)
        echo "repo=" >> $GITHUB_OUTPUT
        ;;
    esac
```

## Verification

Test your setup:

```bash
# 1. Create a test task
task "Test task - delete me" -t thinking

# 2. Check it was created
gh issue list --repo YOUR_USERNAME/workflow

# 3. Watch the workflow run
gh run watch

# 4. Check the issue for results
gh issue view 1
```

## Troubleshooting

### "Resource not accessible by integration"

The workflow needs write permissions. Ensure `orchestrator.yml` has:

```yaml
permissions:
  issues: write
  contents: read
```

### "ANTHROPIC_API_KEY not set"

Add the secret in GitHub repo settings (see step 2).

### Code tasks not running

- Check you have a self-hosted runner online
- Verify `REPO_ACCESS_TOKEN` is set
- Ensure the token has `repo` scope

### Labels not found

Run `./scripts/setup-labels.sh` or create them manually.

## Cost Considerations

- **GitHub Actions**: Free for public repos, 2000 mins/month for private
- **Anthropic API**: ~$0.003 per 1K input tokens, ~$0.015 per 1K output tokens
  - Typical writing task: ~$0.05-0.10
  - Typical thinking task: ~$0.05-0.10
  - Code tasks: Varies by complexity
