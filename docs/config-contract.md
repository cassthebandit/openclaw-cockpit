# Configuration

The Go application reads `~/.config/openclaw-cockpit/config.json`. Start with
[`examples/config.example.json`](../examples/config.example.json), which lists
all supported settings with built-in defaults, or the smaller
[`config.organized.json`](../examples/config.organized.json). Missing keys keep
their defaults. A missing default file is normal; an explicitly selected missing
file is an error. No rebuild is needed. **All settings take effect on restart;
there is no hot reload.**

```sh
mkdir -p ~/.config/openclaw-cockpit
cp examples/config.example.json ~/.config/openclaw-cockpit/config.json
# Edit the file, then validate without starting tmux or the TUI:
openclaw-cockpit --validate-config
openclaw-cockpit --dump-config --explain-config
openclaw-cockpit --config examples/config.organized.json
```

## Ownership and supported settings

All keys below belong to the Go app and require restart. Durations are strings
accepted by Go's duration parser, such as `500ms`, `3m`, or `1h30m`. No setting
grants control/cleanup authority; interactive key forwarding still requires the
explicit `--control` flag.

| Key | Type; default | Allowed values and effect |
| --- | --- | --- |
| `interval` | duration; `1s` | `100ms`–`1h`; tmux snapshot polling |
| `fps` | integer; `60` | 1–120; maximum render frames per second |
| `cols` | integer; `0` | 0–32; overview preference, 0 automatic; group width policy still applies |
| `capture_budget` | integer; `6` | 1–120; background captures per structural tick |
| `capture_rate` | integer; `60` | 1–120; aggregate capture dispatches per second across both paths |
| `capture_min_lines` | integer; `80` | 1–5000; minimum capture depth |
| `capture_max_lines` | integer; `600` | minimum depth–5000; maximum capture depth |
| `capture_slack_lines` | integer; `40` | 0–1000; lines beyond viewport, bounded by min/max |
| `organize` | boolean; `false` | group/sort overview cards |
| `preserve_colors` | boolean; `false` | preserve supported preview ANSI colors, never unsafe terminal controls |
| `exclude_sessions` | string array; `[]` | exact tmux session names omitted from snapshots |
| `footer_max_height` | integer; `4` | ≥1 lines; status footer bound |
| `janitor_stale_after` | duration; `3m` | positive; sidecar freshness display only |
| `stale_threshold` | duration; `1h` | positive; quiet-session stale display only |
| `collapsed_groups` | string array; `[]` | registered group names initially collapsed |
| `expanded_groups` | string array; `[]` | registered group names initially expanded; cannot also be collapsed |
| `tmux` | string; empty | binary path/name; empty resolves from PATH |
| `openclaw_runtime` | boolean; `false` | enable optional read-only runtime source |
| `runtime_script` | string; empty | Python source location; empty discovers the public helper bundle |
| `runtime_limit` | integer; `80` | 1–1000 delivered cards; shown/total counts remain distinct |
| `runtime_interval` | duration; `5s` | `100ms`–`1h`; independent source refresh |
| `runtime_timeout` | duration; `20s` | `100ms`–`5m`; live source deadline |
| `dump_runtime_timeout` | duration; `45s` | `100ms`–`5m`; deliberately longer one-shot dump deadline |
| `janitor_status` | string; empty | optional sidecar path; empty disables sidecar reading |
| `grouping.agent_keywords` | string array; example defaults | fallback agent classification by chrome |
| `grouping.service_keywords` | string array; example defaults | fallback service classification by chrome |
| `grouping.dashboard_keywords` | string array; example defaults | fallback dashboard classification by chrome |
| `grouping.viewer_keywords` | string array; example defaults | fallback viewer classification by chrome |

Grouping arrays replace the corresponding built-in list, including an empty
array to disable that heuristic. Keywords are case-insensitive substrings (up
to 100 entries of 100 bytes, nonempty safe text). Only name/window/title/command
chrome is matched, never goal or transcript text. Explicit managed-agent and
runtime source identities retain precedence. Personal installation keywords are
not public defaults; add your own through these lists. Manual collapse wins
after initial group seeding. Group ranks, colors, body-size policy and urgent
**auto-open remain fixed or future work**, not config settings.

