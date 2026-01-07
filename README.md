# Bruno's Task Queue

A personal task queue using GitHub Issues + Claude Code on a self-hosted runner.

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                         INPUT                                    │
│                                                                  │
│   Terminal           Phone            Web            Claude      │
│   task "..."         GitHub App       github.com     Desktop     │
│      │                  │                │              │        │
│      └──────────────────┴────────────────┴──────────────┘        │
│                              │                                   │
│                              ▼                                   │
│                    ┌──────────────────┐                          │
│                    │  bborn/workflow  │                          │
│                    │   GitHub Repo    │                          │
│                    │  Issues = Tasks  │                          │
│                    └────────┬─────────┘                          │
│                             │                                    │
└─────────────────────────────┼────────────────────────────────────┘
                              │ on: issues: labeled
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      PROCESSING                                  │
│                                                                  │
│    ┌────────────────────────────────────────────────────────┐   │
│    │           Self-Hosted Runner (Hetzner VPS)             │   │
│    │                                                         │   │
│    │  Claude Code processes tasks using Claude Max sub      │   │
│    │  No API costs - just ~€4/month for the server          │   │
│    │                                                         │   │
│    └────────────────────────────────────────────────────────┘   │
│                             │                                    │
│         ┌───────────────────┼───────────────────┐               │
│         ▼                   ▼                   ▼               │
│    ┌─────────┐        ┌─────────┐        ┌─────────┐           │
│    │  Code   │        │ Writing │        │Thinking │           │
│    │  Agent  │        │  Agent  │        │  Agent  │           │
│    └────┬────┘        └────┬────┘        └────┬────┘           │
│         │                  │                  │                 │
│         ▼                  ▼                  ▼                 │
│    PR in target       Comment with       Comment with          │
│    repo               content            analysis              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Usage

### Create Tasks

```bash
# Simple task (auto-triaged by Claude)
task "Add dark mode to the app"

# With type
task "Fix the login redirect bug" -t code -p offerlab
task "Write investor update email" -t writing
task "Should we use Redis or Postgres for caching?" -t thinking

# With priority
task "Server is down" -t code -p offerlab -P high

# With details
task "Write welcome email" -t writing -b "For new users, friendly tone, mention key features"
```

### List Tasks

```bash
task list                      # All open tasks
task list -p offerlab          # Filter by project
task list -s ready             # Filter by status
task list -t code              # Filter by type
task list -a                   # Include closed
```

### Review & Close

```bash
task review                    # List tasks ready for review
task review --open             # Open all ready tasks in browser
task review 42                 # Open specific task in browser
task close 42                  # Close a task
```

## Labels

| Label | Purpose |
|-------|---------|
| `project:offerlab` | Offerlab work |
| `project:influencekit` | InfluenceKit work |
| `project:personal` | Personal projects |
| `type:code` | Creates PRs in target repo |
| `type:writing` | Generates content |
| `type:thinking` | Analysis & strategy |
| `status:queued` | Waiting to be processed |
| `status:processing` | Claude working on it |
| `status:ready` | Done, needs review |
| `status:blocked` | Needs clarification |
| `priority:high` | Do soon |
| `priority:low` | Whenever |

## Setup

### 1. Provision Server

Create a Hetzner VPS (CX22, ~€4/month, Ubuntu 24.04).

### 2. Run Setup Script

```bash
ssh root@YOUR_SERVER_IP

# Install everything (Node.js, Claude Code, GitHub Runner, Tailscale)
curl -fsSL https://raw.githubusercontent.com/bborn/workflow/main/scripts/setup-runner.sh | bash
```

Or if repo is private, copy and run `scripts/setup-runner.sh` manually.

### 3. Connect Tailscale

```bash
sudo tailscale up
# Open the auth link and approve
```

### 4. Configure GitHub Runner

Get a token from: https://github.com/bborn/workflow/settings/actions/runners/new

```bash
sudo su - runner
cd ~/actions-runner
./config.sh --url https://github.com/bborn/workflow --token YOUR_TOKEN
sudo ./svc.sh install
sudo ./svc.sh start
```

### 5. Authenticate Claude

```bash
claude auth login
# Follow browser prompts with your Claude Max account
```

### 6. Add CLI to Your Path

On your local machine:

```bash
# Add to ~/.zshrc or ~/.bashrc
export PATH="$PATH:/path/to/workflow/scripts"
```

## Cost

| Item | Cost |
|------|------|
| Hetzner CX22 | ~€4/month |
| Claude Max | Your existing subscription |
| GitHub | Free (private repo) |
| **Total** | **~€4/month** |

## Files

```
workflow/
├── README.md                        # This file
├── .github/
│   ├── workflows/
│   │   └── orchestrator.yml         # Main automation
│   └── ISSUE_TEMPLATE/
│       ├── code.yml
│       ├── writing.yml
│       └── thinking.yml
└── scripts/
    ├── task.sh                      # CLI tool
    ├── setup-runner.sh              # Server provisioning
    └── setup-labels.sh              # One-time label setup
```

## Remote Access

After Tailscale setup, access your runner from anywhere:

```bash
ssh cloud-claude                     # From laptop
# Or use Termius/SSH app on phone
```
