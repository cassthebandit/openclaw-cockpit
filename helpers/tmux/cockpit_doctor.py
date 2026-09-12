#!/usr/bin/env python3
"""Health checks for the OpenClaw tmux cockpit."""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

HELPERS = Path(__file__).resolve().parents[1]
STATE_ROOT = Path(os.environ.get("OPENCLAW_COCKPIT_STATE_DIR", "~/.local/state/openclaw-cockpit")).expanduser()
DEFAULT_WALL_TARGET = "cockpit:dashboard.0"
DEFAULT_JANITOR_SESSION = "cockpit-hygiene"
DEFAULT_INSPECTOR_SESSION = "cockpit-inspector"
DEFAULT_HYGIENE_LOG = STATE_ROOT / "hygiene/janitor.log"
DEFAULT_HYGIENE_STATUS = STATE_ROOT / "hygiene/status.json"
DEFAULT_INSPECTOR_LOG = STATE_ROOT / "inspector/inspector.log"
DEFAULT_LEDGER_ROOT = STATE_ROOT / "cleanup-ledger"
DEFAULT_RUNTIME_SNAPSHOT_SCRIPT = HELPERS / "openclaw_runtime/cockpit_snapshot.py"
COCKPIT_COMMANDS = {"openclaw-cockpit", "openclaw-cockpi"}
# Printable across tmux versions; reject delimiter collisions by exact field count.
TMUX_FIELD_SEP = "|:oc:|"
SERVICE_SESSIONS: set[str] = set()
EDITOR_VIEWER_COMMANDS = {"vim", "nvim", "nano", "cat", "less", "tail", "head", "grep", "rg", "sed", "awk"}
STRONG_RUNTIME_TOKENS = {"codex", "claude", "claude-code", "fable", "opus", "agy", "antigravity"}
RUNTIME_INTERPRETERS = {"node", "nodejs", "bun", "deno"}


Runner = Callable[[list[str]], subprocess.CompletedProcess[str]]


@dataclass
class Check:
    name: str
    status: str
    detail: str = ""
    data: dict[str, Any] | None = None

    def as_dict(self) -> dict[str, Any]:
        out = {"name": self.name, "status": self.status, "detail": self.detail}
        if self.data:
            out["data"] = self.data
        return out


