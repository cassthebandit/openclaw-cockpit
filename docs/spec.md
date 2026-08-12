# OpenClaw Cockpit architecture

This document describes the implementation that exists today. Product intent lives in [`../DESIGN_BRIEF.md`](../DESIGN_BRIEF.md); lifecycle, group, layout, and configuration behavior is defined by the contract documents in this directory.

## Package map

- `cmd/openclaw-cockpit`: CLI flags, build identity, config loading, tmux client setup, and Bubble Tea startup.
- `internal/tmux`: structured session/window/pane snapshots, pane capture, tmux option parsing, and guarded control actions.
- `internal/ui`: state, capture scheduling, lifecycle classification, organized groups, runtime cards, janitor status, rendering, navigation, and input handling.
- `internal/zone`: ANSI-aware hit testing for mouse controls.
- `scripts/`, `gorunfresh`, and `poltergeist.config.json`: optional local development launchers.

Workspace launchers and cleanup tools are intentionally outside this repository. A typical OpenClaw installation keeps them under `~/.openclaw/workspace/tools/`, but Cockpit does not require that workspace to monitor ordinary tmux sessions.

## Data flow

Each refresh begins with tmux topology. Cockpit joins sessions, windows, panes, pane options, and bounded pane captures into a snapshot. The UI then adds any configured janitor status and optional runtime cards, classifies the resulting records, and renders the wall.

The primary inputs are:

- tmux session, window, and pane facts;
- captured pane output;
- optional `@oc_*` tmux metadata;
- optional janitor status JSON;
- optional `runtime-card.v1` snapshot output;
- local view state such as search, scroll, focus, tabs, and collapsed groups.

Capture work is budgeted so a large number of quiet background panes does not crowd out focused or active work. External text is bounded and sanitized before rendering.

## Safety boundary

Monitor-only mode is the default. In that mode Cockpit cannot forward keys and has no cleanup path. Hiding, searching, collapsing, grouping, and opening detail views affect only the dashboard.

`--control` enables deliberate, partial key forwarding to a focused pane. Dashboard-reserved keys still belong to Cockpit, so control mode is not a transparent terminal proxy.

Cockpit never decides that a session is safe to kill. Lifecycle metadata and janitor status are presentation inputs, not delegated authority.

## Organized wall

`--organize` runs records through the lifecycle and workload classifiers defined in [`group-registry.md`](group-registry.md) and [`lifecycle-contract.md`](lifecycle-contract.md). Active agents receive the first claim on space. Failures and cleanup blockers remain visible; services, completed runs, dashboards, viewers, and idle work compress before active work does.

Runtime cards use the same presentation groups but remain visibly sourced from the OpenClaw adapter. Stable tmux and runtime identities prevent routine refreshes from looking like new work.

## Configuration

The implemented JSON config covers footer height, stale display thresholds, and initial group expansion. CLI flags override one-run behavior. Janitor facts remain authoritative for cleanup countdowns. See [`config-contract.md`](config-contract.md) for the exact surface and precedence.

## Verification

Changes should run the same gates as CI:

```sh
gofumpt -w .
golangci-lint run --timeout=5m --max-issues-per-linter=0 --max-same-issues=0
go test ./...
go test -race ./...
go build ./cmd/openclaw-cockpit
```

Changes to terminal behavior should also be exercised against a real disposable tmux session. Packaging changes must preserve the release targets in `.goreleaser.yaml` and the Nix flake.

## Deliberate non-goals

- automatic session cleanup or hold release;
- Gateway, authentication, or OpenClaw configuration mutation;
- a remote tmux API or plugin execution layer;
- treating pane text as proof that a build or review was accepted;
- persistence or restore without a separate safety contract.
