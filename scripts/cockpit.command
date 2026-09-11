#!/bin/bash
set -euo pipefail
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
workspace="${OPENCLAW_WORKSPACE:-$HOME/.openclaw/workspace}"
target='=cass-agents:=dashboard.0'
# Resolve the window by its actual name, never tmux's missing-target fallback.
window_id="$(tmux list-windows -t '=cass-agents' -F '#{window_id} #{window_name}' 2>/dev/null | awk 'substr($0, index($0, " ") + 1) == "dashboard" {print $1; exit}' || true)"
dead=''
if [[ -n "$window_id" ]]; then
  target="$(tmux list-panes -t "$window_id" -F '#{pane_id}' | head -n 1)"
  dead="$(tmux display-message -p -t "$target" '#{pane_dead}' 2>/dev/null || true)"
fi
if [[ "$dead" != 0 ]]; then
  if [[ -x "$workspace/tools/tmux/start_openclaw_cockpit.sh" ]]; then
    rc=0
    "$workspace/tools/tmux/start_openclaw_cockpit.sh" --respawn --dashboard-only || rc=$?
    if [[ "$rc" != 0 && "$rc" != 3 ]]; then exit "$rc"; fi
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
