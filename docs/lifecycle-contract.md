# Lifecycle Contract

This contract defines how OpenClaw Cockpit, `agent_wall.py`, `session_hygiene.py`, tmux, and OpenClaw runtime cards must agree on agent/session state.

Cockpit is presentation-only. It renders lifecycle state; it does not kill sessions, clear holds, decide evidence sufficiency, or mutate cleanup metadata.

## Authorities

- tmux owns session/window/pane existence, pane liveness, pane exit status, size, and capture output.
- `agent_wall.py` owns managed-worker metadata such as kind, agent, owner, project, goal, run root, evidence path, cleanup policy, TTL, state, completion timestamps, hold reason, and display adoption.
- `session_hygiene.py` owns cleanup eligibility, mark/cancel/kill decisions, archive/ledger writes, status sidecar, and refusal reasons.
- OpenClaw runtime-card sources own runtime/service/subsystem health cards.
- Cockpit owns grouping, labels, sorting, countdown display, filters, and read-only operator interaction.

## Managed Worker Metadata

A lifecycle-managed pane needs enough `@oc_*` metadata to support display and cleanup:

- identity: name, kind, agent, owner, project, goal;
- lifecycle: state, completed_at or equivalent completion evidence, updated_at;
- cleanup: cleanup_policy, ttl where required, evidence_path, run_root;
- hold: hold_reason when the parent session requires human review;
- janitor: janitor_state, teardown_marked_at, teardown_reason as set by hygiene.

`release-hold` is a special authority boundary that must be resolved before implementation. The preferred contract is that release clears hold metadata and records release/completion evidence, while `session_hygiene.py` remains the single marking authority on the next cycle. If `agent_wall.py release-hold` is allowed to pre-mark a pane, that exception must be explicit in this document and covered by field-diff tests.

Missing metadata does not give Cockpit permission to infer cleanup authority. It may display the item as unowned, adopted, blocked, or manual, depending on the available evidence.

## Signal Precedence

Lifecycle inputs can disagree. Cockpit must resolve them deterministically instead of letting cards jump between groups on every poll.

Use this presentation precedence for managed agent panes:

| Priority | Signal | Presentation Result |
| --- | --- | --- |
| 1 | Live operator prompt or approval prompt that can continue work | `Active Agents`, even if stale metadata says otherwise. |
| 2 | Live pane with real resumed work after completion/mark | `Active Agents`; janitor mark should be cancelled by hygiene. |
| 3 | Explicit failed/terminal-problem state or nonzero dead pane inside failed-visible window | `Failed Agents`. |
| 4 | Explicit `hold_reason` on non-live work | `Held / Teardown Blocked`, even if `teardown_marked_at` is also present. A held+marked conflict must be labeled as a blocker, not rendered as a clean countdown. |
| 5 | Janitor refusal/blocker after failed-visible or completion grace | `Cleanup Blocked`, with reason. |
| 6 | Valid `teardown_marked_at` or `janitor_state=marked_for_teardown` with no hold/evidence conflict | `Marked For Teardown`, with sidecar countdown when available. |
| 7 | Delivered-idle or terminal-done with valid cleanup path but not yet marked | `Completed Agent Runs` or `Cleanup Blocked`, based on janitor/evidence status. |
| 8 | Healthy service/runtime cards | `Services` or the appropriate runtime failure group. |
| 9 | Unmanaged/manual/adopted fallback | fallback group from [`group-registry.md`](group-registry.md). |

Rules:

- Janitor status sidecar facts override Cockpit's locally configured countdown display.
- `@oc_state=running` does not by itself keep a pane active when semantic completion and janitor state prove delivered-idle cleanup debt.
- Goal text, preview text, and service/dashboard keywords must not steal a managed agent out of its lifecycle group.
- Missing metadata may create `Cleanup Blocked` or fallback display, but it must not create cleanup authority.

## Lifecycle States

### Active

Active means the agent is live or resumable. Examples include:

- starting, running, waiting, blocked, review;
- an approval prompt or operator prompt that can continue if answered;
- a live agent TUI with current output, token counts, or task/progress display.

Active agents belong in `Active Agents`, even when they carry a hold. A hold on live work means "do not clean this up after it completes," not "hide it from active work."

### Delivered Idle

Delivered idle means the live TUI has apparently finished producing the requested artifact and is sitting at a prompt or quiet tail. It is not active work for operator-priority purposes, but it may still need cleanup proof.

Delivered-idle detection must be conservative. Cosmetic tail churn, token accounting, status footer updates, or shell prompt repainting should not cancel teardown as meaningful work.