Paths support `~/`; relative paths resolve against the process working directory.
The dump contains resolved integration locations. Empty `runtime_script` searches
`~/.local/share/openclaw-cockpit/helpers/openclaw_runtime/cockpit_snapshot.py`,
then the helper bundle beside the executable/its sibling share directory, then
`helpers/openclaw_runtime/cockpit_snapshot.py` in the current checkout. A missing
optional source is reported when enabled; validation does not execute it. Source
execution is fixed to `python3 SCRIPT --limit N --format json`, without a shell
or arbitrary command/argument configuration. A Go-only install works without
Python helpers; `go install` does not install their source files.

## Precedence and diagnostics

1. Explicit CLI flags.
2. Explicit supported environment variables (public alias before legacy alias).
3. User JSON file.
4. Built-in defaults.

Only flags actually supplied override the file. For example,
`--organize=false` and `--cols=0` are real overrides; parser defaults are not.
`--config PATH` selects the file; otherwise `OPENCLAW_COCKPIT_CONFIG` may select
it. `--dump-config` prints effective JSON. `--explain-config` reports the file
and explicit per-key override sources to stderr. `--validate-config` exits
without executing tmux, loading runtime cards, or starting the TUI.

Supported CLI settings are `--interval`, `--fps`, `--cols`, `--capture-budget`,
`--tmux`, `--organize`, `--preserve-colors`, `--exclude-session`,
`--openclaw-runtime`, `--openclaw-runtime-script`, `--openclaw-runtime-limit`,
`--openclaw-runtime-interval`, and `--janitor-status`. Other settings are file-only.

Supported environment variables, with retained launcher compatibility aliases:

| Public variable | Legacy alias | Setting |
| --- | --- | --- |
| `OPENCLAW_COCKPIT_COLS` | `CASS_WALL_COLS` | `cols` |
| `OPENCLAW_COCKPIT_FPS` | `CASS_WALL_FPS` | `fps` |
| `OPENCLAW_COCKPIT_INTERVAL` | `CASS_WALL_INTERVAL` | `interval` |
| `OPENCLAW_COCKPIT_CAPTURE_BUDGET` | `CASS_WALL_CAPTURE_BUDGET` | `capture_budget` |
| `OPENCLAW_COCKPIT_RUNTIME_LIMIT` | `CASS_WALL_RUNTIME_LIMIT` | `runtime_limit` |
| `OPENCLAW_COCKPIT_RUNTIME_INTERVAL` | `CASS_WALL_RUNTIME_INTERVAL` | `runtime_interval` |
| `OPENCLAW_COCKPIT_EXCLUDE_SESSIONS` | `CASS_WALL_EXCLUDE_SESSIONS` | `exclude_sessions` (comma-separated) |
| `OPENCLAW_COCKPIT_JANITOR_STATUS` | `CASS_TMUX_HYGIENE_STATUS_FILE` | `janitor_status` |
| `OPENCLAW_COCKPIT_RUNTIME_SCRIPT` | none | `runtime_script` |

Launcher-only variables such as `OPENCLAW_COCKPIT_FORCE_TMUX` /
`TMUXWATCH_FORCE_TMUX` and daemon-log variables remain owned by the development
scripts; they are not presentation settings. Wrapper-injected explicit CLI flags
naturally win over a file, so launchers should not pass defaults unnecessarily.

Invalid/unknown fields, wrong types (including null), out-of-range values,
conflicting groups and trailing JSON produce setting/path diagnostics. Validation
and dump exit nonzero. Interactive startup retains the compatibility behavior:
a visible error and safe built-in fallback, never silent partial application.

## Lifecycle is a separate owner

The optional sibling `~/.config/openclaw-cockpit/lifecycle.json` belongs to the
public lifecycle helpers, not the Go application. Display-only installations
need no lifecycle file. The UI cannot change holds, closeout, eligibility,
archive paths, janitor timing or `kill_not_before`; sidecar timestamps and exact
pane identity are observed facts, not editable predictions. See
[`lifecycle-contract.md`](lifecycle-contract.md) and the helper installation
instructions for that separate component. Full theme editing and urgent-event
auto-open are not shipped settings.
