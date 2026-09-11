#!/usr/bin/env python3
"""Detect unmanaged tmux sessions and add display-only cockpit metadata."""

from __future__ import annotations

import argparse
import json
import re
import shlex
import subprocess
import sys
import time
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

# Printable across tmux versions; reject delimiter collisions by exact field count.
TMUX_FIELD_SEP = "|:oc:|"
INSPECTOR_MANAGED_BY = "tmux_inspector"
DISPLAY_CONTRACT_VERSION = "display-only"
SERVICE_CONTRACT_VERSION = "service-card.v1"

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
    "why_headless",
    "progress_path",
    "end_reason",
    "route_failure_reason",
]

PROTECTED_SESSIONS: set[str] = set()

AGENT_MARKERS = {
    "codex": "codex",
    "claude": "claude",
    "agy": "agy",
    "antigravity": "agy",
    "opencode": "opencode",
    "aider": "aider",
    "fable": "fable",
    "committee": "committee",
}

SERVICE_MARKERS = ("nginx", "redis-server", "postgres")
DASHBOARD_MARKERS = ("openclaw-cockpit", "dashboard")
VIEWER_MARKERS = ("vite", "localhost", "http://", "http.server", "-html")
SHELL_COMMANDS = {"zsh", "bash", "sh", "fish"}
VALID_TARGET_RE = re.compile(r"^%[0-9]+$")


@dataclass
class Pane:
    session: str
    window: str
    pane: str
    title: str
    command: str
    start_command: str
    path: str
    dead: bool
    dead_status: str
    last_activity: int
    meta: dict[str, str]
    pid: str = ""