def run_command(args: list[str]) -> subprocess.CompletedProcess[str]:
    # Preserve Unicode metadata outside tmux under a C locale.
    if args and args[0] == "tmux":
        args = [args[0], "-u", *args[1:]]
    return subprocess.run(args, text=True, encoding="utf-8", stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def ok(name: str, detail: str = "", data: dict[str, Any] | None = None) -> Check:
    return Check(name=name, status="ok", detail=detail, data=data)


def warn(name: str, detail: str = "", data: dict[str, Any] | None = None) -> Check:
    return Check(name=name, status="warn", detail=detail, data=data)


def fail(name: str, detail: str = "", data: dict[str, Any] | None = None) -> Check:
    return Check(name=name, status="fail", detail=detail, data=data)


def default_binary() -> str:
    return os.environ.get("OPENCLAW_COCKPIT_BIN") or "openclaw-cockpit"


def default_cockpit_source() -> str:
    return os.environ.get("OPENCLAW_COCKPIT_SOURCE") or ""


def read_binary_build_identity(path: str, runner: Runner = run_command) -> tuple[dict[str, str] | None, str]:
    """Read VCS identity from a binary's embedded Go build metadata.

    Uses `go version -m <path>` — a non-executing parser — so identity facts
    come from the binary file itself, not from running it. This also covers
    pre-repair binaries that have no --build-info flag; --build-info is
    operator convenience, not the doctor's source of truth.
    """
    if not shutil.which("go"):
        return None, "go toolchain not available"
    cp = runner(["go", "version", "-m", path])
    if cp.returncode != 0:
        return None, cp.stderr.strip() or cp.stdout.strip() or "go version -m failed"
    identity: dict[str, str] = {}
    for line in cp.stdout.splitlines():
        parts = line.strip().split("\t")
        if len(parts) >= 2 and parts[0] == "build":
            key, _, value = parts[1].partition("=")
            if key in {"vcs.revision", "vcs.modified", "vcs.time"}:
                identity[key] = value
    return identity, ""


def check_binary_identity(binary: str, source: str, runner: Runner = run_command) -> Check:
    """Compare the selected binary's embedded identity with the Cockpit source.

    Dirty or mismatched identity is a source-parity failure (the binary must
    not be represented as the reviewed source), distinct from the generic
    binary-missing failure that check_binary reports.
    """
    name = "openclaw_cockpit_build_identity"
    path = shutil.which(binary) or (binary if Path(binary).exists() else None)
    if not path:
        return fail(name, f"{binary} not found on PATH; identity unverifiable")
    identity, err = read_binary_build_identity(path, runner)
    if identity is None:
        return warn(name, f"build identity unverified: {err}", {"path": path})
    revision = identity.get("vcs.revision", "")
    modified = identity.get("vcs.modified", "")
    data: dict[str, Any] = {"path": path, "vcs_revision": revision, "vcs_modified": modified}
    if not revision:
        return warn(name, "binary carries no embedded VCS identity; source parity unverified", data)
    if modified == "true":
        return fail(name, "binary was built from a dirty source tree; source parity cannot be relied on", data)
    if not source:
        return warn(name, "source repo not configured; parity unverified", data)
    source_path = Path(source).expanduser()
    if not source_path.exists():
        return warn(name, f"source repo unavailable at {source}; parity unverified", data)
    cp = runner(["git", "-C", str(source_path), "rev-parse", "HEAD"])
    if cp.returncode != 0:
        return warn(name, "source revision unavailable; parity unverified", data)
    expected = cp.stdout.strip()
    data["expected_revision"] = expected
    if revision != expected:
        return fail(
            name,
            f"binary revision {revision[:12]} does not match source HEAD {expected[:12]}; source parity broken",
            data,
        )
    return ok(name, f"binary matches source HEAD {expected[:12]} (clean build)", data)


def check_binary(binary: str, runner: Runner = run_command) -> Check:
    path = shutil.which(binary)
    if not path:
        return fail("openclaw_cockpit_binary", f"{binary} not found on PATH")
    cp = runner([path, "--help"])
    if cp.returncode != 0:
        return fail("openclaw_cockpit_binary", cp.stderr.strip() or f"{binary} --help failed", {"path": path})
    return ok("openclaw_cockpit_binary", path)


def tmux_display(target: str, fmt: str, runner: Runner = run_command) -> subprocess.CompletedProcess[str]:
    return runner(["tmux", "display-message", "-p", "-t", target, fmt])


def check_dashboard(target: str, runner: Runner = run_command) -> Check:
    cp = tmux_display(target, TMUX_FIELD_SEP.join(["#{pane_current_command}", "#{pane_dead}", "#{pane_pid}"]), runner)
    if cp.returncode != 0:
        return fail("dashboard_pane", cp.stderr.strip() or f"missing {target}")
    fields = cp.stdout.strip().split(TMUX_FIELD_SEP)
    if len(fields) != 3:
        return fail("dashboard_pane", f"malformed tmux response: {cp.stdout!r}")
    command, dead, pid = fields
    if dead == "1":
        return fail("dashboard_pane", f"{target} is dead", {"command": command, "pid": pid})
    command_name = Path(command).name
    if command_name not in COCKPIT_COMMANDS:
        return fail("dashboard_pane", f"{target} is running {command!r}, not OpenClaw Cockpit", {"pid": pid})
    return ok("dashboard_pane", f"{target} running", {"command": command, "pid": pid})


def check_dashboard_command(target: str, runner: Runner = run_command) -> Check:
    pane = tmux_display(target, "#{pane_pid}", runner)
    if pane.returncode != 0:
        return fail("dashboard_command", pane.stderr.strip() or "dashboard pid lookup failed")
    pid = pane.stdout.strip()
    if not pid:
        return fail("dashboard_command", "dashboard pid empty")
    cp = runner(["ps", "-p", pid, "-ww", "-o", "command="])
    if cp.returncode != 0:
        return fail("dashboard_command", cp.stderr.strip() or f"ps failed for pid {pid}")
    try:
        flags = effective_dashboard_flags(cp.stdout)
    except ValueError as exc:
        return warn("dashboard_command", str(exc))
    data = {"command": cp.stdout.strip(), "effectiveFlags": flags}
    if flags["control"]:
        return fail("dashboard_command", "dashboard enables interactive control", data)
    missing = [key for key in ("organize", "openclaw-runtime") if not flags[key]]
    if missing:
        return warn("dashboard_command", "dashboard effective flags disabled: " + ", ".join(missing), data)
    return ok("dashboard_command", "dashboard is effectively monitor-only and organized", data)


def effective_dashboard_flags(command: str) -> dict[str, bool]:
    """Go flag boolean semantics: last value wins; --control owns authority."""
    flags = {"monitor-only": True, "control": False, "organize": False, "openclaw-runtime": False}
    flags.update({key: False for key in ("version", "build-info", "dump", "preserve-colors", "trace-mouse", "dump-config")})
    takes_value = {"openclaw-runtime-script", "openclaw-runtime-limit", "openclaw-runtime-interval", "config", "interval", "fps", "cols", "capture-budget", "tmux", "janitor-status", "exclude-session", "debug-click"}
    tokens = shlex.split(command)
    index = 1
    while index < len(tokens):
        token = tokens[index]
        if token == "--" or not token.startswith("-"):
            break
        key, equal, value = token.lstrip("-").partition("=")
        if key in flags:
            if not equal:
                flags[key] = True
            elif value in {"1", "t", "T", "TRUE", "true", "True"}:
                flags[key] = True
            elif value in {"0", "f", "F", "FALSE", "false", "False"}:
                flags[key] = False
            else:
                raise ValueError("invalid boolean value for " + key)
        elif key in takes_value and not equal:
            index += 1
        elif key not in takes_value:
            raise ValueError("unknown dashboard flag: " + key)
        index += 1
    flags["monitor-only"] = not flags["control"]
    return flags


def check_janitor_session(session: str, runner: Runner = run_command) -> Check:
    cp = runner(["tmux", "has-session", "-t", "=" + session])
    if cp.returncode != 0:
        return fail("janitor_session", cp.stderr.strip() or f"missing {session}")
    pane = tmux_display(session + ":0.0", TMUX_FIELD_SEP.join(["#{pane_current_command}", "#{pane_dead}", "#{pane_pid}"]), runner)
    if pane.returncode != 0:
        return fail("janitor_session", pane.stderr.strip() or "janitor pane missing")
    fields = pane.stdout.strip().split(TMUX_FIELD_SEP)
    if len(fields) != 3:
        return fail("janitor_session", f"malformed tmux response: {pane.stdout!r}")
    command, dead, pid = fields
    if dead == "1":
        return fail("janitor_session", f"{session} pane is dead", {"command": command, "pid": pid})
    return ok("janitor_session", f"{session} running", {"command": command, "pid": pid})


def public_hygiene_command(argv: list[str]) -> bool:
    argv = list(argv)
    if argv and Path(argv[0]).name == "env":
        argv.pop(0)
        while argv and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*=.*", argv[0]):
            argv.pop(0)
    if not argv or not re.fullmatch(r"python(?:[0-9]+(?:\.[0-9]+)*)?", Path(argv[0]).name):
        return False
    if len(argv) > 2 and Path(argv[1]).name == "services.py":
        return argv[2] == "hygiene"
    if argv[1:4] == ["-m", "helpers.tmux.services", "hygiene"]:
        return True
    if len(argv) < 2 or Path(argv[1]).name != "cockpit_public.py":
        return False
    index = 2
    helper = None
    while index < len(argv):
        token = argv[index]
        option, separator, value = token.partition("=")
        if option not in {"--install-config", "--helper"}:
            break
        if not separator:
            index += 1
            if index >= len(argv) or argv[index].startswith("--"):
                return False
            value = argv[index]
        if not value:
            return False
        if option == "--helper":
            if helper is not None:
                return False
            helper = value
        index += 1
    return helper == "tmux.services" and argv[index:index + 1] == ["hygiene"]


def check_janitor_command(session: str, runner: Runner = run_command) -> Check:
    pane = tmux_display(session + ":0.0", "#{pane_pid}", runner)
    if pane.returncode != 0:
        return fail("janitor_command", pane.stderr.strip() or "janitor pid lookup failed")
    pid = pane.stdout.strip()
    if not pid:
        return fail("janitor_command", "janitor pid empty")
    cp = runner(["ps", "-p", pid, "-ww", "-o", "command="])
    if cp.returncode != 0:
        return fail("janitor_command", cp.stderr.strip() or f"ps failed for pid {pid}")
    text = cp.stdout
    try:
        argv = shlex.split(text)
    except ValueError:
        argv = []
    # The public foreground service owns both apply cycles. Recognize its
    # executable/module position, not a coincidental substring in an argument.
    if public_hygiene_command(argv):
        return ok("janitor_command", "public hygiene service owns smoke and kill-safe cycles")
    required = [
        "session_hygiene.py apply --policy smoke --json",
        "session_hygiene.py apply --policy kill-safe --json",
        "--status-file",
    ]
    missing = [needle for needle in required if needle not in text]
    if missing:
        return warn("janitor_command", "janitor command missing canonical apply cycle", {"missing": missing, "command": text.strip()})
    return ok("janitor_command", "janitor command includes smoke and kill-safe apply cycles")


def check_inspector_session(session: str, runner: Runner = run_command) -> Check:
    cp = runner(["tmux", "has-session", "-t", "=" + session])
    if cp.returncode != 0:
        return fail("inspector_session", cp.stderr.strip() or f"missing {session}")
    pane = tmux_display(session + ":0.0", TMUX_FIELD_SEP.join(["#{pane_current_command}", "#{pane_dead}", "#{pane_pid}"]), runner)
    if pane.returncode != 0:
        return fail("inspector_session", pane.stderr.strip() or "inspector pane missing")
    fields = pane.stdout.strip().split(TMUX_FIELD_SEP)
    if len(fields) != 3:
        return fail("inspector_session", f"malformed tmux response: {pane.stdout!r}")
    command, dead, pid = fields
    if dead == "1":
        return fail("inspector_session", f"{session} pane is dead", {"command": command, "pid": pid})
    return ok("inspector_session", f"{session} running", {"command": command, "pid": pid})


def check_recent_log(path: Path, max_age_seconds: int, *, name: str = "janitor_log") -> Check:
    if not path.exists():
        return warn(name, f"missing {path}")
    age = time.time() - path.stat().st_mtime
    if age > max_age_seconds:
        return warn(name, f"{path} is stale: {int(age)}s old", {"age_seconds": int(age)})
    return ok(name, f"{path} updated {int(age)}s ago", {"age_seconds": int(age)})


def check_hygiene_log_cycle(path: Path) -> Check:
    if not path.exists():
        return warn("hygiene_log_cycle", f"missing {path}")
    try:
        with path.open("rb") as handle:
            handle.seek(0, os.SEEK_END)
            size = handle.tell()
            handle.seek(max(0, size - 2_000_000))
            text = handle.read().decode("utf-8", errors="ignore")
    except OSError as exc:
        return warn("hygiene_log_cycle", f"cannot read {path}: {exc}")
    missing = [needle for needle in [" smoke ===", " kill-safe ==="] if needle not in text]
    if missing:
        return warn("hygiene_log_cycle", "recent janitor log missing expected cycle markers", {"missing": missing})
    return ok("hygiene_log_cycle", "recent janitor log includes smoke and kill-safe cycles")


def check_janitor_status_file(path: Path, max_age_seconds: int) -> Check:
    if not path.exists():
        return warn("janitor_status_file", f"missing {path}")
    age = time.time() - path.stat().st_mtime
    if age > max_age_seconds:
        return warn("janitor_status_file", f"{path} is stale: {int(age)}s old", {"age_seconds": int(age)})
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        return warn("janitor_status_file", f"cannot read status JSON: {exc}")
    if payload.get("status_version") != 1:
        return warn("janitor_status_file", f"unexpected status_version {payload.get('status_version')!r}")
    if not isinstance(payload.get("sessions"), dict):
        return warn("janitor_status_file", "status JSON missing sessions object")
    return ok("janitor_status_file", f"{path} updated {int(age)}s ago", {"age_seconds": int(age)})


def check_hygiene_plan(runner: Runner = run_command) -> Check:
    script = HELPERS / "tmux/session_hygiene.py"
    cp = runner([sys.executable, str(script), "plan", "--policy", "kill-safe", "--json"])
    if cp.returncode != 0:
        return fail("hygiene_plan", cp.stderr.strip() or "session_hygiene.py plan failed")
    try:
        items = json.loads(cp.stdout)
    except json.JSONDecodeError as exc:
        return fail("hygiene_plan", f"invalid JSON: {exc}")
    killable = [item["session"] for item in items if item.get("action") == "kill"]
    refused = [item["session"] for item in items if item.get("action") == "refuse"]
    attention = [
        item["session"]
        for item in items
        if item.get("action") == "skip"
        and item.get("reason") == "unmanaged_or_incomplete_contract"
        and item.get("kind") == "detected-work"
    ]
    data = {"killable": killable, "refused": refused, "attention": attention}
    detail = f"{len(killable)} killable, {len(refused)} refused, {len(attention)} attention"
    if killable or refused or attention:
        return warn("hygiene_plan", detail, data)
    return ok("hygiene_plan", detail, data)


def check_openclaw_runtime_snapshot(script: Path, runner: Runner = run_command) -> Check:
    if not script.exists():
        return warn("openclaw_runtime_snapshot", f"missing {script}")
    cp = runner([sys.executable, str(script), "--limit", "1"])
    if cp.returncode != 0:
        return warn("openclaw_runtime_snapshot", cp.stderr.strip() or cp.stdout.strip() or "runtime snapshot failed")
    try:
        payload = json.loads(cp.stdout)
    except json.JSONDecodeError as exc:
        return warn("openclaw_runtime_snapshot", f"invalid JSON: {exc}")
    summary = payload.get("summary") if isinstance(payload, dict) else None
    if not isinstance(summary, dict):
        return warn("openclaw_runtime_snapshot", "snapshot missing summary")
    by_state = summary.get("byState") if isinstance(summary.get("byState"), dict) else {}
    contract = str(payload.get("cardContract") or "")
    data = {
        "attention": int(by_state.get("attention") or 0),
        "active": int(by_state.get("active") or 0),
        "unknown": int(by_state.get("unknown") or 0),
        "total": int(summary.get("total") or 0),
        "cardContract": contract,
    }
    if contract != "runtime-card.v1":
        return warn("openclaw_runtime_snapshot", f"unexpected runtime card contract {contract or '<missing>'}", data)
    detail = f"{data['attention']} attention, {data['active']} active, {data['unknown']} unknown"
    if data["attention"]:
        return warn("openclaw_runtime_snapshot", detail, data)
    return ok("openclaw_runtime_snapshot", detail, data)


def command_basename(command: str) -> str:
    return Path(command.strip().split()[0]).name.lower() if command.strip() else ""


def tokenize_runtime_text(text: str) -> set[str]:
    lowered = text.lower()
    token = ""
    tokens: set[str] = set()
    for char in lowered:
        if char.isalnum() or char in {"_", "-"}:
            token += char
        else:
            if token:
                tokens.add(token)
                tokens.update(part for part in token.split("-") if part)
                tokens.update(part for part in token.split("_") if part)
                token = ""
    if token:
        tokens.add(token)
        tokens.update(part for part in token.split("-") if part)
        tokens.update(part for part in token.split("_") if part)
    return tokens


def read_process_table(runner: Runner = run_command) -> tuple[dict[str, str], dict[str, list[str]]]:
    cp = runner(["ps", "-ww", "-axo", "pid=,ppid=,command="])
    if cp.returncode != 0:
        return {}, {}
    commands: dict[str, str] = {}
    children: dict[str, list[str]] = {}
    for line in cp.stdout.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) < 3:
            continue
        pid, ppid, command = parts
        commands[pid] = command
        children.setdefault(ppid, []).append(pid)
    return commands, children


