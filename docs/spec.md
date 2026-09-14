# OpenClaw Cockpit architecture

This document describes the implementation that exists today. Product intent lives in [`../DESIGN_BRIEF.md`](../DESIGN_BRIEF.md); lifecycle, group, layout, and configuration behavior is defined by the contract documents in this directory.

## Package map

- `cmd/openclaw-cockpit`: CLI flags, build identity, config loading, tmux client setup, and Bubble Tea startup.
- `internal/tmux`: structured session/window/pane snapshots, pane capture, tmux option parsing, and guarded control actions.
- `internal/ui`: state, capture scheduling, lifecycle classification, organized groups, runtime cards, janitor status, rendering, navigation, and input handling.
- `internal/zone`: ANSI-aware hit testing for mouse controls.
- `scripts/`, `gorunfresh`, and `poltergeist.config.json`: optional local development launchers.

Optional public launchers, assignment supervision and session hygiene live in `helpers/tmux/`. Existing-session selection, observation and bounded native retirement are implemented in `adoption.py` under the hygiene owner. Built-in native process/prompt adapters and tested exit profiles live in `runtime_adapters/` with static dispatch in `native_runtimes.py`; they do not own policy, storage or terminal mutations. They use the existing hygiene status/archive stores; the Go dashboard is a reader by default. Explicit `--fit-native` permits guarded terminal geometry changes only (see `layout-contract.md`); it does not grant input or lifecycle authority. Cockpit does not require these helpers to monitor ordinary tmux sessions.

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
gofumpt -l . # must produce no paths (use make fmt to format)
golangci-lint run --timeout=5m --max-issues-per-linter=0 --max-same-issues=0
go test ./...
go test -race ./...
python3 -m unittest discover -s scripts -p 'test_*.py'
python3 -m pytest helpers/tests -q
make cross-build
```

`make check` runs the complete gate above. tmux must be installed; a skipped live test is not runtime proof.

Go integration fixtures share `internal/testutil` and helper Python tests share
`helpers/tests/support.py`. Both own unique short `-S` socket paths under `/tmp`,
start tmux with `-f /dev/null`, and tear down only their own server and socket
directory. No fixture uses the operator's default server. Python tests import
the `helpers.tmux` package directly and can run individually from the repo root
(for example, `python3 -m pytest helpers/tests/test_holds.py -q`). Shared data and
resource context managers do not merge the helper pytest and script unittest
runner lifecycles; both remain separate gates in `Makefile`.

Changes to terminal behavior should also be exercised against a real disposable tmux session. Packaging changes must preserve the release targets in `.goreleaser.yaml` and the Nix flake.

## Deliberate non-goals

- cleanup authority in the dashboard, or automatic hold release;
- Gateway, authentication, or OpenClaw configuration mutation;
- a remote tmux API or plugin execution layer;
- treating pane text as proof that a build or review was accepted;
- persistence or restore without a separate safety contract.

Additional runtime interfaces can be contributed using the [adapter contract](runtime-adapters.md).
