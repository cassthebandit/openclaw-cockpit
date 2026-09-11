"""Read-only OpenClaw SQLite restoration and upstream audit parity. No subprocesses."""
from __future__ import annotations
import argparse
import json
import os
import sqlite3
import time
import urllib.parse
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping

SOURCE_PROVENANCE = "openclaw.sqlite mode=ro: task_runs+task_delivery_state+flow_runs"
SOURCE_PROVENANCE_FIXTURE = "json fixtures: tasks+flows+audit"
SOURCE_PROVENANCE_MIXED = "openclaw.sqlite mode=ro + json fixtures"

# Schema, restoration enums and audit rules mirror this upstream release.
UPSTREAM_PARITY_VERSION = "2026.6.6"
# OPENCLAW_STATE_DIR is the OpenClaw root, not the Cockpit helper state root.
DEFAULT_STATE_ROOT = "~/.openclaw"
STATE_DB_RELATIVE_PATH = ("state", "openclaw.sqlite")
DEFAULT_DB_TIMEOUT_SECONDS = 2.0

TASK_RUN_COLUMNS = (
    "task_id",
    "runtime",
    "task_kind",
    "source_id",
    "requester_session_key",
    "owner_key",
    "scope_kind",
    "child_session_key",
    "parent_flow_id",
    "parent_task_id",
    "agent_id",
    "run_id",
    "label",
    "task",
    "status",
    "delivery_status",
    "notify_policy",
    "created_at",
    "started_at",
    "ended_at",
    "last_event_at",
    "cleanup_after",
    "error",
    "progress_summary",
    "terminal_summary",
    "terminal_outcome",
)
TASK_DELIVERY_STATE_COLUMNS = ("task_id", "requester_origin_json", "last_notified_event_at")
FLOW_RUN_COLUMNS = (
    "flow_id",
    "shape",
    "sync_mode",
    "owner_key",
    "requester_origin_json",
    "controller_id",
    "revision",
    "status",
    "notify_policy",
    "goal",
    "current_step",
    "blocked_task_id",
    "blocked_summary",
    "state_json",
    "wait_json",
    "cancel_requested_at",
    "created_at",
    "updated_at",
    "ended_at",
)
# Exact consumed-table/column contract. Unknown extra columns are tolerated;
# anything missing here fails closed. `PRAGMA user_version` is recorded as
# evidence only: OpenClaw keeps the global schema at version 1 while adding
# tables and columns, so it cannot gate compatibility on its own.
REQUIRED_SCHEMA: dict[str, tuple[str, ...]] = {
    "task_runs": TASK_RUN_COLUMNS,
    "task_delivery_state": TASK_DELIVERY_STATE_COLUMNS,
    "flow_runs": FLOW_RUN_COLUMNS,
}

# Persisted enum vocabularies, copied from the installed OpenClaw registry. An
# out-of-vocabulary value throws there, so it fails visibly here too.
TASK_RUNTIME_ORDER = ("subagent", "acp", "cli", "cron")
TASK_STATUS_ORDER = ("queued", "running", "succeeded", "failed", "timed_out", "cancelled", "lost")
TASK_RUNTIMES = frozenset(TASK_RUNTIME_ORDER)
TASK_STATUSES = frozenset(TASK_STATUS_ORDER)
TASK_DELIVERY_STATUSES = frozenset(
    {"pending", "delivered", "session_queued", "failed", "parent_missing", "not_applicable"}
)
TASK_NOTIFY_POLICIES = frozenset({"done_only", "state_changes", "silent"})
TASK_TERMINAL_OUTCOMES = frozenset({"succeeded", "blocked"})
TASK_SCOPE_KINDS = frozenset({"session", "system"})
TASK_FLOW_STATUSES = frozenset(
    {"queued", "running", "waiting", "blocked", "succeeded", "failed", "cancelled", "lost"}
)
TASK_FLOW_SYNC_MODES = frozenset({"task_mirrored", "managed"})

# Registry-level status sets. These are deliberately distinct from the card-level
# sets above: a registry task is never "waiting", and flow terminality differs.
TASK_ACTIVE_STATUSES = frozenset({"queued", "running"})
TASK_FAILURE_STATUSES = frozenset({"failed", "timed_out", "lost"})
FLOW_TERMINAL_STATUSES = frozenset({"cancelled", "failed", "succeeded", "lost"})
FLOW_STALE_STATUSES = ("running", "waiting", "blocked")

