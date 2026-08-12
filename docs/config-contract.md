# Configuration Contract

Cockpit needs a durable configuration surface so timers, colors, group policies, and layout behavior stop living as scattered constants. This document defines what should become configurable and what must remain owned by lifecycle authorities.

## Principles

- Defaults must reproduce the golden wall fixture defined in [`layout-contract.md`](layout-contract.md) without a config file.
- Config tunes presentation and timing; it does not invent lifecycle truth.
- Every setting needs one owner, a default, and a testable effect.
- Config should be reloadable only when the implementation can prove the wall updates without corrupting render state.
- Invalid config should fail visibly and fall back safely, not silently change cleanup meaning.

## Candidate Config File

Implemented first pass (stdlib-parseable, no new dependency):

```text
~/.config/openclaw-cockpit/config.json
```

overridable with `--config <path>`; `--dump-config` prints the effective
config. A TOML surface (`config.toml`) remains an allowed future migration once
a TOML dependency is justified.

Project/test fixtures can use repo-local config files, but the installed wall should read the user config path unless overridden by CLI flag or environment variable.

## Implemented Settings (first pass)

- `footer_max_height` (int, default 4);
- `janitor_stale_after` (duration string, default "3m") — sidecar freshness display only;
- `stale_threshold` (duration string, default "1h") — session stale display only;
- `collapsed_groups` / `expanded_groups` (registry group names) — default accordion state overrides; manual operator toggles still win.

Unknown fields are rejected (strict decoding), so no cleanup-authority setting
can be introduced through config. Invalid config prints a visible error and
falls back to built-in defaults. Group policy tables, theme tokens, and layout
column settings remain future extractions under this contract.

## Settings To Extract First

### Display Timers

- failed visible grace window;
- janitor status stale-after duration;
- stale session threshold.

These tune presentation only. They do not decide whether a session is eligible to kill.

### Janitor-Derived Timers

- completed idle mark delay;
- mark-to-kill grace window;
- kill_not_before timestamp.

These are janitor-authority values. Cockpit may expose them in effective config or display settings, but it must read, derive, or reconcile them from janitor policy/status. If Cockpit config and janitor status disagree, the UI displays the janitor sidecar's actual `kill_not_before` and surfaces the config conflict.

### Group Policies

For each group:

- display label;
- rank/order;
- default collapsed/open;
- auto-open policy;
- max/min body height;
- preferred columns;
- compact mode;
- whether it can steal focus/attention.

### Layout

- preferred maximum columns;
- active-agent minimum body height;
- service/completed compact body height;
- footer max height;
- capture lines;
- poll interval;
- fast capture budget;
- runtime-card refresh interval.

### Theme

- group colors;
- border colors;
- header colors;
- severity colors;
- stale/blocked/held/failed/marked colors;
- scroll indicator color.

Color settings should use named tokens in code. Hardcoded numeric colors should be defaults in one theme definition, not scattered constants.

### Integration Paths

- janitor status sidecar path;
- runtime-card source path;
- default launcher geometry behavior;
- dashboard self-exclusion session/window names.

Runtime-card source commands are out of scope for the first config pass. Adding executable config requires a separate security model and explicit approval.

## Explicit Non-Config

These should not be configurable in Cockpit because they belong to other authorities:

- whether a session is safe to kill;
- whether evidence is valid enough for cleanup;
- whether a hold can be ignored;
- exact tmux kill target selection;
- external model/provider routing;
- Gateway or OpenClaw config mutation;
- public/human-send behavior.

## Precedence

Recommended precedence:

1. Janitor status sidecar for cleanup countdown facts and `kill_not_before`.
2. CLI flags for one-run presentation overrides.
3. Environment variables for launcher-controlled defaults.
4. User config file.
5. Built-in defaults.

The final effective config should be inspectable through a debug or dump path.

## Validation

Config validation should reject:

- negative durations;
- zero-width/zero-height layout settings where a positive value is required;
- duplicate group ranks;
- unknown group policy names;
- invalid colors;
- janitor-derived timers that conflict with status sidecar data without surfacing the conflict;
- cleanup-affecting settings that Cockpit is not allowed to own.

## Acceptance test inventory

- Default config reproduces the golden wall fixture in `layout-contract.md` at 120x40.
- Invalid config returns a visible error and does not corrupt the UI.
- Theme defaults are centralized.
- Group auto-open behavior can be changed in config.
- Footer max height can be changed in config.
- Janitor status path can be configured.
- Effective config can be dumped for debugging.
- Config cannot make Cockpit override janitor `kill_not_before`.
