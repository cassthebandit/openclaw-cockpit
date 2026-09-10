#!/bin/bash
set -euo pipefail
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
workspace="${OPENCLAW_WORKSPACE:-$HOME/.openclaw/workspace}"
if ! tmux has-session -t '=cass-agents' 2>/dev/null; then
  if [[ -x "$workspace/tools/tmux/start_openclaw_cockpit.sh" ]]; then
    "$workspace/tools/tmux/start_openclaw_cockpit.sh" --respawn
  else
    tmux new-session -d -s cass-agents openclaw-cockpit --organize --monitor-only --exclude-session cass-agents
  fi
fi
exec tmux attach-session -t '=cass-agents'
