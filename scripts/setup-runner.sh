#!/bin/bash
# setup-runner.sh
#
# Sets up a Hetzner VPS as a GitHub Actions self-hosted runner with Claude Code.
# Run this script as root on a fresh Ubuntu 24.04 server.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/bborn/workflow/main/scripts/setup-runner.sh | bash
#
# Or:
#   scp setup-runner.sh root@your-server:/root/
#   ssh root@your-server 'bash /root/setup-runner.sh'

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}=== Task Queue Runner Setup ===${NC}"
echo ""

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   echo -e "${RED}This script must be run as root${NC}"
   exit 1
fi

# ============================================
# Step 1: System Update
# ============================================
echo -e "${YELLOW}[1/8] Updating system...${NC}"
apt update && apt upgrade -y
echo -e "${GREEN}✓ System updated${NC}"

# ============================================
# Step 2: Install Dependencies
# ============================================
echo -e "${YELLOW}[2/8] Installing dependencies...${NC}"
apt install -y curl git jq build-essential

# Install Node.js 20
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt install -y nodejs
echo -e "${GREEN}✓ Node.js $(node --version) installed${NC}"

# ============================================
# Step 3: Install Claude Code
# ============================================
echo -e "${YELLOW}[3/8] Installing Claude Code...${NC}"
npm install -g @anthropic-ai/claude-code
echo -e "${GREEN}✓ Claude Code installed${NC}"

# ============================================
# Step 4: Create Runner User
# ============================================
echo -e "${YELLOW}[4/8] Creating runner user...${NC}"
if id "runner" &>/dev/null; then
    echo "User 'runner' already exists"
else
    useradd -m -s /bin/bash runner
    echo "runner ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/runner
fi
echo -e "${GREEN}✓ Runner user created${NC}"

# ============================================
# Step 5: Download GitHub Actions Runner
# ============================================
echo -e "${YELLOW}[5/8] Downloading GitHub Actions Runner...${NC}"
RUNNER_VERSION="2.321.0"
RUNNER_DIR="/home/runner/actions-runner"

sudo -u runner mkdir -p "$RUNNER_DIR"
cd "$RUNNER_DIR"

sudo -u runner curl -sL -o actions-runner.tar.gz \
    "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
sudo -u runner tar xzf actions-runner.tar.gz
rm actions-runner.tar.gz

echo -e "${GREEN}✓ GitHub Actions Runner downloaded${NC}"

# ============================================
# Step 6: Install Tailscale (remote access)
# ============================================
echo -e "${YELLOW}[6/8] Installing Tailscale...${NC}"
curl -fsSL https://tailscale.com/install.sh | sh
echo -e "${GREEN}✓ Tailscale installed${NC}"

# ============================================
# Step 7: Install GitHub CLI
# ============================================
echo -e "${YELLOW}[7/8] Installing GitHub CLI...${NC}"
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null
apt update
apt install gh -y
echo -e "${GREEN}✓ GitHub CLI installed${NC}"

# ============================================
# Step 8: Create Project Directories
# ============================================
echo -e "${YELLOW}[8/8] Creating project directories...${NC}"
PROJECTS_DIR="/home/runner/projects"
sudo -u runner mkdir -p "$PROJECTS_DIR"

# Set up git config for runner
sudo -u runner git config --global user.name "Bruno's Runner"
sudo -u runner git config --global user.email "runner@bborn.dev"
sudo -u runner git config --global init.defaultBranch main

echo -e "${GREEN}✓ Project directories created at $PROJECTS_DIR${NC}"

# ============================================
# Step 9: Print Next Steps
# ============================================
echo ""
echo -e "${BLUE}=== Setup Complete ===${NC}"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo ""
echo "1. Connect Tailscale (for remote access):"
echo -e "   ${BLUE}sudo tailscale up${NC}"
echo "   Open the auth link and approve on your tailnet"
echo ""
echo "2. Get a runner registration token from GitHub:"
echo "   https://github.com/bborn/workflow/settings/actions/runners/new"
echo ""
echo "3. Configure the runner (as runner user):"
echo -e "   ${BLUE}sudo su - runner${NC}"
echo -e "   ${BLUE}cd ~/actions-runner${NC}"
echo -e "   ${BLUE}./config.sh --url https://github.com/bborn/workflow --token YOUR_TOKEN${NC}"
echo ""
echo "4. Install and start as a service:"
echo -e "   ${BLUE}sudo ./svc.sh install${NC}"
echo -e "   ${BLUE}sudo ./svc.sh start${NC}"
echo ""
echo "5. Authenticate GitHub CLI (as runner user):"
echo -e "   ${BLUE}gh auth login${NC}"
echo "   (Select HTTPS, authenticate with browser)"
echo ""
echo "6. Clone project repos:"
echo -e "   ${BLUE}cd ~/projects${NC}"
echo -e "   ${BLUE}gh repo clone bborn/offerlab${NC}"
echo -e "   ${BLUE}gh repo clone bborn/influencekit${NC}"
echo ""
echo "7. Authenticate Claude Code:"
echo -e "   ${BLUE}claude auth login${NC}"
echo "   (Follow the browser prompts to log in with your Claude Max account)"
echo ""
echo "8. Verify Claude Code works:"
echo -e "   ${BLUE}claude -p 'Say hello'${NC}"
echo ""
echo -e "${GREEN}Public IP: $(curl -s ifconfig.me)${NC}"
echo -e "${DIM}After Tailscale: ssh root@<tailscale-hostname>${NC}"
