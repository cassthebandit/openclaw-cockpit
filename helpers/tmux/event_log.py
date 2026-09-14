"""Small durable session history. Transcripts and cleanup archives live elsewhere."""
from __future__ import annotations

import argparse
import fcntl
import sys
import json
import os
import tempfile
import re
import uuid
from collections import deque
from datetime import datetime, timezone
from pathlib import Path

try:
    from . import lifecycle
except ImportError:
    import lifecycle

MAX_BYTES = 10_000_000
MAX_RECORD_BYTES = 8192


def append(event: str, *, session: str, identity: str, source: str = "automatic",
           result: str = "", reason: str = "", archive_path: str = "",
           config: dict | None = None, component: str = "janitor",
           reason_code: str | None = None, details: dict | None = None) -> None:
    """Commit one bounded record before returning; errors propagate to the owner.

    Only lifecycle fields belong here, never prompts, captures, command lines or
    arbitrary runtime payloads. A separate stable lock survives atomic trimming.
    """
    values = config if config is not None and config.get("log_dir") else lifecycle.load(overrides=config)[0]
    limit = values.get("session_log_max_bytes", MAX_BYTES)
    if "session_log_max_bytes" in values:
        lifecycle.validate({"session_log_max_bytes": limit})
    if not values.get("log_dir"):
        raise ValueError("resolved event config requires log_dir")
    record = {"time": datetime.now(timezone.utc).isoformat(timespec="milliseconds"),
              "session": session, "identity": identity, "event": event,
              "source": source, "result": result, "reason": reason}
    if archive_path:
        record["archive_path"] = archive_path
    if source not in {"automatic", "manual"}:
        raise ValueError("event source must be automatic or manual")
    for key, value in record.items():
        if not isinstance(value, str) or any(ord(c) < 32 or ord(c) == 127 for c in value):
            raise ValueError("event fields must be text without controls: " + key)
        if len(value) > 2048:
            raise ValueError("event field exceeds size limit: " + key)
    code = reason_code or (reason.split(":", 1)[0] if re.fullmatch(r"[a-z][a-z0-9_]*(?::.*)?", reason) else "unspecified")
    if not re.fullmatch(r"[a-z][a-z0-9_]*", code):
        raise ValueError("invalid reason_code")
    if component not in {"assignment", "janitor", "holds", "adoption", "manual_close", "logger"}:
        raise ValueError("invalid component")
    typed = dict(details or {})
    validate_details(typed)
    record.update(schema_version=1, event_id=str(uuid.uuid4()), component=component,
                  reason_code=code, details=typed)
    data = (json.dumps(record, ensure_ascii=True, separators=(",", ":")) + "\n").encode()
    if len(data) > MAX_RECORD_BYTES or len(data) > limit:
        raise ValueError("event record exceeds size limit")
    path = Path(values["log_dir"]) / "sessions.jsonl"
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    lock_fd = os.open(str(path) + ".lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(lock_fd, "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if path.is_symlink():
            raise ValueError("event log must not be a symlink")
        size = path.stat().st_size if path.exists() else 0
        if size:
            with path.open("rb") as stream:
                stream.seek(-1, os.SEEK_END)
                if stream.read(1) != b"\n":
                    raise ValueError("event log has an unterminated record; preserve and inspect before appending")
        if size + len(data) <= limit:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "ab") as stream:
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
        else:
            # Trim to 75% capacity so subsequent appends reuse bounded headroom.
            # Even the minimum configured cap leaves room for a maximum record.
            marker = dict(time=record["time"], session="", identity="", event="history_trimmed",
                          source="automatic", result="trimmed", reason="size_limit", schema_version=1,
                          event_id=str(uuid.uuid4()), component="logger", reason_code="size_limit",
                          details={"history_incomplete": True})
            marker_data = (json.dumps(marker, separators=(",", ":")) + "\n").encode()
            target = max(limit * 3 // 4, len(data) + len(marker_data))
            budget = min(limit, target) - len(data) - len(marker_data)
            if budget < 0:
                raise ValueError("event and retention marker exceed size limit")
            tail = b""
            if budget:
                with path.open("rb") as stream:
                    start = max(0, size - budget)
                    stream.seek(start)
                    tail = stream.read(budget)
                    if start:
                        tail = tail.partition(b"\n")[2]
                if tail and not tail.endswith(b"\n"):
                    tail = tail.rpartition(b"\n")[0] + b"\n" if b"\n" in tail else b""
            # A single fresh marker covers all dropped history; old retention
            # markers carry no session history and must not crowd out records.
            retained = []
            for line in tail.splitlines(keepends=True):
                try:
                    previous = json.loads(line)
                except ValueError:
                    previous = None
                if not isinstance(previous, dict) or previous.get("event") != "history_trimmed":
                    retained.append(line)
            tail = b"".join(retained)
            fd, temporary = tempfile.mkstemp(prefix=".sessions-", dir=path.parent)
            try:
                with os.fdopen(fd, "wb") as stream:
                    stream.write(tail + marker_data + data)
                    stream.flush()
                    os.fsync(stream.fileno())
                os.replace(temporary, path)
            finally:
                if os.path.exists(temporary):
                    os.unlink(temporary)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)


