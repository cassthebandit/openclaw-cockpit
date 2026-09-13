"""Small durable session history. Transcripts and cleanup archives live elsewhere."""
from __future__ import annotations

import fcntl
import json
import os
import tempfile
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
           config: dict | None = None) -> None:
    """Commit one bounded record before returning; errors propagate to the owner.

    Only lifecycle fields belong here, never prompts, captures, command lines or
    arbitrary runtime payloads. A separate stable lock survives atomic trimming.
    """
    values = config if config is not None and config.get("log_dir") else lifecycle.load()[0]
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
            record[key] = value[:2045] + "..."
    data = (json.dumps(record, ensure_ascii=True, separators=(",", ":")) + "\n").encode()
    if len(data) > MAX_RECORD_BYTES or len(data) > MAX_BYTES:
        raise ValueError("event record exceeds size limit")
    path = Path(values["log_dir"]) / "sessions.jsonl"
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    lock_fd = os.open(str(path) + ".lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(lock_fd, "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if path.is_symlink():
            raise ValueError("event log must not be a symlink")
        size = path.stat().st_size if path.exists() else 0
        if size + len(data) <= MAX_BYTES:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "ab") as stream:
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
        else:
            # Keep only the tail that fits, beginning after a complete newline.
            budget = MAX_BYTES - len(data)
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
            fd, temporary = tempfile.mkstemp(prefix=".sessions-", dir=path.parent)
            try:
                with os.fdopen(fd, "wb") as stream:
                    stream.write(tail + data)
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
