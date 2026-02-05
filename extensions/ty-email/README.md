# ty-email

Email interface for TaskYou. Send emails to create tasks, reply to provide input, receive status updates.

## Quick Start

```bash
# Build
cd extensions/ty-email
go build -o ty-email ./cmd

# Setup (interactive wizard)
./ty-email init

# Run
./ty-email serve
```

## How It Works

1. You email `yourname+ty@gmail.com` from your phone/computer
2. Gmail filter routes it to a `ty-email` label
3. ty-email polls that label via IMAP
4. Claude classifies your intent (create task, provide input, query status)
5. ty-email executes the appropriate `ty` command
6. You get a reply email with confirmation

```
You ──email──▶ Gmail ──IMAP──▶ ty-email ──▶ Claude (classify)
                                    │
                                    ▼
                              ty CLI (execute)
                                    │
                                    ▼
You ◀──reply── Gmail ◀──SMTP── ty-email
```

## Setup

Run the interactive wizard:

```bash
./ty-email init
```

This walks you through:

1. **Gmail address** - Your email, generates a `+ty` alias for tasks
2. **App password** - Create one at https://myaccount.google.com/apppasswords
3. **Gmail filter** - Routes emails to the `ty-email` label
4. **Claude API key** - For intent classification
5. **TaskYou CLI** - Path to `ty` binary

### Gmail Filter Setup

The wizard explains this, but here's the summary:

1. Go to Gmail Settings > Filters
2. Create filter: `To: yourname+ty@gmail.com`
3. Actions: Skip Inbox, Apply label `ty-email`

Now emails to `yourname+ty@gmail.com` go to that label, and ty-email processes them.

## Running

### Foreground (for testing)

```bash
./ty-email serve
```

### Background with launchd (macOS)

Create `~/Library/LaunchAgents/com.ty-email.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://schemas.apple.com/dtds/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.ty-email</string>
    <key>ProgramArguments</key>
    <array>
        <string>/path/to/ty-email</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/ty-email.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/ty-email.err</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>ANTHROPIC_API_KEY</key>
        <string>sk-ant-...</string>
    </dict>
</dict>
</plist>
```

Then:

```bash
launchctl load ~/Library/LaunchAgents/com.ty-email.plist
launchctl start com.ty-email

# Check status
launchctl list | grep ty-email

# View logs
tail -f /tmp/ty-email.log

# Stop
launchctl stop com.ty-email
launchctl unload ~/Library/LaunchAgents/com.ty-email.plist
```

### Background with systemd (Linux)

Create `~/.config/systemd/user/ty-email.service`:

```ini
[Unit]
Description=ty-email daemon
After=network.target

[Service]
ExecStart=/path/to/ty-email serve
Restart=always
RestartSec=10
Environment=ANTHROPIC_API_KEY=sk-ant-...

[Install]
WantedBy=default.target
```

Then:

```bash
systemctl --user daemon-reload
systemctl --user enable ty-email
systemctl --user start ty-email

# Check status
systemctl --user status ty-email

# View logs
journalctl --user -u ty-email -f
```

## Commands

```bash
# Interactive setup
ty-email init

# Run daemon (polls every 30s)
ty-email serve

# Process once and exit
ty-email process

# Test classification with sample email
echo -e "Subject: Fix the bug\n\nThe login is broken" | ty-email test

# Check status
ty-email status
```

## Usage Examples

### Create a Task

Email to `yourname+ty@gmail.com`:

```
Subject: Fix the checkout page

Users are seeing 500 errors when checking out
```

Reply you'll receive:

```
Created task: Fix the checkout page
Project: personal
Status: backlog

I've created this task for you. Run `ty execute` when ready.
```

### Create and Execute Immediately

```
Subject: Fix the checkout page and run it

Users are seeing 500 errors
```

The "and run it" triggers immediate execution.

### Provide Input to Blocked Task

When a task needs input, you get an email. Just reply:

```
Go with option 1, that's the root cause.
```

ty-email routes your input to the correct task.

### Query Status

```
Subject: What's happening with the checkout fix?
```

You'll get a status update on matching tasks.

## Configuration

Config lives at `~/.config/ty-email/config.yaml`:

```yaml
adapter:
  type: imap
  imap:
    server: imap.gmail.com:993
    username: you@gmail.com
    password_cmd: echo 'your-app-password'
    folder: ty-email
    poll_interval: 30s

smtp:
  server: smtp.gmail.com:587
  username: you@gmail.com
  password_cmd: echo 'your-app-password'
  from: you@gmail.com

classifier:
  provider: claude
  model: claude-sonnet-4-20250514
  api_key_cmd: echo $ANTHROPIC_API_KEY

taskyou:
  cli: ty

security:
  allowed_senders:
    - you@gmail.com  # Only process emails from yourself
```

## Security

- **Sender whitelist** - Only emails from `security.allowed_senders` are processed. Random people can't create tasks by emailing your +ty address.
- **No code execution** - ty-email only calls `ty` CLI commands. The LLM just classifies intent.
- **Local credentials** - Email passwords and API keys stay local, never sent to LLM.
- **State tracking** - Processed emails are tracked in `~/.local/share/ty-email/state.db` to avoid duplicates.

## Troubleshooting

### "no API key configured"

Your `ANTHROPIC_API_KEY` env var isn't set in the shell running ty-email. Either:
- Run `source ~/.zshrc` before `ty-email serve`
- Add the env var to your launchd/systemd config
- Use `api_key_cmd` with a command that outputs the key

### "SMTP not configured"

The `smtp` section in config is missing or incomplete. Re-run `ty-email init`.

### Emails not being processed

1. Check the Gmail filter is correct (`To: yourname+ty@gmail.com` → label `ty-email`)
2. Check ty-email can connect: `ty-email serve` should show "connected to IMAP server"
3. Check the email is from an allowed sender (see `security.allowed_senders`)

### Test without sending real email

```bash
echo -e "Subject: Test task\n\nThis is a test" | ty-email test
```
