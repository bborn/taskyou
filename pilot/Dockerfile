FROM node:22-slim

RUN apt-get update && apt-get install -y \
    git \
    curl \
    python3 \
    python3-pip \
    tmux \
    sqlite3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Create a non-root user for running tasks
RUN useradd -m -s /bin/bash taskyou

# Copy the task runner script
COPY sandbox-init.sh /usr/local/bin/sandbox-init.sh
RUN chmod +x /usr/local/bin/sandbox-init.sh

USER taskyou

ENTRYPOINT ["/usr/local/bin/sandbox-init.sh"]
