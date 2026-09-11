#!/usr/bin/env python3
"""OpenClaw Cockpit managed tmux launcher.

Creates tmux sessions that Cockpit can render as cockpit items through
pane-scoped @oc_* metadata. This helper owns orchestration metadata; Cockpit
stays a passive monitor.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shlex
import subprocess
import sys
import time
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path
try:
    from . import assignment
    from .runtime_commands import build_claude_tui_command, build_codex_tui_command, build_agy_tui_command
except ImportError:
    import assignment
    from runtime_commands import build_claude_tui_command, build_codex_tui_command, build_agy_tui_command

STATE_DIR = Path(os.environ.get("OPENCLAW_COCKPIT_STATE_DIR", str(Path.home() / ".local/state/openclaw-cockpit"))).expanduser() / "agent-wall"
SMOKE_PREFIX = "oc-vis-smoke-"
CLAUDE_UNSET_ENV = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"]
# Local subscription intent: remove ambient alternate route selectors. Settings
# and stored credentials still require redacted /status verification at runtime.
CLAUDE_SUBSCRIPTION_UNSET_ENV = CLAUDE_UNSET_ENV + [
    "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
    "ANTHROPIC_PROFILE", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR",
    "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR",
]
CLAUDE_FABLE_MODEL = "claude-fable-5-1"
CLAUDE_FABLE_EFFORT = "high"
DEFAULT_TUI_COLS = 160
DEFAULT_TUI_ROWS = 45
TUI_TERM = "xterm-256color"
# Filled from the locally built, pinned Linux/arm64 image and verified before launch.
TMUX_FIELD_SEP = "\x1f"
VISIBLE_AGENT_RUNTIMES = {"claude", "fable", "codex", "agy", "antigravity", "omp"}
GENERIC_VISIBLE_RUNTIME_ALLOW_KINDS = {"smoke", "batch-worker"}
RUNTIME_PROCESS_TOKENS = {
    "claude": {"claude", "claude-code"},
    "codex": {"codex"},
    "agy": {"agy", "antigravity"},
    "omp": {"omp"},
}
# Role labels accepted for a Claude runtime pane, and the model family each one
# promises. `claude` stays a generic compatibility label for any Claude model;
# `opus` and `fable` are operator-relevant model roles Cockpit displays, so the
# launcher refuses a pane whose metadata role and selected model disagree.
CLAUDE_MODEL_PREFIX = "claude-"
CLAUDE_ROLE_MODEL_PREFIXES = {
    "opus": "claude-opus-",
    "fable": "claude-fable-",
}
# Runtime proof is position-aware: only what the pane actually executes counts.
ENV_ASSIGNMENT_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")
ENV_WRAPPER_BASENAMES = {"env"}
INTERPRETER_BASENAMES = {
    "bash",
    "bun",
    "dash",
    "deno",
    "fish",
    "ksh",
    "node",
    "nodejs",
    "perl",
    "python",
    "python2",
    "python3",
    "ruby",
    "sh",
    "zsh",
}
# Option arity for the wrapper and interpreter shapes this tool actually
# supports. Skipping every dash-token blindly lets an option *value* land in an
# executable position, so `env -P /tmp/claude /usr/bin/other` or
# `node --require /tmp/claude/loader.js /opt/app/main.js` would falsely prove a
# Claude runtime. These tables are deliberately small and explicit; an option
# outside them has undeterminable arity and fails closed (no runtime proof)
# rather than guessing which token is executed.
ENV_OPTIONS_WITH_OPERAND = {"--chdir", "--unset", "-C", "-P", "-u"}
ENV_FLAGS_WITHOUT_OPERAND = {"--debug", "--ignore-environment", "--null", "-0", "-i", "-v"}
# `env -S 'cmd args'` hides the command inside one operand: no token is a
# reliable executable position, so the shape yields no proof.
ENV_OPTIONS_WITHOUT_EXECUTABLE = {"--split-string", "-S"}
INTERPRETER_OPTIONS_WITH_OPERAND = {
    "--conditions",
    "--env-file",
    "--experimental-loader",
    "--icu-data-dir",
    "--import",
    "--loader",
    "--redirect-warnings",
    "--report-dir",
    "--report-filename",
    "--require",
    "--title",
    "-C",
    "-W",
    "-X",
    "-r",
}
INTERPRETER_FLAGS_WITHOUT_OPERAND = {
    "--enable-source-maps",
    "--interactive",
    "--no-deprecation",
    "--no-warnings",
    "--preserve-symlinks",
    "--preserve-symlinks-main",
    "--trace-warnings",
    "--use-strict",
    "--zero-fill-buffers",
    "-B",
    "-E",
    "-I",
    "-O",
    "-b",
    "-d",
    "-i",
    "-q",
    "-s",
    "-u",
    "-v",
}
# Inline code, a checked-but-not-executed file, or a module resolved by name:
# these shapes execute no script path, so no later token is an entrypoint.
INTERPRETER_OPTIONS_WITHOUT_SCRIPT = {
    "--check",
    "--eval",
    "--print",
    "-c",
    "-e",
    "-m",
    "-p",
}
# Suffixes stripped before comparing an executable name to a runtime token, so
# the resolved `.../claude-code/bin/claude.exe` entrypoint still proves Claude.
EXECUTABLE_SUFFIXES = {".bat", ".cjs", ".cmd", ".exe", ".js", ".mjs", ".ps1"}
READINESS_TIMEOUT_SECONDS = 12.0

OC_FIELDS = [
    "contract_version",
    "managed_by",
    "kind",
    "agent",
    "owner",
    "project",
    "goal",
    "state",
    "run_root",
    "thread_id",
    "session_id",
    "started_at",
    "updated_at",
    "completed_at",
    "exit_code",
    "ttl",
    "cleanup_policy",
    "evidence_path",
    "hold_reason",
    "hold_until",
    "why_headless",
    "pane_log",
    "progress_path",
    "end_reason",
    "route_failure_reason",
    "teardown_marked_at",
    "teardown_reason",
    "janitor_state",
    "last_meaningful_activity_at",
    "geometry_managed",
    "launch_cols",
    "launch_rows",
]
UNSETTABLE_STATE_FIELDS = {"end_reason", "route_failure_reason", "teardown_marked_at", "teardown_reason", "janitor_state", "last_meaningful_activity_at"}
RECOVERY_STATES = {"running", "waiting", "blocked", "done", "stale"}


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def run_tmux(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    # tmux otherwise replaces framing controls with underscores in a C locale.
    return subprocess.run(
        ["tmux", "-u", *args],
        check=check,
        text=True, encoding="utf-8",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


class VisibleContractError(RuntimeError):
    pass


class ReadinessTimeout(RuntimeError):
    pass


def session_exists(name: str) -> bool:
    return run_tmux("has-session", "-t", name, check=False).returncode == 0


def pane_rows(target: str) -> list[dict[str, str]]:
    fmt = TMUX_FIELD_SEP.join(
        [
            "#{session_name}",
            "#{window_index}",
            "#{window_name}",
            "#{pane_index}",
            "#{pane_id}",
            "#{pane_title}",
            "#{pane_current_command}",
        ]
    )
    cp = run_tmux("list-panes", "-t", target, "-F", fmt)
    rows: list[dict[str, str]] = []
    for line in cp.stdout.splitlines():
        fields = line.split(TMUX_FIELD_SEP)
        if len(fields) != 7:
            continue
        rows.append(
            {
                "session": fields[0],
                "window_index": fields[1],
                "window_name": fields[2],
                "pane_index": fields[3],
                "pane": fields[4],
                "title": fields[5],
                "command": fields[6],
            }
        )
    return rows


def session_pane_rows(name: str) -> list[dict[str, str]]:
    fmt = TMUX_FIELD_SEP.join(
        [
            "#{session_name}",
            "#{window_index}",
            "#{window_name}",
            "#{pane_index}",
            "#{pane_id}",
            "#{pane_title}",
            "#{pane_current_command}",
        ]
    )
    cp = run_tmux("list-panes", "-a", "-F", fmt)
    rows: list[dict[str, str]] = []
    for line in cp.stdout.splitlines():
        fields = line.split(TMUX_FIELD_SEP)
        if len(fields) != 7 or fields[0] != name:
            continue
        rows.append(
            {
                "session": fields[0],
                "window_index": fields[1],
                "window_name": fields[2],
                "pane_index": fields[3],
                "pane": fields[4],
                "title": fields[5],
                "command": fields[6],
            }
        )
    return rows


def first_pane(target: str) -> str:
    rows = pane_rows(target)
    pane = rows[0]["pane"] if rows else ""
    if not pane:
        raise SystemExit(f"no pane found for {target}")
    return pane


def unique_pane_for_session(name: str) -> str:
    rows = session_pane_rows(name)
    if len(rows) == 1:
        return rows[0]["pane"]
    if not rows:
        raise SystemExit(f"no pane found for ={name}")
    print(f"session {name!r} has {len(rows)} panes; rerun with --pane", file=sys.stderr)
    for row in rows:
        print(
            (
                f"  pane={row['pane']} window={row['window_index']}:{row['window_name']} "
                f"pane_index={row['pane_index']} title={row['title']!r} command={row['command']!r}"
            ),
            file=sys.stderr,
        )
    raise SystemExit(2)


def sanitize_tmux_option_value(value: object) -> str:
    text = str(value)
    return "".join(" " if ord(ch) < 32 or ord(ch) == 127 else ch for ch in text)


def set_pane_options(pane: str, values: dict[str, str | None]) -> None:
    # Claim ownership before publishing the rest of the metadata.
    for key in sorted(values, key=lambda key: key != "managed_by"):
        value = values[key]
        if value is None or value == "":
            continue
        run_tmux("set-option", "-p", "-t", pane, f"@oc_{key}", sanitize_tmux_option_value(value))


def unset_pane_options(pane: str, keys: set[str]) -> None:
    disallowed = sorted(key for key in keys if key not in UNSETTABLE_STATE_FIELDS)
    if disallowed:
        raise SystemExit(f"refusing to unset non-allowlisted cockpit fields: {', '.join(disallowed)}")
    for key in sorted(keys):
        run_tmux("set-option", "-p", "-u", "-t", pane, f"@oc_{key}")


def unset_hold_reason(pane: str) -> None:
    """Clear only @oc_hold_reason on a pane.

    Deliberately narrow: this helper can unset nothing else. It exists so
    release-hold can clear an evidence hold without widening the global
    UNSETTABLE_STATE_FIELDS allowlist (which intentionally excludes hold_reason).
    """
    run_tmux("set-option", "-p", "-u", "-t", pane, "@oc_hold_reason")


def read_pane_metadata(pane: str) -> dict[str, str]:
    """Return the current @oc_* metadata for a single pane as a plain dict."""
    fmt = TMUX_FIELD_SEP.join(["#{pane_id}", *[f"#{{@oc_{field}}}" for field in OC_FIELDS]])
    cp = run_tmux("list-panes", "-a", "-F", fmt, check=False)
    if cp.returncode != 0:
        return {}
    for line in cp.stdout.splitlines():
        fields = line.split(TMUX_FIELD_SEP)
        if len(fields) != 1 + len(OC_FIELDS):
            continue
        if fields[0] != pane:
            continue
        return {key: value for key, value in zip(OC_FIELDS, fields[1:]) if value != ""}
    return {}


def hold_deadline(args: argparse.Namespace) -> str:
    """New keep-open requests are renewable leases, not indefinite holds."""
    if not args.hold_reason:
        return ""
    hours = getattr(args, "hold_hours", 24)
    if not 0 < hours <= 8760:
        raise SystemExit("--hold-hours must be between 0 and 8760")
    return (datetime.now(timezone.utc) + timedelta(hours=hours)).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def metadata_from_args(args: argparse.Namespace, *, state: str | None) -> dict[str, str | None]:
    now = utc_now()
    values = {
        "contract_version": "1",
        "managed_by": "agent_wall",
        "kind": args.kind,
        "agent": args.agent,
        "owner": args.owner,
        "project": args.project,
        "goal": args.goal,
        "run_root": args.run_root,
        "thread_id": args.thread_id,
        "session_id": args.session_id,
        "started_at": now,
        "updated_at": now,
        "ttl": args.ttl,
        "cleanup_policy": args.cleanup_policy,
        "evidence_path": args.evidence_path,
        "hold_reason": args.hold_reason,
        "hold_until": hold_deadline(args),
        "why_headless": getattr(args, "why_headless", ""),
        "pane_log": getattr(args, "pane_log", ""),
        "progress_path": getattr(args, "progress_path", ""),
        "end_reason": getattr(args, "end_reason", ""),
        "route_failure_reason": getattr(args, "route_failure_reason", ""),
        "geometry_managed": getattr(args, "geometry_managed", ""),
        "launch_cols": getattr(args, "launch_cols", ""),
        "launch_rows": getattr(args, "launch_rows", ""),
    }
    if state is not None:
        values["state"] = state
    return values


def display_annotation_from_args(args: argparse.Namespace) -> dict[str, str | None]:
    now = utc_now()
    contract_version = "service-card.v1" if args.kind == "service" else "display-only"
    return {
        "contract_version": contract_version,
        "managed_by": "manual_adopt",
        "kind": args.kind,
        "agent": args.agent,
        "owner": args.owner,
        "project": args.project,
        "goal": args.goal,
        "state": args.state,
        "run_root": args.run_root,
        "thread_id": args.thread_id,
        "session_id": args.session_id,
        "updated_at": now,
        "ttl": args.ttl,
        "cleanup_policy": "manual",
        "evidence_path": args.evidence_path,
        "hold_reason": args.hold_reason,
        "hold_until": hold_deadline(args),
        "why_headless": getattr(args, "why_headless", ""),
        "progress_path": getattr(args, "progress_path", ""),
        "end_reason": getattr(args, "end_reason", ""),
        "route_failure_reason": getattr(args, "route_failure_reason", ""),
    }


def command_mentions_visible_runtime(command: list[str]) -> str:
    for token in command:
        name = Path(token).name.lower()
        if name in VISIBLE_AGENT_RUNTIMES:
            return name
    return ""


def require_path_under_run_root(raw: str, run_root: str, label: str) -> None:
    if not raw.strip():
        raise SystemExit(f"{label} is required")
    root = Path(run_root).expanduser().resolve()
    path = Path(raw).expanduser()
    resolved = path.resolve() if path.is_absolute() else (root / path).resolve()
    try:
        resolved.relative_to(root)
    except ValueError:
        raise SystemExit(f"{label} must stay under --run-root: {resolved}")


def validate_launch_contract(args: argparse.Namespace, *, generic: bool) -> None:
    kind = getattr(args, "kind", "").strip()
    agent = getattr(args, "agent", "").strip().lower()
    if not kind:
        raise SystemExit("--kind is required")
    if getattr(args, "cleanup_policy", "") == "smoke" and kind != "smoke":
        raise SystemExit("cleanup_policy=smoke requires kind=smoke")
    ttl = getattr(args, "ttl", "")
    if ttl and ttl != "never" and ttl.isdigit():
        args.ttl = ttl + "s"

    if kind == "batch-worker":
        if not getattr(args, "why_headless", "").strip():
            raise SystemExit("batch-worker requires --why-headless")
        require_path_under_run_root(getattr(args, "progress_path", ""), args.run_root, "--progress-path")
        if getattr(args, "cleanup_policy", "") in {"", "manual"}:
            args.cleanup_policy = "kill_on_done"
        if args.cleanup_policy == "kill_after_ttl" and (not getattr(args, "ttl", "") or args.ttl == "never"):
            args.ttl = "5m"
        return

    if getattr(args, "why_headless", "").strip() and kind != "batch-worker":
        raise SystemExit("--why-headless is only valid for kind=batch-worker")

    if generic:
        runtime = agent if agent in VISIBLE_AGENT_RUNTIMES else command_mentions_visible_runtime(getattr(args, "cmd_args", []))
        if runtime and kind not in GENERIC_VISIBLE_RUNTIME_ALLOW_KINDS:
            raise SystemExit(
                f"generic spawn refuses {runtime} visible-agent work; use spawn-{runtime if runtime != 'fable' else 'claude'} "
                "or kind=batch-worker with --why-headless and --progress-path"
            )
        return

    if kind in {"batch", "shell", "service", "smoke"}:
        raise SystemExit(f"TUI launcher cannot use kind={kind}; use generic spawn for that kind")


def positive_int_arg(value: object, *, label: str) -> int:
    try:
        parsed = int(str(value))
    except (TypeError, ValueError):
        raise SystemExit(f"{label} must be a positive integer") from None
    if parsed <= 0:
        raise SystemExit(f"{label} must be a positive integer")
    return parsed


def normalize_tui_geometry(args: argparse.Namespace) -> tuple[int, int]:
    cols = positive_int_arg(getattr(args, "cols", DEFAULT_TUI_COLS), label="--cols")
    rows = positive_int_arg(getattr(args, "rows", DEFAULT_TUI_ROWS), label="--rows")
    args.geometry_managed = "1"
    args.launch_cols = str(cols)
    args.launch_rows = str(rows)
    return cols, rows


def write_wrapper(
    name: str,
    command: list[str],
    *,
    pane_log: str = "",
    launch_record: str = "",
) -> Path:
    safe_name = "".join(ch if ch.isalnum() or ch in "._-" else "_" for ch in name)
    root = STATE_DIR / safe_name
    root.mkdir(parents=True, exist_ok=True)
    wrapper = root / "run.sh"
    command_line = shlex.join(command)
    pane_log_q = shlex.quote(pane_log) if pane_log else ""
    launch_record_q = shlex.quote(launch_record) if launch_record else ""
    logging_setup = ""
    logging_teardown = ""
    if pane_log and launch_record:
        logging_setup = f"""