def process_tree_commands(
    root_pid: str,
    runner: Runner = run_command,
    process_table: tuple[dict[str, str], dict[str, list[str]]] | None = None,
) -> list[str]:
    if not root_pid:
        return []
    commands, children = process_table if process_table is not None else read_process_table(runner)
    out: list[str] = []
    stack = [root_pid]
    seen: set[str] = set()
    while stack:
        pid = stack.pop()
        if pid in seen:
            continue
        seen.add(pid)
        command = commands.get(pid)
        if command:
            out.append(command)
        stack.extend(children.get(pid, []))
    return out


def tmux_model_lane_rows(runner: Runner = run_command) -> tuple[list[dict[str, str]], list[str]]:
    fields = [
        "#{session_name}",
        "#{window_name}",
        "#{pane_id}",
        "#{pane_title}",
        "#{pane_current_command}",
        "#{pane_dead}",
        "#{pane_pid}",
        "#{@oc_contract_version}",
        "#{@oc_managed_by}",
        "#{@oc_kind}",
        "#{@oc_agent}",
        "#{@oc_cleanup_policy}",
        "#{@oc_evidence_path}",
        "#{@oc_why_headless}",
        "#{@oc_progress_path}",
    ]
    cp = runner(["tmux", "list-panes", "-a", "-F", TMUX_FIELD_SEP.join(fields)])
    if cp.returncode != 0:
        return [], [cp.stderr.strip() or "tmux list-panes failed"]
    rows: list[dict[str, str]] = []
    errors: list[str] = []
    for line in cp.stdout.splitlines():
        values = line.split(TMUX_FIELD_SEP)
        if len(values) != len(fields):
            errors.append(f"malformed tmux row: {line!r}")
            continue
        rows.append(
            {
                "session": values[0],
                "window": values[1],
                "pane": values[2],
                "title": values[3],
                "command": values[4],
                "dead": values[5],
                "pid": values[6],
                "contract_version": values[7],
                "managed_by": values[8],
                "kind": values[9],
                "agent": values[10],
                "cleanup_policy": values[11],
                "evidence_path": values[12],
                "why_headless": values[13],
                "progress_path": values[14],
            }
        )
    return rows, errors