The mark/cancel loop must also persist a fresh normalized tail baseline at mark time. A stale pre-mark hash baseline can make a stable completed pane cancel every cycle even when the visible completion screen has not changed. In the current failure shape, the completion-screen path can re-mark every cycle while the skip path that would refresh the baseline never runs again, so the loop becomes permanent until the mark-time baseline bookkeeping is fixed. The first implementation spec must cover both problems: normalized completion-screen comparison and mark-time baseline bookkeeping.

Delivered idle maps to:

- `Held / Teardown Blocked` when a hold is present;
- `Marked For Teardown` only after a valid janitor mark and no hold/evidence conflict;
- `Cleanup Blocked` when evidence/run-root/archive metadata is invalid;
- `Completed Agent Runs` when it is completed informational debt not yet marked and not blocked.

### Meaningful Output Predicate

For the janitor mark/cancel loop, output is meaningful only when it proves the pane resumed work or needs operator input.

Treat as meaningful:

- a new operator prompt, approval prompt, or question that can change execution;
- new command output after a shell prompt that is not merely repainting the prompt/status line;
- new task/progress content that no longer matches a known completion/delivered-idle screen;
- a transition from completed/delivered-idle text back into running/building/reviewing text.

Do not treat as meaningful:

- token counter repaint;
- runtime status/footer repaint;
- clock/timestamp repaint;
- shell prompt repaint with no command;
- terminal wrapping/color/ANSI churn;
- pane-log capture bookkeeping;
- text that still matches the same delivered-idle/completion screen after normalization.

The first implementation spec must define the normalization fixture and completion-screen fixture before changing janitor mark-cancel behavior.

### Held

Held means an explicit `hold_reason` exists or the janitor refused cleanup because a hold blocks it.

Contract:

- Holds block ordinary automatic cleanup.
- Holds block marking and killing unless an explicit override/release path is used. This applies before mark, after mark, and at kill time.
- Panes marked under older rules must have a defined migration behavior when a hold is later observed or when hold enforcement is hardened. Acceptable behavior is to cancel/refuse or refuse at kill time; silently killing is not acceptable.
- Releasing a hold must be deliberate and should require evidence-captured confirmation.
- A released hold may stamp completion metadata and move the pane into teardown if the cleanup contract is otherwise satisfied.

### Marked For Teardown

Marked means the janitor wrote `teardown_marked_at` or `janitor_state=marked_for_teardown`.

Contract:

- Only actually marked sessions may appear in a group named `Marked For Teardown`.
- The operator should see a countdown or "kill not before" time when available.
- Current janitor constants are `ACTIVE_IDLE_MARK_SECONDS=300`, `TEARDOWN_GRACE_SECONDS=300`, and `FAILED_VISIBLE_SECONDS=900`.
- Target next-code defaults are now decided: `2m` stable delivered-idle before marking, `8m` mark-to-kill grace, and `10m` failed-visible grace. The operator rule is that completed work should be gone in about 10 minutes once it is done, assuming valid evidence and no hold/blocker.
- The mark-to-kill window rendered by Cockpit must match janitor policy or display the janitor-provided `kill_not_before`; Cockpit must not guess a countdown that disagrees with hygiene.
- Mark cancellation should be rare and explainable. It may cancel for real operator prompts or meaningful output, not cosmetic render churn.
- Mark cancellation must compare against a mark-time normalized baseline, not a stale skip-path observation. A statically completed pane should retain its mark across repeated janitor cycles.
- Timer/policy changes must be separate from correctness fixes in implementation history, so dry-run failures can be attributed to either behavior repair or timing policy, not both.

### Cleanup Blocked

Cleanup blocked means the pane is not active and would otherwise be cleanup debt, but hygiene cannot safely mark or kill it.

Common blockers:

- active hold;
- missing evidence path;
- relative or missing run root;
- evidence outside the run root;
- missing, empty, stale, or non-regular evidence file;
- invalid TTL or unknown cleanup policy;
- archive or ledger write failure;
- multi-pane session where the cleanup policy only permits a single exact target;
- unmanaged/manual/adopted state.

Blocked cleanup must not be mislabeled as marked. The label should tell the operator why it is stuck.

### Failed

Failed means a managed agent or runtime card reached a terminal/problem state such as failed, route-fail, safety-fail, nonzero dead pane, or terminal-problem.

Contract:

- Failed agents should remain visible for a configured failure-visible grace window.
- After the visible window, cleanup can proceed only if evidence is valid and no hold blocks it.
- Empty failed-lane artifacts are non-evidence and should show as cleanup blocked, not immortal failed cards with no explanation.
- Empty evidence is not a reason to auto-kill. The remediation path is to write a truthful non-empty degraded/non-evidence artifact, repair the run root/evidence path, or use an explicitly authorized manual cleanup path. `ABSOLUTE_MAX_SECONDS`, if used later, may relabel urgency or page the operator; it must not bypass evidence requirements.

### Cleaned