mkdir -p "$(dirname {pane_log_q})" "$(dirname {launch_record_q})"
: > {pane_log_q}
tmux pipe-pane -o -t "$TMUX_PANE" "cat >> {pane_log_q}" >/dev/null 2>&1 || true
cat > {launch_record_q} <<'JSON'
{json.dumps({"command_kind": "generic", "argv": command, "pane_log_sensitive": True}, indent=2)}
JSON
"""
        logging_teardown = 'tmux pipe-pane -t "$TMUX_PANE" >/dev/null 2>&1 || true\n'
    wrapper.write_text(
        f"""#!/usr/bin/env bash
set +e
{logging_setup}\
# A new CLI invocation must not inherit completion from a prior incarnation.
for oc_field in completed_at exit_code end_reason teardown_marked_at teardown_reason; do
  tmux set-option -pu -t "$TMUX_PANE" "@oc_$oc_field" >/dev/null 2>&1 || true
done
tmux set-option -p -t "$TMUX_PANE" @oc_state running >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
{command_line}
exit_code=$?
if [ "$exit_code" -eq 0 ]; then
  oc_state=done
  oc_end_reason=expected_exit
else
  oc_state=failed
  oc_end_reason=process_exit_nonzero
fi
{logging_teardown}\
tmux set-option -p -t "$TMUX_PANE" @oc_state "$oc_state" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_exit_code "$exit_code" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_end_reason "$oc_end_reason" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
echo "[agent-wall] exited with status $exit_code"
exit "$exit_code"
""",
        encoding="utf-8",
    )
    wrapper.chmod(0o700)
    return wrapper










def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require_pinned_artifact(
    path: Path,
    expected_sha256: str,
    *,
    label: str,
    executable: bool = False,
) -> None:
    if not path.is_file():
        raise SystemExit(f"missing {label} launch artifact: {path}")
    if sha256_file(path) != expected_sha256:
        raise SystemExit(f"{label} launch artifact digest mismatch: {path}")
    if executable and not os.access(path, os.X_OK):
        raise SystemExit(f"{label} launch artifact is not executable: {path}")
























def docker_lane_name(prefix: str, session_name: str) -> str:
    slug = re.sub(r"[^a-z0-9_.-]+", "-", session_name.lower()).strip("-.") or "lane"
    digest = hashlib.sha256(session_name.encode()).hexdigest()[:10]
    return f"{prefix}-{slug[:35]}-{digest}"








def write_tui_wrapper(
    name: str,
    command: list[str],
    *,
    command_kind: str,
    prompt_file: str,
    pane_log: str,
    debug_file: str,
    launch_record: str,
    exit_label: str | None = None,
    parallel_contract: dict[str, object] | None = None,
    launch_id: str = "",
) -> Path:
    safe_name = "".join(ch if ch.isalnum() or ch in "._-" else "_" for ch in name)
    root = STATE_DIR / safe_name
    root.mkdir(parents=True, exist_ok=True)
    wrapper = root / f"run-{command_kind}.sh"
    command_line = shlex.join(command)
    pane_log_q = shlex.quote(pane_log)
    launch_record_q = shlex.quote(launch_record)
    label = exit_label or command_kind
    launch_payload: dict[str, object] = {
        "command_kind": command_kind,
        "argv": command,
        "prompt_file": prompt_file,
        "pane_log_sensitive": True,
        "debug_file": debug_file,
        "debug_log_sensitive": True,
    }
    if parallel_contract is not None:
        launch_payload["parallel_contract"] = parallel_contract
    # Managed supervisor owns final state; an obsolete wrapper must never stamp a replacement.
    final_metadata = "" if launch_id else 'tmux pipe-pane -t "$TMUX_PANE" >/dev/null 2>&1\ntmux set-option -p -t "$TMUX_PANE" @oc_state "$oc_state" >/dev/null 2>&1\ntmux set-option -p -t "$TMUX_PANE" @oc_exit_code "$exit_code" >/dev/null 2>&1\ntmux set-option -p -t "$TMUX_PANE" @oc_end_reason "$oc_end_reason" >/dev/null 2>&1\ntmux set-option -p -t "$TMUX_PANE" @oc_completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1\ntmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1\n'
    wrapper.write_text(
        f"""#!/usr/bin/env bash