# Audit thresholds and retention windows, copied verbatim from OpenClaw 2026.6.6.
STALE_QUEUED_MS = 10 * 60_000
STALE_RUNNING_MS = 30 * 60_000
FLOW_STALE_RUNNING_MS = 30 * 60_000
FLOW_STALE_WAITING_MS = 30 * 60_000
FLOW_STALE_BLOCKED_MS = 30 * 60_000
FLOW_CANCEL_STUCK_MS = 5 * 60_000
DEFAULT_TASK_RETENTION_MS = 10080 * 60_000
LOST_TASK_RETENTION_MS = 1440 * 60_000
TASK_AUDIT_CODES = (
    "stale_queued",
    "stale_running",
    "lost",
    "delivery_failed",
    "missing_cleanup",
    "inconsistent_timestamps",
)
FLOW_AUDIT_CODES = (
    "restore_failed",
    "stale_running",
    "stale_waiting",
    "stale_blocked",
    "cancel_stuck",
    "missing_linked_tasks",
    "blocked_task_missing",
    "inconsistent_timestamps",
)
class SnapshotError(Exception):
    """Raised when snapshot input cannot be read safely."""


# Distinguishes "column absent or unparseable" from a persisted JSON `null`,
# which OpenClaw keeps as a present-but-null record field.
MISSING = object()


@dataclass
class SourcePayloads:
    tasks: dict[str, Any]
    flows: dict[str, Any]
    audit: dict[str, Any]
    provenance: str = field(default=SOURCE_PROVENANCE)


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def now_ms() -> int:
    return int(time.time() * 1000)


def load_json_file(path: Path) -> dict[str, Any]:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise SnapshotError(f"cannot read {path}: {exc}") from exc
    return parse_json_object(text, label=str(path))


def parse_json_object(text: str, *, label: str) -> dict[str, Any]:
    stripped = text.strip()
    if not stripped:
        raise SnapshotError(f"{label} is empty")
    start = stripped.find("{")
    if start < 0:
        raise SnapshotError(f"{label} does not contain a JSON object")
    try:
        payload = json.loads(stripped[start:])
    except json.JSONDecodeError as exc:
        raise SnapshotError(f"{label} is not valid JSON: {exc}") from exc
    if not isinstance(payload, dict):
        raise SnapshotError(f"{label} must be a JSON object")
    return payload


def resolve_state_db(explicit: str | None = None, env: Mapping[str, str] | None = None) -> Path:
    """Resolve the shared OpenClaw state database.

    Order: explicit `--state-db`, then `OPENCLAW_STATE_DIR` (an OpenClaw *root*
    directory), then `~/.openclaw`.
    """
    environ = os.environ if env is None else env
    if explicit and explicit.strip():
        return Path(explicit.strip()).expanduser()
    root = (environ.get("OPENCLAW_STATE_DIR") or "").strip()
    base = Path(root).expanduser() if root else Path(DEFAULT_STATE_ROOT).expanduser()
    return base.joinpath(*STATE_DB_RELATIVE_PATH)


def open_state_db(path: Path, *, timeout: float) -> sqlite3.Connection:
    """Open one short-lived read-only connection.

    `mode=ro` refuses writes at the SQLite layer, so no write pragma, checkpoint,
    or maintenance statement can be issued through this handle. A crash-hot WAL
    that only a writer can recover fails here on purpose; reopening read-write to
    recover it is forbidden.
    """
    if not path.exists():
        raise SnapshotError(f"OpenClaw state database not found: {path}")
    uri = f"file:{urllib.parse.quote(str(path))}?mode=ro"
    try:
        return sqlite3.connect(uri, uri=True, timeout=max(0.1, float(timeout)))
    except sqlite3.Error as exc:
        raise SnapshotError(f"cannot open {path} read-only: {exc}") from exc


def schema_evidence(con: sqlite3.Connection) -> str:
    try:
        version = con.execute("PRAGMA user_version").fetchone()[0]
    except (sqlite3.Error, TypeError, IndexError):
        return "user_version=unknown"
    return f"user_version={version}"


