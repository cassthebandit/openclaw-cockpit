#!/usr/bin/env python3
"""Classify and safely clean OpenClaw tmux sessions.

Default posture is dry-run. Destructive cleanup requires either a complete
managed @oc_* contract with an eligible cleanup policy or an exact
--allow-session selection. Apply still requires a dead, single-pane, unlinked,
ungrouped session; selection never overrides those topology safeguards.
"""

from __future__ import annotations

import argparse
import contextlib
import fcntl
import functools
import uuid
import hashlib
import json
import os
import re
import shlex
import subprocess
import sys
import tempfile
from dataclasses import asdict, dataclass, replace
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

try:
    from . import lifecycle, adoption, event_log, holds, manual_close, format_guard
except ImportError:
    import lifecycle
    import adoption
    import event_log
    import holds
    import manual_close
    import format_guard

STATE_ROOT = Path(os.environ.get("OPENCLAW_COCKPIT_STATE_DIR", "~/.local/state/openclaw-cockpit")).expanduser()

OC_FIELDS = [
    "launch_id",
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
    "progress_path",
    "end_reason",
    "route_failure_reason",
    "teardown_marked_at",
    "teardown_reason",
    "janitor_state",
    "last_meaningful_activity_at",
    "keep_open",
    "completed_retention_seconds",
    "failed_retention_seconds",
]

