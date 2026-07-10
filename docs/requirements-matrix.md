# Requirements Matrix

This matrix turns Daniel's 2026-07-09 brain dump and the current bug diagnosis into source-of-truth documentation and future implementation gates.

This is a planning and gate map, not execution approval. Any metadata restamping, hold release, janitor test rewrite, live hygiene apply cycle, tmux session mutation, or Cockpit code change requires a separate explicit implementation approval.

## Product Requirements

| Requirement | Type | Source Of Truth | Next Gate |
| --- | --- | --- | --- |
| Single terminal pane shows all important CLI/agent work. | Vision | `DESIGN_BRIEF.md` | README usage and layout tests |
| Active agents are first and visually dominant. | Product/Layout | `DESIGN_BRIEF.md`, `docs/layout-contract.md` | Group/order/height regression tests |
| Operator can see real interactive TUI state. | Product/Layout | `docs/layout-contract.md` | Active card bottom-anchor tests and live screenshot/smoke |
| Supporting system state remains visible without stealing focus. | Product/Layout | `DESIGN_BRIEF.md`, `docs/layout-contract.md` | Group compactness tests |
| Cockpit stays presentation-only. | Boundary | `DESIGN_BRIEF.md`, `docs/lifecycle-contract.md` | Tests keep cleanup controls disabled/routed to hygiene |
| Settings and colors move to config. | Config | `docs/config-contract.md` | Config parser/theme tests |
| Group names/order/policies are canonical. | Contract | `docs/group-registry.md` | group registry fixture tests |

## Lifecycle Requirements

| Requirement | Type | Source Of Truth | Next Gate |
| --- | --- | --- | --- |
| Holds block automatic cleanup. | Contract | `docs/lifecycle-contract.md` | Janitor and UI grouping tests |
| Hold release is explicit and evidence-aware. | Contract | `docs/lifecycle-contract.md` | `agent_wall.py release-hold` tests in workspace |
| Actually marked panes show teardown countdown. | Contract/UI | `docs/lifecycle-contract.md`, `docs/layout-contract.md` | Countdown render tests |
| Cleanup-blocked panes show blocker reason. | Contract/UI | `docs/lifecycle-contract.md` | Evidence/refusal mapping tests |
| Failed panes clear after visible grace only when evidence is valid. | Contract | `docs/lifecycle-contract.md` | Failed grace/evidence tests |
| Empty evidence files are blockers, not success. | Contract | `docs/lifecycle-contract.md` | Failed empty-artifact test |
| Relative run roots block cleanup with visible reason. | Contract | `docs/lifecycle-contract.md` | Relative run-root test |
| Cosmetic output does not cancel teardown. | Contract | `docs/lifecycle-contract.md` | Tail-churn test |
| Signal conflicts are resolved deterministically. | Contract | `docs/lifecycle-contract.md` | precedence fixture table |
| Delivered-idle has a defined group mapping. | Contract | `docs/lifecycle-contract.md`, `docs/group-registry.md` | delivered-idle classification tests |
| Held+marked conflict is not rendered as a valid countdown. | Contract | `docs/lifecycle-contract.md` | held+marked conflict test |

## Layout Requirements

| Requirement | Type | Source Of Truth | Next Gate |
| --- | --- | --- | --- |
| Terminal expand removes dotted dead space. | Layout/Runtime | `docs/layout-contract.md` | mechanical resize matrix plus tmux attached-client smoke |
| Terminal shrink preserves borders and rows. | Layout/Runtime | `docs/layout-contract.md` | mechanical resize matrix plus screenshot/snapshot test |
| Footer/helper text uses full width and stays compact. | Layout | `docs/layout-contract.md`, `docs/config-contract.md` | footer max-height tests |
| Manual accordion collapse is respected. | UX | `docs/layout-contract.md` | collapse persistence tests |
| Agent-focused groups can auto-open by policy. | UX/Config | `docs/group-registry.md`, `docs/layout-contract.md`, `docs/config-contract.md` | edge-event auto-open tests |
| Services and completed groups compress first. | Layout | `docs/layout-contract.md` | height allocation tests |

## Current Bug Mapping

| Observed Bug | Classification | Contract Fix | Code Phase Gate |
| --- | --- | --- | --- |
| Items marked for teardown are not tearing down. | Lifecycle/janitor drift | Distinguish true marked, blocked, held, failed grace, and evidence refusal. | Future implementation gate: plan/apply/status tests with each reason |
| Active agents appear and disappear. | Grouping/source drift | Signal precedence and managed active identity win over stale/preview/goal/service heuristics. | Future implementation gate: precedence/classification tests |
| Dotted outline/dead space after resize. | Layout/runtime contract gap | Launcher and UI must follow attached terminal geometry. | tmux client resize smoke |
| Held teardown items stay indefinitely. | Hold/release contract gap | Holds are stop signs; release path is explicit; blocked label explains why. | Future implementation gate: hold release plus UI label tests |
| Failed items never go away. | Evidence contract gap | Failed grace clears only with non-empty valid evidence; empty artifacts show blocked until remediated. | Future implementation gate: failed evidence tests |
| Accordions reopen after operator closes them. | Verified mechanism: collapse state is process-memory only (`state.go` seeds defaults once; nothing persists it) and the launcher's `respawn-pane` restarts the process, resetting every manual collapse; group flicker from janitor mark/cancel oscillation was the other reopen impression (fixed by the janitor baseline + classifier slices). | Manual collapse persists across snapshot/janitor/runtime refresh within a process (tested). Respawn resets to registry defaults — accepted behavior for this arc, recorded here; view-state persistence across respawn is deliberate future scope. | Manual collapse persistence tests (implemented: `TestManualCollapsePersistsAcrossJanitorAndSnapshotRefresh`, `TestGroupCollapseTogglePersistsAcrossSeeding`) |
| Footer/helper text consumes too much space. | Layout priority bug | Footer max height and wide rendering are configurable. | footer constraint tests |
| tmux status row consumes bottom space. | Launcher/config decision | Dashboard status row defaults to top, with top/bottom/hidden setting. | launcher/status-row smoke |
| Colors/settings hardcoded. | Config gap | Theme/timer/group defaults centralize in config. | config/theme tests |

## Documentation Set

- `DESIGN_BRIEF.md`: product job, boundaries, operator experience, acceptance standard.
- `VISION.md`: short product vision and principles.
- `docs/lifecycle-contract.md`: lifecycle/janitor/metadata contract.
- `docs/group-registry.md`: canonical group names, ranks, definitions, and accordion policies.
- `docs/layout-contract.md`: resize, active priority, accordion, footer, visual contract.
- `docs/config-contract.md`: settings extraction and validation contract.
- `docs/spec.md`: implementation architecture and roadmap, subordinate to the above contracts.
- `README.md`: operator-facing install/run/usage summary.
- Workspace `tools/tmux/README.md`: integration runbook for launchers, janitor, and visible model workers.