def validate_state_schema(con: sqlite3.Connection, *, path: Path) -> None:
    """Require the exact tables and columns this adapter consumes.

    Unknown extra tables and columns pass; a missing required table or column
    fails closed with a bounded, non-secret message. No row values are read here,
    so nothing secret-bearing can reach the error text.
    """
    try:
        present = {row[0] for row in con.execute("SELECT name FROM sqlite_master WHERE type = 'table'")}
    except sqlite3.Error as exc:
        raise SnapshotError(f"cannot read schema from {path}: {exc}") from exc
    missing_tables = sorted(name for name in REQUIRED_SCHEMA if name not in present)
    if missing_tables:
        raise SnapshotError(
            f"{path} is not a supported OpenClaw state database: missing table(s) "
            f"{', '.join(missing_tables)} ({schema_evidence(con)})"
        )
    for table, columns in REQUIRED_SCHEMA.items():
        try:
            found = {row[1] for row in con.execute(f"PRAGMA table_info({table})")}
        except sqlite3.Error as exc:
            raise SnapshotError(f"cannot read schema for {table} in {path}: {exc}") from exc
        missing_columns = [column for column in columns if column not in found]
        if missing_columns:
            raise SnapshotError(
                f"{path} table {table} is missing required column(s) "
                f"{', '.join(missing_columns)} ({schema_evidence(con)})"
            )


def is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def optional_number(value: Any) -> int | None:
    return int(value) if is_number(value) else None


def optional_text(value: Any) -> str | None:
    """Mirror OpenClaw's `row.col ? {...} : {}` truthiness: blank means absent."""
    return value if isinstance(value, str) and value else None


def parse_persisted_value(value: Any, allowed: frozenset[str], *, label: str, kind: str = "task") -> str:
    if isinstance(value, str) and value in allowed:
        return value
    raise SnapshotError(f"invalid persisted {kind} {label}: {value!r}")


def parse_optional_terminal_outcome(value: Any) -> str | None:
    if value is None or value == "":
        return None
    return parse_persisted_value(value, TASK_TERMINAL_OUTCOMES, label="terminal outcome")


def parse_sqlite_json(raw: Any) -> Any:
    """Tolerant persisted-JSON reader, matching OpenClaw's own behaviour.

    OpenClaw throws on an invalid persisted enum but swallows an unparseable JSON
    column, so a corrupt `state_json`/`wait_json`/`requester_origin_json` degrades
    that one field instead of blanking the whole runtime view.
    """
    if not isinstance(raw, str) or not raw.strip():
        return MISSING
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return MISSING


def parse_delivery_context(raw: Any) -> dict[str, Any] | None:
    """Project the delivery-context fields OpenClaw persists.

    OpenClaw additionally runs the parsed value through its private channel-route
    normalizer. That module is out of bounds for this adapter, and the only
    cockpit consumer is the flow card's `requesterSessionKey` display field, which
    takes part in no dedupe, merge, or presentation key.
    """
    parsed = parse_sqlite_json(raw)
    if parsed is MISSING or not isinstance(parsed, dict):
        return None
    context: dict[str, Any] = {}
    for key in ("channel", "to", "accountId"):
        value = parsed.get(key)
        if isinstance(value, str) and value.strip():
            context[key] = value.strip()
    thread_id = parsed.get("threadId")
    if isinstance(thread_id, str) and thread_id.strip():
        context["threadId"] = thread_id.strip()
    elif is_number(thread_id):
        context["threadId"] = thread_id
    return context or None


