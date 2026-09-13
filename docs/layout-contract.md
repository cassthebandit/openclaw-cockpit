# Layout Contract

This contract defines how the Cockpit wall should allocate terminal space, resize with tmux, and preserve operator view state.

## Layout Goal

The wall must make active agent work readable first. Everything else is secondary. A full screen with active agents should show enough live TUI content to understand progress without opening detail view for every pane.

## Terminal Resize

Cockpit must track the actual terminal size on both expand and shrink.

Expand behavior:

- The wall uses newly available width and height on the next render.
- No dotted tmux dead space remains around the dashboard.
- Existing cards reflow to the new width instead of preserving stale render geometry.

Shrink behavior:

- Borders and card bodies remain coherent.
- Text is truncated or reflowed by owned layout code, not cut by stale terminal rows.
- Mouse/zone hit boxes are recomputed after the render.
- Footer and scroll indicators stay within the viewport.

When Cockpit is launched inside tmux, the launcher/runtime must keep the tmux window geometry synchronized with the attached terminal client. Manual `resize-window` is a recovery action, not an operating requirement.

## Space Priority

Vertical and horizontal space follow the canonical group order in [`group-registry.md`](group-registry.md), with this layout priority:

1. Required header/title/filter chrome.
2. Active agent cards.
3. Held or teardown-blocked agent cards.
4. Actually marked teardown countdown cards.
5. Cleanup-blocked cards with explicit blocker reason.
6. Failed agent cards.
7. Operational and subsystem failures.
8. Services.
9. Completed runs and informational cards.
10. Idle/unowned fallback work.
11. Footer/helper text.

The footer must remain compact. It can scroll, truncate, or become a single wide line before it steals meaningful active-agent height.

## Active Agent Card Rules

Active agent cards must:

- bottom-anchor output so token counts, prompts, and latest progress remain visible unless the operator has manually scrolled the card;
- show enough height to read current plans/tasks when space allows;
- update near real time without forcing every service/runtime card to capture every tick;
- avoid being stolen into service, dashboard, viewer, or subsystem groups by goal text or preview text;
- remain stable enough that the operator can track a card across refreshes.

If the wall cannot show all active agents with useful height, it should compress lower-priority groups first, then use whole-wall scroll indicators.

## Accordion Policy

Accordion state is view state, not lifecycle truth.

Each group policy is defined in [`group-registry.md`](group-registry.md). Supported policy terms:

- `auto_open_on_urgent` (future, not implemented): may reopen when new active or urgent content appears.
- `respect_manual_collapse`: stays collapsed until the operator opens it.
- `default_open`: initial state only.
- `default_collapsed`: initial state only.

Manual collapse must not be overwritten by routine refresh, runtime-card reload, janitor-status reload, or service noise. Only policy-defined edge events may reopen a group.

## Width And Columns

Columns should adapt to terminal width and group priority:

- active groups should use the fewest columns needed to keep each active card at or above the configured active minimum body height before lower-priority groups expand;
- service/completed groups may use more compact columns;
- one-card groups should use the available row instead of leaving unused columns;
- column caps should be configurable;
- width calculations must use terminal-display width semantics, not byte length.

## Footer And Helper Text

The footer should summarize status, filters, runtime events, stale warnings, and janitor health without becoming the main content.

Rules:

- Default footer height should be small.
- Footer width should use the full terminal width.
- Long helper text should truncate, scroll, or move to detail/help mode.
- Janitor status should be concise but must show stale/invalid/missing status.
- The footer must never overlap or push card content outside the viewport.

## tmux Status Row

The dashboard is often launched inside tmux, which may reserve a status row at the bottom of the client. That row is outside Cockpit's Bubble Tea viewport, but it still affects the operator experience because it consumes visible terminal space.

Default decision for the launcher/config phase:

- move tmux status to the top for the dashboard session when possible, so Cockpit keeps the bottom row for active TUI tails and its own footer;
- expose a launcher/config setting with allowed values `top`, `bottom`, and `hidden`;
- do not change global tmux status behavior outside the dashboard session.

Whatever choice is made, Cockpit's internal footer must not assume that the tmux status row is available for its own content.

## Visual Vocabulary

The wall should use familiar compact controls:

- `[^]` / `[v]` for detail/maximize.
- `[-]` / `[+]` for card collapse.
- Group carets for accordion state.
- Countdown text for marked teardown.
- Explicit blocker reason text for cleanup blocked.

Color is supporting information, not the only state indicator. Group colors and severity colors belong in theme/config tokens.

## Acceptance test inventory

- Expanding the terminal removes tmux dead space without manual resize. Mechanical gate: after resize to each tested width/height, rendered frame width equals terminal width, every rendered line display-width is <= terminal width, and tmux reports the dashboard window size matches the attached client size.
- Shrinking the terminal preserves coherent borders and no scattered render rows. Mechanical gate: no orphan ANSI fragments, no line exceeds terminal width, footer and scroll indicators remain inside viewport.
- Active group receives more height than services/completed when active work exists.
- One active card can relax past soft caps when lower groups are compacted.
- Manual collapse persists across snapshot reloads and janitor/runtime updates.
- Future policy-defined active/urgent auto-open is not implemented; current manual collapse persists across refreshes.
- Footer height stays within configured maximum.
- Group labels and countdowns remain visible within narrow widths.
- Launcher/config behavior around tmux status row is explicit and documented.

