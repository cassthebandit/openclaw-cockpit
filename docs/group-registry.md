# Group Registry

This is the canonical operator-facing group registry. `DESIGN_BRIEF.md` may summarize it, but this table is the detailed source of truth for group names, order, definitions, and accordion behavior.

| Rank | Group | Definition | Primary Source | Default Accordion Policy |
| --- | --- | --- | --- | --- |
| 0 | `Active Agents` | Live or resumable agent TUIs: starting, running, waiting, blocked, review, approval/prompt, or real resumed work. Live held agents stay here with a hold indicator. | tmux liveness, `agent_wall.py`, semantic lifecycle | Open by default; manual collapse persists. |
| 1 | `Held / Teardown Blocked` | Completed, delivered-idle, stale, or dead agent panes blocked by explicit hold or held+marked conflict. | `@oc_hold_reason`, janitor refusal | Open when non-empty; manual collapse persists. |
| 2 | `Marked For Teardown` | Sessions actually marked by the janitor and eligible for countdown because no hold/evidence conflict blocks the countdown. | `@oc_teardown_marked_at`, `@oc_janitor_state`, janitor status sidecar | Open when non-empty; manual collapse persists. |
| 3 | `Cleanup Blocked` | Non-active cleanup debt that cannot safely be marked or killed because required contract data is missing or invalid. | janitor refusal reason, evidence/run-root validation | Open when non-empty; manual collapse persists. |
| 4 | `Failed Agents` | Failed or terminal-problem agent panes inside the configured failure-visible window, or failed panes still awaiting operator judgment. | `@oc_state`, pane exit status, semantic lifecycle | Open when non-empty; manual collapse persists. |
| 5 | `Operational Failures` | OpenClaw workflow/runtime delivery failures that need operator judgment. | runtime card presentation group | Open when non-empty; manual collapse persists. |
| 6 | `Sub-System Failures` | Platform, route, skeleton, source-health, or subsystem failures. | runtime card presentation group | Open when non-empty; manual collapse persists. |
| 7 | `Services` | Healthy long-running monitors, bridges, daemons, and service cards. | `@oc_kind=service`, service chrome | Collapsed by default; respect manual collapse. |
| 8 | `Completed Agent Runs` | Completed agent cards that are not held, not failed-visible, not marked, and not cleanup-blocked. Finished non-agent managed panes that are stale/dead/done but do not fit a higher-priority agent/service/dashboard/viewer group fall back here unless explicitly classified as `Idle / Unowned`. | semantic lifecycle, runtime cards | Collapsed by default; respect manual collapse. |
| 9 | `Active Work` | Non-agent live work that is not a service/dashboard/viewer. | tmux chrome/liveness | Respect manual collapse. |
| 10 | `Dashboards` | Dashboard/self-monitoring UI sessions. | tmux chrome | Respect manual collapse. |
| 11 | `Viewers` | Browser/dev-server/viewer sessions. | tmux chrome | Respect manual collapse. |
| 12 | `Idle / Unowned` | Shell-only, unmanaged, or unowned fallback panes that do not need operator attention. Non-agent completed panes go here only when the classifier proves they are unmanaged/manual/unowned rather than lifecycle-managed cleanup debt. | tmux chrome/liveness | Collapsed by default; respect manual collapse. |

## Future: Edge Events (not implemented)

Current behavior seeds group defaults once and preserves manual collapse. Urgent-event auto-open is a future design, not a shipped setting or behavior.

In that future design, an auto-open event is a transition, not steady-state presence. A group may auto-open only when a session enters that group's urgent/opening condition since the last snapshot or since the operator manually acknowledged/collapsed it.

Examples:

- new session enters `Active Agents`;
- existing session enters failed state;
- janitor first marks a session for teardown;
- janitor first reports a cleanup blocker;
- hold reason first appears on a non-live pane.

Routine polls, unchanged janitor status, service noise, runtime-card refreshes, or repeated presence of the same failed/blocked item must not reopen a manually collapsed group.

## Sorting Within Groups

Default sort order inside a group should be deterministic:

1. urgent/awaiting-operator before ordinary running;
2. newest edge event before older edge events;
3. active capture change before quiet items;
4. session name as a stable final tiebreak.

Card identity should be stable by tmux session ID plus pane ID when available. If a pane is adopted or represented by a runtime card, the owning source must provide a stable identity key.