def row_to_task_record(row: sqlite3.Row) -> dict[str, Any]:
    """Reconstruct one `openclaw tasks list --json` task record from a row."""
    scope_kind = parse_persisted_value(row["scope_kind"], TASK_SCOPE_KINDS, label="scope kind")
    owner_key = row["owner_key"]
    record: dict[str, Any] = {
        "taskId": row["task_id"],
        "runtime": parse_persisted_value(row["runtime"], TASK_RUNTIMES, label="runtime"),
    }
    for key, column in (("taskKind", "task_kind"), ("sourceId", "source_id")):
        value = optional_text(row[column])
        if value is not None:
            record[key] = value
    requester = (row["requester_session_key"] or "").strip() or owner_key
    record["requesterSessionKey"] = "" if scope_kind == "system" else requester
    record["ownerKey"] = owner_key
    record["scopeKind"] = scope_kind
    for key, column in (
        ("childSessionKey", "child_session_key"),
        ("parentFlowId", "parent_flow_id"),
        ("parentTaskId", "parent_task_id"),
        ("agentId", "agent_id"),
        ("runId", "run_id"),
        ("label", "label"),
    ):
        value = optional_text(row[column])
        if value is not None:
            record[key] = value
    record["task"] = row["task"]
    record["status"] = parse_persisted_value(row["status"], TASK_STATUSES, label="status")
    record["deliveryStatus"] = parse_persisted_value(
        row["delivery_status"], TASK_DELIVERY_STATUSES, label="delivery status"
    )
    record["notifyPolicy"] = parse_persisted_value(row["notify_policy"], TASK_NOTIFY_POLICIES, label="notify policy")
    created_at = optional_number(row["created_at"])
    record["createdAt"] = 0 if created_at is None else created_at
    for key, column in (
        ("startedAt", "started_at"),
        ("endedAt", "ended_at"),
        ("lastEventAt", "last_event_at"),
        ("cleanupAfter", "cleanup_after"),
    ):
        value = optional_number(row[column])
        if value is not None:
            record[key] = value
    for key, column in (
        ("error", "error"),
        ("progressSummary", "progress_summary"),
        ("terminalSummary", "terminal_summary"),
    ):
        value = optional_text(row[column])
        if value is not None:
            record[key] = value
    terminal_outcome = parse_optional_terminal_outcome(row["terminal_outcome"])
    if terminal_outcome is not None:
        record["terminalOutcome"] = terminal_outcome
    return record


def row_to_sync_mode(row: sqlite3.Row) -> str:
    """Null/blank `sync_mode` falls back through `shape`, exactly as OpenClaw does."""
    raw = row["sync_mode"]
    if raw is None or raw == "":
        return "task_mirrored" if row["shape"] == "single_task" else "managed"
    return parse_persisted_value(raw, TASK_FLOW_SYNC_MODES, label="sync mode", kind="task flow")


def row_to_flow_record(row: sqlite3.Row) -> dict[str, Any]:
    """Reconstruct one restored TaskFlow record from a row."""
    record: dict[str, Any] = {"flowId": row["flow_id"], "syncMode": row_to_sync_mode(row)}
    record["ownerKey"] = row["owner_key"]
    requester_origin = parse_delivery_context(row["requester_origin_json"])
    if requester_origin is not None:
        record["requesterOrigin"] = requester_origin
    controller_id = optional_text(row["controller_id"])
    if controller_id is not None:
        record["controllerId"] = controller_id
    revision = optional_number(row["revision"])
    record["revision"] = 0 if revision is None else revision
    record["status"] = parse_persisted_value(row["status"], TASK_FLOW_STATUSES, label="status", kind="task flow")
    record["notifyPolicy"] = parse_persisted_value(row["notify_policy"], TASK_NOTIFY_POLICIES, label="notify policy")
    record["goal"] = row["goal"]
    for key, column in (
        ("currentStep", "current_step"),
        ("blockedTaskId", "blocked_task_id"),
        ("blockedSummary", "blocked_summary"),
    ):
        value = optional_text(row[column])
        if value is not None:
            record[key] = value
    state_json = parse_sqlite_json(row["state_json"])
    if state_json is not MISSING:
        record["stateJson"] = state_json
    wait_json = parse_sqlite_json(row["wait_json"])
    if wait_json is not MISSING:
        record["waitJson"] = wait_json
    cancel_requested_at = optional_number(row["cancel_requested_at"])
    if cancel_requested_at is not None:
        record["cancelRequestedAt"] = cancel_requested_at
    created_at = optional_number(row["created_at"])
    updated_at = optional_number(row["updated_at"])
    record["createdAt"] = 0 if created_at is None else created_at
    record["updatedAt"] = 0 if updated_at is None else updated_at
    ended_at = optional_number(row["ended_at"])
    if ended_at is not None:
        record["endedAt"] = ended_at
    return record