VALID_SESSION_RE = re.compile(r"^[A-Za-z0-9_.:@%+=,/-]+$")
STATE_CLEANABLE = {"done", "failed", "stale", "blocked"}
# Printable across tmux versions; reject delimiter collisions by exact field count.
TMUX_FIELD_SEP = "|:oc:|"
SMOKE_PREFIX = "oc-vis-smoke-"
STATUS_VERSION = 1
DEFAULT_STATUS_FILE = STATE_ROOT / "hygiene/status.json"
# Live-dashboard rule: terminal panes are transient UI, not retained evidence.
# The cleanup archive and ledger preserve the pane output and metadata.
ACTIVE_IDLE_MARK_SECONDS = 180
TEARDOWN_GRACE_SECONDS = 60
FAILED_VISIBLE_SECONDS = 180
ABSOLUTE_MAX_SECONDS = 3600
OPERATOR_TAIL_LINES = 12
COMPLETION_MARKERS = (
    "sautéed for ",
    "sauteed for ",
    "crunched for ",
    "worked for ",
    "cooked for ",
    "brewed for ",
    "baked for ",
    "ran for ",
    "done in ",
    "completed in ",
    "finished in ",
    "goal achieved",
    "goal complete",
    "task complete",
    "all done",
    "ready_for_parent_review",
)
ACTIVE_MARKERS = (
    "esc to interrupt",
    "still running",
    "waiting for background terminal",
    "ctrl + t to view transcript",
    "ctrl+t to view transcript",
    "tool call in progress",
    "running tool",
    "executing command",
    "generating…",
    "generating...",
    "thinking…",
    "esc to cancel",
)
OPERATOR_MARKERS = (
    "approve plan",
    "approval required",
    "apply changes?",
    "proceed?",
    "permission requested",
    "allow command",
    "allow this command",
    "device code",
    "authorize",
    "log in to github",
    "ready to code?",
    "would you like to proceed?",
)
MENU_PROMPT_MARKERS = (
    "choose an option",
    "select an option",
    "what would you like to do",
    "press 1",
)
NUMBERED_CHOICE_RE = re.compile(r"(?m)^\s*(?:[1-9][0-9]*[\.)]\s+|[❯>]\s*[1-9][0-9]*\b)")
COMPLETION_SHAPE_RE = re.compile(r"(?i)\b[\wÀ-ÿ]+(?:ed|n)\s+for\s+\d+\s*(?:s|m|h|d|sec|secs|second|seconds|min|mins|minute|minutes|hr|hrs|hour|hours)\b")
ANSI_CSI_RE = re.compile(r"\x1b\[[0-9;:?]*[ -/]*[@-~]")
ANSI_OSC_RE = re.compile(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
SECRET_PATTERNS = [
    re.compile(r"(?i)(authorization:\s*bearer\s+)[^\s]+"),
    re.compile(r"(?i)((?:api[_-]?key|token|secret|password|credential)[A-Za-z0-9_. -]{0,40}[:=]\s*)[^\s'\"<>]+"),
]
ADOPTED_CODEX_EVIDENCE_FILENAMES = (
    "COMPLETION_AUDIT.md",
    "RESULT.md",
    "REPORT.md",
    "FINAL_REVIEW.md",
    "EXECUTION_SUMMARY.md",
    "CLOSEOUT.md",
    "SUMMARY.md",
)


@dataclass
class Pane:
    session: str
    window: str
    pane: str
    title: str
    command: str
    path: str
    created: str
    last_activity: str
    dead: bool
    dead_status: str
    meta: dict[str, str]
    pid: str = ""
    process_started: str = ""
    server_session_id: str = ""
    window_linked: str = ""
    session_grouped: str = ""
    server_socket: str = ""
    server_pid: str = ""
    server_started: str = ""
    session_windows: str = ""
    window_panes: str = ""
    session_attached: str = ""
    pane_in_mode: str = ""
    pane_input_off: str = ""
    pane_pipe: str = ""
    pane_synchronized: str = ""
    remain_on_exit: str = ""
    window_activity: str = ""
    self_ancestor: str = "0"


OBS_FIELDS = ("socket_path", "pid", "start_time", "session_windows", "window_panes",
              "session_attached", "pane_in_mode", "pane_input_off", "pane_pipe",
              "synchronize-panes", "remain-on-exit", "window_activity")
SOCKET_PATH = ""


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


def run_tmux(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    # Preserve Unicode metadata even when called outside tmux in a C locale.
    return subprocess.run(
        ["tmux", "-u", *(["-S", SOCKET_PATH] if SOCKET_PATH else []), *args],
        check=check,
        text=True, encoding="utf-8",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def redact_text(text: str) -> str:
    redacted = text
    for pattern in SECRET_PATTERNS:
        redacted = pattern.sub(r"\1<redacted>", redacted)
    return redacted


def safe_slug(value: str) -> str:
    slug = re.sub(r"[^A-Za-z0-9_.-]+", "_", value.strip())
    return slug.strip("._") or "session"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def default_archive_root() -> Path:
    return STATE_ROOT / "cleanup-ledger"


def validate_session_name(name: str) -> None:
    if not name or any(ch in name for ch in "\r\n\t\0"):
        raise ValueError("empty_or_control_character_session_name")
    if not VALID_SESSION_RE.match(name):
        raise ValueError("unsupported_session_name")


def exact_target(session: str) -> str:
    validate_session_name(session)
    return "=" + session


def parse_iso(value: str) -> datetime | None:
    value = value.strip()
    if not value:
        return None
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def parse_ttl(value: str) -> tuple[int | None, str]:
    value = value.strip().lower()
    if value in {"", "never"}:
        return None, "never"
    if re.fullmatch(r"[1-9][0-9]*", value):
        return int(value), "ok"
    match = re.fullmatch(r"([1-9][0-9]*)([smhd])", value)
    if not match:
        return None, "invalid_ttl"
    amount = int(match.group(1))
    unit = match.group(2)
    multiplier = {"s": 1, "m": 60, "h": 3600, "d": 86400}[unit]
    return amount * multiplier, "ok"


def strip_ansi(text: str) -> str:
    text = ANSI_CSI_RE.sub("", text)
    text = ANSI_OSC_RE.sub("", text)
    return "".join(ch for ch in text if ch in "\n\t" or (ord(ch) >= 32 and ord(ch) != 127))


def non_empty_lines(text: str) -> list[str]:
    return [line.rstrip(" \t\r") for line in text.splitlines() if line.strip()]


def bounded_tail(lines: list[str], count: int) -> list[str]:
    return lines[-count:] if len(lines) > count else lines


def normalize_tail_for_hash(text: str) -> str:
    lines = bounded_tail(non_empty_lines(strip_ansi(text)), 24)
    kept: list[str] = []
    for line in lines:
        lowered = line.strip().lower()
        if not lowered:
            continue
        if re.fullmatch(r"[│|▏▕▎▐\s╭╰╮╯─┌└┐┘]+", lowered):
            continue
        if re.search(r"\b(?:gpt|claude|gemini|agy|tokens?)\b.*(?:%|\d+[km]?\b)", lowered):
            continue
        if re.search(r"\b\d{1,2}:\d{2}(?::\d{2})?\b", lowered) and len(lowered) < 80:
            continue
        if "clear to save tokens" in lowered:
            continue
        kept.append(" ".join(lowered.split()))
    return "\n".join(kept)


def normalized_tail_hash(text: str) -> str:
    normalized = normalize_tail_for_hash(text)
    return hashlib.sha256(normalized.encode("utf-8")).hexdigest() if normalized else ""


def screen_has_active_marker(text: str) -> bool:
    lowered = "\n".join(bounded_tail(non_empty_lines(strip_ansi(text)), 50)).lower()
    return any(marker in lowered for marker in ACTIVE_MARKERS)


def screen_has_completion_marker(text: str) -> bool:
    lowered = "\n".join(bounded_tail(non_empty_lines(strip_ansi(text)), 50)).lower()
    return any(marker in lowered for marker in COMPLETION_MARKERS) or bool(COMPLETION_SHAPE_RE.search(lowered))


def screen_has_idle_prompt(text: str) -> bool:
    lines = bounded_tail(non_empty_lines(strip_ansi(text)), 8)
    for line in lines:
        trimmed = line.strip().lstrip("│|╭╰╮╯─┌└┐┘▏▕▎▐ ")
        if trimmed.startswith(("›", "❯", "▌", ">")):
            return True
    lowered = "\n".join(lines).lower()
    return any(marker in lowered for marker in ("type your message", "waiting for your input", "ready for the next", "ready for input"))


def screen_has_operator_prompt(text: str) -> bool:
    lines = bounded_tail(non_empty_lines(strip_ansi(text)), OPERATOR_TAIL_LINES)
    lowered = "\n".join(lines).lower()
    if not lowered:
        return False
    if any(marker in lowered for marker in OPERATOR_MARKERS):
        return True
    has_menu_phrase = any(marker in lowered for marker in MENU_PROMPT_MARKERS)
    has_numbered_choice = bool(NUMBERED_CHOICE_RE.search("\n".join(lines).lower()))
    if has_menu_phrase and has_numbered_choice:
        return True
    if re.search(r"\b[1-9][0-9]*\.\s*(approve|reject|deny|edit|allow)\b", lowered):
        return True
    return False


def managed_tui_completion_screen(text: str) -> bool:
    if screen_has_active_marker(text) or screen_has_operator_prompt(text):
        return False
    return screen_has_completion_marker(text) and screen_has_idle_prompt(text)


def load_status(path: str | Path | None) -> dict[str, Any]:
    if not path:
        return {"status_version": STATUS_VERSION, "sessions": {}}
    status_path = Path(path).expanduser()
    try:
        payload = json.loads(status_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {"status_version": STATUS_VERSION, "sessions": {}}
    if not isinstance(payload, dict) or payload.get("status_version") != STATUS_VERSION:
        return {"status_version": STATUS_VERSION, "sessions": {}}
    sessions = payload.get("sessions")
    if not isinstance(sessions, dict):
        payload["sessions"] = {}
    records = payload.get("adoptions", {})
    if not isinstance(records, dict) or any(not isinstance(record, dict) for record in records.values()):
        # Corrupt enrollment cannot authorize a native request. A new explicit
        # owner decision is required; historical display fields never enroll.
        payload["adoptions"] = {}
    return payload


def write_status(path: str | Path | None, payload: dict[str, Any]) -> None:
    if not path:
        return
    status_path = Path(path).expanduser()
    status_path.parent.mkdir(parents=True, exist_ok=True)
    payload["status_version"] = STATUS_VERSION
    payload["generated_at"] = utc_now().replace(microsecond=0).isoformat().replace("+00:00", "Z")
    fd, tmp_name = tempfile.mkstemp(prefix=".status.", suffix=".json", dir=str(status_path.parent))
    try:
        with open(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, indent=2, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        Path(tmp_name).replace(status_path)
        directory = os.open(status_path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    except Exception:
        Path(tmp_name).unlink(missing_ok=True)
        raise


@contextlib.contextmanager
def status_lock(path):
    if not path:
        yield
        return
    lock = Path(str(Path(path).expanduser()) + ".lock")
    lock.parent.mkdir(parents=True, exist_ok=True)
    with lock.open("a") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def serialized_status(function):
    @functools.wraps(function)
    def call(args):
        writes = function.__name__ in {"cmd_apply", "cmd_release", "cmd_revoke"} or getattr(args, "write_status", False)
        writes = writes or (function.__name__ == "cmd_manual_close" and getattr(args, "execute", False))
        with status_lock(getattr(args, "status_file", "")) if writes and not getattr(args, "dry_run", False) else contextlib.nullcontext():
            return function(args)
    return call


def effective_config(args):
    if hasattr(args, "lifecycle_overrides"):
        return lifecycle.load(getattr(args, "lifecycle_config", None), overrides=args.lifecycle_overrides)[0]
    return getattr(args, "config", dict(lifecycle.DEFAULTS))


def tmux_set_pane_options(pane: Pane, values: dict[str, str], *, unset: list[str] | None = None) -> None:
    for key in unset or []:
        run_tmux("set-option", "-p", "-u", "-t", pane.pane, f"@oc_{key}", check=True)
    for key, value in values.items():
        if value == "":
            continue
        run_tmux("set-option", "-p", "-t", pane.pane, f"@oc_{key}", value, check=True)


def isoformat(dt: datetime) -> str:
    return dt.replace(microsecond=0).isoformat().replace("+00:00", "Z")


def list_panes() -> list[Pane]:
    fmt = TMUX_FIELD_SEP.join(
        [
            "#{session_name}",
            "#{window_name}",
            "#{pane_id}",
            "#{pane_title}",
            "#{pane_current_command}",
            "#{pane_current_path}",
            "#{session_created}",
            "#{window_activity}",  # tmux has no pane_last_activity; window activity is conservative.
            "#{pane_dead}",
            "#{pane_dead_status}",
            "#{pane_pid}",
            *[f"#{{@oc_{field}}}" for field in OC_FIELDS],
            "#{session_id}",
            "#{window_linked}",
            "#{session_grouped}",
            *["#{" + field + "}" for field in OBS_FIELDS],
        ]
    )
    cp = run_tmux("list-panes", "-a", "-F", fmt, check=False)
    if cp.returncode != 0:
        if "failed to connect to server" in cp.stderr or "no server running" in cp.stderr:
            return []
        raise SystemExit(cp.stderr.strip() or "tmux list-panes failed")
    # One native process snapshot, not one subprocess per pane. This birth
    # time distinguishes an old session's completion metadata from a respawn.
    births: dict[str, str] = {}
    try:
        processes = subprocess.run(["ps", "-axo", "pid=,lstart="], text=True,
                                   capture_output=True, timeout=5, check=True,
                                   env={**os.environ, "LC_ALL": "C"})
        for row in processes.stdout.splitlines():
            fields = row.strip().split(maxsplit=1)
            if len(fields) == 2:
                try:
                    started = datetime.strptime(fields[1], "%a %b %d %H:%M:%S %Y").astimezone(timezone.utc)
                    births[fields[0]] = isoformat(started)
                except ValueError:
                    continue
    except (OSError, subprocess.SubprocessError):
        pass  # Unknown live birth fails closed below; dead panes remain eligible.
    ancestors = {str(os.getpid())}
    ancestry_ok = False
    try:
        cp_ps = subprocess.run(["ps", "-axo", "pid=,ppid="], text=True, capture_output=True, timeout=5, check=True)
        parents = dict(row.split() for row in cp_ps.stdout.splitlines() if len(row.split()) == 2)
        current = str(os.getpid())
        while current in parents and parents[current] not in ancestors:
            current = parents[current]
            ancestors.add(current)
        ancestry_ok = "1" in ancestors or "0" in ancestors
    except (OSError, subprocess.SubprocessError):
        pass
    panes: list[Pane] = []
    skipped = 0
    for line in cp.stdout.splitlines():
        fields = line.split(TMUX_FIELD_SEP)
        if len(fields) != 14 + len(OC_FIELDS) + len(OBS_FIELDS):
            skipped += 1
            continue
        meta = dict(zip(OC_FIELDS, fields[11:11 + len(OC_FIELDS)]))
        panes.append(
            Pane(
                session=fields[0],
                window=fields[1],
                pane=fields[2],
                title=fields[3],
                command=fields[4],
                path=fields[5],
                created=fields[6],
                last_activity=fields[7],
                dead=fields[8] == "1",
                dead_status=fields[9],
                pid=fields[10],
                process_started=births.get(fields[10], ""),
                server_session_id=fields[11 + len(OC_FIELDS)],
                window_linked=fields[12 + len(OC_FIELDS)],
                session_grouped=fields[13 + len(OC_FIELDS)],
                **dict(zip(("server_socket", "server_pid", "server_started", "session_windows", "window_panes",
                            "session_attached", "pane_in_mode", "pane_input_off", "pane_pipe", "pane_synchronized",
                            "remain_on_exit", "window_activity"), fields[-len(OBS_FIELDS):])),
                self_ancestor="1" if fields[10] in ancestors else ("0" if ancestry_ok else ""),
                meta=meta,
            )
        )
    if skipped:
        print(f"warning: skipped {skipped} malformed tmux pane row(s)", file=sys.stderr)
    return panes


def group_by_session(panes: list[Pane]) -> dict[str, list[Pane]]:
    grouped: dict[str, list[Pane]] = {}
    for pane in panes:
        grouped.setdefault(pane.session, []).append(pane)
    return grouped


def full_contract(pane: Pane) -> bool:
    meta = pane.meta
    return (
        meta.get("contract_version", "").strip() == "1"
        and meta.get("managed_by", "").strip() == "agent_wall"
        and bool(meta.get("kind", "").strip())
        and bool(meta.get("cleanup_policy", "").strip())
    )


def effective_state(pane: Pane) -> str:
    state = pane.meta.get("state", "").strip() or "unknown"
    if pane.dead:
        status = pane.dead_status.strip()
        if status and status != "0":
            return "failed"
    if pane.dead and state in {"starting", "running", "waiting", "blocked", "unknown"}:
        return "stale"
    return state


def resolve_evidence_path(pane: Pane) -> tuple[Path | None, str]:
    raw = pane.meta.get("evidence_path", "").strip()
    if not raw:
        return None, "evidence_path_empty"
    root_raw = pane.meta.get("run_root", "").strip()
    if not root_raw:
        return None, "evidence_without_run_root"
    root = Path(root_raw)
    if not root.is_absolute():
        return None, "relative_run_root"
    root = root.resolve()
    path = Path(raw).expanduser()
    if path.is_absolute():
        resolved = path.resolve()
    else:
        resolved = (root / path).resolve()
    try:
        resolved.relative_to(root)
    except ValueError:
        return None, "evidence_outside_run_root"
    return resolved, "ok"


def evidence_file_ok(path: Path) -> tuple[bool, str]:
    if not path.exists():
        return False, "evidence_missing"
    if not path.is_file():
        return False, "evidence_not_regular_file"
    if path.stat().st_size <= 0:
        return False, "evidence_empty"
    return True, "ok"


def resolve_adopted_codex_evidence(pane: Pane) -> tuple[Path | None, str]:
    raw = pane.path.strip()
    if not raw:
        return None, "adopted_run_root_empty"
    root = Path(raw).expanduser()
    if not root.is_absolute():
        return None, "adopted_run_root_relative"
    root = root.resolve()
    try:
        root.relative_to((STATE_ROOT / "runs").resolve())
    except ValueError:
        return None, "adopted_run_root_outside_state_runs"
    if not root.is_dir():
        return None, "adopted_run_root_not_directory"
    session_started_at = max(filter(None, (parse_iso(pane.process_started), epoch_datetime(pane.created))), default=None)
    if session_started_at is None:
        return None, "adopted_completed_age_unknown"
    stale_seen = False
    for name in ADOPTED_CODEX_EVIDENCE_FILENAMES:
        candidate = (root / name).resolve()
        if not candidate.is_relative_to(root):
            return None, "adopted_evidence_outside_run_root"
        ok, reason = evidence_file_ok(candidate)
        if ok:
            mtime = datetime.fromtimestamp(candidate.stat().st_mtime, timezone.utc)
            if mtime < session_started_at + timedelta_seconds(1):
                stale_seen = True
                continue
            return candidate, "ok"
        if reason not in {"evidence_missing", "evidence_empty"}:
            return None, "adopted_" + reason
    if stale_seen:
        return None, "adopted_evidence_stale"
    return None, "adopted_evidence_missing"


def pane_identity(panes: list[Pane]) -> str:
    """Deterministic identity of a *complete* pane set.

    Revalidation detects replacement of any pane in the planned set. Explicit
    selection does not permit live or multi-pane retirement at apply. Sorting
    keeps this identity independent of tmux listing order.
    """
    return "|".join(sorted(f"{pane.server_socket}:{pane.server_pid}:{pane.server_started}:{pane.session}:{pane.server_session_id}:{pane.pane}@{pane.created}:{pane.pid}:{pane.process_started}" for pane in panes))


def result(
    *,
    session: str,
    action: str,
    reason: str,
    panes: list[Pane],
    policy_source: str = "none",
    tmux_target: str | None = None,
) -> dict[str, Any]:
    primary = panes[0] if panes else None
    meta = primary.meta if primary else {}
    return {
        "session": session,
        "action": action,
        "reason": reason,
        "policy_source": policy_source,
        "tmux_target": tmux_target or "",
        "pane_id": primary.pane if primary else "",
        "pane_created": primary.created if primary else "",
        "pane_pid": primary.pid if primary else "",
        "pane_count": len(panes),
        "pane_identity": pane_identity(panes),
        "pane_contracts": [dict(p.meta) for p in panes],
        "state": effective_state(primary) if primary else "unknown",
        "dead": bool(primary.dead) if primary else False,
        "kind": meta.get("kind", ""),
        "cleanup_policy": meta.get("cleanup_policy", "") or "manual",
        "ttl": meta.get("ttl", "") or "never",
        "project": meta.get("project", ""),
        "goal": meta.get("goal", ""),
        "evidence_path": meta.get("evidence_path", ""),
        "progress_path": meta.get("progress_path", ""),
        "why_headless": meta.get("why_headless", ""),
        "end_reason": meta.get("end_reason", ""),
        "route_failure_reason": meta.get("route_failure_reason", ""),
    }


def archive_base_for(item: dict[str, Any], panes: list[Pane], args: argparse.Namespace) -> Path:
    if item.get("pre_exit_archive"):
        return Path(item["pre_exit_archive"]) / "final"
    primary = panes[0] if panes else None
    run_root_raw = primary.meta.get("run_root", "").strip() if primary and not item.get("adoption_identity") else ""
    if run_root_raw:
        run_root = Path(run_root_raw).expanduser()
        if run_root.is_absolute():
            return run_root.resolve() / ".tmux-cleanup"
    return Path(args.archive_root).expanduser().resolve()


def pane_archive_name(pane: Pane, index: int) -> str:
    pane_id = safe_slug(pane.pane.replace("%", "pane-"))
    window = safe_slug(pane.window)
    return f"{index:02d}-{window}-{pane_id}.log"


def evidence_snapshot(pane: Pane) -> dict[str, Any]:
    evidence, status = resolve_evidence_path(pane)
    out: dict[str, Any] = {"status": status}
    if status != "ok" or evidence is None:
        return out
    out["path"] = str(evidence)
    if evidence.exists() and evidence.is_file():
        stat = evidence.stat()
        out["size"] = stat.st_size
        out["mtime"] = datetime.fromtimestamp(stat.st_mtime, timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        out["sha256"] = sha256_file(evidence)
    return out


def capture_pane_text(pane: Pane, *, lines: int = 240) -> str:
    cp = run_tmux("capture-pane", "-pt", pane.pane, "-S", f"-{lines}", check=False)
    if cp.returncode != 0:
        return ""
    return cp.stdout


def session_age_seconds(pane: Pane, now: datetime) -> int | None:
    return epoch_age_seconds(pane.created, now)


def idle_age_seconds(pane: Pane, now: datetime) -> int | None:
    return epoch_age_seconds(pane.last_activity, now)


def epoch_age_seconds(raw_value: str, now: datetime) -> int | None:
    created = epoch_datetime(raw_value)
    if created is None:
        return None
    return int((now - created).total_seconds())


def epoch_datetime(raw_value: str) -> datetime | None:
    raw = raw_value.strip()
    if not raw:
        return None
    try:
        return datetime.fromtimestamp(int(raw), timezone.utc)
    except (ValueError, OSError, OverflowError):
        return None


def lifecycle_time(pane: Pane, now: datetime, *, include_mark: bool = True) -> tuple[datetime | None, str]:
    meta = pane.meta
    candidates: list[tuple[str, datetime | None]] = []
    if include_mark:
        candidates.append(("teardown_marked_at", parse_iso(meta.get("teardown_marked_at", ""))))
    candidates.extend(
        [
            ("completed_at", parse_iso(meta.get("completed_at", ""))),
            ("updated_at", parse_iso(meta.get("updated_at", ""))),
            ("started_at", parse_iso(meta.get("started_at", ""))),
            ("session_created", epoch_datetime(pane.created)),
        ]
    )
    for reason, value in candidates:
        if value is not None:
            return value, reason
    return None, "age_unknown"


def failed_age_time(pane: Pane) -> tuple[datetime | None, str]:
    meta = pane.meta
    for reason, value in (
        ("completed_at", parse_iso(meta.get("completed_at", ""))),
        ("updated_at", parse_iso(meta.get("updated_at", ""))),
        ("started_at", parse_iso(meta.get("started_at", ""))),
        ("session_created", epoch_datetime(pane.created)),
    ):
        if value is not None:
            return value, reason
    return None, "age_unknown"


def mark_item(
    session: str,
    panes: list[Pane],
    reason: str,
    now: datetime,
    *,
    kill_not_before: datetime | None = None,
    tail_hash: str = "",
) -> dict[str, Any]:
    item = result(session=session, action="mark", reason=reason, panes=panes, policy_source="managed")
    item["teardown_marked_at"] = isoformat(now)
    item["teardown_reason"] = reason
    item["janitor_state"] = "marked_for_teardown"
    item["kill_not_before"] = isoformat(kill_not_before or (now.replace(microsecond=0) + timedelta_seconds(TEARDOWN_GRACE_SECONDS)))
    # The mark-time normalized tail baseline. Cancellation must compare against
    # this, not a stale pre-completion observation, or a statically completed
    # pane cancels its own mark every cycle.
    if tail_hash:
        item["tail_hash"] = tail_hash
    return item


def cancel_mark_item(session: str, panes: list[Pane], reason: str, *, tail_hash: str = "") -> dict[str, Any]:
    item = result(session=session, action="cancel_mark", reason=reason, panes=panes, policy_source="managed")
    item["janitor_state"] = "active"
    if tail_hash:
        item["tail_hash"] = tail_hash
    return item


def timedelta_seconds(seconds: int):
    from datetime import timedelta

    return timedelta(seconds=seconds)


def previous_session_status(
    status_state: dict[str, Any] | None, session: str, pane: Pane | None = None
) -> dict[str, Any]:
    if not status_state:
        return {}
    sessions = status_state.get("sessions")
    if not isinstance(sessions, dict):
        return {}
    value = sessions.get(session)
    if not isinstance(value, dict):
        return {}
    if pane is not None:
        # A relaunched session reusing a name must not inherit the previous
        # pane's tail baseline or mark bookkeeping.
        recorded_created = str(value.get("pane_created") or "")
        recorded_pane_id = str(value.get("pane_id") or "")
        if str(value.get("pane_pid") or "") != pane.pid:
            return {}
        if recorded_created and recorded_created != pane.created:
            return {}
        if recorded_pane_id and recorded_pane_id != pane.pane:
            return {}
    return value


def active_idle_mark_due(pane: Pane, now: datetime, status_entry: dict[str, Any], tail_hash: str) -> tuple[bool, str]:
    if not tail_hash:
        return False, "tail_hash_empty"
    previous_hash = str(status_entry.get("tail_hash") or "")
    stable_since = parse_iso(str(status_entry.get("tail_hash_since") or ""))
    if previous_hash != tail_hash or stable_since is None:
        return False, "tail_hash_observation_started"
    if (now - stable_since).total_seconds() >= ACTIVE_IDLE_MARK_SECONDS:
        return True, "active_idle_without_meaningful_output"
    return False, "active_idle_grace_active"


def codex_completion_screen(text: str) -> bool:
    if screen_has_operator_prompt(text) or screen_has_active_marker(text):
        return False
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    tail = "\n".join(lines[-8:])
    lowered_tail = tail.lower()
    has_codex_marker = "openai codex" in text.lower() or "codex" in lowered_tail
    has_completion_marker = "goal achieved" in lowered_tail or "goal complete" in lowered_tail
    has_prompt_marker = any(line.startswith("›") for line in lines[-6:])
    return has_codex_marker and has_completion_marker and has_prompt_marker


def codex_idle_finished_screen(text: str) -> bool:
    return managed_tui_completion_screen(text)


def managed_tui_completion_time(pane: Pane, text: str | None = None) -> tuple[datetime | None, str]:
    meta = pane.meta
    if meta.get("managed_by", "").strip() != "agent_wall":
        return None, "not_agent_wall_managed"
    if not managed_tui_completion_screen(capture_pane_text(pane) if text is None else text):
        return None, "managed_tui_not_complete"
    evidence, evidence_status = resolve_evidence_path(pane)
    if evidence_status != "ok" or evidence is None:
        return None, evidence_status
    evidence_ok, evidence_reason = evidence_file_ok(evidence)
    if not evidence_ok:
        return None, evidence_reason
    session_started_at = max(filter(None, (parse_iso(pane.process_started), epoch_datetime(pane.created))), default=None)
    mtime = datetime.fromtimestamp(evidence.stat().st_mtime, timezone.utc)
    if session_started_at is not None and mtime < session_started_at + timedelta_seconds(1):
        return None, "evidence_stale"
    return mtime, "managed_tui_completed"


def managed_codex_tui_completion_time(pane: Pane) -> tuple[datetime | None, str]:
    return managed_tui_completion_time(pane)


def adopted_completed_codex(
    session: str,
    panes: list[Pane],
    *,
    policy: str,
    now: datetime,
    adopted_grace: int,
) -> dict[str, Any] | None:
    if len(panes) != 1 or policy != "kill-safe":
        return None
    pane = panes[0]
    meta = pane.meta
    if meta.get("contract_version", "").strip() != "display-only":
        return None
    if meta.get("managed_by", "").strip() != "tmux_inspector":
        return None
    if meta.get("kind", "").strip() != "detected-agent":
        return None
    if meta.get("cleanup_policy", "").strip() != "manual":
        return None
    if "codex" not in " ".join([meta.get("agent", ""), pane.command, pane.title, session]).lower():
        return None
    if hold_is_active(pane, now):
        return result(session=session, action="refuse", reason="hold_reason_active_adopted", panes=panes, policy_source="adopted_codex")
    age = session_age_seconds(pane, now)
    if age is None:
        return result(session=session, action="refuse", reason="adopted_completed_age_unknown", panes=panes, policy_source="adopted_codex")
    if age < adopted_grace:
        return result(session=session, action="skip", reason="adopted_completion_grace_active", panes=panes, policy_source="adopted_codex")
    idle_age = idle_age_seconds(pane, now)
    if idle_age is None:
        return result(session=session, action="refuse", reason="adopted_completed_idle_unknown", panes=panes, policy_source="adopted_codex")
    if idle_age < adopted_grace:
        return result(session=session, action="skip", reason="adopted_completion_idle_grace_active", panes=panes, policy_source="adopted_codex")
    evidence, evidence_status = resolve_adopted_codex_evidence(pane)
    if evidence_status != "ok" or evidence is None:
        return result(session=session, action="refuse", reason=evidence_status, panes=panes, policy_source="adopted_codex")
    if not codex_completion_screen(capture_pane_text(pane)):
        return result(session=session, action="skip", reason="adopted_codex_not_complete", panes=panes, policy_source="adopted_codex")
    item = result(
        session=session,
        action="kill",
        reason="adopted_codex_goal_achieved",
        panes=panes,
        policy_source="adopted_codex",
        tmux_target=exact_target(session),
    )
    item["evidence_path"] = str(evidence)
    return item


def archive_cleanup_at(item: dict[str, Any], panes: list[Pane], archive_base: Path, *, fallback_from: str = "") -> dict[str, Any]:
    if not panes:
        raise RuntimeError("archive_panes_missing")
    now = utc_now()
    stamp = now.strftime("%Y%m%dT%H%M%S%fZ")
    archive_dir = archive_base / f"{stamp}-{safe_slug(item['session'])}"
    archive_dir.mkdir(parents=True, mode=0o700, exist_ok=False)
    captures: list[dict[str, Any]] = []
    for index, pane in enumerate(panes, start=1):
        cp = run_tmux("capture-pane", "-p", "-J", "-S", "-", "-t", pane.pane, check=False)
        if cp.returncode != 0:
            raise RuntimeError("archive_capture_failed:" + (cp.stderr.strip() or pane.pane))
        capture_name = pane_archive_name(pane, index)
        capture_path = archive_dir / capture_name
        capture_path.write_text(redact_text(cp.stdout), encoding="utf-8")
        captures.append(
            {
                "pane": pane.pane,
                "window": pane.window,
                "path": str(capture_path),
                "redaction": "best_effort",
            }
        )
    primary = panes[0]
    metadata = {
        "archived_at": now.replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "item": item,
        "panes": [asdict(pane) for pane in panes],
        "captures": captures,
        "evidence": evidence_snapshot(primary),
        "archive_fallback_from": fallback_from,
    }
    metadata_path = archive_dir / "metadata.json"
    metadata_path.write_text(json.dumps(metadata, indent=2, sort_keys=True), encoding="utf-8")
    return {
        "archive_path": str(archive_dir),
        "metadata_path": str(metadata_path),
        "capture_count": len(captures),
        "redaction": "best_effort",
        "archive_fallback_from": fallback_from,
    }


def archive_cleanup(item: dict[str, Any], panes: list[Pane], args: argparse.Namespace) -> dict[str, Any]:
    preferred = archive_base_for(item, panes, args)
    central = Path(args.archive_root).expanduser().resolve()
    try:
        return archive_cleanup_at(item, panes, preferred)
    except Exception as exc:
        if preferred == central:
            raise
        fallback = archive_cleanup_at(item, panes, central, fallback_from=f"{preferred}: {exc}")
        fallback["archive_fallback"] = True
        return fallback


def append_jsonl(path: Path, obj: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(obj, sort_keys=True) + "\n")
        handle.flush()
        os.fsync(handle.fileno())


def ledger_paths(item: dict[str, Any], panes: list[Pane], args: argparse.Namespace) -> list[Path]:
    paths = [Path(args.archive_root).expanduser().resolve() / "cleanup.jsonl"]
    if panes and not item.get("adoption_identity"):
        run_root_raw = panes[0].meta.get("run_root", "").strip()
        if run_root_raw:
            run_root = Path(run_root_raw).expanduser()
            if run_root.is_absolute():
                paths.append(run_root.resolve() / ".tmux-cleanup" / "cleanup.jsonl")
    unique: list[Path] = []
    seen: set[Path] = set()
    for path in paths:
        if path not in seen:
            unique.append(path)
            seen.add(path)
    return unique


def log_event(item: dict, args: argparse.Namespace, event: str, *, result: str = "", archive_path: str = "") -> None:
    contracts = item.get("pane_contracts") or [{}]
    identity = contracts[0].get("launch_id") or item.get("adoption_identity") or item.get("pane_identity", "")
    event_log.append(event, session=item["session"], identity=identity,
                     source="manual" if item.get("policy_source") in {"manual", "operator_allow_session"} else "automatic",
                     result="".join(c if ord(c) >= 32 and ord(c) != 127 else " " for c in result),
                     reason="".join(c if ord(c) >= 32 and ord(c) != 127 else " "
                                    for c in redact_text(str(item.get("reason", "")))),
                     archive_path=archive_path, config=effective_config(args))


def write_ledger_event(
    item: dict[str, Any],
    panes: list[Pane],
    args: argparse.Namespace,
    *,
    event: str,
    archive: dict[str, Any] | None = None,
    kill_returncode: int | None = None,
    kill_stderr: str = "",
) -> None:
    obj = {
        "event": event,
        "time": utc_now().replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "session": item["session"],
        "action": item["action"],
        "reason": item["reason"],
        "policy_source": item["policy_source"],
        "tmux_target": item["tmux_target"],
        "kind": item["kind"],
        "cleanup_policy": item["cleanup_policy"],
        "state": item["state"],
        "dead": item["dead"],
        "archive": archive or {},
        "pre_exit_archive": item.get("pre_exit_archive", ""),
        "kill_returncode": kill_returncode,
        "kill_stderr": kill_stderr,
    }
    log_event(item, args, event,
              result="attempt" if kill_returncode is None else "success" if kill_returncode == 0 else "failed",
              archive_path=(archive or {}).get("archive_path", ""))
    errors: list[str] = []
    paths = ledger_paths(item, panes, args)
    for index, path in enumerate(paths):
        try:
            append_jsonl(path, obj)
        except OSError as exc:
            errors.append(f"{path}: {exc}")
            if index == 0:
                raise
    if errors:
        obj["ledger_write_warnings"] = errors


def eligible_managed(
    session: str,
    panes: list[Pane],
    *,
    policy: str,
    grace: int,
    now: datetime,
    status_state: dict[str, Any] | None = None,
    override_hold: bool = False,
    adopted_grace: int = 3600,
) -> dict[str, Any]:
    if len(panes) != 1:
        return result(session=session, action="refuse", reason="multi_pane_session_refused", panes=panes)

    pane = panes[0]
    text = ""
    if full_contract(pane) and pane.meta.get("cleanup_policy", "").strip() in {"manual", "hide", ""}:
        return result(session=session, action="skip", reason="policy_" + (pane.meta.get("cleanup_policy") or "manual"), panes=panes, policy_source="managed")
    # All automatic paths share this veto, including smoke and adopted jobs.
    # Capture failure/empty output cannot prove a live job is safe to retire.
    if not pane.dead:
        text = capture_pane_text(pane)
        protected = ("operator_prompt" if screen_has_operator_prompt(text)
                     else "active_work" if screen_has_active_marker(text)
                     else "capture_empty" if not text.strip() else "")
        if protected:
            if hold_is_active(pane, now) and not override_hold:
                item = result(session=session, action="refuse", reason="hold_reason_active", panes=panes, policy_source="managed")
                item["janitor_state"] = "protected"
                return item
            if pane.meta.get("teardown_marked_at", "").strip():
                reason = "operator_prompt_after_teardown_mark" if protected == "operator_prompt" else "meaningful_output_after_teardown_mark" if protected == "active_work" else "completion_not_proven"
                return cancel_mark_item(session, panes, reason, tail_hash=normalized_tail_hash(text))
            item = result(session=session, action="skip", reason="operator_prompt_active" if protected == "operator_prompt" else f"state_not_cleanable:{effective_state(pane)}", panes=panes, policy_source="managed")
            item["janitor_state"] = "operator_prompt" if protected == "operator_prompt" else "active"
            return item
    if not pane.dead:
        birth = parse_iso(pane.process_started)
        if birth is None:
            return result(session=session, action="refuse", reason="process_birth_unknown", panes=panes)
        # A retained done flag or teardown mark belongs to the previous process
        # unless completion is fresh for this incarnation. No TTL repairs that.
        completed = parse_iso(pane.meta.get("completed_at", ""))
        if completed is None and effective_state(pane) in {"failed", "blocked"}:
            completed, _ = failed_age_time(pane)
        if completed is None and pane.meta.get("cleanup_policy") == "smoke":
            return result(session=session, action="refuse", reason="completion_identity_missing", panes=panes)
        tui_completed, _ = managed_tui_completion_time(pane, text)
        if completed is not None and completed < birth + timedelta_seconds(1) and tui_completed is None:
            return result(session=session, action="refuse", reason="completion_predates_process", panes=panes)
        marked = parse_iso(pane.meta.get("teardown_marked_at", ""))
        if marked is not None and marked < birth + timedelta_seconds(1) and tui_completed is None:
            return cancel_mark_item(session, panes, "mark_predates_process")
    if not full_contract(pane):
        adopted = adopted_completed_codex(session, panes, policy=policy, now=now, adopted_grace=adopted_grace)
        if adopted is not None:
            return adopted
        return result(session=session, action="skip", reason="unmanaged_or_incomplete_contract", panes=panes)

    meta = pane.meta
    cleanup = meta.get("cleanup_policy", "").strip() or "manual"
    kind = meta.get("kind", "").strip()
    state = effective_state(pane)
    if meta.get("keep_open") == "1":
        return result(session=session, action="refuse", reason="explicit_keep_open", panes=panes, policy_source="managed")
    try:
        grace = int(meta.get("completed_retention_seconds") or grace)
        failed_retention = int(meta.get("failed_retention_seconds") or FAILED_VISIBLE_SECONDS)
        if grace < 0 or failed_retention < 0:
            raise ValueError("negative retention")
    except ValueError:
        return result(session=session, action="refuse", reason="invalid_job_retention", panes=panes, policy_source="managed")
    has_hold = hold_is_active(pane, now)

    if cleanup in {"manual", "hide", ""}:
        return result(session=session, action="skip", reason=f"policy_{cleanup or 'manual'}", panes=panes, policy_source="managed")

    ttl_seconds, ttl_status = parse_ttl(meta.get("ttl", "never"))
    if ttl_status == "invalid_ttl":
        return result(session=session, action="refuse", reason="invalid_ttl", panes=panes, policy_source="managed")

    if cleanup == "smoke":
        if has_hold and not override_hold:
            return result(session=session, action="refuse", reason="hold_reason_active", panes=panes, policy_source="managed")
        if policy not in {"kill-safe", "smoke"}:
            return result(session=session, action="skip", reason="policy_filter_excludes_smoke", panes=panes, policy_source="managed")
        if kind != "smoke":
            return result(session=session, action="refuse", reason="smoke_policy_without_smoke_kind", panes=panes, policy_source="managed")
        if not session.startswith(SMOKE_PREFIX):
            return result(session=session, action="refuse", reason="smoke_policy_without_smoke_prefix", panes=panes, policy_source="managed")
        if state not in STATE_CLEANABLE:
            return result(session=session, action="skip", reason=f"state_not_cleanable:{state}", panes=panes, policy_source="managed")
        return result(session=session, action="kill", reason="managed_smoke_complete", panes=panes, policy_source="managed", tmux_target=exact_target(session))

    if policy != "kill-safe":
        return result(session=session, action="skip", reason=f"policy_filter_excludes_{cleanup}", panes=panes, policy_source="managed")

    if cleanup not in {"kill_after_ttl", "kill_on_done"}:
        return result(session=session, action="refuse", reason=f"unknown_cleanup_policy:{cleanup}", panes=panes, policy_source="managed")

    def evidence_refusal() -> dict[str, Any] | None:
        evidence, evidence_status = resolve_evidence_path(pane)
        reason = ""
        if evidence_status != "ok" or evidence is None:
            reason = evidence_status
        else:
            evidence_ok, evidence_reason = evidence_file_ok(evidence)
            if not evidence_ok:
                reason = evidence_reason
        if not reason:
            return None
        # Evidence refusals are cleanup debt hygiene cannot safely act on;
        # publish that truthfully instead of a blank sidecar state.
        item = result(session=session, action="refuse", reason=reason, panes=panes, policy_source="managed")
        item["janitor_state"] = "cleanup_blocked"
        return item

    def evidence_warning(item: dict[str, Any]) -> dict[str, Any]:
        refusal = evidence_refusal()
        if refusal is not None:
            item["evidence_warning"] = refusal["reason"]
            item["janitor_state"] = "archiving_non_evidence"
        return item

    def hold_refusal() -> dict[str, Any] | None:
        """Single hold gate for every mark and kill decision on this pane.

        Every managed branch here shares one code (`hold_reason_active`) on
        purpose: an operator reading the sidecar needs to know the pane is
        protected, not which internal branch noticed. Other contexts add a
        suffix (`_adopted`, `_allow_session`, `_at_apply`), so the stable
        machine contract is the `hold_reason_active*` family, matched by
        prefix. A held pane also never publishes a countdown, because a
        `kill_not_before` that can never fire reads as pending cleanup.
        """
        if not has_hold or override_hold:
            return None
        item = result(session=session, action="refuse", reason="hold_reason_active", panes=panes, policy_source="managed")
        item["janitor_state"] = "protected"
        return item

    completed_at = parse_iso(meta.get("completed_at", ""))
    virtual_completion_reason = ""
    captured_text = text
    marked_at = parse_iso(meta.get("teardown_marked_at", ""))
    if marked_at is not None:
        # Migration rule (refuse-at-kill): a mark stamped before hold
        # hardening becomes inert while a hold is present; never kill it.
        if refusal := hold_refusal():
            return refusal
        captured_text = text
        tail_hash = normalized_tail_hash(captured_text)
        if screen_has_operator_prompt(captured_text):
            return cancel_mark_item(session, panes, "operator_prompt_after_teardown_mark", tail_hash=tail_hash)
        status_entry = previous_session_status(status_state, session, pane)
        previous_hash = str(status_entry.get("tail_hash") or "")
        has_completion_screen = managed_tui_completion_screen(captured_text)
        if (
            previous_hash
            and tail_hash
            and previous_hash != tail_hash
            and not has_completion_screen
        ):
            # Meaningful output means the screen no longer matches a known
            # completion/delivered-idle shape. A capture that still shows the
            # completion screen only proves the pre-mark baseline was stale;
            # it must not cancel the mark.
            return cancel_mark_item(session, panes, "meaningful_output_after_teardown_mark", tail_hash=tail_hash)
        marked_age = (now - marked_at).total_seconds()
        if marked_age < TEARDOWN_GRACE_SECONDS:
            item = result(session=session, action="skip", reason="teardown_grace_active", panes=panes, policy_source="managed")
            item["janitor_state"] = "marked_for_teardown"
            item["kill_not_before"] = isoformat(marked_at + timedelta_seconds(TEARDOWN_GRACE_SECONDS))
            return item
        if not previous_hash:
            if tail_hash and not has_completion_screen:
                return cancel_mark_item(session, panes, "meaningful_output_after_teardown_mark", tail_hash=tail_hash)
            if state not in STATE_CLEANABLE:
                if not tail_hash:
                    item = result(
                        session=session,
                        action="skip",
                        reason="teardown_baseline_missing",
                        panes=panes,
                        policy_source="managed",
                    )
                    item["janitor_state"] = "marked_for_teardown"
                    item["kill_not_before"] = isoformat(marked_at + timedelta_seconds(TEARDOWN_GRACE_SECONDS))
                    return item
                item = result(session=session, action="skip", reason="teardown_baseline_missing", panes=panes, policy_source="managed")
                item["janitor_state"] = "marked_for_teardown"
                item["kill_not_before"] = isoformat(marked_at + timedelta_seconds(TEARDOWN_GRACE_SECONDS))
                item["tail_hash"] = tail_hash
                return item
        elif state not in STATE_CLEANABLE and not tail_hash:
            item = result(session=session, action="skip", reason="teardown_capture_empty", panes=panes, policy_source="managed")
            item["janitor_state"] = "marked_for_teardown"
            item["kill_not_before"] = isoformat(marked_at + timedelta_seconds(TEARDOWN_GRACE_SECONDS))
            return item
        if state not in STATE_CLEANABLE and not has_completion_screen:
            return cancel_mark_item(session, panes, "completion_not_proven", tail_hash=tail_hash)
        item = result(
            session=session,
            action="kill",
            reason=meta.get("teardown_reason", "").strip() or "teardown_grace_expired",
            panes=panes,
            policy_source="managed",
            tmux_target=exact_target(session),
        )
        item["kill_not_before"] = isoformat(marked_at + timedelta_seconds(TEARDOWN_GRACE_SECONDS))
        return evidence_warning(item)

    if state not in STATE_CLEANABLE:
        captured_text = text
        tui_completed_at, tui_reason = managed_tui_completion_time(pane, captured_text)
        if tui_completed_at is None:
            if screen_has_operator_prompt(captured_text):
                if meta.get("teardown_marked_at", "").strip():
                    return cancel_mark_item(session, panes, "operator_prompt_after_teardown_mark")
                item = result(session=session, action="skip", reason="operator_prompt_active", panes=panes, policy_source="managed")
                item["janitor_state"] = "operator_prompt"
                return item
            status_entry = previous_session_status(status_state, session, pane)
            tail_hash = normalized_tail_hash(captured_text)
            _, idle_reason = active_idle_mark_due(pane, now, status_entry, tail_hash)
            if refusal := hold_refusal():
                refusal["tail_hash"] = tail_hash
                refusal["tail_hash_reason"] = idle_reason
                return refusal
            # Stable output is useful display information, never completion
            # evidence. Unknown quiet jobs remain running until positively done.
            item = result(session=session, action="skip", reason=f"state_not_cleanable:{state}", panes=panes, policy_source="managed")
            item["janitor_state"] = "active"
            item["tail_hash"] = tail_hash
            item["tail_hash_reason"] = idle_reason
            return item
        state = "done"
        completed_at = tui_completed_at
        virtual_completion_reason = tui_reason

    # Every remaining path — done, stale, failed, blocked, virtual completion,
    # kill_on_done, and TTL — ends in a kill decision. Gate all of them once.
    if refusal := hold_refusal():
        return refusal

    if completed_at is None:
        if state in {"failed", "blocked"}:
            completed_at, _ = failed_age_time(pane)
        elif pane.dead:
            completed_at = parse_iso(meta.get("updated_at", "")) or epoch_datetime(pane.created)
        if completed_at is None:
            return result(session=session, action="refuse", reason="completed_at_missing_or_invalid", panes=panes, policy_source="managed")

    age = (now - completed_at).total_seconds()
    if state in {"failed", "blocked"}:
        if age < failed_retention:
            item = result(session=session, action="skip", reason="failed_visible_grace_active", panes=panes, policy_source="managed")
            item["janitor_state"] = "cleanup_pending"
            item["kill_not_before"] = isoformat(completed_at + timedelta_seconds(failed_retention))
            return item
        return evidence_warning(result(
            session=session,
            action="kill",
            reason=virtual_completion_reason or "failed_visible_grace_expired",
            panes=panes,
            policy_source="managed",
            tmux_target=exact_target(session),
        ))

    if state in {"done", "stale"} or virtual_completion_reason:
        if age < grace:
            return result(session=session, action="skip", reason="completion_grace_active", panes=panes, policy_source="managed")
        return evidence_warning(result(
            session=session,
            action="kill",
            reason=virtual_completion_reason or "terminal_live_grace_expired",
            panes=panes,
            policy_source="managed",
            tmux_target=exact_target(session),
        ))

    if cleanup == "kill_on_done":
        if refusal := evidence_refusal():
            return refusal
        if age < grace:
            return result(session=session, action="skip", reason="completion_grace_active", panes=panes, policy_source="managed")
        return result(
            session=session,
            action="kill",
            reason=virtual_completion_reason or "managed_kill_on_done",
            panes=panes,
            policy_source="managed",
            tmux_target=exact_target(session),
        )

    if ttl_seconds is None:
        return result(session=session, action="skip", reason="ttl_never", panes=panes, policy_source="managed")
    if age < ttl_seconds:
        return result(session=session, action="skip", reason="ttl_active", panes=panes, policy_source="managed")
    if refusal := evidence_refusal():
        return refusal
    return result(
        session=session,
        action="kill",
        reason=virtual_completion_reason or "managed_ttl_expired",
        panes=panes,
        policy_source="managed",
        tmux_target=exact_target(session),
    )


def override_hold_allows(args: argparse.Namespace, session: str) -> bool:
    """True only for sessions the operator named with --allow-session.

    `--override-hold` is not a global amnesty: it applies to the exact names the
    operator typed and to nothing else, so one deliberately released session
    cannot take unrelated held sessions with it.
    """
    if not getattr(args, "override_hold", False):
        return False
    return session in set(getattr(args, "allow_session", None) or [])


def build_plan(args: argparse.Namespace) -> list[dict[str, Any]]:
    now = utc_now()
    config = effective_config(args)
    allowed = set(args.allow_session or [])
    status_state = load_status(getattr(args, "status_file", ""))
    out: list[dict[str, Any]] = []
    for session, panes in sorted(group_by_session(list_panes()).items()):
        try:
            if reason := adoption.protection(panes, config):
                item = result(session=session, action="refuse", reason=reason, panes=panes)
                record = adoption.record_for(status_state, panes[0])
                if record:
                    if not record.get("attempt"):
                        record["invalidated"] = reason
                    item.update(adoption_identity=adoption.identity(panes[0]), adoption_record=record)
                out.append(item)
                continue
            adopted = adoption.plan(sys.modules[__name__], panes, args, status_state, config, now)
            if adopted is not None and session not in allowed:
                if adopted.get("adoption_record"):
                    # Reserve observation capacity within this cycle too. This
                    # is local planning state; plain plan still writes nothing.
                    status_state.setdefault("adoptions", {})[adopted["adoption_identity"]] = adopted["adoption_record"]
                out.append(adopted)
                continue
            if session in allowed:
                has_hold = any(hold_is_active(p, now) for p in panes)
                if has_hold and not override_hold_allows(args, session):
                    out.append(
                        result(
                            session=session,
                            action="refuse",
                            reason="hold_reason_active_allow_session",
                            panes=panes,
                            policy_source="operator_allow_session",
                            tmux_target=exact_target(session),
                        )
                    )
                    continue
                out.append(
                    result(
                        session=session,
                        action="kill",
                        reason="exact_allow_session",
                        panes=panes,
                        policy_source="operator_allow_session",
                        tmux_target=exact_target(session),
                    )
                )
                continue
            out.append(
                eligible_managed(
                    session,
                    panes,
                    policy=args.policy,
                    grace=args.grace,
                    now=now,
                    status_state=status_state,
                    override_hold=override_hold_allows(args, session),
                    adopted_grace=getattr(args, "adopted_grace", 3600),
                )
            )
        except ValueError as exc:
            out.append(result(session=session, action="refuse", reason=str(exc), panes=panes))
    return out


def print_items(items: list[dict[str, Any]], *, as_json: bool) -> None:
    if as_json:
        print(json.dumps(items, indent=2, sort_keys=True))
        return
    for item in items:
        print(
            f"{item['action']:<6} {item['session']:<36} "
            f"{item['reason']:<36} {item['kind'] or 'unowned':<10} {item['cleanup_policy']}"
        )


@serialized_status
def cmd_plan(args: argparse.Namespace) -> int:
    plan = build_plan(args)
    if getattr(args, "write_status", False):
        write_status(getattr(args, "status_file", ""), status_payload(plan, args))
    print_items(plan, as_json=args.json)
    return 0


@serialized_status
def cmd_list(args: argparse.Namespace) -> int:
    plan = build_plan(args)
    if getattr(args, "write_status", False):
        write_status(getattr(args, "status_file", ""), status_payload(plan, args))
    print_items(plan, as_json=args.json)
    return 0


def record_status_changes(items: list[dict], args: argparse.Namespace) -> None:
    previous = load_status(getattr(args, "status_file", "")).get("sessions", {})
    for item in items:
        old = previous.get(item["session"], {})
        identity = item.get("pane_identity", "")
        same = old.get("logged_identity") == identity
        signatures = dict(old.get("logged_states", {})) if same else {}
        if not same:
            log_event(item, args, "discovered", result="observed")
        policy = getattr(args, "policy", "")
        signature = [item.get("action"), item.get("teardown_reason") or item.get("reason")]
        if signatures.get(policy) != signature:
            log_event(item, args, "cleanup_state", result=item.get("janitor_state") or item["action"])
        signatures[policy] = signature
        item.update(logged_identity=identity, logged_states=signatures)


def status_payload(items: list[dict[str, Any]], args: argparse.Namespace) -> dict[str, Any]:
    previous = load_status(getattr(args, "status_file", ""))
    previous_sessions = previous.get("sessions") if isinstance(previous.get("sessions"), dict) else {}
    now = utc_now()
    sessions: dict[str, Any] = {}
    records = dict(previous.get("adoptions", {}))
    for item in items:
        if item.get("adoption_identity"):
            if item.get("removed"):
                records.pop(item["adoption_identity"], None)
            elif item.get("adoption_record"):
                records[item["adoption_identity"]] = item["adoption_record"]
        session = str(item.get("session") or "")
        if not session:
            continue
        prior = previous_sessions.get(session) if isinstance(previous_sessions, dict) else {}
        if not isinstance(prior, dict):
            prior = {}
        prior_created = str(prior.get("pane_created") or "")
        prior_pane_id = str(prior.get("pane_id") or "")
        item_created = str(item.get("pane_created") or "")
        item_pane_id = str(item.get("pane_id") or "")
        if str(prior.get("pane_pid") or "") != str(item.get("pane_pid") or "") or (prior_created and item_created and prior_created != item_created) or (
            prior_pane_id and item_pane_id and prior_pane_id != item_pane_id
        ):
            # A relaunched session reusing a name must not inherit the old
            # pane's tail baseline through the sidecar.
            prior = {}
        tail_hash = str(item.get("tail_hash") or prior.get("tail_hash") or "")
        prior_hash = str(prior.get("tail_hash") or "")
        tail_hash_since = str(prior.get("tail_hash_since") or "")
        if tail_hash and tail_hash != prior_hash:
            tail_hash_since = isoformat(now)
        elif tail_hash and not tail_hash_since:
            tail_hash_since = isoformat(now)
        state = str(item.get("janitor_state") or "")
        if not state:
            if item.get("action") == "kill":
                state = "cleanup_pending"
            elif item.get("action") == "mark":
                state = "marked_for_teardown"
            elif item.get("action") == "refuse" and str(item.get("reason", "")).startswith("hold_reason_active"):
                state = "protected"
            elif item.get("action") == "refuse":
                state = refusal_state(str(item.get("reason", "")))
            elif item.get("action") == "skip":
                state = "active"
        sessions[session] = {
            "janitor_state": state,
            "marked_at": item.get("teardown_marked_at") or item.get("marked_at") or "",
            "reason": item.get("teardown_reason") or item.get("reason") or "",
            "kill_not_before": item.get("kill_not_before") or "",
            "pane_id": item_pane_id,
            "pane_created": item_created,
            "pane_pid": item.get("pane_pid", ""),
            "pane_identity": item.get("pane_identity", ""),
            "logged_identity": item.get("logged_identity", prior.get("logged_identity", "")),
            "logged_states": item.get("logged_states", prior.get("logged_states", {})),
            "existing_session": bool(item.get("adoption_identity")),
            "observations": item.get("observations", {}),
            "tail_hash": tail_hash,
            "tail_hash_since": tail_hash_since,
            "last_action": item.get("action") or "",
            "last_refusal": item.get("reason") if item.get("action") == "refuse" else "",
        }
    return {
        "status_version": STATUS_VERSION,
        "interval_s": getattr(args, "interval", 0),
        "policy": getattr(args, "policy", ""),
        "sessions": sessions,
        "adoptions": records,
        "last_cycle": {
            "policy": getattr(args, "policy", ""),
            "kill": sum(1 for item in items if item.get("action") == "kill"),
            "request_exit": sum(1 for item in items if item.get("action") == "request_exit"),
            "mark": sum(1 for item in items if item.get("action") == "mark"),
            "cancel_mark": sum(1 for item in items if item.get("action") == "cancel_mark"),
            "skip": sum(1 for item in items if item.get("action") == "skip"),
            "refuse": sum(1 for item in items if item.get("action") == "refuse"),
        },
    }


def hold_is_active(pane: Pane, now: datetime) -> bool:
    # Expiry removes only the review hold. Completion and ownership still have
    # their own checks; a missing/malformed old deadline remains protected.
    return holds.review_active(pane.meta, now)


def revalidate_target(item: dict[str, Any], *, allow_hold: bool, args: argparse.Namespace | None = None) -> tuple[list[Pane], str]:
    """Re-read live tmux state for one planned session immediately before acting.

    Planning and applying are separated in time. A hold added, or a pane
    replaced, inside that window must stop the action before any mark, archive,
    ledger kill attempt, or kill happens — not be discovered afterwards, when
    the ledger already claims cleanup was attempted on a pane that no longer
    exists or is now protected.
    """
    session = str(item.get("session") or "")
    panes = group_by_session(list_panes()).get(session, [])
    if not panes:
        return [], "panes_missing_at_apply"
    # The whole planned pane set must still be present, unchanged, and nothing
    # else: an equal count with an unchanged primary pane is not proof that the
    # session is still the one that was planned.
    planned_identity = str(item.get("pane_identity") or "")
    if planned_identity != pane_identity(panes):
        return panes, "pane_identity_changed_at_apply"
    # The set fingerprint is order-independent, but mark and archive act on the
    # primary pane, so its position has to be stable too.
    primary = panes[0]
    if str(item.get("pane_id") or "") != primary.pane:
        return panes, "pane_identity_changed_at_apply"
    if str(item.get("pane_created") or "") != primary.created:
        return panes, "pane_identity_changed_at_apply"
    if args is not None:
        try:
            config = effective_config(args)
        except ValueError as error:
            return panes, "policy_reload_failed:" + str(error)
        if reason := adoption.protection(panes, config, adoption=bool(item.get("adoption_identity")),
                                        record=item.get("adoption_record")):
            return panes, reason
        if item.get("adoption_identity"):
            if reason := adoption.topology(panes):
                return panes, reason
            fresh = adoption.plan(sys.modules[__name__], panes, args, load_status(args.status_file), config, utc_now())
            if fresh is None or fresh["action"] != item["action"] or fresh["reason"] != item["reason"]:
                return panes, "adoption_eligibility_changed_at_apply"
    if any(p.meta.get("keep_open") == "1" for p in panes):
        return panes, "explicit_keep_open_at_apply"
    if not allow_hold and any(hold_is_active(p, utc_now()) for p in panes):
        return panes, "hold_reason_active_at_apply"
    if item.get("action") != "cancel_mark":
        if item.get("pane_contracts") != [dict(p.meta) for p in panes]:
            return panes, "pane_contract_changed_at_apply"
        if args is not None and item.get("policy_source") != "operator_allow_session" and not item.get("adoption_identity"):
            fresh = eligible_managed(session, panes, policy=args.policy, grace=args.grace,
                                     now=utc_now(), status_state=load_status(getattr(args, "status_file", "")),
                                     override_hold=allow_hold, adopted_grace=getattr(args, "adopted_grace", 3600))
            if fresh["action"] != item["action"] or fresh["reason"] != item["reason"]:
                return panes, "eligibility_changed_at_apply:" + fresh["reason"]
    return panes, "ok"


def refusal_state(reason: str) -> str:
    if reason.startswith("hold_reason_active"):
        return "protected"
    if reason in {"explicit_keep_open", "explicit_keep_open_at_apply", "live_session_requires_explicit_retirement"}:
        return "retained_until_exit"
    return "cleanup_blocked"


def apply_refusal(item: dict[str, Any], reason: str) -> dict[str, Any]:
    applied = dict(item)
    record = dict(item.get("adoption_record", {}))
    if record.get("release_id") and not record.get("attempt") and reason not in {"action_cap_deferred", "recovery_lock_busy"}:
        record["invalidated"] = reason
        record.pop("quiet_since", None)
        applied["adoption_record"] = record
    applied["action"] = "refuse"
    applied["reason"] = reason
    applied["janitor_state"] = "shutdown_unconfirmed" if record.get("attempt") else refusal_state(reason)
    return applied


def dead_retirement_expectations(pane: Pane) -> dict[str, str]:
    return {**({"socket_path": pane.server_socket, "pid": pane.server_pid, "start_time": pane.server_started,
                "session_attached": pane.session_attached, "pane_in_mode": pane.pane_in_mode} if pane.server_socket else {}),
            "session_id": pane.server_session_id, "session_name": pane.session,
            "pane_id": pane.pane, "pane_pid": pane.pid,
            "session_created": pane.created, "pane_dead": "1",
            "session_windows": "1", "window_panes": "1", "window_linked": "0",
            "session_grouped": "0",
            **{"@oc_" + key: value for key, value in pane.meta.items()}}


def dead_retirement_refusal(panes: list[Pane]) -> str:
    if len(panes) != 1:
        return "multi_pane_requires_explicit_retirement"
    pane = panes[0]
    if not re.fullmatch(r"\$[0-9]+", pane.server_session_id) or not re.fullmatch(r"%[0-9]+", pane.pane):
        return "server_identity_unknown"
    # Older tmux can report window_linked=0 for grouped-session windows.
    # Unknown grouping is not evidence of a standalone session either.
    if pane.session_grouped != "0":
        return "grouped_or_unknown_session_requires_explicit_retirement"
    if pane.window_linked != "0":
        return "linked_or_unknown_window_requires_explicit_retirement"
    # Validate before archive I/O; literal escaping preserves every comparison,
    # including free text, without interpreting user values as tmux formats.
    try:
        format_guard.all_equal(dead_retirement_expectations(pane))
    except ValueError:
        return "unsafe_guard_literal"
    if not pane.dead:
        return "live_session_requires_explicit_retirement"
    return ""


def guarded_dead_retirement(pane: Pane) -> subprocess.CompletedProcess[str]:
    """Synchronous tmux guard bound to the exact server session AND pane.

    Bare pane IDs are ambiguous across linked/grouped sessions. Reject both
    topologies and use the immutable session ID for format context and kill target.
    Resolve the session's sole pane; its exact ID is checked in the predicate.
    Do not put a pane ID in the window slot of a compound tmux target.
    No shell job, asynchronous branch, or fuzzy session-name target is used.
    """
    if reason := dead_retirement_refusal([pane]):
        return subprocess.CompletedProcess([], 1, "", reason)
    condition = format_guard.all_equal(dead_retirement_expectations(pane))
    cp = run_tmux("if-shell", "-F", "-t", pane.server_session_id + ":", condition,
                  "kill-session -t " + shlex.quote(pane.server_session_id),
                  "display-message -p PR4_RETIREMENT_REFUSED", check=False)
    if "PR4_RETIREMENT_REFUSED" in cp.stdout:
        return subprocess.CompletedProcess(cp.args, 1, cp.stdout, "tmux_guard_changed")
    return cp


def guarded_native_exit(pane: Pane, profile: dict) -> subprocess.CompletedProcess[str]:
    expectations = dead_retirement_expectations(pane)
    expectations.update(pane_dead="0", session_attached="0", pane_in_mode="0",
                        pane_input_off="0", pane_pipe="0", **{"synchronize-panes": "0", "remain-on-exit": "on"},
                        pane_current_command=profile["command"])
    try:
        condition = format_guard.all_equal(expectations)
    except ValueError:
        return subprocess.CompletedProcess([], 1, "", "unsafe_guard_literal")
    cp = run_tmux("if-shell", "-F", "-t", pane.server_session_id + ":", condition,
                  shlex.join(["send-keys", "-t", pane.pane, *profile["keys"]]),
                  "display-message -p ADOPTION_EXIT_REFUSED", check=False)
    if "ADOPTION_EXIT_REFUSED" in cp.stdout:
        return subprocess.CompletedProcess(cp.args, 1, cp.stdout, "tmux_guard_changed")
    return cp


def prepare_enrollment(panes: list[Pane], record: dict, args: argparse.Namespace) -> tuple[list[Pane], dict]:
    """Capture prior output and prepare known runtime exit/logging in one call."""
    p = panes[0]
    # Validate native identity and prompt before disabling the old pane pipe.
    candidate = replace(p, pane_pipe="0", remain_on_exit="on")
    evidence, reason = adoption.observe(sys.modules[__name__], candidate, record)
    if reason:
        raise ValueError(reason)
    if getattr(args, "dry_run", False):
        return [candidate], evidence
    item = result(session=p.session, action="prepare", reason=args.reason, panes=panes, policy_source="manual")
    item["adoption_identity"] = adoption.identity(p)
    archive = archive_cleanup(item, panes, args)
    write_ledger_event(item, panes, args, event="enrollment_prepare_attempt", archive=archive)
    fresh = group_by_session(list_panes()).get(p.session, [])
    config = effective_config(args)
    if (pane_identity(fresh) != pane_identity(panes) or [x.meta for x in fresh] != [x.meta for x in panes]
            or adoption.protection(fresh, config, adoption=True, record=record) or adoption.topology(fresh)
            or config["live_retirement"] != "observed" or not adoption.selected(p.session, config)):
        raise ValueError("enrollment preparation target or policy changed")
    expected = dead_retirement_expectations(p)
    expected.update(pane_dead="0", pane_pipe=p.pane_pipe, **{"remain-on-exit": p.remain_on_exit})
    condition = format_guard.all_equal(expected)
    command = shlex.join(["set-option", "-w", "-t", p.pane, "remain-on-exit", "on"])
    command += " ; " + shlex.join(["pipe-pane", "-t", p.pane])
    cp = run_tmux("if-shell", "-F", "-t", p.server_session_id + ":", condition,
                  command, "display-message -p ENROLLMENT_PREPARE_REFUSED", check=False)
    if cp.returncode or "ENROLLMENT_PREPARE_REFUSED" in cp.stdout:
        raise ValueError("enrollment preparation guard failed")
    prepared = group_by_session(list_panes()).get(p.session, [])
    if pane_identity(prepared) != pane_identity(panes) or [x.meta for x in prepared] != [x.meta for x in panes]:
        raise ValueError("enrollment preparation identity changed")
    log_event(item, args, "enrollment_prepared", result="saved output; native exit retention ready", archive_path=archive["archive_path"])
    return prepared, evidence


@serialized_status
def cmd_release(args: argparse.Namespace) -> int:
    config = effective_config(args)
    if config["live_retirement"] != "observed":
        raise ValueError("owner release requires live_retirement=observed")
    if not args.reason.strip() or len(args.reason) > 2048 or any(ord(c) < 32 for c in args.reason):
        raise ValueError("release reason must be nonempty bounded text without controls")
    panes = [p for p in list_panes() if adoption.identity(p) == args.identity]
    if not panes:
        raise ValueError("exact_incarnation_not_found")
    p = panes[0]
    # Include the whole topology; don't filter away a sibling pane.
    panes = group_by_session(list_panes()).get(p.session, [])
    if not panes or adoption.identity(panes[0]) != args.identity:
        raise ValueError("exact_incarnation_changed")
    reason = adoption.protection(panes, config) or adoption.topology(panes)
    if reason or p.dead or not adoption.selected(p.session, config):
        raise ValueError(reason or "live_selected_incarnation_required")
    data, digest = adoption.result_bytes(args.result)
    record = {"release_id": uuid.uuid4().hex, "released_at": isoformat(utc_now()),
              "session": p.session,
              "reason": args.reason, "profile": args.profile, "result_path": args.result,
              "result_sha256": digest, "result_bytes": len(data), "attestation": adoption.RISK}
    if getattr(args, "attest_screen_sampling", False):
        record["screen_sampling"] = True
    args._enrollment_pane = p
    extra = adoption.native_runtimes.enrollment(adoption.PROFILES[args.profile], args, adoption.result_bytes)
    if record.keys() & extra.keys():
        raise ValueError("adapter_enrollment_overwrites_common_fields")
    record.update(extra)
    if reason := adoption.protection(panes, config, adoption=True, record=record):
        raise ValueError(reason)
    if getattr(args, "prepare", False):
        panes, evidence = prepare_enrollment(panes, record, args)
        p = panes[0]
    else:
        evidence, reason = adoption.observe(sys.modules[__name__], panes[0], record)
        if reason:
            raise ValueError(reason)
    record.update(baseline=evidence, last_observed=isoformat(utc_now()), quiet_since=isoformat(utc_now()))
    state = load_status(args.status_file)
    records = state.setdefault("adoptions", {})
    if args.identity not in records and len(records) >= adoption.MAX_RECORDS:
        raise ValueError("adoption_record_capacity_reached")
    prior = records.get(args.identity, {})
    if prior.get("attempt"):
        # A new explicit owner decision can rearm, but retain the previous
        # archive/attempt linkage as history in this same record.
        record["previous_attempt"] = prior["attempt"]
    fresh = group_by_session(list_panes()).get(p.session, [])
    if pane_identity(fresh) != pane_identity(panes) or [x.meta for x in fresh] != [x.meta for x in panes]:
        raise ValueError("release_identity_or_contract_changed")
    config = effective_config(args)
    if (adoption.protection(fresh, config, adoption=True, record=record) or adoption.topology(fresh)
            or not adoption.selected(p.session, config) or config["live_retirement"] != "observed"):
        raise ValueError("release_policy_changed")
    if getattr(args, "prepare", False) and getattr(args, "dry_run", False):
        fresh = [replace(fresh[0], pane_pipe="0", remain_on_exit="on")]
    corroboration, reason = adoption.observe(sys.modules[__name__], fresh[0], record)
    if reason or corroboration != evidence:
        raise ValueError(reason or "release_activity_changed")
    if not getattr(args, "dry_run", False):
        log_event(result(session=p.session, action="adopt", reason=args.reason, panes=panes, policy_source="manual"),
                  args, "adopted", result="registered")
        records[args.identity] = record
        write_status(args.status_file, state)
    print(json.dumps({"identity": args.identity, "dry_run": getattr(args, "dry_run", False), "release": record}, indent=2))
    return 0


@serialized_status
def cmd_revoke(args: argparse.Namespace) -> int:
    state = load_status(args.status_file)
    record = state.get("adoptions", {}).get(args.identity)
    if not record:
        raise ValueError("exact_release_not_found")
    forget = getattr(args, "forget", False)
    if forget and record.get("attempt"):
        raise ValueError("cannot_forget_consumed_exit_attempt")
    record["invalidated"] = "owner_revoked"
    record.pop("quiet_since", None)
    if forget:
        state["adoptions"].pop(args.identity)
    if not getattr(args, "dry_run", False):
        event_log.append("adoption_forgotten" if forget else "adoption_revoked",
                         session=record.get("session", "unknown-session"), identity=args.identity,
                         source="manual", result="attempt", reason="owner_revoked",
                         config=effective_config(args))
        write_status(args.status_file, state)
    print(json.dumps({"identity": args.identity, "revoked": True, "dry_run": getattr(args, "dry_run", False),
                      "forgotten": forget, "attempt_retained": bool(record.get("attempt"))}))
    return 0


def request_exit(item: dict, args: argparse.Namespace) -> dict:
    archive = {}
    record = {}
    try:
        panes, reason = revalidate_target(item, allow_hold=False, args=args)
        if reason != "ok":
            return apply_refusal(item, reason)
        p = panes[0]
        record = dict(item["adoption_record"])
        archive = archive_cleanup(item, panes, args)
        data, digest = adoption.result_bytes(record["result_path"])
        if digest != record["result_sha256"]:
            return apply_refusal(item, "release_result_changed")
        result_path = Path(archive["archive_path"]) / "released-result.bin"
        with result_path.open("xb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        # All pre-exit evidence, including captures/metadata, must be durable
        # before consumption. Any failure leaves the live pane untouched.
        for path in Path(archive["archive_path"]).iterdir():
            with path.open("rb") as stream:
                os.fsync(stream.fileno())
        directory = os.open(archive["archive_path"], os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
        write_ledger_event(item, panes, args, event="exit_request_prepared", archive=archive)
        panes, reason = revalidate_target(item, allow_hold=False, args=args)
        if reason != "ok":
            return apply_refusal(item, reason)
        evidence, reason = adoption.observe(sys.modules[__name__], panes[0], record)
        if reason or evidence != record["baseline"]:
            return apply_refusal(item, reason or "new_activity_invalidated_release")
        request_config = effective_config(args)
        guard_context = contextlib.nullcontext()
        if record.get("assignment_recovery"):
            try:
                from . import assignment_recovery
            except ImportError:
                import assignment_recovery
            guard_context = assignment_recovery.final_guard(record["assignment_recovery"])
        with guard_context:
            attempt = {"id": uuid.uuid4().hex, "consumed_at": isoformat(utc_now()),
                       "release_id": record["release_id"], **archive}
            state = load_status(args.status_file)
            current = state.get("adoptions", {}).get(item["adoption_identity"], {})
            if current.get("release_id") != record["release_id"] or current.get("attempt") or current.get("invalidated"):
                return apply_refusal(item, "release_authority_changed")
            record["attempt"] = attempt
            item["adoption_record"] = record
            state["adoptions"][item["adoption_identity"]] = record
            write_status(args.status_file, state)  # consumed BEFORE any native key
            # Policy/protection and runtime evidence are checked again after disk I/O.
            fresh = group_by_session(list_panes()).get(p.session, [])
            config = effective_config(args)
            reason = adoption.protection(fresh, config, adoption=True, record=record) or adoption.topology(fresh)
            if (reason or config != request_config or pane_identity(fresh) != pane_identity(panes) or [x.meta for x in fresh] != [x.meta for x in panes]
                    or config["live_retirement"] != "observed" or not adoption.selected(p.session, config)):
                return apply_refusal(item, reason or "consumed_request_revalidation_failed")
            evidence, reason = adoption.observe(sys.modules[__name__], fresh[0], record)
            if reason or evidence != record["baseline"]:
                return apply_refusal(item, reason or "consumed_request_activity_changed")
            cp = guarded_native_exit(fresh[0], adoption.PROFILES[record["profile"]])
        write_ledger_event(item, fresh, args, event="exit_request_result", archive=archive,
                           kill_returncode=cp.returncode, kill_stderr=cp.stderr.strip())
        if cp.returncode and cp.stderr == "tmux_guard_changed":
            # The synchronous false branch proves no key was sent. Preserve its
            # archive history, but do not pretend there was an ambiguous send.
            record["previous_attempt"] = record.pop("attempt")
            record["invalidated"] = "tmux_guard_changed"
            record.pop("quiet_since", None)
            item["adoption_record"] = record
            state["adoptions"][item["adoption_identity"]] = record
            write_status(args.status_file, state)
            return apply_refusal(item, "tmux_guard_changed")
        applied = dict(item)
        applied.update(archive, janitor_state="exit_requested" if cp.returncode == 0 else "shutdown_unconfirmed",
                       reason="native_exit_requested" if cp.returncode == 0 else "shutdown_unconfirmed:" + cp.stderr.strip())
        return applied
    except BlockingIOError as error:
        reason = "exit_request_failed:" + str(error) if record.get("attempt") else "recovery_lock_busy"
        refused = apply_refusal(item, reason)
        refused.update(archive)
        return refused
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        refused = apply_refusal(item, "exit_request_failed:" + str(error))
        refused.update(archive)
        return refused


@serialized_status
def cmd_apply(args: argparse.Namespace) -> int:
    plan = build_plan(args)
    kill_items = [item for item in plan if item["action"] in {"kill", "request_exit"}]
    allowed_kill_sessions = {item["session"] for item in kill_items[: args.max_kills]} if args.max_kills is not None else {item["session"] for item in kill_items}
    requested: list[dict[str, Any]] = []
    killed: list[dict[str, Any]] = []
    marked: list[dict[str, Any]] = []
    canceled: list[dict[str, Any]] = []
    skipped: list[dict[str, Any]] = []
    refused: list[dict[str, Any]] = []
    for item in plan:
        if item["action"] == "request_exit":
            if item["session"] not in allowed_kill_sessions:
                refused.append(apply_refusal(item, "action_cap_deferred"))
                continue
            applied = request_exit(item, args)
            (requested if applied["action"] == "request_exit" else refused).append(applied)
            continue
        if item["action"] == "mark":
            panes, status = revalidate_target(item, allow_hold=override_hold_allows(args, item["session"]), args=args)
            if status != "ok":
                refused.append(apply_refusal(item, "mark_panes_missing" if status == "panes_missing_at_apply" else status))
                continue
            pane = panes[0]
            marked_at = item.get("teardown_marked_at") or isoformat(utc_now())
            try:
                tmux_set_pane_options(
                    pane,
                    {
                        "teardown_marked_at": marked_at,
                        "teardown_reason": str(item.get("teardown_reason") or item.get("reason") or "marked_for_teardown"),
                        "janitor_state": "marked_for_teardown",
                        "updated_at": marked_at,
                    },
                )
            except (OSError, subprocess.SubprocessError) as exc:
                detail = getattr(exc, "stderr", "") or str(exc)
                refused.append(apply_refusal(item, "metadata_write_failed:" + str(detail).strip()[:512]))
                continue
            marked.append(item)
            continue
        if item["action"] == "cancel_mark":
            # Cancelling a mark only clears teardown bookkeeping, but it must
            # still land on the pane that was planned, not its replacement.
            panes, status = revalidate_target(item, allow_hold=True)
            if status != "ok":
                refused.append(apply_refusal(item, "cancel_mark_panes_missing" if status == "panes_missing_at_apply" else status))
                continue
            pane = panes[0]
            try:
                tmux_set_pane_options(
                    pane,
                    {
                        "janitor_state": "active",
                        "updated_at": isoformat(utc_now()),
                    },
                    unset=["teardown_marked_at", "teardown_reason", "last_meaningful_activity_at"],
                )
            except (OSError, subprocess.SubprocessError) as exc:
                detail = getattr(exc, "stderr", "") or str(exc)
                refused.append(apply_refusal(item, "metadata_write_failed:" + str(detail).strip()[:512]))
                continue
            canceled.append(item)
            continue
        if item["action"] != "kill":
            (refused if item["action"] == "refuse" else skipped).append(item)
            continue
        if item["session"] not in allowed_kill_sessions:
            capped = dict(item)
            capped["action"] = "refuse"
            capped["reason"] = f"kill_cap_deferred:{len(kill_items)}>{args.max_kills}"
            refused.append(capped)
            continue
        # Last checkpoint before anything irreversible: archive, ledger, kill.
        panes, status = revalidate_target(item, allow_hold=override_hold_allows(args, item["session"]), args=args)
        if status != "ok":
            refused.append(apply_refusal(item, status))
            continue
        if reason := dead_retirement_refusal(panes):
            refused.append(apply_refusal(item, reason))
            continue
        try:
            archive = archive_cleanup(item, panes, args)
        except Exception as exc:
            applied = dict(item)
            applied["action"] = "refuse"
            applied["reason"] = "archive_failed:" + str(exc)
            refused.append(applied)
            continue
        # Archiving can take time; a new hold, respawn or resumed work during it
        # must still prevent the kill. Preserve the archive as refusal evidence.
        panes, status = revalidate_target(item, allow_hold=override_hold_allows(args, item["session"]), args=args)
        if status != "ok":
            refused.append(apply_refusal(item, status))
            continue
        try:
            write_ledger_event(item, panes, args, event="kill_attempt", archive=archive)
        except OSError as exc:
            refused.append(apply_refusal(item, "ledger_failed:" + str(exc)))
            continue
        # No archive/ledger I/O may intervene after this final validation.
        panes, status = revalidate_target(item, allow_hold=override_hold_allows(args, item["session"]), args=args)
        if status != "ok":
            refused.append(apply_refusal(item, status))
            continue
        # A live CLI can resume without changing its PID or metadata. tmux
        # cannot atomically validate Python's screen/evidence predicate. Retain
        # it; only exited, single-pane sessions reach the server-side guard.
        if len(panes) != 1 or not panes[0].dead:
            refused.append(apply_refusal(item, "live_session_requires_explicit_retirement"))
            continue
        cp = guarded_dead_retirement(panes[0])
        if cp.returncode == 0:
            try:
                if any(p.server_session_id == panes[0].server_session_id for p in list_panes()):
                    cp = subprocess.CompletedProcess(cp.args, 1, cp.stdout, "closure_unconfirmed")
            except (OSError, ValueError, subprocess.SubprocessError):
                cp = subprocess.CompletedProcess(cp.args, 1, cp.stdout, "closure_verification_unavailable")
        applied = dict(item)
        applied.update(archive)
        write_ledger_event(
            item,
            panes,
            args,
            event="kill_result",
            archive=archive,
            kill_returncode=cp.returncode,
            kill_stderr=cp.stderr.strip(),
        )
        if cp.returncode == 0:
            applied["removed"] = True
            applied["janitor_state"] = "removed"
            killed.append(applied)
        else:
            applied["action"] = "refuse"
            applied["reason"] = "tmux_kill_failed:" + (cp.stderr.strip() or "unknown")
            refused.append(applied)
    result_obj = {"requested": requested, "killed": killed, "marked": marked, "cancelled": canceled, "skipped": skipped, "refused": refused}
    all_items = killed + requested + marked + canceled + skipped + refused
    record_status_changes(all_items, args)
    write_status(getattr(args, "status_file", ""), status_payload(all_items, args))
    if args.json:
        print(json.dumps(result_obj, indent=2, sort_keys=True))
    else:
        print(f"requested: {len(requested)}")
        print(f"killed: {len(killed)}")
        for item in killed:
            print(f"  {item['session']} ({item['reason']})")
        print(f"marked: {len(marked)}")
        print(f"cancelled: {len(canceled)}")
        print(f"skipped: {len(skipped)}")
        print(f"refused: {len(refused)}")
    return 0


@serialized_status
def cmd_manual_close(args: argparse.Namespace) -> int:
    return manual_close.run(sys.modules[__name__], args)


def add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--lifecycle-config", default=argparse.SUPPRESS)
    parser.add_argument("--socket-path", default="", help="Use exactly this tmux server socket; no cross-server discovery.")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--include-unowned", action="store_true", help="Compatibility flag; unowned sessions are included by default.")
    parser.add_argument("--policy", choices=["kill-safe", "smoke"], default="kill-safe")
    parser.add_argument("--grace", type=int, default=60)
    parser.add_argument("--adopted-grace", type=int, default=3600, help="Minimum session age before completed adopted Codex TUI cleanup.")
    parser.add_argument("--allow-session", action="append", default=[])
    parser.add_argument(
        "--override-hold",
        action="store_true",
        help=(
            "Allow cleanup of a held session. Valid only together with --allow-session, "
            "and applies to those exact names only."
        ),
    )
    parser.add_argument("--max-kills", type=int, default=10, help="Maximum removals and native exit requests per apply run. Use a larger value for explicit bulk cleanup.")
    parser.add_argument(
        "--archive-root",
        default=str(default_archive_root()),
        help="Central cleanup archive/ledger root for unmanaged or fallback cleanup evidence.",
    )
    parser.add_argument(
        "--status-file",
        default=str(DEFAULT_STATUS_FILE),
        help="Versioned janitor status JSON sidecar written atomically after apply, or plan/list with --write-status.",
    )
    parser.add_argument("--write-status", action="store_true", help="Observation-only cycle: write status/observations without terminal mutations.")
    parser.add_argument("--interval", type=int, default=0, help="Loop interval hint for status JSON consumers.")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Plan and apply safe tmux session hygiene.")
    add_common(parser)
    sub = parser.add_subparsers(dest="subcommand")

    list_p = sub.add_parser("list", help="Classify tmux sessions without changing them.")
    add_common(list_p)
    list_p.set_defaults(func=cmd_list)

    plan = sub.add_parser("plan", help="Dry-run cleanup plan.")
    add_common(plan)
    plan.set_defaults(func=cmd_plan)

    apply = sub.add_parser("apply", help="Apply only currently eligible cleanup actions.")
    add_common(apply)
    apply.set_defaults(func=cmd_apply)

    release = sub.add_parser("release-existing", help="Enroll an exact live incarnation; never clears holds or stamps success.")
    add_common(release)
    release.add_argument("--identity", required=True, help="Exact adoption_identity from plan JSON")
    release.add_argument("--result", required=True, help="Absolute saved result file, at most 16 MiB")
    release.add_argument("--reason", required=True)
    release.add_argument("--wrapper", default="", help="Exact legacy Bash wrapper file for the known wrapper profile")
    release.add_argument("--profile", choices=sorted(adoption.PROFILES), required=True)
    release.add_argument("--assignment-dir", help="Exact original assignment directory for supported obsolete-supervisor recovery")
    release.add_argument("--owner-reviewed-result", action="store_true", help="Attest the saved report was reviewed and accepted for recovery")
    release.add_argument("--allow-missing-receipt", action="store_true", help="Accept recovery without native receipt; original supervisor must record incomplete, not success")
    release.add_argument("--attest-no-background-work", action="store_true", required=True)
    release.add_argument("--attest-screen-sampling", action="store_true",
                         help="Accept sampled-screen quietness: identical redraws do not veto; changes between samples may be missed (required for AGY)")
    release.add_argument("--dry-run", action="store_true")
    release.add_argument("--prepare", action="store_true", help="Archive existing output and prepare exit retention/logging before enrollment")
    release.set_defaults(func=cmd_release)
    revoke = sub.add_parser("revoke-existing", help="Revoke exact enrollment while preserving any consumed attempt.")
    add_common(revoke)
    revoke.add_argument("--identity", required=True)
    revoke.add_argument("--dry-run", action="store_true")
    revoke.add_argument("--forget", action="store_true", help="Free an unused enrollment slot; refuses any consumed exit attempt")
    revoke.set_defaults(func=cmd_revoke)

    close = sub.add_parser("kill-session", help="Preview exact-session archive-and-close; manual backup only.")
    add_common(close)
    close.add_argument("--name", required=True)
    close.add_argument("--identity", default="", help="Exact identity returned by preview; required for --execute")
    close.add_argument("--reason", default="operator requested closure")
    close.add_argument("--execute", action="store_true")
    close.set_defaults(func=cmd_manual_close)

    # Subcommand defaults must not erase options explicitly given before it.
    for child in (list_p, plan, apply, release, revoke, close):
        for action in child._actions:
            if action.dest != "help":
                action.default = argparse.SUPPRESS
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    actual_argv = list(sys.argv[1:] if argv is None else argv)
    args = parser.parse_args(actual_argv)
    mapping = {"grace": "completed_retention_seconds", "adopted_grace": "adopted_grace_seconds", "max_kills": "max_kills", "archive_root": "archive_dir", "status_file": "status_file", "interval": "cleanup_interval_seconds"}
    overrides = {}
    for field, key in mapping.items():
        flag = "--" + field.replace("_", "-")
        if any(arg == flag or arg.startswith(flag + "=") for arg in actual_argv):
            overrides[key] = getattr(args, field)
    try:
        config, _ = lifecycle.load(getattr(args, "lifecycle_config", None), overrides=overrides)
    except ValueError as error:
        parser.error(str(error))
    args.lifecycle_overrides = overrides
    args.config = config
    global SOCKET_PATH
    SOCKET_PATH = args.socket_path
    if SOCKET_PATH and not Path(SOCKET_PATH).is_absolute():
        parser.error("--socket-path must be absolute")
    global STATE_ROOT, ACTIVE_IDLE_MARK_SECONDS, TEARDOWN_GRACE_SECONDS, FAILED_VISIBLE_SECONDS
    STATE_ROOT = Path(config["state_dir"])
    ACTIVE_IDLE_MARK_SECONDS = config["active_idle_mark_seconds"]
    TEARDOWN_GRACE_SECONDS = config["teardown_grace_seconds"]
    FAILED_VISIBLE_SECONDS = config["failed_retention_seconds"]
    for field, key in mapping.items():
        setattr(args, field, config[key])
    if args.subcommand is None:
        args.subcommand = "plan"
        args.func = cmd_plan
    if getattr(args, "grace", 0) < 0:
        parser.error("--grace must be >= 0")
    if getattr(args, "adopted_grace", 0) < 0:
        parser.error("--adopted-grace must be >= 0")
    if getattr(args, "max_kills", 0) is not None and args.max_kills < 1:
        parser.error("--max-kills must be >= 1")
    if getattr(args, "override_hold", False) and not getattr(args, "allow_session", None) and args.subcommand != "kill-session":
        # A blanket override would reap every held pane on the host. Overriding
        # a hold is only ever meant for named sessions the operator inspected.
        parser.error("--override-hold requires at least one explicit --allow-session <name>")
    try:
        return args.func(args)
    except (OSError, ValueError) as error:
        parser.exit(2, str(error) + "\n")


if __name__ == "__main__":
    raise SystemExit(main())