def row_is_service_excluded(row: dict[str, str]) -> bool:
    if row.get("session") in SERVICE_SESSIONS:
        return True
    if row.get("kind") == "service":
        return True
    return False


def row_is_editor_viewer(row: dict[str, str]) -> bool:
    if command_basename(row.get("command", "")) in EDITOR_VIEWER_COMMANDS:
        return True
    return False


def command_mentions_runtime_executable(command: str) -> bool:
    if command_basename(command) in EDITOR_VIEWER_COMMANDS:
        return False
    try:
        parts = shlex.split(command)
    except ValueError:
        parts = command.split()
    if not parts:
        return False

    def part_mentions_runtime(part: str) -> bool:
        name = Path(part).name.lower()
        if name in STRONG_RUNTIME_TOKENS:
            return True
        if any(name.startswith(token + "-") for token in STRONG_RUNTIME_TOKENS):
            return True
        path_parts = {piece.lower() for piece in Path(part).parts}
        if "claude-code" in path_parts:
            return True
        return False

    if part_mentions_runtime(parts[0]):
        return True
    if Path(parts[0]).name.lower() not in RUNTIME_INTERPRETERS:
        return False
    for part in parts[1:]:
        if part_mentions_runtime(part):
            return True
    return False


def row_has_strong_runtime_indicator(
    row: dict[str, str],
    runner: Runner = run_command,
    *,
    include_names: bool,
    process_table: tuple[dict[str, str], dict[str, list[str]]] | None = None,
) -> bool:
    if command_mentions_runtime_executable(row.get("command", "")):
        return True
    if any(command_mentions_runtime_executable(command) for command in process_tree_commands(row.get("pid", ""), runner, process_table)):
        return True
    if not include_names:
        return False
    texts = [row.get("session", ""), row.get("window", ""), row.get("title", ""), row.get("agent", "")]
    tokens: set[str] = set()
    for text in texts:
        tokens.update(tokenize_runtime_text(text))
    if tokens & STRONG_RUNTIME_TOKENS:
        return True
    return False


