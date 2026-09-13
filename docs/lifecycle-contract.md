# Lifecycle contract

The Go monitor displays state. The optional [public helper suite](../helpers/README.md)
owns launch, assignment closeout and retirement. No private installation is required.

## Ownership

- tmux owns stable server session/pane identity, process identity and actual liveness.
- `agent_wall.py` owns managed metadata, launch options and explicit hold operations.
- `assignment.py` owns one interactive assignment receipt and its direct runtime child.
- `session_hygiene.py` alone owns ordinary teardown marks, archive/ledger writes and
  retirement eligibility. It revalidates at each destructive boundary.
- The runtime bridge owns read-only source-health cards.
- Cockpit owns grouping, sorting, preview and labels, never cleanup permission.

The launcher claims `managed_by=agent_wall` before other metadata. The inspector
checks ownership and pane process identity at each server-side write, including
partially claimed panes. Missing metadata never grants cleanup authority.

## Completion is not silence

For supported Claude/Codex interactive launches, completion requires a saved,
nonempty result snapshot, a launch/session/generation-bound receipt, its exact
marker in the final response, and a matching synchronous main Stop hook. User,
tool, approval or interrupted activity invalidates the pending receipt. Claude
background task/wakeup state must be explicitly empty. Disabled or unsupported
hooks do not authorize closeout.

The supervisor then retires only its directly owned runtime child and observes
actual exit. Native Stop alone is not process exit. The immutable result snapshot
and final response remain available after the terminal closes. This establishes
assignment completion, not independent review or correctness of the result.

`--keep-open` records completion but retains the runtime for follow-up. Resumed
user work clears completion and returns the assignment to running. A regular
intermediate answer, approval request, quiet TUI or screen keyword must not close
an assignment. Generic process exits are reported with their actual exit code;
zero exit status alone is not an interactive assignment receipt.

## Holds and releases

Temporary holds and permanent keep-open choices are different. A live held job
remains active. A completed retained job is completed, not falsely running.

`release-hold` requires the explicit evidence-captured and allow-hygiene flags. It
clears hold_reason and stamps completion fields only. It does not pre-mark, archive,
kill, hide or detach anything. Hygiene makes its own fresh decision on a later
cycle; release is not proof that a live runtime exited.

## Retirement boundary

Ordinary automatic removal requires an actually exited, single-pane, single-window,
unlinked managed session. The final synchronous tmux predicate matches immutable
server session ID, pane ID, pane PID, session creation time and metadata. It never
uses a delayed shell command or a reusable session name as removal authority.

Required evidence remains nonempty, regular, contained within the declared absolute
run root and valid under the policy. Hold, identity, topology and evidence checks
are repeated before archive, ledger and removal. Archive/ledger failure preserves
the terminal. A replaced pane, new hold or altered contract cancels/refuses the old
plan. Unsupported format-bearing guard literals are refused, not interpolated.

The planner may describe completed live work or mark/cancel state. Those observations
do not grant live-process removal. The apply boundary refuses live terminals even
if their screens look complete. Cosmetic screen churn and normalized-tail tracking
remain display/planning observations, not assignment completion proof.

Failed visible grace, completed grace and mark-to-removal grace are separate. Their
current helper defaults are 180s, 60s and 60s respectively. A configured policy can
change retention, but cannot bypass preservation checks. Historical proposed
2m/8m/10m timings are not defaults. The UI uses actual sidecar `kill_not_before`
for countdowns, not a guessed local deadline.

## Presentation

- Actual resumed activity/approval is active.
- Timestamped terminal assignment outcome is completed/failed even when a retained
  runtime process remains live. Stale preview text does not undo that outcome.
- Active holds remain active with a hold indicator; completed holds are retained.
- Held plus marked is a conflict/blocker, never a clean countdown.
- Only an actual valid janitor mark appears as marked for teardown.
- Missing/invalid evidence or archive failure is cleanup blocked, not authorization.
- Runtime workflow failures and source/platform failures use their distinct groups.

The [group registry](group-registry.md) owns display names and ordering. None of
these labels changes hygiene authority.

## Status and bridge contracts

Hygiene emits version, generated time, actual interval/policy, per-session state,
reason, mark/deadline and cycle counts. The reader matches session and pane identity,
including pane PID, so stale rows cannot attach to replacement jobs. Missing/stale
status remains visible as missing/stale information, never invented eligibility.

Runtime snapshots preserve structured error explanations. `visibleRuntimeCardCount`
is the number delivered; `totalVisibleRuntimeCardCount` is the pre-limit count;
`truncated` states whether truncation occurred. Consumers support older producers
and recalculate delivered count if applying their own cap.

## Verification

Run the public helper suites and Go suites from the repository. Coverage includes
ownership races/partial claims, actual isolated tmux replacement/linked topology,
archive/ledger failures, cancellation write failures, holds, receipt invalidation,
direct-child closeout and coherent SQLite snapshots. Runtime-version smoke evidence
must distinguish real interactive runtime checks from mocked hook tests.