def summarize_task_records(records: list[dict[str, Any]]) -> dict[str, Any]:
    """`taskSummary` for a flow's linked tasks, matching `summarizeTaskRecords`."""
    by_status = dict.fromkeys(TASK_STATUS_ORDER, 0)
    by_runtime = dict.fromkeys(TASK_RUNTIME_ORDER, 0)
    summary: dict[str, Any] = {
        "total": 0,
        "active": 0,
        "terminal": 0,
        "failures": 0,
        "byStatus": by_status,
        "byRuntime": by_runtime,
    }
    for record in records:
        status = text(record.get("status"))
        runtime = text(record.get("runtime"))
        summary["total"] += 1
        if status in by_status:
            by_status[status] += 1
        if runtime in by_runtime:
            by_runtime[runtime] += 1
        if status in TASK_ACTIVE_STATUSES:
            summary["active"] += 1
        else:
            summary["terminal"] += 1
        if status in TASK_FAILURE_STATUSES:
            summary["failures"] += 1
    return summary


def resolve_task_retention_ms(status: str) -> int:
    return LOST_TASK_RETENTION_MS if status == "lost" else DEFAULT_TASK_RETENTION_MS


def resolve_task_cleanup_after(task: dict[str, Any]) -> int:
    for key in ("endedAt", "lastEventAt", "createdAt"):
        value = task.get(key)
        if is_number(value):
            return int(value) + resolve_task_retention_ms(text(task.get("status")))
    return resolve_task_retention_ms(text(task.get("status")))


def resolve_effective_task_cleanup_after(task: dict[str, Any]) -> int:
    """Lost tasks take the earlier of persisted and status-derived cleanup."""
    status_cleanup_after = resolve_task_cleanup_after(task)
    cleanup_after = task.get("cleanupAfter")
    if not is_number(cleanup_after):
        return status_cleanup_after
    if text(task.get("status")) == "lost":
        return min(int(cleanup_after), status_cleanup_after)
    return int(cleanup_after)


def finding_sort_key(severity: str, age_ms: Any, created_at: int) -> tuple[int, int, int]:
    """Errors first, then oldest finding first, then oldest record first."""
    age = int(age_ms) if is_number(age_ms) else -1
    return (0 if severity == "error" else 1, -age, created_at)


def audit_finding(
    *,
    severity: str,
    code: str,
    detail: str,
    age_ms: int | None = None,
    task: dict[str, Any] | None = None,
    flow: dict[str, Any] | None = None,
) -> dict[str, Any]:
    finding: dict[str, Any] = {"severity": severity, "code": code, "detail": detail}
    if age_ms is not None:
        finding["ageMs"] = age_ms
    if task is not None:
        finding["task"] = task
    if flow is not None:
        finding["flow"] = flow
    return finding


def task_reference_at(task: dict[str, Any]) -> int:
    for key in ("lastEventAt", "startedAt", "createdAt"):
        value = task.get(key)
        if is_number(value):
            return int(value)
    return 0


def find_task_timestamp_inconsistency(task: dict[str, Any]) -> dict[str, Any] | None:
    created_at = int(task.get("createdAt") or 0)
    started_at = task.get("startedAt")
    ended_at = task.get("endedAt")
    status = text(task.get("status"))
    if started_at and started_at < created_at:
        return audit_finding(
            severity="warn", code="inconsistent_timestamps", task=task, detail="startedAt is earlier than createdAt"
        )
    if ended_at and started_at and ended_at < started_at:
        return audit_finding(
            severity="warn", code="inconsistent_timestamps", task=task, detail="endedAt is earlier than startedAt"
        )
    if status in TASK_ACTIVE_STATUSES and ended_at:
        return audit_finding(
            severity="warn",
            code="inconsistent_timestamps",
            task=task,
            detail=f"{status} task should not already have endedAt",
        )
    return None


