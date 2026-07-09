# OpenClaw Cockpit - Operator wall for tmux-backed agent work

`openclaw-cockpit` is a Charmbracelet-powered dashboard for watching tmux sessions, CLI agents, OpenClaw runtime cards, services, and evidence-bearing review/build lanes without leaving the terminal.

It began as Peter Steinberger's `tmuxwatch`; this repo keeps that history and MIT attribution while turning the tool into an OpenClaw-specific cockpit with runtime, ACP, TaskFlow, service, lifecycle, and display-only operator cards.

Product principles and scope boundaries live in [`VISION.md`](VISION.md). The top-level design contract lives in [`DESIGN_BRIEF.md`](DESIGN_BRIEF.md). Lifecycle, group, layout, and config contracts live in [`docs/lifecycle-contract.md`](docs/lifecycle-contract.md), [`docs/group-registry.md`](docs/group-registry.md), [`docs/layout-contract.md`](docs/layout-contract.md), and [`docs/config-contract.md`](docs/config-contract.md). Detailed architecture and roadmap notes live in [`docs/spec.md`](docs/spec.md).

## Highlights
- **Live tmux snapshot**: Polls `list-sessions`, `list-windows`, and `list-panes`, stitches the hierarchy together, and shows the latest capture-pane output per session.
- **Agent-first operator wall**: Organized mode is being aligned to the contract groups: active agent TUIs, held/blocked cleanup, teardown countdowns, failed agents, operational/subsystem failures, services, and completed work. The current implementation still has known lifecycle-label drift documented in the contract set.
- **Tab-aware layout**: The strip lists the grid plus every visible tmux session; click or `shift+left/right` to jump tabs, `ctrl+m` toggles full-screen, and `esc` returns to the grid.
- **Keyboard & mouse aware**: `/` to search, arrow/PageUp/PageDown to scroll, collapse cards with `z`/`Z`, maximise via `ctrl+m` or the `[^]` control, and mouse clicks/scrolls to focus, collapse, hide cards, or switch tabs.
- **Command palette (`ctrl+P`)**: Run view actions such as refresh and show hidden from a centered overlay. Stale cleanup entries are disabled and route operators to `session_hygiene.py` / `safe_kill.py`.
- **Automation friendly**: `--dump` prints the current tmux topology as JSON for scripts or debugging.

## Install & Run
```sh
# Install directly with Go tooling
go install github.com/cassthebandit/openclaw-cockpit/cmd/openclaw-cockpit@latest
openclaw-cockpit --version  # should print OpenClaw Cockpit 0.9.5

# Nix/NixOS
nix run github:cassthebandit/openclaw-cockpit

# Or add to your flake inputs for system integration
# inputs.openclaw-cockpit.url = "github:cassthebandit/openclaw-cockpit";
# Then use: inputs.openclaw-cockpit.packages.${system}.default

# best practice: spawn inside tmux so key bindings work as expected
tmux new-session -d -s watch './gorunfresh --trace-mouse'
tmux attach -t watch

# prefer to run outside tmux?
./gorunfresh --dump   # runs directly unless TMUXWATCH_FORCE_TMUX=1 is set
./gorunfresh --watch  # starts poltergeist + polter --watch to auto-restart after successful builds
./scripts/run-hot.sh  # single command that starts daemon in the background and launches openclaw-cockpit with hot reload
```
Press `q` (or double `ctrl+c`) to exit. Prefer running OpenClaw Cockpit in its own tmux session to keep the UI isolated from your workspaces. For local development you can substitute `./gorunfresh --debug-click 30,10 --trace-mouse` inside the session to replay a mouse event while inspecting BubbleZone logs.

## CLI Flags
- `--interval <duration>`: tmux poll frequency (default `1s`).
- `--tmux <path>`: tmux binary to execute (defaults to `$PATH`).
- `--dump`: emit the current snapshot as indented JSON and exit.
- `--version`: print the build/version string.
- `--organize`: organize overview cards into Cockpit groups.
- `--janitor-status <path>`: read the janitor status sidecar JSON for cleanup counts/countdowns.

## Keyboard & Mouse Cheat Sheet
```
/ or ctrl+f        open search; type to filter sessions/windows/panes
esc                clear search, close palette, or leave detail view
shift+left/right   switch tabs
H                  show hidden sessions
X                  cleanup disabled; use session_hygiene.py or safe_kill.py
ctrl+X             cleanup disabled; use session_hygiene.py or safe_kill.py
ctrl+P             open/close the command palette
ctrl+m             maximise/restore the focused session
z / Z              collapse focused session / expand all sessions
q / ctrl+c         quit (double ctrl+c quits even if pane is alive)
mouse              click `[^]/[v]` to maximise/restore, `[-]/[+]` to collapse/expand, `[x]` to hide locally; scroll to browse logs
```

## Architecture
- `cmd/openclaw-cockpit/`: CLI entry point, flag parsing, Bubble Tea program setup.
- `internal/tmux/`: thin wrapper over the tmux binary (snapshot capture, capture-pane, send-keys, kill-session, option queries).
- `internal/ui/`: Bubble Tea model split into focused files (`model`, `update`, `handlers`, `cards`, `status`, `palette`, `overlay`, etc.).
- `docs/`: contributor docs (`AGENTS.md`, `idiomatic-go.md`).

The UI intentionally avoids third-party “magic”; it leans on Bubble Tea + Lip Gloss primitives so behaviour is explicit.

## Development Workflow
Use the pnpm scripts to mirror the Go tooling:
```sh
pnpm format  # gofumpt -w .
pnpm lint    # golangci-lint run
pnpm test    # go test ./...
pnpm build   # go build ./cmd/openclaw-cockpit
pnpm start   # runs ./gorunfresh (prefer inside tmux)
```

You can still call the Go targets directly:
```sh
# format + lint
make fmt
make lint

# run tests (includes table-driven/unit tests in internal/ui and internal/tmux)
go test ./...

# fresh rebuild & run inside tmux (guards against outside usage)
./gorunfresh --dump  # validate tmux JSON snapshot; respects TMUXWATCH_FORCE_TMUX

go run ./cmd/openclaw-cockpit --dump  # (inside tmux) validate tmux JSON snapshot for debugging
```
Guidelines live in `docs/idiomatic-go.md`; treat it as required reading. Key points:
- Always run the app inside tmux; tests that touch tmux spawn/destroy their own sessions.
- Run `gofumpt`, `golangci-lint`, and `govulncheck` before opening a PR.
- Table-driven tests go beside their packages (`*_test.go`); fixtures live under `testdata/`.
- The `gorunfresh` helper clears cache and re-runs the app but refuses to execute unless you launch it from within tmux.

## Roadmap (short list)
- Align lifecycle labels and janitor behavior with `docs/lifecycle-contract.md`.
- Make resize, accordion persistence, active-agent priority, and footer constraints match `docs/layout-contract.md`.
- Extract group policy, colors/theme, timers, and capture budgets per `docs/config-contract.md`.
- Revisit pane interaction history and saved layouts only after the operator-wall contract is stable.

## License
Released under the [MIT License](./LICENSE).
