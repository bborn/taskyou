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
echo -e "${YELLOW}[1/6] Updating system...${NC}"
apt update && apt upgrade -y
echo -e "${GREEN}✓ System updated${NC}"

# ============================================
# Step 2: Install Dependencies
# ============================================
echo -e "${YELLOW}[2/6] Installing dependencies...${NC}"
apt install -y curl git jq build-essential

# Install Node.js 20
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt install -y nodejs
echo -e "${GREEN}✓ Node.js $(node --version) installed${NC}"

# ============================================
# Step 3: Install Claude Code
# ============================================
echo -e "${YELLOW}[3/6] Installing Claude Code...${NC}"
npm install -g @anthropic-ai/claude-code
echo -e "${GREEN}✓ Claude Code installed${NC}"

# ============================================
# Step 4: Create Runner User
# ============================================
echo -e "${YELLOW}[4/6] Creating runner user...${NC}"
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
echo -e "${YELLOW}[5/6] Downloading GitHub Actions Runner...${NC}"
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
# Step 6: Print Next Steps
# ============================================
echo ""
echo -e "${BLUE}=== Setup Complete ===${NC}"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo ""
echo "1. Get a runner registration token from GitHub:"
echo "   https://github.com/bborn/workflow/settings/actions/runners/new"
echo ""
echo "2. Configure the runner (as runner user):"
echo -e "   ${BLUE}sudo su - runner${NC}"
echo -e "   ${BLUE}cd ~/actions-runner${NC}"
echo -e "   ${BLUE}./config.sh --url https://github.com/bborn/workflow --token YOUR_TOKEN${NC}"
echo ""
echo "3. Install and start as a service:"
echo -e "   ${BLUE}sudo ./svc.sh install${NC}"
echo -e "   ${BLUE}sudo ./svc.sh start${NC}"
echo ""
echo "4. Authenticate Claude Code:"
echo -e "   ${BLUE}claude auth login${NC}"
echo "   (Follow the browser prompts to log in with your Claude Max account)"
echo ""
echo "5. Verify Claude Code works:"
echo -e "   ${BLUE}claude -p 'Say hello'${NC}"
echo ""
echo -e "${GREEN}Runner IP: $(curl -s ifconfig.me)${NC}"