def list_task_audit_findings(tasks: list[dict[str, Any]], *, now: int) -> list[dict[str, Any]]:
    findings: list[dict[str, Any]] = []
    for task in tasks:
        age = max(0, now - task_reference_at(task))
        status = text(task.get("status"))
        if status == "queued" and age >= STALE_QUEUED_MS:
            findings.append(
                audit_finding(
                    severity="warn",
                    code="stale_queued",
                    task=task,
                    age_ms=age,
                    detail="queued task has not advanced recently",
                )
            )
        if status == "running" and age >= STALE_RUNNING_MS:
            findings.append(
                audit_finding(
                    severity="error", code="stale_running", task=task, age_ms=age, detail="running task appears stuck"
                )
            )
        if status == "lost":
            retained = is_number(task.get("cleanupAfter")) and resolve_effective_task_cleanup_after(task) > now
            reported_error = text(task.get("error")).strip()
            if retained:
                detail = reported_error or "task lost its backing session and is retained until cleanupAfter"
            else:
                detail = reported_error or "task lost its backing session"
            findings.append(
                audit_finding(
                    severity="warn" if retained else "error", code="lost", task=task, age_ms=age, detail=detail
                )
            )
        # Silent notify policy suppresses the delivery finding even though the
        # task card still classifies the failed delivery as attention.
        if text(task.get("deliveryStatus")) == "failed" and text(task.get("notifyPolicy")) != "silent":
            findings.append(
                audit_finding(
                    severity="warn",
                    code="delivery_failed",
                    task=task,
                    age_ms=age,
                    detail="terminal update delivery failed",
                )
            )
        if status not in TASK_ACTIVE_STATUSES and status != "lost" and not is_number(task.get("cleanupAfter")):
            findings.append(
                audit_finding(
                    severity="warn",
                    code="missing_cleanup",
                    task=task,
                    age_ms=age,
                    detail="terminal task is missing cleanupAfter",
                )
            )
        inconsistency = find_task_timestamp_inconsistency(task)
        if inconsistency:
            findings.append(inconsistency)
    return sorted(
        findings,
        key=lambda finding: finding_sort_key(
            finding["severity"], finding.get("ageMs"), int(finding["task"].get("createdAt") or 0)
        ),
    )


def flow_reference_at(flow: dict[str, Any]) -> int:
    updated_at = flow.get("updatedAt")
    if is_number(updated_at):
        return int(updated_at)
    return int(flow.get("createdAt") or 0)


def has_blocking_metadata(flow: dict[str, Any]) -> bool:
    if text(flow.get("blockedTaskId")).strip() or text(flow.get("blockedSummary")).strip():
        return True
    return flow.get("waitJson") is not None


def find_flow_timestamp_inconsistency(flow: dict[str, Any]) -> dict[str, Any] | None:
    created_at = int(flow.get("createdAt") or 0)
    updated_at = int(flow.get("updatedAt") or 0)
    ended_at = flow.get("endedAt")
    if updated_at < created_at:
        return audit_finding(
            severity="warn", code="inconsistent_timestamps", flow=flow, detail="updatedAt is earlier than createdAt"
        )
    if ended_at and ended_at < created_at:
        return audit_finding(
            severity="warn", code="inconsistent_timestamps", flow=flow, detail="endedAt is earlier than createdAt"
        )
    if ended_at and ended_at < updated_at:
        return audit_finding(
            severity="warn", code="inconsistent_timestamps", flow=flow, detail="endedAt is earlier than updatedAt"
        )
    return None


def flow_stale_threshold_ms(status: str) -> int:
    if status == "running":
        return FLOW_STALE_RUNNING_MS
    if status == "waiting":
        return FLOW_STALE_WAITING_MS
    return FLOW_STALE_BLOCKED_MS


