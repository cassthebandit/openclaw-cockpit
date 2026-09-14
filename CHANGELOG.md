# Changelog

## [Unreleased]

### Fixed
- Require explicit adoption selection for legacy detected-session cleanup; completion-looking terminal text alone no longer selects a session for removal.
- Reject initial assignment submission until native session binding and any required bootstrap are ready, without consuming the one-shot submission.
- Keep a single snapshot polling loop across startup, errors, and manual refresh; share capture observations across snapshot and fast paths.
- Display capture-error toasts promptly and avoid rebuilding unchanged frames after successful native-size completion.

### Changed
- Amortize capped session-history trimming with headroom and one history-incomplete marker.
- Replace repeated ANSI regex processing with a differential-tested scanner while preserving classification semantics.
- Consolidate disposable tmux fixtures and make helper tests independently runnable without import-order dependencies.

## [0.9.5] - 2026-07-07

### Changed
- Reorganized the Cockpit wall around live work: a new **Interactive Agents** band leads, followed by **Your Call** (operator decisions), **System Problems** (terminal failures / stale), a consolidated **Runtime** band, and **Completed Agent Runs**. Agent identity is now classified before attention and dashboard/viewer heuristics, so a live agent is never scattered across sections or stolen into Dashboards by its goal or preview text.
- Consolidated the runtime source-card bands (`current_work` / `route_health` / `delivery_handoff` / `source_unknown`) into one **Runtime** section; `needs_decision` cards route to **Your Call**.

### Added
- **Lifecycle truth**: finished-but-idle managed agent TUIs (Codex / Fable-Claude / Gemini / AGY) are detected read-only and shown under **Completed Agent Runs** with a "finished · awaiting review" badge instead of appearing to still run. This is presentation-only and writes no tmux metadata.
- **Accordions**: group dividers collapse/expand with a caret, count, and summary. Interactive Agents and Your Call default expanded; System Problems expands when non-empty; other bands default collapsed.
- **Gated whole-wall scroll**: the wall scrolls as one when the current rendered layout overflows, without fighting per-card scroll. Wheel over a scrollable card scrolls the card; wheel over a gutter/divider or an unscrollable/at-boundary card scrolls the wall.
- `agent_wall.py release-hold`: a dedicated, non-destructive command to clear `@oc_hold_reason` and stamp completion metadata after evidence capture, with exact-target enforcement, `--dry-run` JSON, and explicit `--evidence-captured` / `--allow-hygiene-after-release` confirmation. It never kills, archives, hides, detaches, or applies session hygiene.

## [0.9.4] - 2026-07-07

### Changed
- Let OpenClaw runtime source-card groups render 5 across on wide Cockpit walls while keeping active and urgent work cards spacious.
- Clarified cleanup controls in README and CLI help: stale cleanup is routed to `session_hygiene.py` / `safe_kill.py`; `--control` restores key forwarding, not session cleanup.

### Fixed
- Reworded the local `[x]` hide toast so it no longer implies a tmux session was closed.

## [0.9.3] - 2026-06-11

### Added
- Added Nix flake packaging for `nix run` installs and NixOS/system integration (thanks @mipmip).
- Added Poltergeist-powered hot reload helpers for local tmuxwatch development.
- `pnpm start`, `build`, `test`, `lint`, and `format` scripts for a uniform local workflow alongside the Go tooling.
- Added CLI flag integration tests and full-path Makefile tool resolution (thanks @micahstubbs).
- GitHub Actions CI for formatting, tests, and lint checks on pull requests and `main`.
- Added GoReleaser archives for macOS, Linux, and Windows, with automatic Homebrew tap updates.

### Changed
- Updated the Charmbracelet stack and aligned the UI with the stable `tea.View` API, including an internal mouse-zone adapter for the new module paths.
- Made the dashboard layout more adaptive with wrapped tabs, centered empty state content, stretched headers, and active/stale session counts.
- Reduced pane capture load, rotated background capture work, preserved manual scroll positions, and truncated oversized stale-session footer summaries.
- Expanded tmux no-server detection for macOS socket errors.
- Updated Go dependencies, Nix lockfile, pnpm package-manager pin, and GitHub Actions versions.

### Fixed
- Validate `--debug-click` before tmux discovery so argument errors work without an installed tmux binary and Nix package checks pass.

## [0.9.2] - 2025-11-05

### Added
- Graceful empty state panel when no tmux sessions are present, highlighting how to start a new one.
- Tests covering `clampHeight`, empty state rendering, tmux “no server” detection, and layout viewport spacing.

### Changed
- Footer layout now reserves explicit vertical spacing, keeping the status legend visible regardless of grid height.
- Card viewports trimmed to prevent clipping at the bottom of the grid while preserving tab/hover affordances.
- Homebrew release process documented in `RELEASE.md` and cross-linked from `AGENTS.md`.

### Fixed
- Handling the “tmux not running” and “tmux missing” cases without surfacing errors; UI shows the empty state instead of partial rendering.
- Mouse hit-testing and footer placement when the card grid empties or shrinks quickly.

## [0.9.1] - 2025-11-05

### Added
- Per-session tabs alongside the Overview tab so each tmux session can be opened directly; tabs support mouse selection and keyboard cycling (`shift+left/right`).
- Hover affordances for cards and controls (`[^]`, `[-]`, `[x]`) to make clickable targets more discoverable.
- Footer viewport that keeps the key legend visible regardless of card height, plus `gorunfresh` helper for quick rebuilds.

### Changed
- Session headers now suppress the default tmux hostname title, keeping pane names focused on meaningful commands.
- Grid layout clamps card heights and spacing to prevent overlap with the footer on short terminals.
- README refreshed with Homebrew install instructions and idiomatic Go guide linked from docs.

### Fixed
- Mouse hit-testing across cards and tab bar to ensure clicks land on the correct session.
- Stale session detection recalculated on new pane output, reducing false positives.
- Status footer clipping when cards occupied the entire terminal height.

## [0.9] - 2025-11-05

- Initial implementation of the `tmuxwatch` CLI with Bubble Tea interface.
- tmux session/window/pane polling and auto-refresh.
- Added debug `--dump` mode for printing snapshots and hardened tmux timestamp parsing to accept missing data.
- Rebuilt the UI around session preview cards that capture active panes, auto-scroll, and share terminal space intelligently.
- Added live search/filter across sessions, windows, and panes.
- Mouse support: click to focus, scroll, and close cards; printable keys now forward to the focused tmux pane.
- Cards pulse briefly when new output arrives to highlight active panes.
- Headers now surface pane last activity timestamps and exit statuses.
- Ctrl+C now forwards to the live pane; press twice quickly to exit tmuxwatch (single press still quits if no live pane is focused).
- Added `.golangci.yml`, gofumpt formatting, `Makefile` helpers, and documentation for modern Go lint/format tooling.
- Integrated Bubble Tea viewport to clamp pane height, enable scrolling (ctrl+d/u, ctrl+f/b, g/G), and prevent oversized buffers from blowing up the layout.
- Documented tmuxwatch intent and primary use cases in the README.