def row_is_valid_visible_contract(row: dict[str, str]) -> bool:
    return (
        row.get("managed_by") == "agent_wall"
        and row.get("contract_version") == "1"
        and row.get("kind") == "visible-agent"
        and bool(row.get("agent"))
        and bool(row.get("cleanup_policy"))
        and bool(row.get("evidence_path"))
    )


def row_is_valid_managed_batch(row: dict[str, str]) -> bool:
    return (
        row.get("managed_by") == "agent_wall"
        and row.get("kind") == "batch-worker"
        and bool(row.get("why_headless"))
        and bool(row.get("progress_path"))
        and bool(row.get("evidence_path"))
        and bool(row.get("cleanup_policy"))
    )


def row_contract_warning_reason(
    row: dict[str, str],
    runner: Runner = run_command,
    process_table: tuple[dict[str, str], dict[str, list[str]]] | None = None,
) -> str:
    if row_is_service_excluded(row):
        return ""
    if row_is_valid_visible_contract(row) or row_is_valid_managed_batch(row):
        return ""
    managed_by = row.get("managed_by", "")
    kind = row.get("kind", "")
    has_runtime_process = row_has_strong_runtime_indicator(row, runner, include_names=False, process_table=process_table)
    if has_runtime_process and row.get("dead") == "1" and not row.get("cleanup_policy"):
        return "stale_model_lane_without_cleanup_contract"
    if has_runtime_process and managed_by == "agent_wall":
        return "managed_model_lane_with_incomplete_contract"
    if has_runtime_process and managed_by:
        return "unrecognized_manager_model_lane"
    if has_runtime_process and not managed_by:
        return "raw_model_lane_without_agent_wall_contract"
    if row_is_editor_viewer(row):
        return ""
    detected_shape = managed_by in {"tmux_inspector", "manual_adopt", "autodetect"} or kind in {"detected-agent", "detected-work"}
    if detected_shape and row_has_strong_runtime_indicator(row, runner, include_names=True, process_table=process_table):
        return "detected_model_lane_without_agent_wall_contract"
    return ""