def list_flow_audit_findings(
    flows: list[dict[str, Any]],
    tasks_by_flow: dict[str, list[dict[str, Any]]],
    *,
    now: int,
) -> list[dict[str, Any]]:
    findings: list[dict[str, Any]] = []
    for flow in flows:
        age = max(0, now - flow_reference_at(flow))
        status = text(flow.get("status"))
        linked_tasks = tasks_by_flow.get(text(flow.get("flowId")), [])
        active_tasks = [task for task in linked_tasks if text(task.get("status")) in TASK_ACTIVE_STATUSES]
        if status == "running" and age >= FLOW_STALE_RUNNING_MS:
            findings.append(
                audit_finding(
                    severity="error",
                    code="stale_running",
                    flow=flow,
                    age_ms=age,
                    detail="running TaskFlow has not advanced recently",
                )
            )
        if status == "waiting" and age >= FLOW_STALE_WAITING_MS:
            findings.append(
                audit_finding(
                    severity="warn",
                    code="stale_waiting",
                    flow=flow,
                    age_ms=age,
                    detail="waiting TaskFlow has not advanced recently",
                )
            )
        if status == "blocked" and age >= FLOW_STALE_BLOCKED_MS:
            findings.append(
                audit_finding(
                    severity="warn",
                    code="stale_blocked",
                    flow=flow,
                    age_ms=age,
                    detail="blocked TaskFlow has not advanced recently",
                )
            )
        # Cancel-stuck ages from cancelRequestedAt, not from the flow reference
        # time, and only fires while no linked child is still active.
        cancel_requested_at = flow.get("cancelRequestedAt")
        if (
            is_number(cancel_requested_at)
            and status not in FLOW_TERMINAL_STATUSES
            and not active_tasks
            and now - int(cancel_requested_at) >= FLOW_CANCEL_STUCK_MS
        ):
            findings.append(
                audit_finding(
                    severity="warn",
                    code="cancel_stuck",
                    flow=flow,
                    age_ms=max(0, now - int(cancel_requested_at)),
                    detail="cancel-requested TaskFlow has no active child tasks but is still nonterminal",
                )
            )
        if (
            text(flow.get("syncMode")) == "managed"
            and status in FLOW_STALE_STATUSES
            and age >= flow_stale_threshold_ms(status)
            and not linked_tasks
            and not has_blocking_metadata(flow)
        ):
            findings.append(
                audit_finding(
                    severity="error" if status == "running" else "warn",
                    code="missing_linked_tasks",
                    flow=flow,
                    age_ms=age,
                    detail="managed TaskFlow has no linked tasks or wait state",
                )
            )
        blocked_task_id = text(flow.get("blockedTaskId")).strip()
        if blocked_task_id and not any(text(task.get("taskId")) == blocked_task_id for task in linked_tasks):
            findings.append(
                audit_finding(
                    severity="warn",
                    code="blocked_task_missing",
                    flow=flow,
                    age_ms=age,
                    detail=f"blocked TaskFlow points at missing task {blocked_task_id}",
                )
            )
        inconsistency = find_flow_timestamp_inconsistency(flow)
        if inconsistency:
            findings.append(inconsistency)
    return sorted(
        findings,
        key=lambda finding: finding_sort_key(
            finding["severity"], finding.get("ageMs"), int((finding.get("flow") or {}).get("createdAt") or 0)
        ),
    )


def summarize_audit_findings(findings: list[dict[str, Any]], codes: tuple[str, ...]) -> dict[str, Any]:
    summary: dict[str, Any] = {"total": 0, "warnings": 0, "errors": 0, "byCode": dict.fromkeys(codes, 0)}
    for finding in findings:
        summary["total"] += 1
        code = text(finding.get("code"))
        if code in summary["byCode"]:
            summary["byCode"][code] += 1
        if text(finding.get("severity")) == "error":
            summary["errors"] += 1
        else:
            summary["warnings"] += 1
    return summary


def build_audit_payload(
    task_findings: list[dict[str, Any]],
    flow_findings: list[dict[str, Any]],
) -> dict[str, Any]:
    """Combine task and flow findings into the `tasks audit --json` envelope.

    Only the fields the card builder consumes are emitted. The CLI additionally
    embeds each finding's whole task/flow record; nothing downstream reads it and
    omitting it keeps the snapshot inside Cockpit's output cap.
    """
    combined: list[tuple[tuple[int, int, int], dict[str, Any]]] = []
    for finding in task_findings:
        task = finding["task"]
        entry = {
            "kind": "task",
            "severity": finding["severity"],
            "code": finding["code"],
            "detail": finding["detail"],
            "status": text(task.get("status")),
            "token": text(task.get("taskId")),
        }
        if "ageMs" in finding:
            entry["ageMs"] = finding["ageMs"]
        combined.append(
            (finding_sort_key(finding["severity"], finding.get("ageMs"), int(task.get("createdAt") or 0)), entry)
        )
    for finding in flow_findings:
        flow = finding.get("flow") or {}
        entry = {
            "kind": "task_flow",
            "severity": finding["severity"],
            "code": finding["code"],
            "detail": finding["detail"],
            "status": text(flow.get("status")) if flow else "n/a",
        }
        if "ageMs" in finding:
            entry["ageMs"] = finding["ageMs"]
        if flow:
            entry["token"] = text(flow.get("flowId"))
        combined.append(
            (finding_sort_key(finding["severity"], finding.get("ageMs"), int(flow.get("createdAt") or 0)), entry)
        )
    combined.sort(key=lambda item: item[0])
    findings = [entry for _, entry in combined]
    errors = sum(1 for finding in findings if finding["severity"] == "error")
    return {
        "count": len(findings),
        "filteredCount": len(findings),
        "displayed": len(findings),
        "filters": {"severity": None, "code": None, "limit": None},
        "summary": {
            **summarize_audit_findings(task_findings, TASK_AUDIT_CODES),
            "taskFlows": summarize_audit_findings(flow_findings, FLOW_AUDIT_CODES),
            "combined": {"total": len(findings), "errors": errors, "warnings": len(findings) - errors},
        },
        "findings": findings,
    }


