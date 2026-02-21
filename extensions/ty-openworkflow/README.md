# ty-openworkflow

A TaskYou sidecar for spawning workflows on ephemeral compute platforms using the [OpenWorkflow](https://github.com/openworkflowdev/openworkflow) architecture.

## Overview

ty-openworkflow enables tasks to spawn durable, fault-tolerant workflows on various compute platforms:

- **Local exec**: Run workflows as local processes (development/testing)
- **Docker**: Run workflows in isolated containers
- **Cloudflare Workers**: Run workflows on the edge (serverless)

The extension follows the OpenWorkflow architecture pattern, providing:

- **Deterministic replay**: Steps are memoized and can be replayed from any point
- **Durable sleep**: Workflows can pause and resume across restarts
- **Fault tolerance**: Failed workers are automatically recovered
- **TaskYou integration**: Workflow runs are linked to tasks for tracking

## Installation

```bash
cd extensions/ty-openworkflow
go build -o ty-openworkflow ./cmd
```

Or install directly:

```bash
go install github.com/bborn/workflow/extensions/ty-openworkflow/cmd@latest
```

## Quick Start

1. Initialize configuration:

```bash
ty-openworkflow init
```

2. Deploy a workflow:

```bash
cat <<'EOF' | ty-openworkflow deploy my-workflow -f -
async function workflow(input, { step, sleep }) {
  const greeting = await step("greet", () => {
    return `Hello, ${input.name}!`;
  });

  await sleep("wait", 5000);

  const result = await step("complete", () => {
    return { message: greeting, timestamp: Date.now() };
  });

  return result;
}
EOF
```

3. Start a workflow run:

```bash
ty-openworkflow start my-workflow -i '{"name": "World"}'
```

4. Check status:

```bash
ty-openworkflow status <run-id>
```

## Workflow Code

Workflows are functions that receive input and a context object with `step` and `sleep` helpers:

### JavaScript/Node.js

```javascript
async function workflow(input, { step, sleep }) {
  // Steps are memoized - safe to replay
  const data = await step("fetch-data", async () => {
    const res = await fetch(input.url);
    return res.json();
  });

  // Durable sleep - survives restarts
  await sleep("wait", 60000);  // 1 minute

  // Process the data
  const result = await step("process", () => {
    return transform(data);
  });

  return result;
}
```

### Python

```python
def workflow(input, ctx):
    # Steps are memoized
    data = ctx.step("fetch-data", lambda: fetch_data(input["url"]))

    # Durable sleep
    ctx.sleep("wait", 60)  # 60 seconds

    # Process
    result = ctx.step("process", lambda: transform(data))

    return result
```

## Commands

### Deploy a workflow

```bash
ty-openworkflow deploy <workflow-id> [flags]

Flags:
  -f, --file string        Workflow code file (or pipe to stdin)
  -a, --adapter string     Compute adapter (exec, docker, cloudflare)
      --name string        Workflow name
      --description string Workflow description
      --version string     Workflow version (default "1.0.0")
      --runtime string     Runtime (node, python) (default "node")
```

### Start a workflow run

```bash
ty-openworkflow start <workflow-id> [flags]

Flags:
  -i, --input string       Input JSON
      --input-file string  Input JSON file
  -t, --task string        Task title (creates linked task)
      --create-task        Create a linked task
```

### Check run status

```bash
ty-openworkflow status <run-id> [flags]

Flags:
      --json    Output as JSON
```

### List workflows and runs

```bash
ty-openworkflow list workflows
ty-openworkflow list runs [flags]

Flags:
  -w, --workflow string  Filter by workflow ID
  -s, --status string    Filter by status
  -n, --limit int        Max results (default 20)
```

### Cancel a run

```bash
ty-openworkflow cancel <run-id>
```

### Start the server

```bash
ty-openworkflow serve
```

Starts the webhook server and background polling for workflow status updates.

## Configuration

Configuration file: `~/.config/ty-openworkflow/config.yaml`

```yaml
data_dir: ~/.config/ty-openworkflow
default_adapter: exec

adapters:
  exec:
    enabled: true
    work_dir: ~/.config/ty-openworkflow/exec

  docker:
    enabled: false
    work_dir: ~/.config/ty-openworkflow/docker
    network: ""

  cloudflare:
    enabled: false
    account_id: ""
    api_token_cmd: "op read 'op://Private/Cloudflare/api_token'"
    namespace: ""

webhook:
  enabled: false
  port: 8765
  host: localhost
  path: /webhook
  external_url: ""

taskyou:
  cli: ty
  project: ""
  auto_create_tasks: true

poll_interval: 30s
```

## Compute Adapters

### Local Exec

Runs workflows as local Node.js or Python processes. Best for development and testing.

```yaml
adapters:
  exec:
    enabled: true
    work_dir: ~/.config/ty-openworkflow/exec
```

### Docker

Runs workflows in isolated Docker containers. Requires Docker installed.

```yaml
adapters:
  docker:
    enabled: true
    work_dir: ~/.config/ty-openworkflow/docker
    network: my-network  # Optional
```

### Cloudflare Workers

Runs workflows on Cloudflare's edge network. Requires:
- Cloudflare account
- API token with Workers permissions
- KV namespace for state

```yaml
adapters:
  cloudflare:
    enabled: true
    account_id: your-account-id
    api_token_cmd: "op read 'op://Private/Cloudflare/api_token'"
    namespace: your-kv-namespace
```

## TaskYou Integration

Workflows can be linked to TaskYou tasks:

```bash
# Create task automatically
ty-openworkflow start my-workflow -i '{"data": "value"}' --create-task

# Specify task title
ty-openworkflow start my-workflow -t "Process data job"
```

When a workflow completes or fails, the linked task is updated automatically.

## Architecture

```
                                    ┌─────────────────────┐
                                    │  Compute Platform   │
                                    │  (exec/docker/cf)   │
                                    └──────────┬──────────┘
                                               │
┌──────────────┐    ┌──────────────┐    ┌──────┴──────┐
│   TaskYou    │◄───│ty-openworkflow│◄───│   Webhook   │
│  (via CLI)   │    │   (sidecar)   │    │  Callback   │
└──────────────┘    └──────────────┘    └─────────────┘
       ▲                   │
       │                   │
       └───────────────────┘
         Task Updates

Components:
- Compute Adapters: Interface to execution platforms
- Runner: Orchestrates workflow deployment and execution
- Bridge: Communicates with TaskYou via CLI
- State: SQLite database for tracking workflows and runs
- Webhook: Receives completion callbacks from compute platforms
```

## OpenWorkflow Concepts

### Deterministic Replay

Each workflow step is memoized. When a workflow is replayed (e.g., after a crash), completed steps return their cached results without re-executing.

### Durable Sleep

The `sleep()` function creates a checkpoint. The workflow can be stopped and resumed later, continuing from where it left off.

### Step Types

- `step(name, fn)`: Execute arbitrary code, cache result
- `sleep(name, duration)`: Pause execution durably

### Fault Tolerance

Workers heartbeat while executing. If a worker crashes, another worker can pick up the workflow and replay from the last checkpoint.

## License

MIT