def check_model_lane_contracts(runner: Runner = run_command) -> Check:
    rows, errors = tmux_model_lane_rows(runner)
    process_table = read_process_table(runner) if rows else ({}, {})
    warnings: list[dict[str, str]] = []
    for row in rows:
        reason = row_contract_warning_reason(row, runner, process_table)
        if not reason:
            continue
        warnings.append(
            {
                "session": row.get("session", ""),
                "pane": row.get("pane", ""),
                "command": row.get("command", ""),
                "window": row.get("window", ""),
                "title": row.get("title", ""),
                "managed_by": row.get("managed_by", ""),
                "kind": row.get("kind", ""),
                "agent": row.get("agent", ""),
                "reason": reason,
            }
        )
    data: dict[str, Any] = {}
    if warnings:
        data["warnings"] = warnings
    if errors:
        data["malformed"] = errors
    if warnings or errors:
        detail_parts = []
        if warnings:
            detail_parts.append(f"{len(warnings)} unmanaged model lane(s)")
        if errors:
            malformed_count = sum(1 for error in errors if error.startswith("malformed"))
            other_count = len(errors) - malformed_count
            if malformed_count:
                detail_parts.append(f"{malformed_count} malformed row(s)")
            if other_count:
                detail_parts.append(f"{other_count} scan error(s)")
        return warn("model_lane_contracts", ", ".join(detail_parts), data)
    return ok("model_lane_contracts", "no unmanaged model lanes")


