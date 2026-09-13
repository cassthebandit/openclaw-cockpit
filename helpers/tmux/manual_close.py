"""Explicit exact-session archive-and-close backup; never an automatic fallback."""
from __future__ import annotations

import json
import os
import shlex
import subprocess
import time


def process_tree(pid: str) -> dict[str, str]:
    cp = subprocess.run(["ps", "-axo", "pid=,ppid=,lstart="], capture_output=True,
                        text=True, check=True, timeout=10)
    rows = {}
    for line in cp.stdout.splitlines():
        fields = line.split(None, 2)
        if len(fields) == 3 and fields[0].isdigit() and fields[1].isdigit():
            rows[fields[0]] = (fields[1], fields[2])
    selected = {pid}
    while True:
        expanded = selected | {p for p, (parent, _) in rows.items() if parent in selected}
        if expanded == selected:
            return {p: rows[p][1] for p in selected if p in rows}
        selected = expanded


def surviving_processes(original: dict[str, str]) -> list[str]:
    if not original:
        return []
    cp = subprocess.run(["ps", "-p", ",".join(sorted(original)), "-o", "pid=,lstart="],
                        capture_output=True, text=True, timeout=10)
    if cp.returncode not in {0, 1}:
        raise ValueError("process verification unavailable")
    return [fields[0] for line in cp.stdout.splitlines()
            if len(fields := line.split(None, 1)) == 2 and original.get(fields[0]) == fields[1]]


def inspect(h, args, *, expected=None):
    panes = h.group_by_session(h.list_panes()).get(args.name, [])
    if not panes:
        raise ValueError("exact session not found")
    p = panes[0]
    config = h.effective_config(args)
    if reason := h.adoption.protection(panes, config):
        raise ValueError(reason)
    if reason := h.adoption.topology(panes):
        raise ValueError(reason)
    if (h.holds.active(p.meta) and not getattr(args, "override_hold", False)):
        raise ValueError("session held; explicit --override-hold required")
    identity = h.adoption.identity(p)
    if getattr(args, "identity", "") and args.identity != identity:
        raise ValueError("session identity changed since preview")
    expectations = h.dead_retirement_expectations(p)
    expectations.update(pane_dead="1" if p.dead else "0", session_attached="0", pane_in_mode="0")
    if any(any(c in str(v) for c in "#,{}\n\r") for v in expectations.values()):
        raise ValueError("unsafe_guard_literal")
    if expected is not None and expectations != expected:
        raise ValueError("session identity, hold or topology changed")
    return panes, identity, expectations


def run(h, args):
    h.validate_session_name(args.name)
    panes, identity, expected = inspect(h, args)
    report = {"session": args.name, "identity": identity, "execute": bool(getattr(args, "execute", False)),
              "action": "archive-and-close", "removed": False}
    if not getattr(args, "execute", False):
        print(json.dumps(report, indent=2))
        return 0
    if not getattr(args, "identity", ""):
        raise ValueError("--execute requires --identity from a fresh preview")
    args.reason = getattr(args, "reason", "operator requested closure")
    if not args.reason.strip() or len(args.reason) > 2048 or any(ord(c) < 32 for c in args.reason):
        raise ValueError("--reason requires bounded nonempty text without controls")
    original = process_tree(panes[0].pid) if not panes[0].dead else {}
    item = h.result(session=args.name, action="kill", reason=args.reason, panes=panes,
                    policy_source="manual", tmux_target=panes[0].server_session_id)
    item["adoption_identity"] = identity
    archive = h.archive_cleanup(item, panes, args)
    # Persist captures and metadata before any terminal action.
    root = h.Path(archive["archive_path"])
    for path in root.iterdir():
        with path.open("rb") as stream:
            os.fsync(stream.fileno())
    fd = os.open(root, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    h.write_ledger_event(item, panes, args, event="manual_close_attempt", archive=archive)
    panes, _, expected = inspect(h, args, expected=expected)
    # No archive or log I/O after this reread. tmux itself compares immutable
    # identity, exact contract, attachment and topology in the action queue.
    checks = ["#{==:#{" + k + "}," + str(v) + "}" for k, v in expected.items()]
    condition = checks[0]
    for check in checks[1:]:
        condition = "#{&&:" + condition + "," + check + "}"
    cp = h.run_tmux("if-shell", "-F", "-t", panes[0].server_session_id + ":", condition,
                    "kill-session -t " + shlex.quote(panes[0].server_session_id),
                    "display-message -p MANUAL_CLOSE_REFUSED", check=False)
    refused = cp.returncode != 0 or "MANUAL_CLOSE_REFUSED" in cp.stdout
    remaining = h.list_panes()
    removed = not refused and not any(h.adoption.identity(p) == identity for p in remaining)
    survivors = surviving_processes(original)
    for _ in range(10):
        if not survivors:
            break
        time.sleep(.1)
        survivors = surviving_processes(original)
    report.update(archive, removed=removed, surviving_processes=survivors,
                  result="closed" if removed and not survivors else "incomplete")
    h.write_ledger_event(item, panes, args, event="manual_close_result", archive=archive,
                         kill_returncode=0 if removed and not survivors else 1,
                         kill_stderr="surviving processes: " + ",".join(survivors) if survivors else cp.stderr.strip())
    if removed and not survivors:
        state = h.load_status(args.status_file)
        if state.get("adoptions", {}).pop(identity, None) is not None:
            h.write_status(args.status_file, state)
    print(json.dumps(report, indent=2))
    return 0 if removed and not survivors else 1
