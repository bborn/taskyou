# VHS Testing for TaskYou

Automated visual testing and user persona simulation using [VHS](https://github.com/charmbracelet/vhs).

## Prerequisites

```sh
brew install vhs    # Installs VHS + ttyd + ffmpeg
make build          # Build the ty binary
```

## Quick Start

```sh
# Run all recordings (seeds isolated test DB automatically)
./vhs/run-all.sh

# Run just feature tapes or persona tapes
./vhs/run-all.sh tapes
./vhs/run-all.sh personas

# Run a single tape
vhs vhs/tapes/01-first-launch.tape
```

## How It Works

### Database Isolation

All VHS tests use `WORKTREE_DB_PATH` to point to an isolated test database at `/tmp/vhs-taskyou-test/tasks.db`. This **never touches your real TaskYou data**. The daemon PID file is also colocated with the test database, so test daemons don't conflict with your real one.

The `seed-data.sh` script creates:
- 4 projects (backend, frontend, infrastructure, product)
- 12 sample tasks across all projects and task types

### Feature Tapes (`tapes/`)

Focused recordings of specific features:

| Tape | Feature | What it captures |
|------|---------|-----------------|
| `00-smoke-test` | Launch | Minimal VHS + ty verification |
| `01-first-launch` | Dashboard | Initial view, help overlay, navigation |
| `02-create-task` | Task creation | New task form, field navigation |
| `03-navigate-kanban` | Navigation | Column switching, quick-select, task detail |
| `04-filter-search` | Filter & Search | Filter bar, project filter, command palette |
| `05-settings` | Settings | Theme/project/type configuration |
| `06-keyboard-power-user` | Shortcuts | Rapid keyboard workflow, status changes |

### Persona Tapes (`personas/`)

Full user journey simulations:

| Persona | Who | Scenario |
|---------|-----|----------|
| `newcomer` | Sarah, new developer | First launch, explore, create first task |
| `power-user` | Alex, senior dev | Morning triage, rapid shortcuts, filtering |
| `project-manager` | Jordan, PM | Weekly review, project-by-project filtering |
| `developer` | Mike, developer | Pick task, view details, search, status change |

## LLM Analysis Workflow

After running tapes, generate an analysis prompt:

```sh
# Generate analysis prompt with screenshot paths
./vhs/analyze.sh > /tmp/vhs-analysis-prompt.md

# List all captured screenshots
./vhs/analyze.sh --list
```

Then have an LLM agent read the screenshots:

```
Read each screenshot in vhs/output/screenshots/ and analyze the UX
following the framework in vhs/analyze.sh
```

The analysis framework evaluates:
1. **Visual Clarity** — information hierarchy, labels, spacing
2. **Discoverability** — are actions visible? shortcuts communicated?
3. **Feedback & State** — does UI show current state clearly?
4. **Efficiency** — can power users work fast?
5. **Edge Cases** — rendering issues, overflow, missing elements

## Output

Generated files go to `vhs/output/` (gitignored):

```
vhs/output/
├── gifs/           # Animated GIF recordings
└── screenshots/    # PNG snapshots at key interaction points
```

## Writing New Tapes

Use the VHS tape syntax. Key commands:

```tape
# Setup
Set Shell bash
Set Width 1400
Set Height 800
Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env PATH "./bin:${PATH}"

# Interaction
Type "ty"              # Type text
Enter                  # Press enter
Sleep 2s               # Wait
Down / Up / Left / Right  # Arrow keys
Ctrl+c                 # Control combinations
Tab                    # Tab key
Escape                 # Escape key

# Capture
Screenshot path.png    # Take screenshot
Output recording.gif   # Set GIF output path
```

Always include the `WORKTREE_DB_PATH` env var for isolation. Run `./vhs/seed-data.sh` first if running individual tapes.