def check_writable(path: Path, name: str) -> Check:
    try:
        path.mkdir(parents=True, exist_ok=True)
        with tempfile.NamedTemporaryFile(prefix=".doctor-", dir=path, delete=False) as handle:
            handle.write(b"ok\n")
            tmp = Path(handle.name)
        tmp.unlink()
    except OSError as exc:
        return fail(name, f"{path} not writable: {exc}")
    return ok(name, f"{path} writable")


def run_checks(args: argparse.Namespace, runner: Runner = run_command) -> list[Check]:
    return [
        check_binary(args.binary, runner),
        check_binary_identity(args.binary, args.source, runner),
        check_dashboard(args.wall_target, runner),
        check_dashboard_command(args.wall_target, runner),
        check_janitor_session(args.janitor_session, runner),
        check_janitor_command(args.janitor_session, runner),
        check_recent_log(Path(args.hygiene_log), args.max_log_age, name="janitor_log"),
        check_hygiene_log_cycle(Path(args.hygiene_log)),
        check_janitor_status_file(Path(args.hygiene_status), args.max_log_age),
        check_inspector_session(args.inspector_session, runner),
        check_recent_log(Path(args.inspector_log), args.max_inspector_log_age, name="inspector_log"),
        check_hygiene_plan(runner),
        check_model_lane_contracts(runner),
        check_openclaw_runtime_snapshot(Path(args.runtime_snapshot_script), runner),
        check_writable(Path(args.ledger_root), "ledger_root"),
    ]


