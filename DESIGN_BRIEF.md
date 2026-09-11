# OpenClaw Cockpit Design Brief

OpenClaw Cockpit is a single-pane operator wall for local CLI and agent work. It runs in the terminal, reads tmux and optional OpenClaw metadata, and gives an operator a live view of what is actively happening without forcing them to step through panes, logs, sidecars, and helper scripts by hand.

Cockpit is not a general tmux theme, a process supervisor, or the cleanup authority. It is the presentation layer for work that is owned by tmux, OpenClaw launch metadata, runtime cards, and the janitor. Its job is to make the state of the system obvious enough that the operator can trust what is active, what is waiting, what failed, what is held, and what will disappear automatically.

## Product Job

When an operator opens Cockpit, the first screen should answer five questions:

1. What agents are actively running or waiting for input?
2. What completed work is intentionally held for review?
3. What completed work is already on the path to teardown?
4. What failed and still needs operator judgment?
5. Are the supporting services and runtime cards healthy enough to trust the wall?

The active-agent view is the priority. Supporting groups are useful only when they do not steal space, attention, or meaning from active work.

## Operator Experience

Cockpit should feel like a control-room wall, not a pile of tmux panes. The operator should be able to leave it open for long sessions and glance at it repeatedly without reinterpreting its vocabulary each time.

The first viewport should prioritize active agent TUIs. Active cards should be large enough to show the useful tail of the real TUI, including token counts, prompt/approval states, live task lists, current command output, and completion markers. Completed or non-agent information should compress before active work does.

The wall should resize with the terminal. Expanding the terminal should use the new width and height instead of leaving dotted tmux dead space. Shrinking the terminal should reflow cleanly instead of cutting borders or scattering stale rendered content across rows.

Operators should not have to patch or manually resize tmux windows for the wall to make sense. If the dashboard is attached inside tmux, the launcher and runtime must keep the tmux window geometry synchronized with the attached client.

## Ownership Boundaries

Cockpit owns:

- reading tmux topology and pane capture data;
- reading OpenClaw presentation metadata and runtime cards;
- grouping, sorting, filtering, collapsing, and rendering cards;
- presenting janitor status and countdowns;
- preserving view-only operator preferences when configured;
- exposing safe navigation and read-only inspection controls.

Cockpit does not own:

- killing sessions or processes;
- deciding whether an evidence artifact is sufficient;
- clearing holds;
- mutating OpenClaw config, Gateway, bindings, or agent sessions;
- inventing lifecycle state when metadata is missing;
- treating a pane screenshot or tail as proof that a review/build is accepted.

The cleanup authority is `session_hygiene.py`. The launch metadata authority is `agent_wall.py`. tmux is the process and pane authority. Cockpit is the wall that presents those authorities coherently.

## Source-Of-Truth Order

Use this order when docs disagree:

1. Detailed contract docs: lifecycle, group registry, layout, and config.
2. `DESIGN_BRIEF.md` for product intent and boundary summary.
3. `docs/spec.md` for current implementation architecture.
4. `README.md` for operator-facing usage.
5. Historical diagnosis artifacts and run notes.

If detailed contracts conflict with each other, the owning domain wins: lifecycle owns state and cleanup meaning; group registry owns labels/order/accordion policy; layout owns space and resize behavior; config owns settings and precedence. Any unresolved conflict is implementation-blocking until patched.

## Core State Model

The operator-facing groups are a presentation of lifecycle facts, not independent states. The canonical group names, ranks, definitions, and accordion policies live in [`docs/group-registry.md`](docs/group-registry.md).

The current source implements the registry groups and keeps their labels tied to lifecycle facts. Changes to classification must update the owning contract and its regression tests together.

## Cleanup Contract

Automatic cleanup must be boring and trustworthy.

Cleanup may happen only when the owning metadata says the session is managed, the cleanup policy is eligible, evidence resolves to a non-empty file under the run root, no hold blocks cleanup, archive and ledger writes succeed, and the janitor chooses an allowed exact tmux target.

Holds are real stop signs. A held pane must not be marked or killed by ordinary hygiene. Releasing a hold is a separate explicit action that must confirm evidence has already been captured or synthesized.

Failed panes should remain visible long enough for the operator to notice them, then clear automatically only after the evidence contract is satisfied. Empty evidence files, relative run roots, missing artifacts, invalid paths, and archive failures are cleanup blockers, not reasons to silently kill or silently keep pretending the item is "marked."

## Layout Contract

Active work gets the first claim on space. The wall should allocate height and width by priority:

1. Header and filters stay compact.
2. Active agent groups get useful vertical height before any lower-priority group expands.
3. Held, failed, and blocked groups remain visible but compact.
4. Services and completed groups compress first.
5. Footer/helper text remains useful but must not consume active-agent space.

Accordions are operator view state. Groups may have configurable auto-open policies. Agent-focused groups can reopen on edge transitions into active or urgent work. Failure, subsystem, service, completed, and lower-priority groups must respect manual collapse unless the group registry says that exact transition may reopen them.

## Configuration Direction

The tool needs a real configuration surface, but it should stay small and explicit. The first durable config should cover:

- lifecycle timers and visible grace windows;
- group order, labels, and auto-open policy;
- default column caps and per-group layout constraints;
- footer/helper height;
- theme and color tokens;
- capture depth and poll/capture budgets;
- janitor status sidecar path.

Configuration should not become a second hidden source of lifecycle truth. It can tune presentation and timers, but the state model still comes from tmux, `agent_wall.py`, runtime cards, and janitor status.

## Acceptance Standard

A Cockpit change is not accepted because the code compiles. It is accepted when the wall proves the operator contract:

- active agents are first, stable, and readable;
- holds, marks, failed panes, blocked cleanup, and completed runs use distinct labels;
- teardown countdowns match janitor facts;
- failed and completed panes clear when eligible and visibly block when not eligible;
- manual accordion collapse is respected according to policy;
- terminal expand and shrink behavior leaves no dead space, cut borders, or corrupted render rows;
- footer/helper content stays compact;
- configuration defaults reproduce the intended wall without hardcoded colors and timings scattered through the UI.

Behavior changes should begin with a regression test that states the operator-visible contract.

## Runtime helper delivery

The optional public `helpers/` suite supplies launch, assignment closeout and safe
exited-session retirement. The Go monitor remains read-only. Example environments
and configurations ship publicly; private adoption contains only installation
choices and private experimental profiles. See `helpers/README.md` and the lifecycle
contract for the executable boundary.
