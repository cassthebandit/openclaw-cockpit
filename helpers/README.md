# Public runtime helpers

Cockpit's Go binary is a passive monitor. These optional Python helpers launch
managed terminal jobs, publish runtime snapshots and retire eligible exited jobs.
They are shipped in the same release archive and need no private repository.

## Requirements and layout

Use Python 3.10+ and tmux on macOS or Linux. Interactive launchers additionally
require the selected authenticated Codex or Claude CLI. Windows users can run the
Go monitor, but the POSIX/tmux helper suite needs WSL or a Unix host. No Python
third-party dependencies are needed. Keep the `helpers` directory intact.

- `tmux/agent_wall.py`: launch/mark/hold orchestration.
- `tmux/runtime_commands.py`: runtime-specific argument preparation.
- `tmux/assignment.py`: saved-result completion receipt and direct-child supervisor.
- `tmux/session_hygiene.py`: inspect, plan and apply exited-job retirement.
- `tmux/tmux_inspector.py`: annotate unowned panes without overwriting managed jobs.
- `tmux/cockpit_doctor.py`: read-only integration diagnostics.
- `openclaw_runtime/cockpit_snapshot.py`: read-only database-to-card bridge.

Each entrypoint supports `--help`. Launchers create local artifacts; they do not
install services, alter global runtime hooks, or configure authentication.
State defaults to `~/.local/state/openclaw-cockpit`. Set
`OPENCLAW_COCKPIT_STATE_DIR` for a separate installation. Use a private writable
state directory: prompts, runtime logs and saved results can contain sensitive data.

## Interactive assignment

From an extracted release or source checkout:

```sh
python3 helpers/tmux/agent_wall.py spawn-codex \
  --name review-example --run-root "$PWD/review-run" \
  --prompt-file "$PWD/examples/assignment.md" \
  --cd "$PWD" --sandbox workspace-write --model gpt-6-astra --effort high
```

Choose an available model and runtime permissions appropriate to the task. Normal
runtime workspace/hook/tool trust prompts still apply; this helper does not bypass
them. Use `spawn-claude` with `--model`/`--effort` for Claude. Its default
subscription route removes alternate API environment selectors; choose
`--auth-route configured` to intentionally preserve your own configured route.
That selection is not a billing/authentication attestation.

Add `--keep-open` to retain an interactive worker for follow-up. A temporary
`--hold-reason` is separate from the permanent keep-open choice.

The launcher appends a completion instruction to a private copy of the prompt.
The worker saves its result, obtains a unique receipt, and emits its marker in the
final response. A matching synchronous main Stop hook validates the receipt. The
supervisor owns and reaps that runtime child; a normal reply, idle time, tool
approval, stale receipt or background activity does not authorize closeout.
Result completion is not independent review of result correctness.

Unsupported/disabled runtime hooks leave the terminal open and report why;
they are not treated as successful automatic completion. Hook support is
runtime-version dependent. The helper never edits global hooks or copies auth.

## Retirement

```sh
python3 helpers/tmux/session_hygiene.py plan --json
# Inspect the plan before using the corresponding apply subcommand.
```

Retirement preserves the existing exact-identity, exited-pane, unlinked-session,
hold and evidence checks. Saving a completion receipt does not authorize killing
an unrelated or still-live terminal. The launcher also preserves a failed launch
for inspection instead of issuing delayed name-based removal.

## Development

```sh
python3 -m pip install pytest==8.4.2
python3 -m pytest helpers/tests -q
```

Live tests create isolated tmux servers. Runtime smoke results and minimum hook
compatibility are recorded with the change; never infer them from mocked tests.