def overall(checks: list[Check]) -> str:
    if any(check.status == "fail" for check in checks):
        return "fail"
    if any(check.status == "warn" for check in checks):
        return "warn"
    return "ok"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Check OpenClaw Cockpit health.")
    try:
        if __package__:
            from . import lifecycle
        else:
            import lifecycle
        policy, _ = lifecycle.load()
    except ValueError as error:
        parser.error(str(error))
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--binary", default=default_binary())
    parser.add_argument("--source", default=default_cockpit_source(), help="Cockpit source repo for build-identity parity")
    parser.add_argument("--wall-target", default=DEFAULT_WALL_TARGET)
    parser.add_argument("--janitor-session", default=DEFAULT_JANITOR_SESSION)
    parser.add_argument("--inspector-session", default=DEFAULT_INSPECTOR_SESSION)
    parser.add_argument("--hygiene-log", default=str(Path(policy["log_dir"]) / "janitor.log"))
    parser.add_argument("--hygiene-status", default=policy["status_file"])
    parser.add_argument("--inspector-log", default=str(Path(policy["inspector_log_dir"]) / "inspector.log"))
    parser.add_argument("--ledger-root", default=policy["archive_dir"])
    parser.add_argument("--runtime-snapshot-script", default=str(DEFAULT_RUNTIME_SNAPSHOT_SCRIPT))
    parser.add_argument("--max-log-age", type=int, default=180)
    parser.add_argument("--max-inspector-log-age", type=int, default=30)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    checks = run_checks(args)
    status = overall(checks)
    payload = {"status": status, "checks": [check.as_dict() for check in checks]}
    if args.json:
        print(json.dumps(payload, indent=2, sort_keys=True))
    else:
        print(f"status: {status}")
        for check in checks:
            print(f"{check.status:<4} {check.name:<20} {check.detail}")
    return 1 if status == "fail" else 0


if __name__ == "__main__":
    raise SystemExit(main())