@dataclass
class Classification:
    eligible: bool
    reason: str
    values: dict[str, str]


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def run_tmux(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    # Preserve Unicode metadata even when called outside tmux in a C locale.
    return subprocess.run(["tmux", "-u", *args], check=check, text=True, encoding="utf-8", stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def sanitize_tmux_option_value(value: object) -> str:
    return str(value).replace("\t", " ").replace("\n", " ").replace("\r", " ").replace("\0", "")


def list_panes() -> list[Pane]:
    fmt = TMUX_FIELD_SEP.join(
        [
            "#{session_name}",
            "#{window_name}",
            "#{pane_id}",
            "#{pane_title}",
            "#{pane_current_command}",
            "#{pane_start_command}",
            "#{pane_current_path}",
            "#{pane_dead}",
            "#{pane_dead_status}",
            "#{pane_last_activity}",
            "#{pane_pid}",
            *[f"#{{@oc_{field}}}" for field in OC_FIELDS],
        ]
    )
    cp = run_tmux("list-panes", "-a", "-F", fmt, check=False)
    if cp.returncode != 0:
        if "failed to connect to server" in cp.stderr or "no server running" in cp.stderr:
            return []
        raise SystemExit(cp.stderr.strip() or "tmux list-panes failed")
    panes: list[Pane] = []
    skipped = 0
    for line in cp.stdout.splitlines():
        fields = line.split(TMUX_FIELD_SEP)
        if len(fields) != 11 + len(OC_FIELDS):
            skipped += 1
            continue
        try:
            last_activity = int(fields[9] or "0")
        except ValueError:
            last_activity = 0
        panes.append(
            Pane(
                session=fields[0],
                window=fields[1],
                pane=fields[2],
                title=fields[3],
                command=fields[4],
                start_command=fields[5],
                path=fields[6],
                dead=fields[7] == "1",
                dead_status=fields[8],
                last_activity=last_activity,
                pid=fields[10],
                meta=dict(zip(OC_FIELDS, fields[11:])),
            )
        )
    if skipped:
        print(f"warning: skipped {skipped} malformed tmux pane row(s)", file=sys.stderr)
    return panes


def has_full_managed_contract(pane: Pane) -> bool:
    meta = pane.meta
    return (
        meta.get("contract_version", "").strip() == "1"
        and meta.get("managed_by", "").strip() == "agent_wall"
        and bool(meta.get("kind", "").strip())
        and bool(meta.get("cleanup_policy", "").strip())
    )


def is_manual_display_annotation(pane: Pane) -> bool:
    managed_by = pane.meta.get("managed_by", "").strip()
    contract = pane.meta.get("contract_version", "").strip()
    return contract == DISPLAY_CONTRACT_VERSION and managed_by not in {"", INSPECTOR_MANAGED_BY}


def search_text(pane: Pane) -> str:
    return " ".join([pane.session, pane.window, pane.title, pane.command, pane.start_command, pane.path]).lower()


def first_marker(text: str, markers: dict[str, str] | tuple[str, ...]) -> str:
    if isinstance(markers, dict):
        for marker, value in markers.items():
            if marker in text:
                return value
        return ""
    for marker in markers:
        if marker in text:
            return marker
    return ""


def infer_kind_agent_goal(pane: Pane) -> tuple[str, str, str, str]:
    text = search_text(pane)
    # Runtime identity comes from the current executable, never a folder/name.
    agent = AGENT_MARKERS.get(Path(pane.command.strip()).name.lower(), "")
    if agent:
        return "detected-agent", agent, infer_project(pane), f"Detected {agent} tmux session"
    service = first_marker(text, SERVICE_MARKERS)
    if service:
        return "detected-service", "", infer_project(pane), "Detected service tmux session"
    dashboard = first_marker(text, DASHBOARD_MARKERS)
    if dashboard:
        return "detected-dashboard", "", "openclaw-cockpit", "Detected dashboard tmux session"
    viewer = first_marker(text, VIEWER_MARKERS)
    if viewer:
        return "detected-viewer", "", infer_project(pane), "Detected viewer tmux session"
    command = pane.command.strip().lower()
    if command in SHELL_COMMANDS:
        return "detected-shell", "", infer_project(pane), "Detected shell tmux session"
    return "detected-work", "", infer_project(pane), "Detected unmanaged tmux session"


def infer_project(pane: Pane) -> str:
    parts = [part for part in Path(pane.path).parts if part]
    for marker in ("projects", "runs", "workspace"):
        if marker in parts:
            index = parts.index(marker)
            if index + 1 < len(parts):
                return parts[index + 1][:80]
    return ""


def infer_state(pane: Pane, stale_seconds: int, now_epoch: int) -> str:
    if pane.dead:
        return "done" if pane.dead_status.strip() in {"", "0"} else "failed"
    if stale_seconds > 0 and pane.last_activity > 0:
        age = now_epoch - pane.last_activity
        if age >= stale_seconds and pane.command.strip().lower() in SHELL_COMMANDS:
            return "stale"
    return "running"


def classify_pane(pane: Pane, *, stale_seconds: int, now: str, now_epoch: int, protected: set[str]) -> Classification:
    if pane.session in protected:
        return Classification(False, "protected_session", {})
    if has_full_managed_contract(pane):
        return Classification(False, "managed_contract", {})
    if is_manual_display_annotation(pane):
        return Classification(False, "manual_display_annotation", {})
    if ownership_claimed(pane.meta):
        return Classification(False, "ownership_claimed", {})
    if not VALID_TARGET_RE.match(pane.pane):
        return Classification(False, "unsupported_pane_target", {})

    kind, agent, project, goal = infer_kind_agent_goal(pane)
    state = infer_state(pane, stale_seconds=stale_seconds, now_epoch=now_epoch)
    contract_version = SERVICE_CONTRACT_VERSION if kind == "detected-service" else DISPLAY_CONTRACT_VERSION
    values = {
        "contract_version": contract_version,
        "managed_by": INSPECTOR_MANAGED_BY,
        "kind": kind,
        "agent": agent,
        "owner": "autodetect",
        "project": project,
        "goal": goal,
        "state": state,
        "ttl": "never",
        "cleanup_policy": "manual",
        "updated_at": now,
    }
    return Classification(True, "detected", values)


def current_display_values(pane: Pane, keys: set[str]) -> dict[str, str]:
    return {key: pane.meta.get(key, "").strip() for key in keys}


def stable_values(values: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in values.items() if key != "updated_at"}


OWNERSHIP_ALLOWED = {
    "managed_by": {"", INSPECTOR_MANAGED_BY},
    "contract_version": {"", DISPLAY_CONTRACT_VERSION, SERVICE_CONTRACT_VERSION},
    "cleanup_policy": {"", "manual"},
    "owner": {"", "autodetect"},
    "run_root": {""},
}


def ownership_claimed(meta: dict[str, str]) -> bool:
    return any(meta.get(key, "") not in allowed for key, allowed in OWNERSHIP_ALLOWED.items())


def set_pane_options(pane: str, values: dict[str, str], *, expected_pid: str = "") -> tuple[bool, str]:
    """Guard every actual server-side mutation, not a stale client read.

    A partial launcher claim in any ownership field blocks subsequent writes.
    if-shell -F runs synchronously in tmux's command queue (no shell child).
    """
    if not VALID_TARGET_RE.fullmatch(pane):
        return False, "unsupported_pane_target"
    checks = []
    for key, allowed in OWNERSHIP_ALLOWED.items():
        alternatives = ["#{==:#{@oc_" + key + "}," + value + "}" for value in sorted(allowed)]
        condition = alternatives[0]
        for alternative in alternatives[1:]:
            condition = "#{||:" + condition + "," + alternative + "}"
        checks.append(condition)
    if expected_pid:
        if not expected_pid.isdigit():
            return False, "unsupported_process_identity"
        checks.append("#{==:#{pane_pid}," + expected_pid + "}")
    condition = checks[0]
    for check in checks[1:]:
        condition = "#{&&:" + condition + "," + check + "}"
    for key, value in values.items():
        if not re.fullmatch(r"[a-z_]+", key):
            return False, "unsupported_metadata_key"
        value = sanitize_tmux_option_value(value)
        if len(value.encode()) > 4096 or any(ord(c) < 32 or ord(c) == 127 for c in value):
            return False, "unsupported_metadata_value"
    for key, value in values.items():
        command = shlex.join(["set-option", "-p", "-t", pane, "@oc_" + key, sanitize_tmux_option_value(value)])
        cp = run_tmux("if-shell", "-F", "-t", pane, condition, command,
                      "display-message -p COCKPIT_ANNOTATION_REFUSED", check=False)
        if cp.returncode or "COCKPIT_ANNOTATION_REFUSED" in cp.stdout:
            return False, "ownership_changed_or_write_failed:" + cp.stderr.strip()[:512]
    return True, "annotated"


def annotate_once(args: argparse.Namespace) -> list[dict[str, Any]]:
    now = utc_now()
    now_epoch = int(time.time())
    protected = set(PROTECTED_SESSIONS) | set(args.protect_session or [])
    events: list[dict[str, Any]] = []
    for pane in list_panes():
        classification = classify_pane(pane, stale_seconds=args.stale_seconds, now=now, now_epoch=now_epoch, protected=protected)
        event: dict[str, Any] = {
            "session": pane.session,
            "pane": pane.pane,
            "command": pane.command,
            "eligible": classification.eligible,
            "reason": classification.reason,
        }
        if classification.values:
            event["values"] = classification.values
        if classification.eligible:
            keys = set(classification.values)
            current = current_display_values(pane, keys)
            stable_changed = stable_values(current) != stable_values(classification.values)
            changed = stable_changed or not current.get("updated_at")
            event["changed"] = changed
            if changed and not args.dry_run:
                written, reason = set_pane_options(pane.pane, classification.values, expected_pid=pane.pid)
                event["applied"] = written
                event["reason"] = reason
                if not written:
                    event["changed"] = False
        events.append(event)
    return events


def cmd_scan(args: argparse.Namespace) -> int:
    now = utc_now()
    now_epoch = int(time.time())
    protected = set(PROTECTED_SESSIONS) | set(args.protect_session or [])
    out = []
    for pane in list_panes():
        classification = classify_pane(pane, stale_seconds=args.stale_seconds, now=now, now_epoch=now_epoch, protected=protected)
        item = asdict(pane)
        item["classification"] = asdict(classification)
        out.append(item)
    print(json.dumps(out, indent=2, sort_keys=True))
    return 0


def cmd_annotate(args: argparse.Namespace) -> int:
    events = annotate_once(args)
    print(json.dumps(events, indent=2, sort_keys=True))
    return 0


def cmd_watch(args: argparse.Namespace) -> int:
    if args.interval < 1:
        raise SystemExit("--interval must be >= 1")
    while True:
        events = annotate_once(args)
        changed = sum(1 for event in events if event.get("changed"))
        print(json.dumps({"time": utc_now(), "panes": len(events), "changed": changed}, sort_keys=True), flush=True)
        time.sleep(args.interval)


def add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--json", action="store_true", help="Reserved; output is JSON.")
    parser.add_argument("--stale-seconds", type=int, default=1800)
    parser.add_argument("--protect-session", action="append", default=[])


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Detect unmanaged tmux sessions for OpenClaw cockpit display.")
    sub = parser.add_subparsers(dest="command", required=True)

    scan = sub.add_parser("scan", help="Read-only scan and classification report.")
    add_common(scan)
    scan.set_defaults(func=cmd_scan)

    annotate = sub.add_parser("annotate", help="Add display-only metadata to eligible unmanaged panes once.")
    add_common(annotate)
    annotate.add_argument("--dry-run", action="store_true")
    annotate.set_defaults(func=cmd_annotate)

    watch = sub.add_parser("watch", help="Repeat display-only annotation.")
    add_common(watch)
    watch.add_argument("--dry-run", action="store_true")
    watch.add_argument("--interval", type=int, default=5)
    watch.set_defaults(func=cmd_watch)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