def load_sqlite_payloads(path: Path, *, timeout: float, now: int) -> SourcePayloads:
    """Take one read-only snapshot of the three control-plane tables.

    The connection is opened, validated, drained, and closed within this call, so
    no read transaction outlives the snapshot and no long-lived WAL reader is
    created.
    """
    con = open_state_db(path, timeout=timeout)
    try:
        con.row_factory = sqlite3.Row
        con.execute("BEGIN")
        validate_state_schema(con, path=path)
        task_columns = ", ".join(TASK_RUN_COLUMNS)
        flow_columns = ", ".join(FLOW_RUN_COLUMNS)
        # Registry order: newest first, ties broken the way OpenClaw's restored
        # in-memory insertion order breaks them.
        task_rows = con.execute(
            f"SELECT {task_columns} FROM task_runs ORDER BY created_at DESC, task_id DESC"
        ).fetchall()
        flow_rows = con.execute(
            f"SELECT {flow_columns} FROM flow_runs ORDER BY created_at DESC, flow_id ASC"
        ).fetchall()
    except sqlite3.Error as exc:
        raise SnapshotError(f"cannot read {path}: {exc}") from exc
    finally:
        con.close()

    tasks = [row_to_task_record(row) for row in task_rows]
    flows = [row_to_flow_record(row) for row in flow_rows]
    tasks_by_flow: dict[str, list[dict[str, Any]]] = {}
    for task in tasks:
        parent_flow_id = text(task.get("parentFlowId"))
        if parent_flow_id:
            tasks_by_flow.setdefault(parent_flow_id, []).append(task)
    flow_entries = []
    for flow in flows:
        linked_tasks = tasks_by_flow.get(text(flow.get("flowId")), [])
        flow_entries.append({**flow, "tasks": linked_tasks, "taskSummary": summarize_task_records(linked_tasks)})
    return SourcePayloads(
        tasks={"count": len(tasks), "runtime": None, "status": None, "tasks": tasks},
        flows={"count": len(flows), "status": None, "flows": flow_entries},
        audit=build_audit_payload(
            list_task_audit_findings(tasks, now=now),
            list_flow_audit_findings(flows, tasks_by_flow, now=now),
        ),
        provenance=SOURCE_PROVENANCE,
    )


def load_sources(args: argparse.Namespace) -> SourcePayloads:
    """Resolve the three payloads from fixtures, SQLite, or a mix of both."""
    fixtures = {
        "tasks": Path(args.tasks_json) if args.tasks_json else None,
        "flows": Path(args.flows_json) if args.flows_json else None,
        "audit": Path(args.audit_json) if args.audit_json else None,
    }
    if all(fixtures.values()):
        return SourcePayloads(
            tasks=load_json_file(fixtures["tasks"]),
            flows=load_json_file(fixtures["flows"]),
            audit=load_json_file(fixtures["audit"]),
            provenance=SOURCE_PROVENANCE_FIXTURE,
        )
    live = load_sqlite_payloads(
        resolve_state_db(getattr(args, "state_db", None)),
        timeout=args.timeout,
        now=now_ms(),
    )
    return SourcePayloads(
        tasks=load_json_file(fixtures["tasks"]) if fixtures["tasks"] else live.tasks,
        flows=load_json_file(fixtures["flows"]) if fixtures["flows"] else live.flows,
        audit=load_json_file(fixtures["audit"]) if fixtures["audit"] else live.audit,
        provenance=SOURCE_PROVENANCE_MIXED if any(fixtures.values()) else SOURCE_PROVENANCE,
    )


def text(value: Any, *, default: str = "") -> str:
    if value is None:
        return default
    return str(value)