## Existing-session runbook

The hygiene helper can adopt selected legacy work without creating a managed
assignment receipt. The feature is off by default; configure the settings in
[lifecycle configuration](lifecycle-configuration.md#existing-session-adoption).
Use a separate test socket and isolated state/archive paths for initial trials.
These commands illustrate a deliberately configured socket and policy file:

```sh
python3 helpers/tmux/lifecycle.py --config /absolute/path/lifecycle.json --validate-config
python3 helpers/tmux/session_hygiene.py plan --socket-path /absolute/path/tmux.sock \
  --lifecycle-config /absolute/path/lifecycle.json --json
# Observation only: writes the status JSON and adjacent lock, never terminal metadata or keys.
python3 helpers/tmux/session_hygiene.py plan --socket-path /absolute/path/tmux.sock \
  --lifecycle-config /absolute/path/lifecycle.json --write-status --json
```

Plain `plan`/`list` have no writes. The existing service loop and `apply` also
accumulate observations; no additional watcher is necessary. Policy is reread
before actions with the original CLI/environment precedence. Invalid reloads
retain the target. `max_kills` caps both native requests and final removals.

Dead adoption needs one unlinked, ungrouped window with one dead pane, known
server/session/pane identity, no attached client or copy mode, valid exit metadata,
and successful scrollback capture. Empty successful capture is allowed. Retention
starts at the first reliable dead observation, without inventing an earlier
completion time or a successful assignment outcome. Required archive/pre-action ledger failure
preserves the pane. Final removal uses the existing synchronous dead-only guard.

For live retirement, set `live_retirement` to `observed` and enroll the exact
`adoption_identity` printed by plan. Enrollment requires a bounded, nonempty,
regular saved result file (at most 16 MiB; no symlink), a reason, and attestation
that no detached/background/scheduled work is intended. It writes only hygiene
state. It does not clear holds, stamp completion, or close a terminal.

```sh
python3 helpers/tmux/session_hygiene.py release-existing \
  --socket-path /absolute/path/tmux.sock --lifecycle-config /absolute/path/lifecycle.json \
  --identity ID_FROM_PLAN --profile claude-2.1.270-direct \
  --result /absolute/path/result.md --reason 'Temporary review finished' \
  --attest-no-background-work --dry-run
# Repeat the reviewed command without --dry-run to enroll.
# For the known pre-supervisor wrapper use instead:
# --profile claude-2.1.270-legacy-bash --wrapper /absolute/path/run-claude_tui.sh
python3 helpers/tmux/session_hygiene.py apply --socket-path /absolute/path/tmux.sock \
  --lifecycle-config /absolute/path/lifecycle.json --json
```

### Runtime adapters and supported profiles

Selection, protections, enrollment, observation timing, archives, single-attempt
consumption and final dead-only removal are shared across runtimes. The built-in
adapters in `runtime_adapters/`, dispatched by `native_runtimes.py`, own process recognition and prompt interpretation;
profiles specify the tested platform/version and native exit gesture. These are
reviewed code, not user-configurable arbitrary commands or a plugin system.

Supported live profiles on **macOS**:

- `claude-2.1.270-direct`: Claude Code 2.1.270 installed as
  `@anthropic-ai/claude-code/bin/claude.exe`, directly owning its pane.
- `claude-2.1.270-legacy-bash`: the same Claude executable under the recognized
  pre-supervisor Bash template, with exactly one native child and an
  exit-propagating metadata tail. The full template, Bash invocation and executable,
  and enrollment hash are checked. Matcher code never executes the shell script.
- `agy-1.2.2-direct`: AGY 1.2.2 standalone macOS arm64 executable, tested
  binary content and unambiguous Ctrl-D binding (two presses in one invocation). Unknown builds, missing/conflicting
  keybindings, different HOME and unexplained children are refused. Use the same
  enrollment command with this profile and no `--wrapper`.
- `codex-0.153.4-npm`: Codex 0.153.4 through its tested npm Node launcher and
  `codex-darwin-arm64` native child. The optional native `codex-code-mode-host` (with no command descendants) and
  known ChatGPT CUA helper tree are
  recognized by OS executable paths, arguments, topology and tested launcher-script
  hashes (unified-computer-use 26.903.71938). Every observed process PID, birth,
  executable and launcher script is bound into the release baseline. Unknown
  descendants, additional helpers, changed scripts or installations are refused.
  Recognizing helpers does **not** prove they are idle; the observed-mode owner
  attestation remains required. This is not support for arbitrary Node trees.

For Codex use the same `release-existing` command above with
`--profile codex-0.153.4-npm` and omit `--wrapper`. Its exit gesture is one Ctrl-D.
ANSI-aware capture distinguishes the tested dim empty-composer placeholder from
an identically worded typed draft. A prior submitted turn and subsequent response,
a supported empty composer/status layout, and no visible activity/approval or
background-work indicators are required. Unknown layouts remain retained.

Other platforms, native-direct Codex installations, Codex shell wrappers, altered
Claude wrappers, and untested versions remain unsupported. Adding support means
extending the appropriate small adapter and passing native process/prompt/exit
fixtures plus common preservation tests; selection/configuration/archive policy
does not need to be duplicated. Unsupported is a diagnostic, never a generic EOF,
process-signal or live-tmux-kill fallback. Wrapper paths/log paths with unsupported
shell quoting also receive a diagnostic.

Native fixtures established the Claude two-Ctrl-D gesture and Codex single-Ctrl-D gesture, each in a single invocation,
retained exit status, draft-editing hazard, and legacy wrapper exit propagation.
Those facts do not establish atomic runtime quiescence. Eligibility also requires
an actually empty final prompt, visible matching version and completed-turn shape,
stable raw capture and result, known process identity and no unexplained children.
Suggestion text is conservatively treated as nonempty input. Observations can
report missing capture, activity, process, version, prompt or result evidence;
missing background telemetry is explicitly **unavailable**, with the owner's
attestation providing the observed-mode residual-risk boundary.

Effective `remain-on-exit=on` and pipe-pane **off** are enrollment prerequisites;
hygiene never changes either. Input-disabled, synchronized input, attachment,
copy mode, holds, keep-open, services, shared topology and managed launch IDs veto
the live path. The legacy launcher normally enables pipe logging, so deliberately
resolving that prerequisite is a separate operator action before enrollment.

Before requesting exit, hygiene archives scrollback, metadata, working-directory
resume context and exact saved result bytes. It durably consumes one release
attempt in the existing status JSON before sending one guarded, profile-specific
exit invocation (`C-d C-d` for Claude; `C-d` for Codex; `C-d C-d` for AGY). The same adjacent `flock` serializes release, revoke, both apply
policies and observation writers. A crash before delivery may consume permission
without sending anything. A crash, ambiguous delivery or continued live pane never
causes automatic resend. Only a new explicit owner decision can rearm it.

Subsequent actual dead observation permits final capture/archive and guarded
removal; the final snapshot is stored under the attempt archive’s `final/`
directory (or the existing central fallback), and its metadata links the pre-exit
archive. Exit alone never means
assignment success. A shell successor is `retained_exited_to_shell`; an unconfirmed
request is `shutdown_unconfirmed`. Missing sessions are not reported removed by
hygiene. Consumed records survive discovery gaps and policy withdrawal. State is
bounded to 256 retained adoption records; capacity exhaustion retains new targets
with a diagnostic. Successfully removed records are pruned. Missing/corrupt state
cannot recreate owner release from screen text, files or old display state.

To withdraw before delivery, disable observed mode, protect the name with a
blacklist/hold, or remove whitelist membership in selected-only mode. Whitelist
removal has no effect in all-except-protected mode. Explicit revocation preserves
a consumed attempt:

```sh
python3 helpers/tmux/session_hygiene.py revoke-existing \
  --socket-path /absolute/path/tmux.sock --lifecycle-config /absolute/path/lifecycle.json \
  --identity ID_FROM_PLAN --dry-run
# Repeat without --dry-run to revoke.
```

Withdrawal cannot undo a sent gesture. Sampling cannot see every inter-sample
activity or lock an unmanaged editor: a raced Ctrl-D can edit a draft, and exiting
can lose in-memory work. Archives preserve captured output and the saved result,
not all runtime state. Resume may be possible from recorded working directory and
native final output; it is not guaranteed. Never escalate to process signals or
live tmux destruction. Rollback means disabling the feature and, when separately
authorized, restoring the prior reviewed installation/configuration through the
normal deployment process. This feature requires no Gateway restart.

Public adapter contributions follow the [runtime adapter guide](runtime-adapters.md):
one module, one static registration, common protection tests and native evidence.

### AGY redraws and sampled-screen consent

AGY 1.2.2 continuously redraws an unchanged terminal; tmux's output timestamp
advances even when no visible work changes. AGY enrollment therefore additionally
requires `--attest-screen-sampling`. This explicit per-release choice uses exact
captured-screen hashes plus process/build/config/result identity for quietness,
not the changing output timestamp. The common engine owns this choice; an adapter
cannot silently activate it. Existing releases remain timestamp-strict by default.

This accepts that activity which appears and disappears between observations can
be missed. It is not telemetry-backed completion. Drafts, visible pending work,
changed captures/processes/results, holds, attachments and other common guards
still veto. The attestation is saved with the release and archive. Without this
choice AGY enrollment refuses with `screen_sampling_attestation_required`.

Example (exact disposable/owner-approved target only):

```sh
python3 helpers/tmux/session_hygiene.py release-existing \
  --socket-path /absolute/path/tmux.sock --lifecycle-config /absolute/path/lifecycle.json \
  --identity ID_FROM_PLAN --profile agy-1.2.2-direct \
  --result /absolute/path/result.md --reason 'Temporary assignment finished' \
  --attest-no-background-work --attest-screen-sampling --dry-run
```
