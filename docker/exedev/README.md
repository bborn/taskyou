# TaskYou on exe.dev

Run TaskYou instantly on exe.dev.

## Quick Start

```bash
ssh exe.dev 'new --name=tasks --prompt="Run: curl -fsSL taskyou.dev/install.sh | bash"'
```

Then connect:

```bash
ssh tasks.exe.xyz
~/.local/bin/task
```

## How It Works

1. Creates a new exe.dev VM with the stock `exeuntu` image
2. Shelley (exe.dev's AI) runs the install script automatically
3. TaskYou is installed to `~/.local/bin/task`
4. SSH in and run `task` to start the Kanban board

## Auto-Launch on Login (Optional)

To have TaskYou launch automatically when you SSH in, add this to your VM's `.bashrc`:

```bash
ssh tasks.exe.xyz 'echo "exec ~/.local/bin/task" >> ~/.bashrc'
```

## What's Included on exe.dev

- **Claude Code** - AI executor for tasks (pre-installed)
- **OpenAI Codex** - Alternative executor
- **Docker** - Container support
- **Git** - Version control
- **Full dev environment** - vim, tmux, ripgrep, etc.

## Custom Docker Image (Experimental)

We also publish a custom image, but note that exe.dev currently has SSH issues with custom images:

```bash
ssh exe.dev new --image=ghcr.io/bborn/taskyou-exe:latest
```

The `--prompt` approach above is recommended until this is resolved.
