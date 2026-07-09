# OpenClaw Cockpit Vision

OpenClaw Cockpit is a fast, dependable single-pane operator wall for local tmux-backed CLI and agent work. It should make active agents, held evidence, teardown state, failures, services, and runtime health obvious without forcing the operator to step through panes or surrender normal tmux workflows.

The top-level product and contract brief lives in [`DESIGN_BRIEF.md`](DESIGN_BRIEF.md). Lifecycle, layout, group, and config contracts live in [`docs/lifecycle-contract.md`](docs/lifecycle-contract.md), [`docs/layout-contract.md`](docs/layout-contract.md), [`docs/group-registry.md`](docs/group-registry.md), and [`docs/config-contract.md`](docs/config-contract.md).

## Product principles

- **Monitor first.** Snapshot accuracy, low capture overhead, readable output, and long-running stability outrank feature breadth.
- **Keyboard first, mouse optional.** Every core navigation and focus workflow must remain efficient from the keyboard; mouse affordances may improve discovery but must not become required.
- **Observe by default.** Hiding, filtering, collapsing, and reordering are Cockpit view state. tmux sessions or processes change only through explicit external tools such as `session_hygiene.py` or `safe_kill.py`; Cockpit itself never performs automatic cleanup.
- **Lifecycle vocabulary must be truthful.** Active, held, marked, blocked, failed, completed, and cleaned labels must map to real lifecycle facts, not convenient buckets.
- **Resize must be owned.** The dashboard should follow terminal expand/shrink behavior without dotted dead space, cut borders, or manual patching.
- **Local and inspectable.** Prefer direct tmux commands, explicit state, and the existing `--dump` automation boundary. Add background services, remote access, network APIs, or plugin execution only for a demonstrated workflow that cannot stay local and simple.
- **Small configuration surface.** Prefer useful defaults and stable CLI flags. Add persistent configuration when repeated operator need establishes a durable setting and owner.
- **Conservative persistence.** Saved layouts and history require a versioned, reviewable format, a preview before restore, and clear handling of commands, paths, environment data, and missing resources.
- **Lean implementation.** Keep the Go package boundaries clear, the Charmbracelet stack current, dependencies justified, and behavior covered close to its owning package.

## Priority order

1. Correct, efficient capture and resilient handling of missing, empty, or changing tmux state.
2. Truthful lifecycle grouping, countdowns, holds, failed-state behavior, and cleanup-blocked labels.
3. Fast navigation, search, focus, and readable status across many sessions and terminal sizes.
4. User-controlled capture depth, refresh behavior, group policy, footer height, and theming when they preserve simplicity.
5. Workspace snapshot and restore only after the persistence safety contract is defined.

Remote tmux, general HTTP/gRPC APIs, notification systems, and arbitrary plugin hooks are later opportunities, not default roadmap commitments. They need a concrete user problem, a bounded security model, and proof that a smaller local integration is insufficient.

## Quality bar

Changes should preserve supported CLI behavior, include regression coverage when practical, pass formatting, lint, unit and race tests, and exercise the built binary against a real tmux session when runtime behavior changes. Packaging changes must keep Go, Nix, GoReleaser, and Homebrew paths reproducible.

Detailed architecture and roadmap notes live in [`docs/spec.md`](docs/spec.md). If docs disagree, use the source-of-truth order in [`DESIGN_BRIEF.md`](DESIGN_BRIEF.md).
