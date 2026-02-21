# ty-sandbox

HTTP/SSE server for running coding agents in cloud sandboxes. Port of [rivet-dev/sandbox-agent](https://github.com/rivet-dev/sandbox-agent) to Go, integrated with TaskYou.

## Install

```sh
go build -o ty-sandbox ./cmd/main.go
```

Or with Docker:

```sh
docker build -t ty-sandbox .
docker run -p 8080:8080 -e ANTHROPIC_API_KEY=sk-... ty-sandbox
```

## Usage

Start the server:

```sh
ty-sandbox --addr :8080
```

With authentication:

```sh
ty-sandbox --addr :8080 --auth-token your-secret
```

## API

### Health

```
GET /v1/health
```

### Agents

List agents:

```
GET /v1/agents
```

Install an agent:

```
POST /v1/agents/{agent}/install
```

### Sessions

Create a session:

```
POST /v1/sessions/{session_id}

{
  "agent": "claude-code",
  "prompt": "Fix the login bug",
  "work_dir": "/workspace"
}
```

List sessions:

```
GET /v1/sessions
```

Send a message:

```
POST /v1/sessions/{session_id}/messages

{"message": "Also update the tests"}
```

Get events (polling):

```
GET /v1/sessions/{session_id}/events
GET /v1/sessions/{session_id}/events?after_sequence=5
```

Stream events (SSE):

```
GET /v1/sessions/{session_id}/events/sse
```

Terminate a session:

```
POST /v1/sessions/{session_id}/terminate
```

### Human-in-the-Loop

Reply to a question:

```
POST /v1/sessions/{session_id}/questions/{question_id}/reply

{"answer": "Yes, proceed"}
```

Reject a question:

```
POST /v1/sessions/{session_id}/questions/{question_id}/reject
```

Reply to a permission request:

```
POST /v1/sessions/{session_id}/permissions/{permission_id}/reply

{"allow": true}
```

## Event Schema

Events follow the universal schema from sandbox-agent. Each event has:

- `event_id` - Unique identifier
- `session_id` - Session this event belongs to
- `type` - Event type (see below)
- `source` - `"agent"` or `"daemon"`
- `sequence` - Monotonically increasing counter
- `time` - ISO 8601 timestamp
- `data` - Type-specific payload

Event types:

| Type | Description |
|------|-------------|
| `session.started` | Session initialized |
| `session.ended` | Session terminated |
| `turn.started` | Conversation turn began |
| `turn.ended` | Conversation turn ended |
| `item.started` | Message or tool call began |
| `item.delta` | Streaming content update |
| `item.completed` | Message or tool call finished |
| `question.requested` | Agent needs user input |
| `question.resolved` | Question was answered |
| `permission.requested` | Tool execution needs approval |
| `permission.resolved` | Permission was granted/denied |
| `error` | Error occurred |

## Supported Agents

| Agent | ID | Status |
|-------|----|--------|
| Claude Code | `claude-code` | Supported |
| Mock | `mock` | For testing |

## TaskYou Integration

When the `ty` CLI is available, sessions can create/update TaskYou tasks. Set `--ty-path` or have `ty` on your PATH.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `SANDBOX_AUTH_TOKEN` | Bearer token for API auth |
| `SANDBOX_WORK_DIR` | Default working directory |
| `ANTHROPIC_API_KEY` | API key for Claude Code |
