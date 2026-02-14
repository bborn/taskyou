# Development Container Configuration

This directory contains configuration for cloud development environments (VS Code Dev Containers, GitHub Codespaces, etc.).

## Features

- Automatically installs Claude CLI on container creation
- Pre-configured Go development environment
- VS Code extensions for Go development

## Manual Installation

If you're not using a devcontainer-compatible environment, you can manually install Claude CLI:

```bash
curl -fsSL https://claude.ai/install.sh | bash
```

After installation, authenticate with:

```bash
claude login
```

## Supported Platforms

- VS Code Dev Containers
- GitHub Codespaces
- Any Docker-based development environment that supports devcontainer.json