Minimum resize matrix for the first code pass:

- outside tmux: 80x24, 120x40, 160x50;
- inside dashboard tmux session: 80x24, 120x40, 160x50;
- expand then shrink sequence: 80x24 -> 160x50 -> 100x30 -> 80x24.

Golden wall fixture:

- at least one active visible agent;
- one delivered-idle pane;
- one held pane;
- one actually marked pane with countdown;
- one cleanup-blocked pane with evidence error;
- one failed-visible pane;
- one service card.


## Acquisition and navigation invariants

Snapshot and fast preview captures share admission and a per-request generation.
A late reply cannot replace a newer preview or release another request. Aggregate
rate budgets and round-robin fairness apply across both paths.

Arrow keys follow each group's rendered rows and horizontal card centers, not
a global column count. Vertical moves choose the nearest center in the next
row, including partial rows and different group widths; horizontal moves never
wrap rows. Collapse and resize recompute navigation from the rendering policy.

Runtime `shown` counts mean delivered cards after producer and consumer caps.
Pre-limit visible totals are labeled `total`; truncation is explicit. Raw,
grouped and hidden source counts remain distinct facts.


## Native-source height versus card height
Organized cards retain allocated body height even when an alternate-screen source is shorter. The source viewport remains bounded by its real screen; padding is not additional content. Lower section headers move down to use the dashboard body allocation. Collapse and scroll/control geometry remain owned by the renderer.

Native CLI resizing is distinct from card sizing. `--fit-native` opts into automatic source fitting using each rendered card's content width and height, after information rows are subtracted. The default monitor remains read-only. Fitting can be enabled without `--control`: key forwarding and cleanup stay disabled, and the footer reports `monitor-only + native fit`. Dumps never acquire sizing ownership.

Only visible, expanded, detached, single-window/single-pane managed agent sessions qualify. Attached, linked, split, dead, display-only and copy-mode panes are not resized. Server-side checks revalidate session/window/pane/PID and ownership before mutations. A window-local owner token prevents competing Cockpits from resizing each other's targets. The next structural poll reconciles changes; fitting is single-flight and never runs from View. Card geometry requests growth or shrinkage, but the terminal never shrinks below its pre-ownership width or height. Compact cards crop a usable native terminal rather than removing prompt/status chrome. The floor stays fixed for that ownership lease, so growing a card does not ratchet the minimum upward. No source-height cap feeds back into card allocation.

On collapse, hiding, attachment, or normal exit, the owner releases its windows and restores the original local `window-size` option (including inheritance). Launch dimensions are restored only for detached unsplit windows; an attached client's dimensions are governed by the restored policy. A previously explicit `manual` policy remains manual. Abrupt SIGKILL/server loss cannot run exit restoration: the local owner token intentionally blocks another Cockpit from silently taking over; inspect the affected window before manually restoring geometry/removing its stale token. Live deployment and any opt-in launcher change require separate operator authorization.

## Stable job stacking

Within each organized section, job precedence is oldest start first, not latest activity. Paint order remains reverse chronological, but every row starts at the left edge. Full rows precede a partial final row; no empty leading slots are reserved. This placement rule applies to every category. Removal compacts surviving cards without changing their chronological order. Section transitions still follow lifecycle state.

Native launch start time (or session creation) establishes order. Synthetic runtime creation age is distinct from activity age. Once observed, an identity's ordering time is fixed for that dashboard process. Missing birth metadata uses first observation and stable identity ties; this fallback cannot reconstruct historical order after a dashboard restart. Output, label changes and shuffled snapshots do not reorder surviving jobs. Keyboard movement and hit testing use the same geometry.

### Native resize regression checks

`TestNativeFitLifecycleFeedback` runs repeated capture, classification, category layout, native resize and real terminal redraw cycles on a disposable tmux socket. It covers idle, working, permission and background-work screens across compact and expanded layouts. It must keep idle category membership stable without preventing genuine activity transitions. `TestNativeSizeFloorDoesNotRatchet` protects the usable minimum, growth and return from growth. Existing full-height rendering, attachment/restoration and all-category alignment/navigation tests remain required. These tests run in `make check` and PR CI; the gate requires tmux instead of accepting skipped integration coverage. The screen fixture is deterministic, not proof of every provider version. `scripts/test_native_feedback.py` builds and exercises the actual dashboard binary on an isolated server as part of the existing Python test discovery in both make check and CI. It fails, rather than skips, without tmux. Capture transport errors are displayed separately and do not become worker output or alter its last observed lifecycle state. Release verification checks live stability after authorized activation.
