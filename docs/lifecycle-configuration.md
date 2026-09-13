# Lifecycle configuration

The optional Python helpers read `~/.config/openclaw-cockpit/lifecycle.json`.
The Go UI reads its separate `config.json`; neither file grants the UI permission
to close terminals. Start from [the complete defaults](../examples/lifecycle.json).
No source edit or rebuild is required.

```sh
mkdir -p "$HOME/.config/openclaw-cockpit"
cp examples/lifecycle-long-retention.json "$HOME/.config/openclaw-cockpit/lifecycle.json"
python3 helpers/tmux/lifecycle.py --validate-config
python3 helpers/tmux/lifecycle.py --dump-config
```

This changes completed retention from 60 to 600 seconds. Retention is measured
from the recorded terminal outcome, not from launch time. Actual removal still
waits for an eligible exited session, valid evidence and a janitor cycle. Longer
retention does not keep a worker actively running; `closeout_default` controls
whether a completed interactive worker exits or stays available.

## Settings

| Key | Default | Effect |
| --- | --- | --- |
| closeout_default | close | `close` or `keep_open`, captured at job launch |
| completed_retention_seconds | 60 | Completed outcome visibility before eligibility |
| failed_retention_seconds | 180 | Failed outcome visibility before eligibility |
| teardown_grace_seconds | 60 | Existing mark-to-retirement grace |
| active_idle_mark_seconds | 180 | Legacy display-only observation window, does not schedule cleanup |
| adopted_grace_seconds | 3600 | Minimum age for conservative adopted-job planning |
| temporary_hold_hours | 24 | New temporary hold duration, maximum 8760 |
| cleanup_interval_seconds | 60 | Foreground hygiene loop interval |
| inspector_interval_seconds | 5 | Foreground inspector loop interval |
| inspector_stale_seconds | 1800 | Inspector stale display threshold |
| inspector_protected_sessions | [] | Additional exact local session names the inspector skips |
| max_kills | 10 | Maximum eligible removals per apply |
| log_max_bytes | 5242880 | Rotate service log to `.log.1` before a cycle at this size |
| state_dir | ~/.local/state/openclaw-cockpit | Helper state base |
| archive_dir | derived | `<state_dir>/cleanup-ledger` |
| status_file | derived | `<state_dir>/hygiene/status.json` |
| log_dir | derived | `<state_dir>/logs`, janitor.log |
| inspector_log_dir | derived | `<state_dir>/logs`, inspector.log |

Timing/count values are nonnegative integers up to 2147483647, except hold hours
may be fractional. Loop intervals, hold hours, max_kills and log_max_bytes must be
positive. Paths must be absolute or start with `~/`. Empty derived paths use the
state directory. Unknown keys, wrong types, non-finite values, trailing JSON and
unreadable explicit files are errors. A missing default file uses built-ins.
Invalid lifecycle configuration prevents cleanup; there is no fallback to a more
permissive policy. Log/archive access failures also cannot authorize removal.

## Precedence and compatibility

Explicit job/CLI choice wins, then supported environment overrides, then file,
then built-in defaults. Dump output includes sources. Examples:

```sh
OPENCLAW_COCKPIT_LIFECYCLE_CONFIG="$PWD/examples/lifecycle-long-retention.json" \
  python3 helpers/tmux/lifecycle.py --dump-config
OPENCLAW_COCKPIT_STATE_DIR="$HOME/.local/state/cockpit-example" \
  python3 helpers/tmux/lifecycle.py --dump-config
python3 helpers/tmux/session_hygiene.py plan --grace 600 --json
```

Use `--lifecycle-config PATH` before a launcher subcommand, or on a hygiene
command. `OPENCLAW_COCKPIT_LIFECYCLE_CONFIG` selects the file for wrappers/services.
Legacy environment aliases remain supported: `CASS_TMUX_HYGIENE_INTERVAL`,
`CASS_TMUX_HYGIENE_STATUS_FILE`, `CASS_TMUX_HYGIENE_LOG_DIR`,
`CASS_TMUX_JANITOR_LOG_MAX_BYTES`, `CASS_TMUX_INSPECTOR_INTERVAL`, and
`CASS_TMUX_INSPECTOR_LOG_DIR`. These names are compatibility only; new
installations should use the portable JSON keys.

