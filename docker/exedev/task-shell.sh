#!/bin/bash
# TaskYou login shell wrapper
# Launches task TUI for interactive sessions

# If SSH_ORIGINAL_COMMAND is set, the user ran a specific command
# In that case, execute it instead of launching task
if [[ -n "$SSH_ORIGINAL_COMMAND" ]]; then
    exec /bin/bash -c "$SSH_ORIGINAL_COMMAND"
fi

# For interactive sessions, launch task
export TASKYOU_RUNNING=1
exec /usr/local/bin/task
