# OpenClaw Cockpit Spec

This document describes the current implementation architecture and roadmap. The product contract lives in [`../DESIGN_BRIEF.md`](../DESIGN_BRIEF.md). Lifecycle, group, layout, and config rules live in [`lifecycle-contract.md`](lifecycle-contract.md), [`group-registry.md`](group-registry.md), [`layout-contract.md`](layout-contract.md), and [`config-contract.md`](config-contract.md).

If this spec conflicts with those contracts, treat the conflict as implementation drift to resolve in the next code phase.

## Current Architecture

- `cmd/openclaw-cockpit`: CLI entry point, flags, version output, Bubble Tea program setup, and platform-specific terminal-tab helpers.
- `internal/tmux`: thin tmux wrapper for structured session/window/pane snapshots, pane captures, options, and cockpit metadata parsing.
- `internal/ui`: Bubble Tea model, update loop, render cache, card rendering, grouping, layout, status/footer, runtime-card integration, janitor status rendering, input handlers, timeline, and stale/lifecycle classifiers.
- `internal/zone`: BubbleZone-compatible hit testing and ANSI-aware zone scanning.
- `gorunfresh` and `scripts/`: local development launch helpers.
- Workspace integration lives outside this repo under `/Users/cass/.openclaw/workspace/tools/tmux/`, including `agent_wall.py`, `session_hygiene.py`, `start_openclaw_cockpit.sh`, and installer/runbook helpers.

## Runtime Inputs

Cockpit reads:

- tmux session/window/pane topology;
- tmux pane output and pane options;
- OpenClaw `@oc_*` metadata from managed panes;
- optional janitor status JSON from `session_hygiene.py`;
- optional OpenClaw runtime cards;
- local UI input such as filters, tabs, detail mode, collapse state, and mouse/keyboard events.

Cockpit writes no lifecycle state. It may write normal process output/logs when launched by surrounding tools, but the UI itself is not the cleanup or metadata mutation authority.

## CLI Surface

Current stable flags include:

- `--interval`: tmux poll frequency.
- `--tmux`: tmux binary path.
- `--dump`: print current tmux snapshot JSON and exit.
- `--version`: print version.
- `--organize`: organize overview cards into cockpit groups.
- `--janitor-status`: read janitor status sidecar JSON.

The launcher may pass additional local defaults such as organized mode, runtime cards, dashboard self-exclusion, column caps, and capture budgets. Those defaults should migrate toward the config contract instead of accumulating as hidden constants.

## Presentation Model

The organized wall groups sessions into the canonical operator-focused accordions listed in [`group-registry.md`](group-registry.md).

The current source does not yet implement every group exactly. The next code phase should add or rename groups as needed to match [`group-registry.md`](group-registry.md) and [`lifecycle-contract.md`](lifecycle-contract.md), especially the distinction between actually marked teardown and cleanup-blocked/refused debt.

## Lifecycle Integration

The UI currently computes presentation state from a mix of:

- pane liveness and dead status;
- `@oc_state`, `@oc_kind`, `@oc_agent`, `@oc_hold_reason`, `@oc_cleanup_policy`, `@oc_evidence_path`, `@oc_teardown_marked_at`, and `@oc_janitor_state`;
- semantic tail detection for completed/failed agent TUIs;
- stale thresholds;
- OpenClaw runtime-card presentation groups;
- janitor status sidecar cycle counts.

This logic exists to make the wall useful, but it must not override the lifecycle contract. The next implementation pass should harden the state machine, labels, and tests around the contract.

## Layout Implementation

The UI uses Bubble Tea v2, Lip Gloss v2, and the local zone scanner. The render path composes:

1. title/filter/header;
2. session cards or organized accordions;
3. footer/status;
4. overlays such as the command palette.

The model tracks terminal width/height, page-scroll state, per-card geometry, hover/focus/cursor state, and render-cache invalidation. The current code already has tests for grouped height allocation, footer accounting, whole-wall scroll, collapse state, and render caching. The next code phase should extend those tests to cover the explicit resize and accordion-policy contracts.

## Roadmap

This roadmap is product-level. The reviewed implementation packet at `memory/runs/cockpit-contract-implementation-packet-20260709T1727/outputs/implementation-packet.md` now uses a Fable-reviewed vertical slice build sequence. Do not treat the roadmap phase numbers as interchangeable with implementation slices:

- Slice 0 preserves the docs/source baseline before code.
- Slice 1 hardens janitor hold behavior before any cancellation-loop repair.
- Slice 2 repairs mark/cancel baseline bookkeeping, sidecar hygiene, and then timer constants.
- Slice 3 drains live debt under separate approval and adds producer degraded-artifact behavior.
- Slice 4 implements lifecycle classifier and group registry in Cockpit.
- Slice 5 implements layout, resize, accordion, footer, and launcher status-row behavior.
- Slice 6 extracts config and theme after behavior is stable.
- Slice 7 performs live verification and install/respawn decision.

If the packet and this roadmap disagree, the design brief and contract docs govern, then the packet must be patched before build approval.

### Phase 1: Monitor Foundations

- tmux snapshot and JSON dump.
- Session/window/pane preview cards.
- Keyboard and mouse navigation.
- Search/filter.
- Detail/maximize mode.

### Phase 2: Operator Wall Contracts

- Truthful lifecycle group labels.
- Separate marked, held, cleanup-blocked, failed, completed, service, operational, and subsystem states.
- Janitor countdown and refusal details.
- Hold semantics aligned with hygiene docs.
- Failed visible grace plus evidence-blocked display.
- Dynamic terminal resize without dead space/corrupt rows.
- Accordion persistence and per-group auto-open policy.
- Footer/helper height constraints.

### Phase 3: Config Extraction

- Central config file and effective-config dump.
- Theme/color tokens.
- Group order, labels, policies, and layout constraints.
- Lifecycle timers aligned with janitor status.
- Capture and refresh budgets.
- Footer max height.

### Phase 4: Operator Polish

- Better detail views for blocked cleanup and failed lanes.
- Focused janitor diagnostics.
- Optional snapshot/restore only after a separate persistence safety contract.
- Packaging and release cleanup.

## Non-Goals For The Current Contract Phase

- No cleanup buttons in Cockpit.
- No direct tmux kill authority in the UI.
- No Gateway/config/auth mutation.
- No remote tmux/API/plugin layer.
- No workspace snapshot/restore.
- No code changes until the docs and contracts are aligned and reviewed.

## Quality Bar

Contract-changing code must include:

- focused unit tests for lifecycle classification and group labels;
- layout tests for height/width/footer behavior;
- janitor-status tests for stale, invalid, missing, marked, refused, and countdown states;
- terminal resize smoke evidence when runtime geometry changes;
- `go test ./...`;
- a built-binary smoke against a real tmux session when UI runtime behavior changes.
