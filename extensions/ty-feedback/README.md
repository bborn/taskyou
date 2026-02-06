# ty-feedback

Embeddable feedback widget for deployed app prototypes. Users submit feedback through an in-app widget, which creates tasks in TaskYou for iterative development.

## Quick Start

```bash
cd extensions/ty-feedback
go build -o ty-feedback ./cmd

# Copy and edit config
cp config.example.yaml ~/.config/ty-feedback/config.yaml

# Start the server
./ty-feedback serve
```

## How It Works

```
App Prototype ──widget.js──▶ ty-feedback server ──ty CLI──▶ TaskYou
     │                            │
     │  POST /api/feedback        │  ty create "Bug: ..."
     │◀────── task #42 ──────────▶│
     │                            │
     └── user sees confirmation ──┘
```

1. Add the widget to your app with a script tag
2. Users click the feedback button to report bugs, request features, or ask questions
3. Each submission creates a task in TaskYou
4. Tasks appear in your queue for review and execution
5. Claude (or another executor) works on the task in an isolated worktree

## Embedding the Widget

Add this script tag to your app's HTML:

```html
<script src="http://your-server:8090/widget.js"></script>
```

With API key authentication:

```html
<script src="http://your-server:8090/widget.js" data-api-key="your-key"></script>
```

Position the button (default: bottom-right):

```html
<script src="http://your-server:8090/widget.js" data-position="bottom-left"></script>
```

Generate the snippet:

```bash
./ty-feedback snippet
```

## API Endpoints

### POST /api/feedback

Submit feedback (creates a task).

```json
{
  "title": "Login button broken",
  "body": "Clicking login does nothing on mobile Safari",
  "category": "bug",
  "url": "https://myapp.example.com/login"
}
```

Response:

```json
{
  "id": 42,
  "title": "[Bug report] Login button broken",
  "status": "backlog",
  "project": "myapp"
}
```

### GET /api/tasks

List tasks for the configured project.

### GET /api/tasks/:id

Get a specific task.

### POST /api/tasks/:id/input

Send input to a blocked task.

```json
{
  "input": "Go with option 2"
}
```

### GET /widget.js

Serves the embeddable JavaScript widget.

### GET /health

Health check.

## Configuration

Config lives at `~/.config/ty-feedback/config.yaml`:

```yaml
server:
  port: 8090
  project: myapp
  allowed_origins:
    - http://localhost:3000
  api_key: optional-secret
  default_tags: feedback
  auto_execute: false

taskyou:
  cli: ty
```

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `server.port` | 8090 | HTTP server port |
| `server.project` | feedback | TaskYou project for created tasks |
| `server.allowed_origins` | (all) | CORS allowed origins |
| `server.api_key` | (none) | Bearer token for API auth |
| `server.default_tags` | (none) | Tags added to all tasks |
| `server.auto_execute` | false | Queue tasks for immediate execution |
| `taskyou.cli` | ty | Path to ty binary |

## Deployment

### On the same server as TaskYou

Run ty-feedback alongside TaskYou on the server where your worktrees live:

```bash
# Start ty-feedback
./ty-feedback serve &

# Your app prototype runs on another port (e.g., 3000)
# Feedback widget submits to ty-feedback on port 8090
# ty-feedback creates tasks via ty CLI
# TaskYou daemon picks up tasks and executes in worktrees
```

### With systemd

```ini
[Unit]
Description=ty-feedback
After=network.target

[Service]
ExecStart=/path/to/ty-feedback serve
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```