# Explicit allowlist prevents arbitrary runtime payloads from becoming log data.
DETAIL_TYPES = {
    "run_id": str, "runtime": str, "model": str, "working_directory": str, "task_ref": str,
    "assignment_state": str, "process_state": str, "exit_code": int,
    "completion_receipt_path": str, "result_path": str, "archive_path": str,
    "hold_id": str, "expires_at": str, "indefinite": bool, "actor_ref": str,
    "decision": str, "blocking_conditions": list, "eligible_after": str,
    "requires_intervention": bool, "retry_at": str, "action_id": str, "action": str,
    "action_outcome": str, "error_code": str, "operation": str, "retryable": bool,
    "history_incomplete": bool, "surviving_processes": list,
}


def validate_details(details: dict) -> None:
    for key, value in details.items():
        if key not in DETAIL_TYPES:
            raise ValueError("unknown event detail: " + key)
        if value is None:
            continue
        if type(value) is not DETAIL_TYPES[key]:
            raise ValueError("invalid event detail type: " + key)
        strings = value if isinstance(value, list) else [value] if isinstance(value, str) else []
        if any(not isinstance(v, str) or len(v) > 2048 or any(ord(c) < 32 or ord(c) == 127 for c in v) for v in strings):
            raise ValueError("invalid event detail text: " + key)
        if key == "action_outcome" and value not in {"attempted", "succeeded", "failed", "unknown"}:
            raise ValueError("invalid action outcome")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="Read session history with explicit coverage limits; never controls sessions.")
    parser.add_argument("path", type=Path)
    parser.add_argument("--identity")
    parser.add_argument("--limit", type=int, default=1000)
    args = parser.parse_args(argv)
    if not 1 <= args.limit <= 100000:
        parser.error("limit must be 1..100000")
    rows, invalid = deque(maxlen=args.limit), []
    matched = 0
    incomplete = None
    legacy = unknown = 0
    with args.path.open(encoding="utf-8") as stream:
        for number, line in enumerate(stream, 1):
            try:
                record = json.loads(line)
                if not isinstance(record, dict):
                    raise ValueError("object required")
                version = record.get("schema_version")
                legacy += version is None
                unknown += version is not None and version != 1
                if record.get("event") == "history_trimmed":
                    incomplete = False
                if args.identity is None or record.get("identity") == args.identity:
                    matched += 1
                    rows.append(record)
            except ValueError:
                invalid.append(number)
    print(json.dumps({"history_complete": incomplete, "legacy_records": legacy,
                      "unknown_version_records": unknown, "invalid_lines": invalid, "query_truncated": matched > args.limit, "events": list(rows)}))
    return 1 if invalid else 0


if __name__ == "__main__":
    sys.exit(main())
