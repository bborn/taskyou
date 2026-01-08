#!/bin/bash
# Install taskd systemd service on the server
# Usage: ./scripts/install-service.sh [server] [user] [dir]

set -e

SERVER="${1:-root@cloud-claude}"
REMOTE_USER="${2:-runner}"
REMOTE_DIR="${3:-/home/runner}"

echo "Installing taskd service on $SERVER..."

ssh "$SERVER" "cat > /etc/systemd/system/taskd.service" << EOF
[Unit]
Description=Task Queue Daemon
After=network.target

[Service]
ExecStart=${REMOTE_DIR}/taskd
WorkingDirectory=${REMOTE_DIR}
User=${REMOTE_USER}
Restart=always
Environment=HOME=${REMOTE_DIR}

[Install]
WantedBy=multi-user.target
EOF

ssh "$SERVER" "systemctl daemon-reload && systemctl enable taskd && systemctl start taskd"

echo "Service installed and started!"
echo "Connect with: ssh -p 2222 cloud-claude"
