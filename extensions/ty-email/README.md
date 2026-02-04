# ty-email

Email interface for TaskYou. Send emails to create tasks, reply to provide input, receive notifications when tasks need attention.

## Architecture

```
Email Provider (Gmail/IMAP/Webhook)
         │
         ▼
    ┌─────────┐
    │ty-email │──▶ LLM API (classify intent)
    │         │──▶ ty CLI (execute actions)
    │         │──▶ SMTP/API (send replies)
    └─────────┘
         │
         ▼
      TaskYou
```

ty-email is a **sidecar** that:
1. Receives emails via adapter (Gmail OAuth, IMAP, or webhook)
2. Uses LLM API to understand natural language → TaskYou commands
3. Executes commands via `ty` CLI
4. Sends reply emails with status/confirmations

## Installation

```bash
cd extensions/ty-email
go build -o ty-email ./cmd
```

## Configuration

```yaml
# ~/.config/ty-email/config.yaml

# Email input - pick one adapter
adapter:
  type: gmail  # gmail, imap, or webhook
  gmail:
    credentials_file: ~/.config/ty-email/gmail-credentials.json
    token_file: ~/.config/ty-email/gmail-token.json
  # imap:
  #   server: imap.fastmail.com:993
  #   username: you@domain.com
  #   password_cmd: "op read 'op://Private/Fastmail/password'"
  # webhook:
  #   listen: :8080
  #   secret: ${WEBHOOK_SECRET}

# Email output
smtp:
  server: smtp.gmail.com:587
  username: you@gmail.com
  password_cmd: "op read 'op://Private/Gmail/app-password'"
  from: you@gmail.com

# LLM for understanding emails
classifier:
  provider: claude  # claude, openai, ollama
  model: claude-sonnet-4-20250514  # or claude-3-5-haiku for cheaper
  api_key_cmd: "op read 'op://Private/Anthropic/api_key'"

# TaskYou integration
taskyou:
  cli: ty  # path to ty binary

# Optional: route emails to projects
routing:
  rules:
    - match: "from:support@"
      project: customer-support
    - match: "subject:[urgent]"
      execute: true  # auto-queue for execution
  default_project: personal
```

## Usage

```bash
# Run as daemon (polls for emails, processes them)
ty-email serve

# Process once and exit
ty-email process

# Test with stdin
echo -e "Subject: Fix the login bug\n\nUsers can't log in on mobile" | ty-email test

# Check status
ty-email status

# Auth setup for Gmail
ty-email auth gmail
```

## How It Works

### Creating Tasks

Send an email:
```
To: tasks@yourdomain.com
Subject: The checkout page is broken

Users are reporting 500 errors when they try to check out.
See attached screenshot.
```

ty-email classifies this as a new task and runs:
```bash
ty create "Fix checkout page 500 errors" --body "Users reporting..." --project website
```

You get a reply:
```
Created task #47: Fix checkout page 500 errors
Status: backlog
Project: website
```

### Providing Input

When a task is blocked (needs your input), you get an email:
```
From: tasks@yourdomain.com
Subject: Re: Fix checkout page 500 errors

[Task #47 needs input]

I found two approaches:
1. Fix the null pointer in the cart service
2. Add better error handling throughout

Which should I do?
```

Just reply:
```
Go with option 1, that's the root cause.
```

ty-email sends your input to the task:
```bash
ty input 47 "Go with option 1, that's the root cause."
```

### Checking Status

```
Subject: What's the status of the checkout fix?
```

ty-email recognizes this as a status query and replies with task details.

## Email Threading

ty-email maintains its own state to track email threads → task mappings. This is stored in `~/.local/share/ty-email/state.db` (SQLite).

When you reply to an email thread, ty-email knows which task it relates to.

## Security Notes

- ty-email only calls `ty` CLI commands - it never executes arbitrary code
- The LLM classifier just returns structured JSON (action type, parameters)
- All actual code execution happens in TaskYou's sandboxed executors
- Email credentials are stored locally, never sent to LLM