Cleaned means hygiene archived the pane output/metadata, wrote the ledger event, and successfully killed the exact tmux session.

Cockpit usually sees cleaned state indirectly because the tmux session disappears. If a runtime card summarizes cleaned work, it must be informational and not presented as live.

## Group Mapping

The canonical group list lives in [`group-registry.md`](group-registry.md). Lifecycle mapping is:

- Active or resumable agent -> `Active Agents`.
- Live held agent -> `Active Agents` with hold indicator.
- Completed/dead/stale held agent -> `Held / Teardown Blocked`.
- Held+marked conflict -> `Held / Teardown Blocked` with conflict/blocker label, not clean countdown.
- Actually janitor-marked agent with no blocker -> `Marked For Teardown`.
- Cleanup-eligible but refused/blocked agent -> `Cleanup Blocked` or `Held / Teardown Blocked`, based on reason.
- Delivered-idle with no blocker and no mark -> `Completed Agent Runs`.
- Failed agent still visible -> `Failed Agents`.
- Failed agent past visible window but evidence-blocked -> `Cleanup Blocked`.
- Healthy service -> `Services`.
- Runtime workflow failure -> `Operational Failures`.
- Platform/source/route/subsystem failure -> `Sub-System Failures`.

Any current implementation that groups all completed cleanup debt under `Marked For Teardown` is contract drift.

## Janitor Status Sidecar

The janitor status sidecar is the bridge between cleanup decisions and the wall. It should expose:

- status version and generated_at;
- interval and policy;
- per-session janitor state;
- marked_at;
- kill_not_before;
- reason;
- last action;
- last refusal;
- cycle counts for killed, marked, cancelled, skipped, and refused.

Cockpit must treat stale or invalid status as a visible janitor-health signal. It should not silently assume cleanup is working.

The sidecar join contract must define:

- stable session and pane keys, including name-reuse behavior;
- state enum semantics, including evidence-refusal rows and cleanup-blocked rows;
- the meaning of `active` as distinct from lifecycle-active presentation;
- sidecar freshness, stale-file behavior, and missing-row behavior;
- `kill_not_before` ownership and countdown source;
- per-session refusal/blocker reason shape.

Reason strings already available from hygiene, such as hold refusal, `evidence_empty`, `relative_run_root`, and `kill_not_before`, are sufficient for a first-pass blocked display. Cockpit must not invent cleanup eligibility when a sidecar row is missing or stale.

## Current Bug Mapping

- Items marked for teardown do not tear down: lifecycle and janitor contract bug. Verify mark-to-kill, evidence, hold, archive blockers, and mark-time tail-hash baseline bookkeeping separately.
- Active agents appear and disappear: grouping is reading mixed signals. Managed active identity must win over goal text, preview text, and non-agent heuristics.
- Held items never clear: hold release and cleanup-blocked semantics are not explicit enough.
- Failed items never go away: failed visible grace is subordinate to evidence validity; empty artifacts block cleanup forever unless the lane writes truthful non-empty non-evidence.
- Relative run roots block cleanup: correct behavior is refusal, but the wall must label it as cleanup blocked rather than pretending it is on countdown.
- Cosmetic output or stale baseline bookkeeping cancels marks: meaningful-output detection must ignore tail noise that does not represent resumed work, and the janitor must persist a fresh mark-time normalized baseline.

## Open Decisions Before Code

- Timer decision is recorded: `2m` mark delay, `8m` teardown grace, `10m` failed-visible grace, with the completed-path rule that done work should disappear in about 10 minutes when evidence is valid and no hold/blocker applies.
- Define the exact delivered-idle completion-screen fixtures and normalized-tail hash rules.
- Resolve `release-hold` authority: either keep hygiene as the only marking authority or document and test a release-time pre-mark exception.
- Keep `ABSOLUTE_MAX_SECONDS` out of cleanup eligibility. A later implementation may use it only as an operator escalation/relabel threshold, never as evidence-bypass authority.

## Required Tests For Next Code Phase

- Held completed pane is not marked or killed without release/override.
- Held already-marked pane is not killed without release/override.
- Panes marked under older rules follow the documented migration behavior.
- Live held pane remains in `Active Agents`.
- Actually marked pane displays under `Marked For Teardown` with countdown.
- Evidence-empty failed pane displays as cleanup blocked after visible grace.
- Held+marked conflict displays as blocked/held conflict, not as a valid countdown.
- Stale `@oc_state=running` plus delivered-idle semantic tail does not stay in `Active Agents`.
- Relative run root displays an explicit cleanup-blocked reason.
- Cosmetic tail churn does not cancel teardown.
- Statically completed pane retains its mark across multiple janitor cycles.
- Real operator prompt after marking cancels teardown.
- Runtime operational/subsystem failures do not reopen unrelated accordions unless policy allows it.