Job metadata captures completed/failed retention and keep-open at launch. Changing
configuration does not retroactively change those existing job choices. Jobs from
older versions without captured retention use the current configured fallback.
Explicit `--cleanup-policy manual` and `--ttl never` are not replaced by launcher
defaults. `--keep-open` and `--close-on-completion` override closeout_default.

Permanent keep-open never expires with a temporary hold. Even manually exiting
the retained runtime leaves the pane protected. Use
`agent_wall.py release-keep-open --name EXACT_NAME` to release that protection;
this metadata action does not close or mark the pane. Releasing a temporary
hold remains the separate evidence-aware `release-hold` command. Neither release
rewrites the job outcome. A valid held completion is reconsidered automatically
when retention is released or expires; new input or changed result blocks it.

Review holds default to `temporary_hold_hours` (24). Renew with
`agent_wall.py keep-open --name EXACT_NAME --hold-reason review --hold-hours 24`.
Use `--indefinite` instead for an explicit until-released review hold. Existing
holds with no deadline remain protected; new defaults are not applied to them.

## Foreground services and reload

```sh
python3 helpers/tmux/services.py hygiene --config "$HOME/.config/openclaw-cockpit/lifecycle.json" --dry-run
python3 helpers/tmux/services.py hygiene --config "$HOME/.config/openclaw-cockpit/lifecycle.json"
# Separate process, if unowned-pane annotation is wanted:
python3 helpers/tmux/services.py inspector --config "$HOME/.config/openclaw-cockpit/lifecycle.json"
```

These are foreground loops, not a new daemon/service manager. Your service manager
or terminal owns their lifetime. `--once` performs one cycle. Restart the loop
after editing service intervals/log paths/inspector settings. Hygiene revalidates
configuration on each child apply invocation; invalid changes stop that invocation
without mutation. New launch policy is loaded on each launcher invocation. No hot
reload or automatic service installation is implied.

## Existing-session adoption

These additions are off by default. `adopt_existing_exited` (boolean, default
`false`) enables selected unknown **dead** sessions. `live_retirement` is `off`
(default), `observed`, or `verified`. Initially `verified` has no adopted-session
contract and reports `verified_unavailable_for_adopted`. It never falls back to
observed. Live enrollment is independent of the dead-adoption switch; a consumed
live attempt can finish dead cleanup while observed mode remains enabled.

`existing_session_mode` defaults to `selected-only`; an empty `cleanup_whitelist`
selects nothing. `all-except-protected` explicitly ignores whitelist selection.
`cleanup_blacklist` always protects, including managed janitor cleanup and exact
`--allow-session`. Neither list changes a managed supervisor's owned process exit.

Each list contains at most 256 objects, each with exactly one `exact` or `glob`
key and a 1–256 character string without controls. Matching is case sensitive and
covers the whole name. Glob supports only `*`; `?`, `[` and `]` are errors in globs
(and literal characters in exact selectors). Duplicate JSON keys are errors.

```json
{
  "adopt_existing_exited": true,
  "existing_session_mode": "selected-only",
  "cleanup_whitelist": [{"exact": "temporary-review"}],
  "cleanup_blacklist": [{"glob": "persistent-*"}],
  "live_retirement": "off",
  "live_retirement_quiet_seconds": 1800
}
```

`live_retirement_quiet_seconds` is a positive integer. Fresh unchanged observations
must cover this interval, followed by `teardown_grace_seconds`. Sampling gaps over
three cleanup intervals (minimum five seconds) reset quiet timing. Observed new
activity, result changes, missing evidence, protection or selection withdrawal
invalidate the release itself; waiting cannot revive it.

Configured `inspector_protected_sessions` also protect hygiene targets. Service,
viewer and runtime metadata, Cockpit's command identity and the janitor's own
process ancestry are preservation inputs. Missing ancestry observations fail
closed. All adoption preserves holds, permanent keep-open and genuine manual/hide
policy. The inspector's automatically generated `manual` annotation is passive.
Any nonempty launch ID prevents adoption, including a partial managed contract.
Pre-supervisor `managed_by=agent_wall` alone does not prevent enrollment.

The same list/mode/timing settings apply to Claude, Codex and AGY. Runtime profiles
are selected at exact-session enrollment, not configured as executable commands
in this file. Unsupported runtime/version/process shapes remain protected; broad
selection does not enable a generic live shutdown fallback.

See the [existing-session runbook](lifecycle-contract.md#existing-session-runbook)
for exact enrollment commands, native profile boundaries and recovery.
