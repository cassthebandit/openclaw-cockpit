#!/bin/bash
set -euo pipefail
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
workspace="${OPENCLAW_WORKSPACE:-$HOME/.openclaw/workspace}"
target='=cass-agents:=dashboard.0'
dead="$(tmux display-message -p -t "$target" '#{pane_dead}' 2>/dev/null || true)"
if [[ "$dead" != 0 ]]; then
  if [[ -x "$workspace/tools/tmux/start_openclaw_cockpit.sh" ]]; then
    "$workspace/tools/tmux/start_openclaw_cockpit.sh" --respawn --dashboard-only
  else
    cmd='exec openclaw-cockpit --organize --monitor-only --exclude-session cass-agents'
    if [[ "$dead" == 1 ]]; then
      tmux respawn-pane -t "$target" "$cmd"
    elif tmux has-session -t '=cass-agents' 2>/dev/null; then
      tmux new-window -d -t '=cass-agents' -n dashboard "$cmd"
    else
      tmux new-session -d -s cass-agents -n dashboard "$cmd"
    fi
  fi
fi
# The app calls this before considering an existing Terminal client for reuse.
tmux select-window -t '=cass-agents:=dashboard'
if [[ "${1:-}" == --ensure ]]; then exit 0; fi
exec tmux attach-session -t '=cass-agents'
