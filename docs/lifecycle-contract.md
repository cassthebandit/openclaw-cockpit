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
