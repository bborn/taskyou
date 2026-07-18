#!/bin/sh
# A trivial long-running service: proof the daemon supervises it.
log="${HOME}/.cache/ty-heartbeat.log"
mkdir -p "$(dirname "$log")"
while true; do
  printf '%s heartbeat (pid %s) from a ty daemon-supervised service\n' "$(date -u +%FT%TZ)" "$$" >> "$log"
  sleep 3
done
