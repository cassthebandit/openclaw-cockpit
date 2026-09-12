# Public runtime helpers

Cockpit's Go binary is a passive monitor. These optional Python helpers launch
managed terminal jobs, publish runtime snapshots and retire eligible exited jobs.
They are shipped in the same release archive and need no private repository.
`go install` installs only the Go binary; obtain the helpers from a release
archive or source checkout and keep their directory structure intact.

## Requirements and layout

Use Python 3.10+ and tmux on macOS or Linux. Interactive launchers additionally
require the selected authenticated Codex or Claude CLI. Windows users can run the
Go monitor, but the POSIX/tmux helper suite needs WSL or a Unix host. No Python
third-party dependencies are needed. Keep the `helpers` directory intact.
The tmux transport uses printable framing with exact field-count validation:
rows containing the delimiter (`|:oc:|`) in data are refused, not misparsed.
UTF-8 mode (`tmux -u`) preserves Unicode outside tmux/C-locale execution.
Invalid UTF-8 bytes fail decoding rather than changing identity data.

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

On first use, Codex may require normal `/hooks` review of the launch-local
hook definition. The stable definition is reused on later jobs. When the runtime
has not bound the launch through a real hook, launcher output says
`prompt_submitted: false` and provides `setup_required` plus `submit_command`.
The saved task has **not** been sent. Complete the runtime's workspace/hook
trust setup, then run that exact `submit_command`. It submits the initial task
only once and refuses a replaced/dead pane. This command does not bypass or
independently attest runtime hook trust; the first real user-prompt hook binds
the session. Do not paste the task into a trust or permission dialog.

Codex creates its runtime session lazily. The launcher therefore requests one
short, tool-free initialization reply before submitting the actual task. A
matching native Stop and initialization marker establish readiness; they never
count as assignment completion. This adds a small initialization turn to Codex
jobs. Claude uses its native SessionStart binding directly.

Add `--keep-open` to retain an interactive worker for follow-up. A temporary
`--hold-reason` is separate from the permanent keep-open choice.

```sh
python3 helpers/tmux/agent_wall.py spawn-codex \
  --name retained-review --run-root "$PWD/retained-run" \
  --prompt-file "$PWD/examples/assignment.md" \
  --cd "$PWD" --sandbox workspace-write --keep-open
```

The launcher appends a completion instruction to a private copy of the prompt.
The worker saves its result, obtains a unique receipt, and emits its marker in the
final response. A matching synchronous main Stop hook validates the receipt. The
supervisor owns and reaps that runtime child; a normal reply, idle time, tool
approval, stale receipt or background activity does not authorize closeout.
Result completion is not independent review of result correctness.

Unsupported/disabled runtime hooks leave the terminal open and report why;
they are not treated as successful automatic completion. Hook support is
runtime-version dependent. The helper never edits global hooks or copies auth.
Native interactive paths were verified on macOS with Codex 0.153.4 (GPT-6 Astra,
high) and Claude Code 2.1.269 (Fable 5.1, high). Later versions require a fresh
compatibility check; these are observed versions, not universal minimums.

## Failed assignments and result recovery

A finished unsuccessful assignment uses the same receipt path with a failed
outcome. This is a worker-side operation after saving its result and diagnostics,
not an operator shortcut for declaring unfinished work complete:

```sh
python3 helpers/tmux/assignment.py finish \
  --run-dir /path/from-launch-output/assignment_run \
  --result /absolute/path/to/failure-report.md --outcome failed
```

The worker then includes the returned marker as its final response line. A
matching native Stop records failure, not success; ordinary workers close and
`--keep-open` workers remain retained. Failure behavior has focused lifecycle
test coverage; the native version checks above prove successful ordinary and
retained flows, not every possible runtime failure.

Launch JSON supplies `assignment_run`, `pane_log`, and `launch_record`. Under
`assignment_run`, `result-*.bin` preserves the exact result bytes, `stop-*.json`
preserves final-message/receipt evidence, and `outcome.json` records the latest
outcome and observed process exit. Earlier result/Stop files survive resumed
work. A retained worker remains available in its tmux session. Inspect
`closeout-error.json` and the pane log when archival or metadata errors block
closeout. Keep these files private and retain the run directory after terminal
retirement if you need the results.

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

Ordinary managed closeout ends the completed CLI. Input queued during its final Stop is not a new assignment; use keep-open when further interaction is intended. A readiness timeout preserves the terminal in waiting state without submitting the assignment; resolve startup and use the saved assignment with `submit-assignment`.
