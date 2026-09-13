#!/usr/bin/env bash
set +e
mkdir -p "$(dirname /tmp/legacy-fixture/pane.log)" "$(dirname /tmp/legacy-fixture/launch.json)"
: > /tmp/legacy-fixture/pane.log
# A new CLI invocation must not inherit completion from a prior incarnation.
for oc_field in completed_at exit_code end_reason teardown_marked_at teardown_reason; do
  tmux set-option -pu -t "$TMUX_PANE" "@oc_$oc_field" >/dev/null 2>&1 || true
done
tmux set-option -p -t "$TMUX_PANE" @oc_state running >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
tmux pipe-pane -o -t "$TMUX_PANE" "cat >> /tmp/legacy-fixture/pane.log" >/dev/null 2>&1 || true
cat > /tmp/legacy-fixture/launch.json <<'JSON'
{
  "command_kind": "claude_tui",
  "argv": [
    "claude",
    "--permission-mode",
    "manual",
    "--name",
    "fixture"
  ],
  "prompt_file": "/tmp/legacy-fixture/prompt.txt",
  "pane_log_sensitive": true,
  "debug_file": "/tmp/legacy-fixture/debug.log",
  "debug_log_sensitive": true
}
JSON
claude --permission-mode manual --name fixture
exit_code=$?
if [ "$exit_code" -eq 0 ]; then
  oc_state=done
  oc_end_reason=expected_exit
else
  oc_state=failed
  oc_end_reason=process_exit_nonzero
fi
tmux pipe-pane -t "$TMUX_PANE" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_state "$oc_state" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_exit_code "$exit_code" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_end_reason "$oc_end_reason" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
echo "[agent-wall] claude TUI exited with status $exit_code"
exit "$exit_code"
