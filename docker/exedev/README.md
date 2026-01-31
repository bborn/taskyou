# TaskYou on exe.dev

Run TaskYou instantly on exe.dev with a single command.

## Quick Start

```bash
ssh exe.dev new --image=ghcr.io/bborn/taskyou-exe:latest
```

This creates a new VM with TaskYou pre-installed. SSH access drops you directly into the TaskYou Kanban board.

## One-Liner

For the fastest experience, name your VM and connect in one step:

```bash
ssh exe.dev new --name=tasks --image=ghcr.io/bborn/taskyou-exe:latest && ssh tasks.exe.xyz
```

## How It Works

1. **exe.dev** spins up a cloud VM based on the `taskyou-exe` image
2. The image extends `exeuntu` with TaskYou pre-installed
3. SSH login automatically launches the TaskYou TUI
4. You're in the Kanban board, ready to create and execute tasks

## Connecting

After creating the VM:

```bash
ssh yourvm.exe.xyz
```

Replace `yourvm` with the VM name from the `new` command output.

## What's Included

- **TaskYou** - Full TUI task manager with Kanban board
- **Claude Code** - AI executor for tasks (pre-installed)
- **OpenAI Codex** - Alternative executor
- **Docker** - Container support
- **Git** - Version control
- **All exeuntu tools** - Full development environment

## Running Commands

To run shell commands instead of launching TaskYou:

```bash
# Run a specific command
ssh yourvm.exe.xyz "ls -la"

# Get a bash shell
ssh yourvm.exe.xyz bash
```

## Quitting TaskYou

Press `q` in the TUI to quit and return to your local terminal.

## Building the Image

```bash
# Build for both amd64 and arm64
make docker-build

# Build and push to registry
make docker-push
```

## Configuration

### Custom Image Tag

```bash
make docker-build DOCKER_TAG=v1.0.0
make docker-push DOCKER_TAG=v1.0.0
```

### Custom Registry

```bash
make docker-build DOCKER_IMAGE=myregistry.io/taskyou-exe
```

## Technical Details

The image uses two mechanisms to ensure TaskYou launches on SSH login:

1. **SSH ForceCommand** - For when exe.dev uses the VM's internal sshd
2. **.bashrc hook** - For when exe.dev uses its own SSH routing

Both respect `SSH_ORIGINAL_COMMAND`, so you can still run specific commands.