set -e
mkdir -p "$(dirname {pane_log_q})" "$(dirname {launch_record_q})"
: > {pane_log_q}
# A new CLI invocation must not inherit completion from a prior incarnation.
for oc_field in completed_at exit_code end_reason teardown_marked_at teardown_reason; do
  tmux set-option -pu -t "$TMUX_PANE" "@oc_$oc_field" >/dev/null 2>&1
done
tmux set-option -p -t "$TMUX_PANE" @oc_launch_id {shlex.quote(launch_id)} >/dev/null 2>&1
tmux set-option -p -t "$TMUX_PANE" @oc_state running >/dev/null 2>&1
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1
tmux pipe-pane -o -t "$TMUX_PANE" "cat >> {pane_log_q}" >/dev/null 2>&1
cat > {launch_record_q} <<'JSON'
{json.dumps(launch_payload, indent=2)}
JSON
set +e
{command_line}
exit_code=$?
if [ "$exit_code" -eq 0 ]; then
  oc_state=done
  oc_end_reason=expected_exit
else
  oc_state=failed
  oc_end_reason=process_exit_nonzero
fi
{final_metadata}
echo "[agent-wall] {label} exited with status $exit_code"
exit "$exit_code"
""",
        encoding="utf-8",
    )
    wrapper.chmod(0o700)
    return wrapper


def write_claude_tui_wrapper(
    name: str,
    command: list[str],
    *,
    prompt_file: str,
    pane_log: str,
    debug_file: str,
    launch_record: str,
) -> Path:
    return write_tui_wrapper(
        name,
        command,
        command_kind="claude_tui",
        prompt_file=prompt_file,
        pane_log=pane_log,
        debug_file=debug_file,
        launch_record=launch_record,
        exit_label="claude TUI",
    )


def wait_for_command(pane: str, expected: str, *, timeout: float = 8.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        cp = run_tmux("display-message", "-p", "-t", pane, "#{pane_current_command}", check=False)
        if cp.returncode == 0 and expected in cp.stdout.strip():
            return
        time.sleep(0.2)
    raise SystemExit(f"pane {pane} did not report command {expected!r} within {timeout:.1f}s")


def wait_for_log_text(path: Path, expected: str, *, timeout: float | None = None, pane: str = "") -> None:
    timeout = READINESS_TIMEOUT_SECONDS if timeout is None else timeout
    deadline = time.time() + timeout
    while time.time() < deadline:
        if path.exists() and expected in path.read_text(encoding="utf-8", errors="ignore"):
            return
        if pane and expected in run_tmux("capture-pane", "-p", "-t", pane).stdout:
            return
        time.sleep(0.2)
    raise SystemExit(f"{path} did not contain {expected!r} within {timeout:.1f}s")


def wait_for_log_activity(path: Path, *, timeout: float | None = None) -> None:
    timeout = READINESS_TIMEOUT_SECONDS if timeout is None else timeout
    deadline = time.time() + timeout
    while time.time() < deadline:
        if path.exists() and path.stat().st_size > 0:
            return
        time.sleep(0.2)
    raise SystemExit(f"{path} did not receive pane output within {timeout:.1f}s")








def expected_runtime_for_command_kind(command_kind: str) -> str:
    if command_kind.startswith("claude"):
        return "claude"
    if command_kind.startswith("codex"):
        return "codex"
    if command_kind.startswith("agy"):
        return "agy"
    if command_kind.startswith("omp"):
        return "omp"
    return ""


def skip_option_tokens(
    parts: list[str],
    index: int,
    *,
    with_operand: set[str],
    without_operand: set[str],
    without_executable: set[str],
) -> tuple[int, str]:
    """Advance past option tokens, honouring option arity and `--`.

    Returns `(index, status)`. `ok` means `parts[index]` is a genuine
    positional token. `end` means the options consumed the whole command line.
    `none` means the options prove there is no executable position left, and
    `unknown` means an option of undeterminable arity was seen. Only `ok`
    licenses treating a token as something the pane executes.
    """
    while index < len(parts):
        token = parts[index]
        if token == "--":
            return index + 1, "ok"
        if not token.startswith("-") or token == "-":
            return index, "ok"
        if token.startswith("--"):
            name, separator, _ = token.partition("=")
            if name in without_executable:
                return index, "none"
            if separator:
                # Attached operands are safe to skip only for options whose
                # operand-taking semantics are explicitly known. Unknown
                # options and operand-free options in this form fail closed.
                if name not in with_operand:
                    return index, "unknown"
                index += 1
                continue
            if name in without_operand:
                index += 1
                continue
            if name in with_operand:
                index += 2
                continue
            return index, "unknown"
        cluster = token[1:]
        position = 0
        while position < len(cluster):
            flag = "-" + cluster[position]
            if flag in without_operand:
                position += 1
                continue
            if flag in without_executable:
                return index, "none"
            if flag in with_operand:
                # The rest of the cluster is the operand when non-empty
                # (`-uNAME`), otherwise the operand is the next token.
                index += 1 if position + 1 < len(cluster) else 2
                break
            return index, "unknown"
        else:
            index += 1
    return index, "end"


def executable_position_tokens(command_text: str) -> list[str]:
    """Return only the executable-position tokens of a command line.

    Runtime proof must come from what the pane is executing, not from a runtime
    name that happens to appear in an unrelated directory or argument. This
    returns argv[0] after skipping an env(1) wrapper together with its options
    and VAR=VALUE assignments, plus the script argument when argv[0] is an
    interpreter (the shape used by node-installed CLI entrypoints). Option
    parsing is arity-aware, so an option *value* such as the module path in
    `node --require /tmp/claude/loader.js /opt/app/main.js` is never mistaken
    for the executed entrypoint.
    """
    try:
        parts = shlex.split(command_text)
    except ValueError:
        parts = command_text.split()
    index = 0
    while index < len(parts):
        token = parts[index]
        if ENV_ASSIGNMENT_RE.match(token):
            index += 1
            continue
        if Path(token).name.lower() in ENV_WRAPPER_BASENAMES:
            index, status = skip_option_tokens(
                parts,
                index + 1,
                with_operand=ENV_OPTIONS_WITH_OPERAND,
                without_operand=ENV_FLAGS_WITHOUT_OPERAND,
                without_executable=ENV_OPTIONS_WITHOUT_EXECUTABLE,
            )
            if status != "ok":
                return []
            continue
        break
    if index >= len(parts):
        return []
    executables = [parts[index]]
    if Path(parts[index]).name.lower() in INTERPRETER_BASENAMES:
        script_index, status = skip_option_tokens(
            parts,
            index + 1,
            with_operand=INTERPRETER_OPTIONS_WITH_OPERAND,
            without_operand=INTERPRETER_FLAGS_WITHOUT_OPERAND,
            without_executable=INTERPRETER_OPTIONS_WITHOUT_SCRIPT,
        )
        if status == "ok" and script_index < len(parts):
            executables.append(parts[script_index])
    return executables


def executable_identifies_runtime(token: str, tokens: set[str]) -> bool:
    """True when one executable-position token names a runtime entrypoint."""
    path = Path(token)
    names = {path.name.lower()}
    if path.suffix.lower() in EXECUTABLE_SUFFIXES:
        names.add(path.stem.lower())
    if names & tokens:
        return True
    if any(name.startswith(runtime + "-") for name in names for runtime in tokens):
        return True
    # A package directory in the executed path still identifies the runtime,
    # e.g. .../@anthropic-ai/claude-code/cli.js. Arguments never reach here.
    return bool({piece.lower() for piece in path.parts} & tokens)


def command_text_mentions_runtime(command_text: str, runtime: str, *, skip_paths: set[str] | None = None) -> bool:
    tokens = RUNTIME_PROCESS_TOKENS.get(runtime, set())
    if not tokens:
        return False
    for skip_path in skip_paths or set():
        if skip_path and skip_path in command_text:
            return False
    return any(executable_identifies_runtime(token, tokens) for token in executable_position_tokens(command_text))


def process_tree_commands(root_pid: str, runner=subprocess.run) -> list[str]:
    if not root_pid:
        return []
    cp = runner(
        ["ps", "-ww", "-axo", "pid=,ppid=,command="],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if cp.returncode != 0:
        return []
    children: dict[str, list[tuple[str, str]]] = {}
    commands: dict[str, str] = {}
    for line in cp.stdout.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) < 3:
            continue
        pid, ppid, command = parts
        commands[pid] = command
        children.setdefault(ppid, []).append((pid, command))
    found: list[str] = []
    stack = [root_pid]
    seen: set[str] = set()
    while stack:
        pid = stack.pop()
        if pid in seen:
            continue
        seen.add(pid)
        command = commands.get(pid)
        if command:
            found.append(command)
        for child_pid, _child_command in children.get(pid, []):
            stack.append(child_pid)
    return found


def pane_contract_snapshot(pane: str) -> dict[str, str]:
    fields = [
        "#{pane_dead}",
        "#{pane_pid}",
        "#{pane_current_command}",
        "#{@oc_contract_version}",
        "#{@oc_managed_by}",
        "#{@oc_kind}",
        "#{@oc_agent}",
        "#{@oc_cleanup_policy}",
        "#{@oc_evidence_path}",
    ]
    cp = run_tmux("display-message", "-p", "-t", pane, TMUX_FIELD_SEP.join(fields), check=False)
    if cp.returncode != 0:
        raise VisibleContractError(cp.stderr.strip() or f"pane {pane} is not readable")
    values = cp.stdout.rstrip("\n").split(TMUX_FIELD_SEP)
    if len(values) != len(fields):
        raise VisibleContractError(f"malformed tmux pane snapshot for {pane}: {cp.stdout!r}")
    return {
        "dead": values[0],
        "pid": values[1],
        "command": values[2],
        "contract_version": values[3],
        "managed_by": values[4],
        "kind": values[5],
        "agent": values[6],
        "cleanup_policy": values[7],
        "evidence_path": values[8],
    }


def allowed_agents_for_runtime(runtime: str) -> set[str]:
    if runtime == "claude":
        # `opus` is the launcher default and a real Cockpit role label, not a
        # foreign runtime. Accepting it here keeps the default visible launch
        # working without erasing the model role from pane metadata.
        return {"claude", "fable", "opus"}
    if runtime == "agy":
        return {"agy", "antigravity"}
    return {runtime}


def validate_claude_role_model(args: argparse.Namespace) -> None:
    """Refuse Claude role/model mismatches before any tmux session exists.

    Pre-creation by contract: this runs before the launcher touches tmux, so a
    mismatch leaves no session behind. The launcher never derives one side from
    the other — an operator who names a role and a model that disagree has made
    an error worth surfacing, not a preference worth guessing.
    """
    agent = (getattr(args, "agent", "") or "").strip().lower()
    model = (getattr(args, "model", "") or "").strip().lower()
    allowed = allowed_agents_for_runtime("claude")
    if agent not in allowed:
        raise SystemExit(
            f"spawn-claude refuses --agent {agent or '(empty)'}; expected one of {', '.join(sorted(allowed))}"
        )
    if not model:
        raise SystemExit("spawn-claude requires --model")
    if not model.startswith(CLAUDE_MODEL_PREFIX):
        raise SystemExit(f"--agent {agent} requires a {CLAUDE_MODEL_PREFIX}* model; got --model {model}")
    required = CLAUDE_ROLE_MODEL_PREFIXES.get(agent)
    if required and not model.startswith(required):
        raise SystemExit(
            f"--agent {agent} requires a {required}* model; got --model {model}. "
            "Set the role and model consistently; the launcher never derives one from the other."
        )
    if model.startswith("claude-fable-"):
        effort = (getattr(args, "effort", "") or "").strip().lower()
        if model != CLAUDE_FABLE_MODEL or effort != CLAUDE_FABLE_EFFORT:
            raise SystemExit(
                "Fable launches require "
                f"--model {CLAUDE_FABLE_MODEL} --effort {CLAUDE_FABLE_EFFORT}; "
                f"got --model {model} --effort {effort or '(empty)'}"
            )


def verify_visible_contract(
    args: argparse.Namespace,
    *,
    pane: str,
    pane_log_path: Path,
    launch_record: str,
    command_kind: str,
    wrapper_path: Path | None = None,
    runtime_probe: object | None = None,
    runtime_command_validator: object | None = None,
) -> dict[str, object]:
    runtime = expected_runtime_for_command_kind(command_kind)
    if not runtime:
        raise VisibleContractError(f"unknown visible runtime for command kind {command_kind!r}")
    snapshot = pane_contract_snapshot(pane)
    if snapshot["dead"] == "1":
        raise VisibleContractError(f"pane {pane} is dead")
    if snapshot["contract_version"] != "1":
        raise VisibleContractError(f"pane {pane} contract_version={snapshot['contract_version']!r}, expected 1")
    if snapshot["managed_by"] != "agent_wall":
        raise VisibleContractError(f"pane {pane} managed_by={snapshot['managed_by']!r}, expected agent_wall")
    if snapshot["kind"] != "visible-agent":
        raise VisibleContractError(f"pane {pane} kind={snapshot['kind']!r}, expected visible-agent")
    if snapshot["agent"] not in allowed_agents_for_runtime(runtime):
        raise VisibleContractError(f"pane {pane} agent={snapshot['agent']!r} does not match runtime {runtime}")
    if not snapshot["cleanup_policy"]:
        raise VisibleContractError(f"pane {pane} missing cleanup policy")
    if not snapshot["evidence_path"]:
        raise VisibleContractError(f"pane {pane} missing evidence path")
    if not pane_log_path.exists() or pane_log_path.stat().st_size == 0:
        raise VisibleContractError(f"pane log missing or empty: {pane_log_path}")
    launch_path = Path(launch_record)
    if not launch_path.exists() or launch_path.stat().st_size == 0:
        raise VisibleContractError(f"launch record missing or empty: {launch_path}")
    if runtime_probe is not None:
        if not callable(runtime_probe):
            raise VisibleContractError("visible runtime probe is not callable")
        matched = str(runtime_probe())
    else:
        skip_paths = {str(wrapper_path)} if wrapper_path else set()
        commands = [snapshot["command"], *process_tree_commands(snapshot["pid"])]
        matched = next((command for command in commands if command_text_mentions_runtime(command, runtime, skip_paths=skip_paths)), "")
    if not matched:
        raise VisibleContractError(
            f"pane {pane} has no {runtime} runtime process; command={snapshot['command']!r} pid={snapshot['pid']}"
        )
    if runtime_command_validator is not None:
        if not callable(runtime_command_validator):
            raise VisibleContractError("visible runtime command validator is not callable")
        runtime_command_validator(matched)
    return {
        "runtime": runtime,
        "pane_pid": snapshot["pid"],
        "pane_command": snapshot["command"],
        "runtime_process": matched,
        "metadata": {
            "managed_by": snapshot["managed_by"],
            "kind": snapshot["kind"],
            "agent": snapshot["agent"],
            "cleanup_policy": snapshot["cleanup_policy"],
            "evidence_path": snapshot["evidence_path"],
        },
    }


def fail_visible_contract(
    args: argparse.Namespace,
    pane: str,
    detail: str,
    *,
    end_reason: str = "visible_contract_failed",
) -> None:
    try:
        set_pane_options(
            pane,
            {
                "state": "failed",
                "updated_at": utc_now(),
                "completed_at": utc_now(),
                "exit_code": "1",
                "end_reason": end_reason,
                "route_failure_reason": detail,
            },
        )
    except Exception:
        pass
    # Preserve the failed launch for diagnosis; never kill a potentially replaced session here.




def resolve_run_root(args: argparse.Namespace, prompt_path: Path) -> Path:
    run_root = Path(args.run_root).expanduser().resolve() if args.run_root else prompt_path.parent
    args.run_root = str(run_root)
    return run_root


def resolve_pane_log(args: argparse.Namespace, run_root: Path, default_name: str = "tui-pane.log") -> str:
    if args.pane_log:
        pane_log_path = Path(args.pane_log).expanduser()
        if not pane_log_path.is_absolute():
            pane_log_path = run_root / pane_log_path
        return str(pane_log_path.resolve())
    return str(run_root / "logs" / default_name)


def ensure_cleanup_defaults(args: argparse.Namespace, *, pane_log: str, run_root: Path) -> None:
    if not getattr(args, "ttl", "") or args.ttl == "never":
        args.ttl = "60s"
    if not getattr(args, "cleanup_policy", "") or args.cleanup_policy == "manual":
        args.cleanup_policy = "kill_on_done"
    if not getattr(args, "evidence_path", ""):
        pane_log_path = Path(pane_log)
        try:
            args.evidence_path = str(pane_log_path.resolve().relative_to(run_root.resolve()))
        except ValueError:
            args.evidence_path = str(pane_log_path.resolve())
    require_path_under_run_root(args.evidence_path, str(run_root), "--evidence-path")
    evidence = Path(args.evidence_path).expanduser()
    if not evidence.is_absolute():
        evidence = run_root / evidence
    evidence.resolve().parent.mkdir(parents=True, exist_ok=True)


def ensure_tui_cleanup_defaults(args: argparse.Namespace, *, pane_log: str, run_root: Path) -> None:
    ensure_cleanup_defaults(args, pane_log=pane_log, run_root=run_root)


def assignment_is_bound(run_dir: str) -> bool:
    return assignment.is_ready(run_dir)


def wait_for_assignment_ready(run_dir: str) -> bool:
    deadline = time.monotonic() + READINESS_TIMEOUT_SECONDS
    while True:
        if assignment_is_bound(run_dir):
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(0.1)


def assignment_identity(root: Path, pane: str) -> list[str]:
    if not re.fullmatch(r"%[0-9]+", pane):
        raise SystemExit("submit-assignment requires an exact pane ID")
    try:
        launch = json.loads((root / "launch.json").read_text())
        process = json.loads((root / "process.json").read_text())
        identity = process.get("pane_identity", [])
        patterns = (r"%[0-9]+", r"[0-9]+", r"\$[0-9]+", r"@[0-9]+", r"[0-9a-f-]{36}")
        valid = (len(identity) == 7 and all(isinstance(value, str) and re.fullmatch(pattern, value)
                 for pattern, value in zip(patterns, identity[:5])))
        if (not valid or identity[0] != pane or identity[4] != launch["run_id"]
                or process.get("run_id") != launch["run_id"]):
            raise ValueError("stored process identity does not match launch")
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise SystemExit(f"assignment/pane identity missing or changed; prompt not submitted: {error}") from error
    return identity


def inject_assignment(pane: str, root: Path) -> None:
    with assignment._locked(root):
        if (root / "submission.json").exists():
            raise SystemExit("initial assignment was already submitted; use the runtime for follow-up")
        _inject_assignment(pane, root)
        assignment._write(root / "submission.json", {"pane": pane, "submitted_at": utc_now()})


def _inject_assignment(pane: str, root: Path) -> None:
    identity = assignment_identity(root, pane)
    fields = ("pane_id", "pane_pid", "session_id", "window_id", "@oc_launch_id")
    conditions = ["#{==:#{" + field + "}," + expected + "}" for field, expected in zip(fields, identity[:5])]
    conditions.append("#{==:#{pane_dead},0}")
    condition = conditions[0]
    for item in conditions[1:]:
        condition = "#{&&:" + condition + "," + item + "}"
    buffer_name = "oc-assignment-" + identity[4]
    # The process tuple comes from this launch's supervisor, not a fresh snapshot
    # of whichever process happens to occupy the pane now. Check in the tmux
    # server immediately before both paste and Enter, including after the delay.
    run_tmux("load-buffer", "-b", buffer_name, str(root / "prompt.md"))
    try:
        commands = [["paste-buffer", "-p", "-b", buffer_name, "-t", pane], ["send-keys", "-t", pane, "Enter"]]
        for index, command in enumerate(commands):
            if index:
                time.sleep(0.35)
            output = run_tmux("if-shell", "-F", "-t", pane, condition, shlex.join(command),
                              "display-message -p COCKPIT_ASSIGNMENT_IDENTITY_CHANGED").stdout
            if "COCKPIT_ASSIGNMENT_IDENTITY_CHANGED" in output:
                raise SystemExit("assignment/pane identity changed; prompt not submitted")
    finally:
        run_tmux("delete-buffer", "-b", buffer_name, check=False)


def cmd_submit_assignment(args: argparse.Namespace) -> int:
    root = Path(args.assignment_run).expanduser().resolve()
    inject_assignment(args.pane, root)
    launch = json.loads((root / "launch.json").read_text())
    print(json.dumps({"pane": args.pane, "submitted": True, "launch_id": launch["run_id"]}))
    return 0


def spawn_tui_session(
    args: argparse.Namespace,
    *,
    command: list[str],
    command_kind: str,
    window_name: str,
    ready_text: str = "",
    debug_file: str = "",
    exit_label: str | None = None,
    dismiss_codex_update: bool = False,
    session_environment: dict[str, str] | None = None,
    runtime_probe: object | None = None,
    runtime_command_validator: object | None = None,
    parallel_contract: dict[str, object] | None = None,
) -> dict[str, object]:
    if session_exists(args.name):
        raise SystemExit(f"tmux session already exists: {args.name}")
    prompt_path = prompt_path_from_args(args)
    run_root = resolve_run_root(args, prompt_path)
    logs_dir = run_root / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)
    pane_log = resolve_pane_log(args, run_root, default_name=f"{args.name}-pane.log")
    ensure_tui_cleanup_defaults(args, pane_log=pane_log, run_root=run_root)
    debug_path = debug_file
    launch_record = str(logs_dir / f"{args.name}-{command_kind}-launch.json")
    cols, rows = normalize_tui_geometry(args)
    assignment_run = None
    if command_kind in {"claude_tui", "codex_tui"}:
        assignment_run = assignment.prepare(
            command_kind.removesuffix("_tui"),
            run_root / "assignments" / uuid.uuid4().hex,
            command, keep_open=bool(getattr(args, "keep_open", False)), bootstrap=command_kind == "codex_tui",
        )
        managed_prompt = Path(assignment_run["run_dir"]) / "prompt.md"
        managed_prompt.write_text(prompt_path.read_text(encoding="utf-8") + assignment_run["prompt_suffix"], encoding="utf-8")
        managed_prompt.chmod(0o600)
        prompt_path = managed_prompt
        command = [sys.executable, str(Path(assignment.__file__).resolve()), "supervise",
                   "--run-dir", assignment_run["run_dir"], "--", *assignment_run["command"]]

    wrapper = write_tui_wrapper(
        args.name,
        command,
        command_kind=command_kind,
        prompt_file=str(prompt_path),
        pane_log=pane_log,
        debug_file=debug_path,
        launch_record=launch_record,
        exit_label=exit_label,
        parallel_contract=parallel_contract,
        launch_id=assignment_run["run_id"] if assignment_run else "",
    )
    new_session_args = ["new-session", "-d", "-x", str(cols), "-y", str(rows)]
    for key, value in (session_environment or {}).items():
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise SystemExit(f"invalid tmux session environment key: {key!r}")
        new_session_args.extend(["-e", f"{key}={value}"])
    new_session_args.extend(["-s", args.name, "-n", window_name, str(wrapper)])
    run_tmux(*new_session_args)
    pane = ""
    pane_log_path = Path(pane_log)
    try:
        run_tmux("set-window-option", "-t", f"{args.name}:{window_name}", "remain-on-exit", "on")
        pane = first_pane(f"{args.name}:{window_name}")
        if args.title:
            run_tmux("select-pane", "-t", pane, "-T", args.title)
        values = metadata_from_args(args, state=None)
        values["pane_log"] = pane_log
        set_pane_options(pane, values)
        if dismiss_codex_update:
            dismiss_codex_update_prompt_if_needed(pane, pane_log_path)
        try:
            if ready_text:
                wait_for_log_text(pane_log_path, ready_text, pane=pane)
            else:
                wait_for_log_activity(pane_log_path)
        except SystemExit as exc:
            raise ReadinessTimeout(str(exc)) from exc
        visible_proof = verify_visible_contract(
            args,
            pane=pane,
            pane_log_path=pane_log_path,
            launch_record=launch_record,
            command_kind=command_kind,
            wrapper_path=wrapper,
            runtime_probe=runtime_probe,
            runtime_command_validator=runtime_command_validator,
        )
        prompt_submitted = not assignment_run or wait_for_assignment_ready(assignment_run["run_dir"])
        if prompt_submitted:
            if assignment_run:
                inject_assignment(pane, Path(assignment_run["run_dir"]))
            else:
                inject_prompt(pane, prompt_path, buffer_name=f"oc-prompt-{args.name}")
    except ReadinessTimeout as exc:
        if pane:
            fail_visible_contract(args, pane, str(exc), end_reason="readiness_timeout")
        raise SystemExit(str(exc)) from exc
    except SystemExit as exc:
        if pane:
            fail_visible_contract(args, pane, str(exc))
        raise
    except VisibleContractError as exc:
        fail_visible_contract(args, pane, str(exc))
        raise SystemExit(f"visible runtime contract failed for {args.name}: {exc}") from exc
    except Exception as exc:
        if pane:
            fail_visible_contract(args, pane, f"{type(exc).__name__}: {exc}")
        raise
    result = {
        "session": args.name,
        "pane": pane,
        "wrapper": str(wrapper),
        "mode": command_kind,
        "prompt_file": str(prompt_path),
        "pane_log": pane_log,
        "debug_file": debug_path,
        "launch_record": launch_record,
        "visible_proof": visible_proof,
        "prompt_submitted": prompt_submitted,
    }
    if assignment_run:
        result["assignment_run"] = assignment_run["run_dir"]
        result["launch_id"] = assignment_run["run_id"]
        if not prompt_submitted:
            result["setup_required"] = "Runtime lifecycle initialization is not ready. Resolve any workspace/hook trust prompts, then submit this saved assignment."
            result["submit_command"] = [sys.executable, str(Path(__file__).resolve()), "submit-assignment", "--pane", pane, "--assignment-run", assignment_run["run_dir"]]
    if parallel_contract is not None:
        result["parallel_contract"] = parallel_contract
    return result


def cmd_spawn(args: argparse.Namespace) -> int:
    if not args.cmd_args:
        raise SystemExit("spawn requires a command after --")
    if session_exists(args.name):
        raise SystemExit(f"tmux session already exists: {args.name}")

    run_root = Path(args.run_root).expanduser().resolve() if args.run_root else Path.cwd().resolve()
    args.run_root = str(run_root)
    logs_dir = run_root / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)
    pane_log = str(logs_dir / f"{args.name}-pane.log")
    if getattr(args, "kind", "") == "batch-worker":
        if not getattr(args, "evidence_path", ""):
            try:
                args.evidence_path = str(Path(pane_log).resolve().relative_to(run_root.resolve()))
            except ValueError:
                args.evidence_path = str(Path(pane_log).resolve())
        validate_launch_contract(args, generic=True)
    else:
        ensure_cleanup_defaults(args, pane_log=pane_log, run_root=run_root)
        validate_launch_contract(args, generic=True)
    launch_record = str(logs_dir / f"{args.name}-generic-launch.json")
    wrapper = write_wrapper(args.name, args.cmd_args, pane_log=pane_log, launch_record=launch_record)
    run_tmux("new-session", "-d", "-s", args.name, "-n", "main", str(wrapper))
    run_tmux("set-window-option", "-t", f"{args.name}:main", "remain-on-exit", "on")
    pane = first_pane(f"{args.name}:main")
    if args.title:
        run_tmux("select-pane", "-t", pane, "-T", args.title)
    values = metadata_from_args(args, state=None)
    values["pane_log"] = pane_log
    set_pane_options(pane, values)
    print(json.dumps({"session": args.name, "pane": pane, "wrapper": str(wrapper), "pane_log": pane_log, "launch_record": launch_record}, indent=2))
    return 0


def cmd_spawn_claude(args: argparse.Namespace) -> int:
    validate_launch_contract(args, generic=False)
    validate_claude_role_model(args)
    prompt_path = prompt_path_from_args(args)
    run_root = resolve_run_root(args, prompt_path)
    logs_dir = run_root / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)
    if not args.debug_file:
        args.debug_file = str(logs_dir / "claude-debug.log")
    command = build_claude_tui_command(args)
    result = spawn_tui_session(
        args,
        command=command,
        command_kind="claude_tui",
        window_name="claude",
        ready_text=args.ready_text or "Claude Code",
        debug_file=args.debug_file,
        exit_label="claude TUI",
    )
    print(json.dumps(result, indent=2))
    return 0


def require_flag_value(parts: list[str], flag: str, expected: str) -> None:
    try:
        index = parts.index(flag)
    except ValueError as exc:
        raise VisibleContractError(f"Claude workflow runtime is missing {flag}") from exc
    if index + 1 >= len(parts) or parts[index + 1] != expected:
        observed = parts[index + 1] if index + 1 < len(parts) else "<missing>"
        raise VisibleContractError(f"Claude workflow runtime {flag}={observed!r}, expected {expected!r}")






def cmd_spawn_codex(args: argparse.Namespace) -> int:
    args.agent = "codex"
    validate_launch_contract(args, generic=False)
    command = build_codex_tui_command(args)
    result = spawn_tui_session(
        args,
        command=command,
        command_kind="codex_tui",
        window_name="codex",
        ready_text=args.ready_text or "OpenAI Codex",
        exit_label="codex TUI",
        dismiss_codex_update=True,
    )
    print(json.dumps(result, indent=2))
    return 0




def cmd_spawn_agy(args: argparse.Namespace) -> int:
    args.agent = "agy"
    validate_launch_contract(args, generic=False)
    command = build_agy_tui_command(args)
    result = spawn_tui_session(
        args,
        command=command,
        command_kind="agy_tui",
        window_name="agy",
        ready_text=args.ready_text or "? for shortcuts",
        exit_label="agy TUI",
    )
    print(json.dumps(result, indent=2))
    return 0








DEGRADED_EVIDENCE_STATES = {"failed", "stale", "blocked"}


def write_degraded_evidence(pane: str, state: str, values: dict[str, str | None], *, target_label: str) -> dict[str, str]:
    """Ensure a terminal problem state leaves truthful non-empty evidence.

    Failed/quota-blocked lanes historically left missing or empty evidence
    files, which session_hygiene refuses forever (evidence_empty) — the pane
    became immortal cleanup debt. When set-state records a terminal problem
    state and the lane's evidence file is missing or empty, write a truthful
    degraded/non-evidence artifact so cleanup can proceed under the
    failed-visible grace rules. Never overwrites non-empty evidence; never
    writes outside the run root; never bypasses evidence validation rules.
    """
    meta = read_pane_metadata(pane)
    run_root_raw = str(values.get("run_root") or meta.get("run_root", "")).strip()
    evidence_raw = meta.get("evidence_path", "").strip()
    if not evidence_raw:
        return {"status": "skipped", "reason": "evidence_path_empty"}
    if not run_root_raw:
        return {"status": "skipped", "reason": "run_root_empty"}
    root = Path(run_root_raw).expanduser()
    if not root.is_absolute():
        return {"status": "skipped", "reason": "relative_run_root"}
    root = root.resolve()
    path = Path(evidence_raw).expanduser()
    resolved = path.resolve() if path.is_absolute() else (root / path).resolve()
    try:
        resolved.relative_to(root)
    except ValueError:
        return {"status": "skipped", "reason": "evidence_outside_run_root"}
    if resolved.exists():
        if not resolved.is_file():
            return {"status": "skipped", "reason": "evidence_not_regular_file"}
        if resolved.stat().st_size > 0:
            return {"status": "kept", "reason": "evidence_already_nonempty", "path": str(resolved)}
    end_reason = str(values.get("end_reason") or "").strip() or "unspecified"
    route_failure = str(values.get("route_failure_reason") or "").strip()
    lines = [
        "DEGRADED RESULT — truthful non-evidence artifact",
        "",
        f"target: {target_label}",
        f"pane: {pane}",
        f"state: {state}",
        f"end_reason: {end_reason}",
    ]
    if route_failure:
        lines.append(f"route_failure_reason: {route_failure}")
    lines += [
        f"recorded_at: {utc_now()}",
        "",
        "This lane reached a terminal problem state without producing its",
        "evidence artifact. agent_wall set-state wrote this degraded record",
        "so the failure is documented and cleanup can proceed instead of the",
        "pane becoming immortal evidence-empty debt. This file is proof of",
        "failure, not proof of completed work.",
    ]
    try:
        resolved.parent.mkdir(parents=True, exist_ok=True)
        resolved.write_text("\n".join(lines) + "\n", encoding="utf-8")
    except OSError as exc:
        return {"status": "error", "reason": str(exc)}
    return {"status": "written", "path": str(resolved)}


def cmd_set_state(args: argparse.Namespace) -> int:
    if not args.name and not args.pane:
        raise SystemExit("set-state requires --name or --pane")
    pane = args.pane or unique_pane_for_session(args.name)
    now = utc_now()
    values: dict[str, str | None] = {
        "state": args.state,
        "updated_at": now,
        "goal": args.goal,
        "run_root": args.run_root,
        "thread_id": args.thread_id,
        "session_id": args.session_id,
        "exit_code": args.exit_code,
        "end_reason": getattr(args, "end_reason", ""),
        "route_failure_reason": getattr(args, "route_failure_reason", ""),
    }
    unset_fields: set[str] = set()
    if args.state in RECOVERY_STATES:
        if not getattr(args, "route_failure_reason", ""):
            unset_fields.add("route_failure_reason")
        if not getattr(args, "end_reason", ""):
            unset_fields.add("end_reason")
    if args.state in {"done", "failed", "stale"}:
        values["completed_at"] = now
        if args.state == "done" and not args.exit_code:
            values["exit_code"] = "0"
        if not getattr(args, "end_reason", ""):
            values["end_reason"] = "operator_set_state"
    if unset_fields:
        unset_pane_options(pane, unset_fields)
    set_pane_options(pane, values)
    out: dict[str, object] = {"target": args.name or pane, "pane": pane, "state": args.state}
    if args.state in DEGRADED_EVIDENCE_STATES:
        out["degraded_evidence"] = write_degraded_evidence(
            pane, args.state, values, target_label=args.name or pane
        )
    print(json.dumps(out, indent=2))
    return 0


def cmd_release_hold(args: argparse.Namespace) -> int:
    """Clear an evidence hold and stamp completion metadata after synthesis.

    Non-destructive by contract: this command only rewrites @oc_* pane metadata.
    It unsets exactly one field (@oc_hold_reason) and stamps completion metadata
    so a *future* session_hygiene plan can treat the pane as reapable. It never
    calls tmux kill-*, archive helpers, session_hygiene apply, hide, or detach —
    clearing the hold makes future cleanup truthful, it does not perform cleanup.

    session_hygiene is the single marking authority: release-hold must not
    pre-stamp teardown_marked_at/teardown_reason/janitor_state. The janitor
    marks the released pane on its next cycle if it is otherwise eligible.
    """
    if not args.name and not args.pane:
        raise SystemExit("release-hold requires --name or --pane (exact target)")
    pane = args.pane or unique_pane_for_session(args.name)

    current = read_pane_metadata(pane)
    now = utc_now()
    end_reason = (args.end_reason or "").strip() or "evidence_captured_release"
    planned_set: dict[str, str | None] = {
        "state": "done",
        "completed_at": now,
        "updated_at": now,
        "end_reason": end_reason,
        "exit_code": "0",
    }
    had_hold = bool((current.get("hold_reason") or "").strip())
    report = {
        "command": "release-hold",
        "target": args.name or pane,
        "pane": pane,
        "dry_run": bool(args.dry_run),
        "current_meta": current,
        "planned_set": planned_set,
        "planned_unset": ["@oc_hold_reason"],
        "had_hold_reason": had_hold,
        # Clearing the hold removes the reason session_hygiene refuses this pane,
        # so after release it becomes eligible for a future hygiene plan.
        "becomes_hygiene_eligible": had_hold,
        "marking_authority": "session_hygiene",
        "side_effects": {
            "kills": False,
            "archives": False,
            "hides": False,
            "detaches": False,
            "session_hygiene_apply": False,
        },
    }

    if args.dry_run:
        report["applied"] = False
        print(json.dumps(report, indent=2, sort_keys=True))
        return 0

    if not (args.evidence_captured and args.allow_hygiene_after_release):
        raise SystemExit(
            "refusing real release-hold without --evidence-captured and "
            "--allow-hygiene-after-release; rerun with --dry-run to preview the plan"
        )

    # Apply: unset only @oc_hold_reason, then stamp completion metadata.
    unset_hold_reason(pane)
    set_pane_options(pane, planned_set)
    report["applied"] = True
    print(json.dumps(report, indent=2, sort_keys=True))
    return 0


def cmd_keep_open(args: argparse.Namespace) -> int:
    pane = unique_pane_for_session(args.name)
    if not args.hold_reason.strip():
        raise SystemExit("keep-open requires a nonempty --hold-reason")
    deadline = hold_deadline(args)
    set_pane_options(pane, {"hold_until": deadline, "hold_reason": args.hold_reason})
    print(json.dumps({"pane": pane, "hold_until": deadline}))
    return 0


def cmd_annotate(args: argparse.Namespace) -> int:
    if not args.name and not args.pane:
        raise SystemExit("annotate requires --name or --pane")
    pane = args.pane or unique_pane_for_session(args.name)
    values = display_annotation_from_args(args)
    if args.kind in {"viewer", "service", "runtime"}:
        values["agent"] = ""
    if not values["agent"]:
        run_tmux("set-option", "-p", "-u", "-t", pane, "@oc_agent")
    set_pane_options(pane, values)
    print(
        json.dumps(
            {
                "target": args.name or pane,
                "pane": pane,
                "mode": "manual_service" if args.kind == "service" else "display_only",
                "managed_for_cleanup": False,
            },
            indent=2,
        )
    )
    return 0


def cmd_list(args: argparse.Namespace) -> int:
    fmt = TMUX_FIELD_SEP.join(
        [
            "#{session_name}",
            "#{window_name}",
            "#{pane_id}",
            "#{pane_title}",
            "#{pane_dead}",
            "#{pane_dead_status}",
            *[f"#{{@oc_{field}}}" for field in OC_FIELDS],
        ]
    )
    cp = run_tmux("list-panes", "-a", "-F", fmt, check=False)
    if cp.returncode != 0:
        print(cp.stderr.strip(), file=sys.stderr)
        return cp.returncode
    items = []
    for line in cp.stdout.splitlines():
        fields = line.split(TMUX_FIELD_SEP)
        if len(fields) != 6 + len(OC_FIELDS):
            continue
        meta = dict(zip(OC_FIELDS, fields[6:]))
        if not any(meta.values()):
            continue
        items.append(
            {
                "session": fields[0],
                "window": fields[1],
                "pane": fields[2],
                "title": fields[3],
                "dead": fields[4] == "1",
                "dead_status": fields[5],
                "meta": meta,
            }
        )
    if args.json:
        print(json.dumps(items, indent=2, sort_keys=True))
    else:
        for item in items:
            meta = item["meta"]
            print(
                f"{item['session']}\t{item['pane']}\t{meta.get('agent') or meta.get('kind')}\t"
                f"{meta.get('state')}\t{meta.get('project')}\t{meta.get('goal')}"
            )
    return 0


def smoke_command(index: int) -> list[str]:
    state_line = smoke_state(index)
    exit_code = 1 if state_line == "failed" else 0
    loops = 8 if index % 4 else 3
    script = (
        f"tmux set-option -p -t \"$TMUX_PANE\" @oc_state {shlex.quote(state_line)} >/dev/null 2>&1 || true; "
        f"for i in $(seq 1 {loops}); do "
        f"echo '[smoke {index:02d}] tick '$i' state={state_line}'; "
        f"sleep 1; "
        f"done; exit {exit_code}"
    )
    return ["bash", "-lc", script]


def smoke_state(index: int) -> str:
    states = ["running", "waiting", "blocked", "failed", "done"]
    return states[(index - 1) % len(states)]


def cmd_smoke_start(args: argparse.Namespace) -> int:
    for i in range(1, args.count + 1):
        name = f"{SMOKE_PREFIX}{i:02d}"
        if session_exists(name):
            continue
        spawn_args = argparse.Namespace(
            name=name,
            cmd_args=smoke_command(i),
            title=f"smoke-{i:02d}",
            kind="smoke",
            agent=["codex", "fable", "agy"][i % 3],
            owner="workshop-4",
            project="Cockpit",
            goal=f"Disposable cockpit smoke worker {i:02d}",
            run_root=str((Path.cwd() / "memory/runs/Cockpit-cockpit-smoke-20260702").resolve()),
            thread_id="",
            session_id=f"smoke-{i:02d}",
            ttl="30m",
            cleanup_policy="smoke",
            evidence_path="",
            hold_reason="",
            why_headless="",
            progress_path="",
            end_reason="",
            route_failure_reason="",
        )
        cmd_spawn(spawn_args)
    return 0


def cmd_smoke_cleanup(args: argparse.Namespace) -> int:
    hygiene = Path(__file__).with_name("session_hygiene.py")
    if not hygiene.exists():
        raise SystemExit(f"missing hygiene tool: {hygiene}")
    cmd = [sys.executable, str(hygiene), "apply", "--policy", "smoke", "--json"]
    cp = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if cp.stdout:
        print(cp.stdout, end="")
    if cp.stderr:
        print(cp.stderr, file=sys.stderr, end="")
    return cp.returncode


def add_metadata_args(p: argparse.ArgumentParser, *, default_agent: str, default_kind: str = "agent") -> None:
    p.add_argument("--name", required=True)
    p.add_argument("--title", default="")
    p.add_argument("--kind", default=default_kind)
    p.add_argument("--agent", default=default_agent)
    p.add_argument("--owner", default="")
    p.add_argument("--project", default="")
    p.add_argument("--goal", default="")
    p.add_argument("--run-root", default="")
    p.add_argument("--thread-id", default="")
    p.add_argument("--session-id", default="")
    p.add_argument("--ttl", default="never")
    p.add_argument("--cleanup-policy", default="manual", choices=["manual", "hide", "kill_after_ttl", "kill_on_done", "smoke"])
    p.add_argument("--evidence-path", default="")
    p.add_argument("--hold-reason", default="")
    p.add_argument("--hold-hours", type=float, default=24, help="Renewable keep-open lease (default: 24h); expiry never proves completion.")
    p.add_argument("--why-headless", default="")
    p.add_argument("--progress-path", default="")
    p.add_argument("--end-reason", default="")
    p.add_argument("--route-failure-reason", default="")


def add_tui_common_args(p: argparse.ArgumentParser) -> None:
    p.add_argument("--keep-open", action="store_true", help="Keep the runtime available after this assignment completes.")
    p.add_argument("--prompt-file", required=True)
    p.add_argument("--pane-log", default="")
    p.add_argument(
        "--cols",
        type=int,
        default=DEFAULT_TUI_COLS,
        help=f"tmux worker width for managed visible TUIs (default {DEFAULT_TUI_COLS}).",
    )
    p.add_argument(
        "--rows",
        type=int,
        default=DEFAULT_TUI_ROWS,
        help=f"tmux worker height for managed visible TUIs (default {DEFAULT_TUI_ROWS}).",
    )
    p.add_argument(
        "--ready-text",
        default="",
        help="Optional pane-log text to wait for before prompt injection. Empty means wait for any output.",
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Manage portable OpenClaw Cockpit tmux jobs.")
    sub = parser.add_subparsers(dest="subcommand", required=True)



    submit = sub.add_parser("submit-assignment", help="Submit the saved initial task after first-use runtime hook trust.")
    submit.add_argument("--pane", required=True)
    submit.add_argument("--assignment-run", required=True)
    submit.set_defaults(func=cmd_submit_assignment)

    spawn = sub.add_parser("spawn", help="Create a metadata-tagged tmux worker session.")
    add_metadata_args(spawn, default_agent="shell", default_kind="shell")
    spawn.add_argument("cmd_args", nargs=argparse.REMAINDER)
    spawn.set_defaults(func=cmd_spawn)

    claude = sub.add_parser(
        "spawn-claude",
        help="Create a metadata-tagged tmux session with the real Claude Code TUI in the visible pane.",
    )
    add_metadata_args(claude, default_agent="claude", default_kind="visible-agent")
    add_tui_common_args(claude)
    claude.add_argument("--model", default="claude-fable-5-1")
    claude.add_argument("--effort", default="high")
    claude.add_argument("--auth-route", choices=["subscription", "configured"], default="subscription",
                        help="Subscription scrubs alternate environment routes; configured preserves explicitly approved auth. Neither attests billing.")
    claude.add_argument("--permission-mode", default="default")
    claude.add_argument("--claude-name", default="")
    claude.add_argument("--debug-file", default="")
    claude.add_argument(
        "--claude-arg",
        action="append",
        default=[],
        help="Additional single Claude CLI argument. Repeat for multiple args.",
    )
    claude.set_defaults(func=cmd_spawn_claude)

    codex = sub.add_parser(
        "spawn-codex",
        help="Create a metadata-tagged tmux session with the real Codex TUI in the visible pane.",
    )
    add_metadata_args(codex, default_agent="codex", default_kind="visible-agent")
    add_tui_common_args(codex)
    codex.add_argument("--model", default="gpt-6-astra")
    codex.add_argument("--effort", choices=["minimal", "low", "medium", "high", "xhigh"],
                       default="high", help="Reasoning effort (default: high).")
    codex.add_argument("--codex-home", default=str(Path.home() / ".codex"))
    codex.add_argument("--ask-for-approval", default="on-request", choices=["on-request", "never"])
    codex.add_argument("--sandbox", default="read-only", choices=["read-only", "workspace-write", "danger-full-access"])
    codex.add_argument("--cd", default="")
    codex.add_argument("--add-dir", action="append", default=[])
    codex.add_argument("--no-alt-screen", action="store_true")
    codex.add_argument(
        "--codex-arg",
        action="append",
        default=[],
        help="Additional single Codex CLI argument. Repeat for multiple args.",
    )
    codex.set_defaults(func=cmd_spawn_codex)

    agy = sub.add_parser(
        "spawn-agy",
        help="Create a metadata-tagged tmux session with the real AGY/Antigravity TUI in the visible pane.",
    )
    add_metadata_args(agy, default_agent="agy", default_kind="visible-agent")
    add_tui_common_args(agy)
    agy.add_argument("--model", default="")
    agy.add_argument(
        "--agy-project",
        default="",
        help="Pass through to 'agy --project'. Cockpit --project remains display metadata.",
    )
    agy.add_argument("--new-project", action="store_true")
    agy.add_argument("--sandbox", action="store_true")
    agy.add_argument("--log-file", default="")
    agy.add_argument("--add-dir", action="append", default=[])
    agy.add_argument(
        "--agy-arg",
        action="append",
        default=[],
        help="Additional single AGY CLI argument. Repeat for multiple args.",
    )
    agy.set_defaults(func=cmd_spawn_agy)

    state = sub.add_parser("set-state", help="Update a worker pane state.")
    state.add_argument("--name", default="")
    state.add_argument("--pane", default="")
    state.add_argument("--state", required=True, choices=["starting", "running", "waiting", "blocked", "done", "failed", "stale"])
    state.add_argument("--goal", default="")
    state.add_argument("--run-root", default="")
    state.add_argument("--thread-id", default="")
    state.add_argument("--session-id", default="")
    state.add_argument("--exit-code", default="")
    state.add_argument("--end-reason", default="")
    state.add_argument("--route-failure-reason", default="")
    state.set_defaults(func=cmd_set_state)

    release = sub.add_parser(
        "release-hold",
        help=(
            "Clear @oc_hold_reason and stamp completion metadata after evidence capture. "
            "Non-destructive: never kills, archives, hides, detaches, or applies session hygiene."
        ),
    )
    release.add_argument("--name", default="", help="Exact session name (must resolve to a single pane).")
    release.add_argument("--pane", default="", help="Exact pane id (e.g. %%12). Takes precedence over --name.")
    release.add_argument("--end-reason", default="", help="Completion reason to stamp (default evidence_captured_release).")
    release.add_argument("--dry-run", action="store_true", help="Print the planned metadata writes as JSON and make no changes.")
    release.add_argument(
        "--evidence-captured",
        action="store_true",
        help="Confirm evidence for this pane has already been synthesized/captured.",
    )
    release.add_argument(
        "--allow-hygiene-after-release",
        action="store_true",
        help="Acknowledge that clearing the hold makes the pane eligible for future hygiene cleanup.",
    )
    release.set_defaults(func=cmd_release_hold)

    keep = sub.add_parser("keep-open", help="Renew a terminal hold without changing cleanup authority or runtime state.")
    keep.add_argument("--name", required=True)
    keep.add_argument("--hold-reason", required=True)
    keep.add_argument("--hold-hours", type=float, default=24)
    keep.set_defaults(func=cmd_keep_open)

    annotate = sub.add_parser(
        "annotate",
        help="Add display-only @oc_* metadata to an existing raw tmux session without cleanup authority.",
    )
    annotate.add_argument("--name", default="")
    annotate.add_argument("--pane", default="")
    annotate.add_argument("--kind", default="agent")
    annotate.add_argument("--agent", default="")
    annotate.add_argument("--owner", default="")
    annotate.add_argument("--project", default="")
    annotate.add_argument("--goal", default="")
    annotate.add_argument("--state", default="running", choices=["starting", "running", "waiting", "blocked", "done", "failed", "stale"])
    annotate.add_argument("--run-root", default="")
    annotate.add_argument("--thread-id", default="")
    annotate.add_argument("--session-id", default="")
    annotate.add_argument("--ttl", default="never")
    annotate.add_argument("--evidence-path", default="")
    annotate.add_argument("--hold-reason", default="")
    annotate.add_argument("--hold-hours", type=float, default=24)
    annotate.set_defaults(func=cmd_annotate)

    list_p = sub.add_parser("list", help="List metadata-tagged tmux items.")
    list_p.add_argument("--json", action="store_true")
    list_p.set_defaults(func=cmd_list)

    smoke_start = sub.add_parser("smoke-start", help="Create disposable oc-vis-smoke-* workers.")
    smoke_start.add_argument("--count", type=int, default=16)
    smoke_start.set_defaults(func=cmd_smoke_start)

    smoke_cleanup = sub.add_parser("smoke-cleanup", help="Kill only disposable oc-vis-smoke-* workers.")
    smoke_cleanup.set_defaults(func=cmd_smoke_cleanup)

    return parser


def dismiss_codex_update_prompt_if_needed(pane: str, path: Path, *, timeout: float = 8.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if path.exists():
            text = path.read_text(encoding="utf-8", errors="ignore")
            if "OpenAI Codex" in text:
                return
            if "Update available" in text and "Press enter to continue" in text:
                run_tmux("send-keys", "-t", pane, "-l", "--", "2")
                run_tmux("send-keys", "-t", pane, "Enter")
                return
        time.sleep(0.2)

def inject_prompt(pane: str, prompt_file: Path, *, buffer_name: str) -> None:
    time.sleep(0.5)
    run_tmux("load-buffer", "-b", buffer_name, str(prompt_file))
    try:
        run_tmux("paste-buffer", "-b", buffer_name, "-t", pane)
        time.sleep(0.35)
        run_tmux("send-keys", "-t", pane, "Enter")
    finally:
        run_tmux("delete-buffer", "-b", buffer_name, check=False)

def prompt_path_from_args(args: argparse.Namespace) -> Path:
    prompt_path = Path(args.prompt_file).expanduser().resolve()
    if not prompt_path.exists():
        raise SystemExit(f"missing prompt file: {prompt_path}")
    if not prompt_path.read_text(encoding="utf-8").strip():
        raise SystemExit(f"empty prompt file: {prompt_path}")
    return prompt_path


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if getattr(args, "subcommand", None) == "spawn" and args.cmd_args and args.cmd_args[0] == "--":
        args.cmd_args = args.cmd_args[1:]
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
