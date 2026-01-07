# Bruno's Task Queue

A personal task queue using GitHub Issues as the single source of truth.

## Why GitHub Issues?

- **Already everywhere**: CLI (`gh`), mobile app, web, Claude MCP
- **One inbox**: All tasks regardless of project (Offerlab, InfluenceKit, personal)
- **Labels for routing**: `project:offerlab`, `project:influencekit`, `type:code`, `type:writing`
- **Built-in automation**: Actions trigger on issue creation
- **Free**: Unlimited private repo issues

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         INPUT                                    │
│                                                                  │
│   Terminal        Phone           Web            Claude          │
│   gh issue        GitHub App      github.com     "check my       │
│   create          + Siri          issues page    task queue"     │
│      │               │               │               │           │
│      └───────────────┴───────────────┴───────────────┘           │
│                              │                                   │
│                              ▼                                   │
│                    ┌──────────────────┐                          │
│                    │   task-queue     │                          │
│                    │   GitHub Repo    │                          │
│                    │                  │                          │
│                    │  Issues = Tasks  │                          │
│                    └────────┬─────────┘                          │
│                             │                                    │
└─────────────────────────────┼────────────────────────────────────┘
                              │
                              │ on: issues: opened
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      PROCESSING                                  │
│                                                                  │
│    ┌────────────────────────────────────────────────────────┐   │
│    │              GitHub Actions Workflow                    │   │
│    │                                                         │   │
│    │  1. Classify task (code/writing/thinking)              │   │
│    │  2. Identify target project from labels                │   │
│    │  3. Dispatch to appropriate handler                    │   │
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
│    PR in target       Comment on         Comment on            │
│    repo (Offerlab,    issue with         issue with            │
│    InfluenceKit)      content            analysis              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                        REVIEW                                    │
│                                                                  │
│    GitHub Issues filtered by label:                             │
│    • "status:ready" — completed, needs review                   │
│    • "status:blocked" — needs clarification                     │
│    • Group by project label for batch review                    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Labels

### Project Labels (where does this belong?)
- `project:offerlab` — Offerlab work
- `project:influencekit` — InfluenceKit work  
- `project:personal` — Personal projects
- `project:learning` — Learning/exploration

### Type Labels (what kind of task?)
- `type:code` — Software engineering, creates PRs
- `type:writing` — Content, emails, docs
- `type:thinking` — Analysis, research, strategy

### Status Labels (where is it?)
- `status:queued` — Waiting for processing (default)
- `status:processing` — Agent working on it
- `status:ready` — Done, needs human review
- `status:blocked` — Agent needs help

### Priority Labels
- `priority:high` — Do soon
- `priority:low` — Whenever

## Quick Start

### 1. Create the repo
```bash
gh repo create task-queue --private --description "Personal task queue"
cd task-queue
```

### 2. Create labels
```bash
# Project labels
gh label create "project:offerlab" --color 0366d6 --description "Offerlab work"
gh label create "project:influencekit" --color 28a745 --description "InfluenceKit work"
gh label create "project:personal" --color 6f42c1 --description "Personal projects"

# Type labels
gh label create "type:code" --color fbca04 --description "Code task, creates PR"
gh label create "type:writing" --color d93f0b --description "Writing task"
gh label create "type:thinking" --color 0e8a16 --description "Analysis/research task"

# Status labels
gh label create "status:queued" --color ededed --description "Waiting for processing"
gh label create "status:processing" --color 1d76db --description "Agent working"
gh label create "status:ready" --color 0e8a16 --description "Ready for review"
gh label create "status:blocked" --color d93f0b --description "Needs clarification"

# Priority
gh label create "priority:high" --color b60205 --description "High priority"
gh label create "priority:low" --color c5def5 --description "Low priority"
```

### 3. Add the shell alias
```bash
# Add to ~/.zshrc or ~/.bashrc
export PATH="$PATH:/path/to/workflow/scripts"
# Or create an alias:
alias task='/path/to/workflow/scripts/task.sh'
```

### 4. Use it
```bash
# Simple task
task "Fix login bug in auth module"

# With labels
task "Add Stripe webhook" --label "project:offerlab" --label "type:code"

# With body
task "Write investor update email" --label "type:writing" --body "Q4 results, new features, roadmap"
```

## Files

```
workflow/
├── README.md                    # This file
├── .github/
│   ├── workflows/
│   │   └── orchestrator.yml     # Main automation
│   └── ISSUE_TEMPLATE/
│       ├── code.yml             # Code task template
│       ├── writing.yml          # Writing task template
│       ├── thinking.yml         # Thinking task template
│       └── config.yml
└── scripts/
    ├── task.sh                  # CLI for creating tasks
    └── setup-labels.sh          # One-time label setup
```
