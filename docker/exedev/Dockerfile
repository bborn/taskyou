# TaskYou exe.dev Image
# Usage: ssh exe.dev new --image=ghcr.io/bborn/taskyou-exe:latest
#
# This image extends exeuntu with TaskYou pre-installed and configured
# to launch immediately on SSH login.

FROM ghcr.io/boldsoftware/exeuntu:latest

USER root

# Install TaskYou
RUN ARCH=$(uname -m) && \
    case ${ARCH} in \
        x86_64) TASK_ARCH="amd64" ;; \
        aarch64|arm64) TASK_ARCH="arm64" ;; \
        *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac && \
    TASK_VERSION=$(curl -fsSL https://api.github.com/repos/bborn/taskyou/releases/latest | jq -r '.tag_name') && \
    curl -fsSL "https://github.com/bborn/taskyou/releases/download/${TASK_VERSION}/task-linux-${TASK_ARCH}" -o /usr/local/bin/task && \
    chmod +x /usr/local/bin/task

# Create TaskYou data directory with proper permissions
RUN mkdir -p /home/exedev/.local/share/task && \
    chown -R exedev:exedev /home/exedev/.local

# Set up TaskYou-specific AGENTS.md instructions
COPY AGENTS.md /home/exedev/.claude/CLAUDE.md
RUN chown exedev:exedev /home/exedev/.claude/CLAUDE.md

# Create a wrapper script that launches task as the login shell
# This runs task on interactive login, with fallback to bash
COPY task-shell.sh /usr/local/bin/task-shell
RUN chmod +x /usr/local/bin/task-shell

# Configure SSH ForceCommand (for when exe.dev uses VM's sshd)
RUN mkdir -p /etc/ssh/sshd_config.d && \
    echo 'Match User exedev' > /etc/ssh/sshd_config.d/taskyou.conf && \
    echo '    ForceCommand /usr/local/bin/task-shell' >> /etc/ssh/sshd_config.d/taskyou.conf

# Also add to .bashrc as fallback (for when exe.dev uses its own SSH handling)
# Only launch task for interactive login shells that aren't already in task
RUN echo '' >> /home/exedev/.bashrc && \
    echo '# Launch TaskYou on interactive login' >> /home/exedev/.bashrc && \
    echo 'if [[ $- == *i* ]] && [[ -z "$TASKYOU_RUNNING" ]] && [[ -z "$TMUX" ]]; then' >> /home/exedev/.bashrc && \
    echo '    export TASKYOU_RUNNING=1' >> /home/exedev/.bashrc && \
    echo '    exec /usr/local/bin/task' >> /home/exedev/.bashrc && \
    echo 'fi' >> /home/exedev/.bashrc

USER exedev
WORKDIR /home/exedev

# Expose TaskYou SSH server port (in addition to web ports)
EXPOSE 8000 9999 2222

LABEL "exe.dev/login-user"="exedev"
LABEL "dev.taskyou/version"="1.0"
