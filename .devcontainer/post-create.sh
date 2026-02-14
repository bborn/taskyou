#!/bin/bash
set -e

echo "Installing Claude CLI..."
curl -fsSL https://claude.ai/install.sh | bash

echo "Claude CLI installation complete!"
claude --version || echo "Claude installed - you may need to run 'claude login' to authenticate"
