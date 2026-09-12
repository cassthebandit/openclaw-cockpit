#!/usr/bin/env python3
"""Render OpenClaw task and TaskFlow state as compact cockpit cards.

This is read-only. It does not mutate Gateway, sessions, tasks, tmux metadata,
or Discord. It exists beside the tmux cockpit so ACP/OpenClaw thread work is
visible without pretending it is a tmux pane.

Production state comes from one short-lived `mode=ro` SQLite connection to
OpenClaw's shared control-plane database. There is no `openclaw` subprocess and
no CLI fallback: a database that cannot be opened, fails the schema guard, or
holds an invalid persisted enum surfaces as the visible source-error card and is
retried by the next refresh.

The task, flow, linked-task, and audit payloads are reconstructed to match what
`openclaw tasks list --json`, `openclaw tasks flow list --json`, and
`openclaw tasks audit --json` return for the fields the card builder consumes,
so JSON fixtures remain the deterministic contract seam.
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
import sys
from pathlib import Path
from typing import Any


try:
    from .source import (
        SOURCE_PROVENANCE,
        SOURCE_PROVENANCE_FIXTURE,
        SOURCE_PROVENANCE_MIXED,
        DEFAULT_STATE_ROOT,
        STATE_DB_RELATIVE_PATH,
        DEFAULT_DB_TIMEOUT_SECONDS,
        TASK_RUN_COLUMNS,
        TASK_DELIVERY_STATE_COLUMNS,
        FLOW_RUN_COLUMNS,
        REQUIRED_SCHEMA,
        TASK_RUNTIME_ORDER,
        TASK_STATUS_ORDER,
        TASK_RUNTIMES,
        TASK_STATUSES,
        TASK_DELIVERY_STATUSES,
        TASK_NOTIFY_POLICIES,
        TASK_TERMINAL_OUTCOMES,
        TASK_SCOPE_KINDS,
        TASK_FLOW_STATUSES,
        TASK_FLOW_SYNC_MODES,
        TASK_ACTIVE_STATUSES,
        TASK_FAILURE_STATUSES,
        FLOW_TERMINAL_STATUSES,
        FLOW_STALE_STATUSES,
        STALE_QUEUED_MS,
        STALE_RUNNING_MS,
        FLOW_STALE_RUNNING_MS,
        FLOW_STALE_WAITING_MS,
        FLOW_STALE_BLOCKED_MS,
        FLOW_CANCEL_STUCK_MS,
        DEFAULT_TASK_RETENTION_MS,
        LOST_TASK_RETENTION_MS,
        TASK_AUDIT_CODES,
        FLOW_AUDIT_CODES,
        SnapshotError,
        MISSING,
        SourcePayloads,
        utc_now,
        now_ms,
        load_json_file,
        parse_json_object,
        resolve_state_db,
        open_state_db,
        schema_evidence,
        validate_state_schema,
        is_number,
        optional_number,
        optional_text,
        parse_persisted_value,
        parse_optional_terminal_outcome,
        parse_sqlite_json,
        parse_delivery_context,
        row_to_task_record,
        row_to_sync_mode,
        row_to_flow_record,
        summarize_task_records,
        resolve_task_retention_ms,
        resolve_task_cleanup_after,
        resolve_effective_task_cleanup_after,
        finding_sort_key,
        audit_finding,
        task_reference_at,
        find_task_timestamp_inconsistency,
        list_task_audit_findings,
        flow_reference_at,
        has_blocking_metadata,
        find_flow_timestamp_inconsistency,
        flow_stale_threshold_ms,
        list_flow_audit_findings,
        summarize_audit_findings,
        build_audit_payload,
        load_sqlite_payloads,
        load_sources,
        text
    )
except ImportError:  # direct-script invocation
    from source import (
        SOURCE_PROVENANCE,
        SOURCE_PROVENANCE_FIXTURE,
        SOURCE_PROVENANCE_MIXED,
        DEFAULT_STATE_ROOT,
        STATE_DB_RELATIVE_PATH,
        DEFAULT_DB_TIMEOUT_SECONDS,
        TASK_RUN_COLUMNS,
        TASK_DELIVERY_STATE_COLUMNS,
        FLOW_RUN_COLUMNS,
        REQUIRED_SCHEMA,
        TASK_RUNTIME_ORDER,
        TASK_STATUS_ORDER,
        TASK_RUNTIMES,
        TASK_STATUSES,
        TASK_DELIVERY_STATUSES,
        TASK_NOTIFY_POLICIES,
        TASK_TERMINAL_OUTCOMES,
        TASK_SCOPE_KINDS,
        TASK_FLOW_STATUSES,
        TASK_FLOW_SYNC_MODES,
        TASK_ACTIVE_STATUSES,
        TASK_FAILURE_STATUSES,
        FLOW_TERMINAL_STATUSES,
        FLOW_STALE_STATUSES,
        STALE_QUEUED_MS,
        STALE_RUNNING_MS,
        FLOW_STALE_RUNNING_MS,
        FLOW_STALE_WAITING_MS,
        FLOW_STALE_BLOCKED_MS,
        FLOW_CANCEL_STUCK_MS,
        DEFAULT_TASK_RETENTION_MS,
        LOST_TASK_RETENTION_MS,
        TASK_AUDIT_CODES,
        FLOW_AUDIT_CODES,
        SnapshotError,
        MISSING,
        SourcePayloads,
        utc_now,
        now_ms,
        load_json_file,
        parse_json_object,
        resolve_state_db,
        open_state_db,
        schema_evidence,
        validate_state_schema,
        is_number,
        optional_number,
        optional_text,
        parse_persisted_value,
        parse_optional_terminal_outcome,
        parse_sqlite_json,
        parse_delivery_context,
        row_to_task_record,
        row_to_sync_mode,
        row_to_flow_record,
        summarize_task_records,
        resolve_task_retention_ms,
        resolve_task_cleanup_after,
        resolve_effective_task_cleanup_after,
        finding_sort_key,
        audit_finding,
        task_reference_at,
        find_task_timestamp_inconsistency,
        list_task_audit_findings,
        flow_reference_at,
        has_blocking_metadata,
        find_flow_timestamp_inconsistency,
        flow_stale_threshold_ms,
        list_flow_audit_findings,
        summarize_audit_findings,
        build_audit_payload,
        load_sqlite_payloads,
        load_sources,
        text
    )

ACTIVE_STATUSES = {"queued", "running", "waiting"}
ATTENTION_TASK_STATUSES = {"failed", "timed_out", "lost"}
ATTENTION_FLOW_STATUSES = {"blocked", "failed"}
DONE_STATUSES = {"succeeded", "cancelled"}
DEFAULT_LIMIT = 40
DAY_MS = 86_400_000
TERMINAL_CARD_VISIBLE_MS = 180_000
CARD_CONTRACT_VERSION = "runtime-card.v1"
SOURCE_TRUTH = "reported_by_openclaw"
TEARDOWN_POLICY = "drop_when_source_absent"

REASON_ORDER = [
    "delivery_failed",
    "acp_turn_failed",
    "done_only_no_final",
    "runtime_failed",
    "blocked_flow_stale",
    "active_runtime",
    "completed_runtime",
    "unknown_runtime_state",
]
REASON_RANK = {reason: index for index, reason in enumerate(REASON_ORDER)}
REASON_DISPLAY = {
    "delivery_failed": ("failed", "needs_attention", "inspect final delivery"),
    "acp_turn_failed": ("failed", "needs_attention", "inspect ACP route"),
    "done_only_no_final": ("blocked", "needs_attention", "inspect final delivery"),
    "runtime_failed": ("failed", "needs_attention", "recover handoff"),
    "blocked_flow_stale": ("blocked", "needs_attention", "mark expected stale or recover handoff"),
    "active_runtime": ("active", "active", "watch"),
    "completed_runtime": ("completed", "completed", "none"),
    "unknown_runtime_state": ("unknown", "unknown", "inspect manually"),
}
DISPLAY_GROUP_STATE = {
    "needs_attention": "attention",
    "active": "active",
    "completed": "done",
    "unknown": "unknown",
}
DISPLAY_GROUP_LIFECYCLE = {
    "needs_attention": "needs_attention",
    "active": "active",
    "completed": "resolved",
    "unknown": "unknown",
}
DISPLAY_GROUP_ACTIONABILITY = {
    "needs_attention": "operator_action",
    "active": "watch",
    "completed": "none",
    "unknown": "inspect",
}
PRESENTATION_GROUP_LABEL = {
    "current_work": "Current Work",
    "needs_decision": "Needs Decision",
    "route_health": "Route Health",
    "delivery_handoff": "Delivery / Handoff",
    "source_unknown": "Source Unknown",
    "expected_controls": "Expected Controls",
    "skeletons": "Skeletons",
    "completed": "Completed",
}
REASON_PRESENTATION = {
    "delivery_failed": {
        "group": "delivery_handoff",
        "why": "Source reports that final delivery or handoff failed.",
        "suggestionKind": "inspect_delivery",
        "suggestedAction": "Inspect final delivery evidence before deciding cleanup.",
        "suggestedCommand": "openclaw tasks audit --json",
        "confidence": "high",
    },
    "acp_turn_failed": {
        "group": "route_health",
        "why": "Source reports an ACP route or turn failure, not necessarily bad project work.",
        "suggestionKind": "inspect_route",
        "suggestedAction": "Inspect ACP route health and permission/prompt behavior.",
        "suggestedCommand": "openclaw tasks audit --json",
        "confidence": "high",
    },
    "done_only_no_final": {
        "group": "delivery_handoff",
        "why": "Work may have finished, but no clean final-delivery marker is present.",
        "suggestionKind": "inspect_handoff",
        "suggestedAction": "Inspect parent/child handoff before marking this resolved.",
        "suggestedCommand": "openclaw tasks flow list --json",
        "confidence": "medium",
    },
    "runtime_failed": {
        "group": "needs_decision",
        "why": "Source reports a runtime failure that needs an operator decision.",
        "suggestionKind": "inspect_runtime",
        "suggestedAction": "Inspect source record and decide whether to recover, retry, or mark expected.",
        "suggestedCommand": "openclaw tasks list --json",
        "confidence": "medium",
    },
    "blocked_flow_stale": {
        "group": "needs_decision",
        "why": "Source reports a blocked or stale flow that should not be resolved automatically.",
        "suggestionKind": "decide_stale_flow",
        "suggestedAction": "Decide whether this is still useful, expected stale, or needs recovery.",
        "suggestedCommand": "openclaw tasks flow list --json",
        "confidence": "medium",
    },
    "active_runtime": {
        "group": "current_work",
        "why": "Source reports current or active runtime work.",
        "suggestionKind": "watch",
        "suggestedAction": "Watch for progress or completion.",
        "suggestedCommand": "",
        "confidence": "high",
    },
    "completed_runtime": {
        "group": "completed",
        "why": "Source reports completed runtime work.",
        "suggestionKind": "none",
        "suggestedAction": "No action suggested.",
        "suggestedCommand": "",
        "confidence": "high",
    },
    "unknown_runtime_state": {
        "group": "source_unknown",
        "why": "Source state is incomplete or not recognized by the cockpit adapter.",
        "suggestionKind": "inspect_unknown",
        "suggestedAction": "Inspect source record manually; do not suppress unknowns.",
        "suggestedCommand": "openclaw tasks audit --json",
        "confidence": "low",
    },
}
PRESENTATION_RANK = {
    "current_work": 0,
    "needs_decision": 1,
    "route_health": 2,
    "delivery_handoff": 3,
    "source_unknown": 4,
    "expected_controls": 5,
    "skeletons": 6,
    "completed": 7,
}


def clipped(value: Any, *, budget: int = 220) -> str:
    raw = " ".join(text(value).split())
    if len(raw) <= budget:
        return raw
    return raw[: max(0, budget - 1)].rstrip() + "..."


def compact_title(value: Any, *, budget: int = 72) -> str:
    raw = " ".join(text(value, default="OpenClaw runtime item").split())
    if raw.startswith("/") or raw.count("/") >= 3:
        raw = Path(raw).name or raw
    return clipped(raw, budget=budget)


def normalize_label(value: Any) -> str:
    raw = text(value).lower()
    raw = re.sub(r"/[^\s]+/", "/", raw)
    raw = re.sub(r"\b[0-9a-f]{7,}\b", "<id>", raw)
    raw = re.sub(r"\s+", " ", raw)
    return raw.strip()


def timestamp_ms(*values: Any) -> int | None:
    numeric = [int(value) for value in values if isinstance(value, (int, float))]
    if not numeric:
        return None
    return max(numeric)


def age_ms(value: Any, *, now: int) -> int | None:
    if not isinstance(value, (int, float)):
        return None
    return max(0, now - int(value))


def state_class_for_task(task: dict[str, Any]) -> str:
    status = text(task.get("status"))
    delivery = text(task.get("deliveryStatus"))
    if status in ACTIVE_STATUSES:
        return "active"
    if status in ATTENTION_TASK_STATUSES or delivery == "failed":
        return "attention"
    if status in DONE_STATUSES:
        return "done"
    return "unknown"


def state_class_for_flow(flow: dict[str, Any]) -> str:
    status = text(flow.get("status"))
    summary = flow.get("taskSummary") if isinstance(flow.get("taskSummary"), dict) else {}
    active = int(summary.get("active") or 0)
    failures = int(summary.get("failures") or 0)
    if status in ACTIVE_STATUSES or active > 0:
        return "active"
    if status in ATTENTION_FLOW_STATUSES or failures > 0:
        return "attention"
    if status in DONE_STATUSES:
        return "done"
    return "unknown"


def reason_for_task(task: dict[str, Any]) -> str:
    status = text(task.get("status")).lower()
    delivery = text(task.get("deliveryStatus")).lower()
    combined = " ".join(
        text(task.get(key))
        for key in ("error", "terminalSummary", "progressSummary", "statusDetail")
    ).lower()
    if delivery == "failed":
        return "delivery_failed"
    if "acp_turn_failed" in combined:
        return "acp_turn_failed"
    if status in ATTENTION_TASK_STATUSES:
        return "runtime_failed"
    if status in ACTIVE_STATUSES:
        return "active_runtime"
    if status in DONE_STATUSES:
        return "completed_runtime"
    return "unknown_runtime_state"


def reason_for_flow(flow: dict[str, Any]) -> str:
    status = text(flow.get("status")).lower()
    notify = text(flow.get("notifyPolicy")).lower()
    summary = flow.get("taskSummary") if isinstance(flow.get("taskSummary"), dict) else {}
    active = int(summary.get("active") or 0)
    failures = int(summary.get("failures") or 0)
    detail = " ".join(text(flow.get(key)) for key in ("blockedSummary", "currentStep", "terminalSummary")).lower()
    if failures > 0:
        return "runtime_failed"
    if status in ATTENTION_FLOW_STATUSES:
        if notify == "done_only" or "progress-only" in detail or "final" in detail:
            return "done_only_no_final"
        return "blocked_flow_stale"
    if status in ACTIVE_STATUSES or active > 0:
        return "active_runtime"
    if status in DONE_STATUSES:
        return "completed_runtime"
    return "unknown_runtime_state"


def reason_for_audit(finding: dict[str, Any]) -> str:
    code = text(finding.get("code")).lower()
    severity = text(finding.get("severity")).lower()
    if code == "delivery_failed":
        return "delivery_failed"
    if code == "acp_turn_failed":
        return "acp_turn_failed"
    if code in {"stale_blocked", "blocked_flow_stale"}:
        return "blocked_flow_stale"
    if severity in {"warn", "error"}:
        return "runtime_failed"
    return "unknown_runtime_state"


def task_card(task: dict[str, Any], *, now: int) -> dict[str, Any]:
    status = text(task.get("status"), default="unknown")
    delivery = text(task.get("deliveryStatus"), default="unknown")
    task_id = text(task.get("taskId"))
    run_id = text(task.get("runId"))
    source_id = text(task.get("sourceId"))
    card_id = text(task_id or run_id or source_id, default="unknown")
    raw_label = text(task.get("label") or task.get("task") or "OpenClaw task")
    summary = clipped(task.get("terminalSummary") or task.get("progressSummary") or task.get("error") or "")
    return {
        "sourceKind": "task",
        "kind": "task",
        "id": card_id,
        "taskId": task_id,
        "sourceId": source_id,
        "rawLabel": raw_label,
        "label": clipped(raw_label, budget=96),
        "runtime": text(task.get("runtime"), default="unknown"),
        "status": status,
        "stateClass": state_class_for_task(task),
        "deliveryStatus": delivery,
        "ownerKey": text(task.get("ownerKey")),
        "requesterSessionKey": text(task.get("requesterSessionKey")),
        "childSessionKey": text(task.get("childSessionKey")),
        "runId": text(task.get("runId")),
        "parentFlowId": text(task.get("parentFlowId")),
        "createdAgeMs": age_ms(task.get("createdAt"), now=now),
        "lastEventAgeMs": age_ms(task.get("lastEventAt"), now=now),
        "createdAtMs": task.get("createdAt"),
        "lastEventAtMs": task.get("lastEventAt"),
        "timestampMs": timestamp_ms(task.get("lastEventAt"), task.get("createdAt")),
        "summary": summary,
        "reason": reason_for_task(task),
        "evidenceIds": [card_id],
        "sourceSummaries": [summary] if summary else [],
    }


def flow_card(flow: dict[str, Any], *, now: int) -> dict[str, Any]:
    status = text(flow.get("status"), default="unknown")
    summary = flow.get("taskSummary") if isinstance(flow.get("taskSummary"), dict) else {}
    flow_id = text(flow.get("flowId"), default="unknown")
    raw_label = text(flow.get("goal") or "TaskFlow")
    summary_text = clipped(flow.get("blockedSummary") or flow.get("currentStep") or "")
    return {
        "sourceKind": "flow",
        "kind": "flow",
        "id": flow_id,
        "rawLabel": raw_label,
        "label": clipped(raw_label, budget=96),
        "runtime": "taskflow",
        "status": status,
        "stateClass": state_class_for_flow(flow),
        "deliveryStatus": text(flow.get("notifyPolicy")),
        "ownerKey": text(flow.get("ownerKey")),
        "requesterSessionKey": text((flow.get("requesterOrigin") or {}).get("to") if isinstance(flow.get("requesterOrigin"), dict) else ""),
        "childSessionKey": "",
        "runId": "",
        "parentFlowId": "",
        "createdAgeMs": age_ms(flow.get("createdAt"), now=now),
        "lastEventAgeMs": age_ms(flow.get("updatedAt"), now=now),
        "createdAtMs": flow.get("createdAt"),
        "lastEventAtMs": flow.get("updatedAt"),
        "timestampMs": timestamp_ms(flow.get("updatedAt"), flow.get("createdAt")),
        "summary": summary_text,
        "reason": reason_for_flow(flow),
        "evidenceIds": [flow_id],
        "sourceSummaries": [summary_text] if summary_text else [],
        "taskSummary": {
            "total": int(summary.get("total") or 0),
            "active": int(summary.get("active") or 0),
            "terminal": int(summary.get("terminal") or 0),
            "failures": int(summary.get("failures") or 0),
        },
    }


def audit_cards(audit: dict[str, Any], *, now: int) -> list[dict[str, Any]]:
    cards: list[dict[str, Any]] = []
    findings = audit.get("findings")
    if not isinstance(findings, list):
        return cards
    for finding in findings:
        if not isinstance(finding, dict):
            continue
        kind = text(finding.get("kind"), default="audit")
        token = text(finding.get("token"), default="unknown")
        status = text(finding.get("status"), default="unknown")
        code = text(finding.get("code"), default="unknown")
        summary = clipped(finding.get("detail") or "")
        cards.append(
            {
                "sourceKind": "audit",
                "kind": "audit",
                "id": token,
                "token": token,
                "label": clipped(f"{kind}: {code}", budget=96),
                "runtime": kind,
                "status": status,
                "stateClass": "attention" if finding.get("severity") in {"warn", "error"} else "unknown",
                "deliveryStatus": "",
                "ownerKey": "",
                "requesterSessionKey": "",
                "childSessionKey": "",
                "runId": "",
                "parentFlowId": "",
                "createdAgeMs": None,
                "lastEventAgeMs": finding.get("ageMs"),
                "createdAtMs": None,
                "lastEventAtMs": None,
                "timestampMs": None,
                "summary": summary,
                "severity": text(finding.get("severity"), default="unknown"),
                "code": code,
                "reason": reason_for_audit(finding),
                "evidenceIds": [token],
                "sourceSummaries": [summary] if summary else [],
            }
        )
    return cards


def unique(values: list[str]) -> list[str]:
    out: list[str] = []
    seen: set[str] = set()
    for value in values:
        value = text(value).strip()
        if not value or value in seen:
            continue
        seen.add(value)
        out.append(value)
    return out


def ordered_source_kinds(records: list[dict[str, Any]]) -> list[str]:
    found = set(unique([text(record.get("sourceKind")) for record in records]))
    order = ["flow", "task", "audit"]
    return [kind for kind in order if kind in found] + sorted(found.difference(order))


def task_indexes(cards: list[dict[str, Any]]) -> dict[str, Any]:
    token_to_tasks: dict[str, list[dict[str, Any]]] = {}
    flow_by_id: dict[str, dict[str, Any]] = {}
    for card in cards:
        source_kind = text(card.get("sourceKind"))
        if source_kind == "flow":
            flow_by_id[text(card.get("id"))] = card
            continue
        if source_kind != "task":
            continue
        for token in unique([text(card.get("taskId")), text(card.get("runId")), text(card.get("sourceId")), text(card.get("id"))]):
            token_to_tasks.setdefault(token, []).append(card)
    ambiguous_tokens = {
        token
        for token, task_list in token_to_tasks.items()
        if len({text(task.get("parentFlowId")) for task in task_list if text(task.get("parentFlowId"))}) > 1
    }
    return {"flow_by_id": flow_by_id, "token_to_tasks": token_to_tasks, "ambiguous_tokens": ambiguous_tokens}


def primary_dedupe_key(card: dict[str, Any], indexes: dict[str, Any]) -> str:
    source_kind = text(card.get("sourceKind"))
    if source_kind == "flow":
        return f"flow:{text(card.get('id'), default='unknown')}"
    if source_kind == "task":
        parent_flow = text(card.get("parentFlowId"))
        if parent_flow:
            return f"flow:{parent_flow}"
        for token in [text(card.get("taskId")), text(card.get("runId")), text(card.get("sourceId")), text(card.get("id"))]:
            if token and token not in indexes["ambiguous_tokens"]:
                return f"task:{token}"
        return f"task:{text(card.get('id'), default='unknown')}"
    if source_kind == "audit":
        token = text(card.get("token") or card.get("id"), default="unknown")
        if token in indexes["flow_by_id"]:
            return f"flow:{token}"
        task_list = indexes["token_to_tasks"].get(token) or []
        if token not in indexes["ambiguous_tokens"] and task_list:
            task = task_list[0]
            parent_flow = text(task.get("parentFlowId"))
            if parent_flow:
                return f"flow:{parent_flow}"
            return f"task:{text(task.get('taskId') or task.get('runId') or task.get('sourceId') or task.get('id'), default=token)}"
        return f"audit:{text(card.get('runtime'), default='audit')}:{token}:{text(card.get('code'), default='unknown')}"
    return f"{source_kind or 'unknown'}:{text(card.get('id'), default='unknown')}"


def best_reason(records: list[dict[str, Any]]) -> str:
    return min((text(record.get("reason"), default="unknown_runtime_state") for record in records), key=lambda item: REASON_RANK.get(item, 999))


def first_record(records: list[dict[str, Any]], *kinds: str) -> dict[str, Any] | None:
    allowed = set(kinds)
    for record in records:
        if text(record.get("sourceKind")) in allowed:
            return record
    return records[0] if records else None


def best_summary(records: list[dict[str, Any]], reason: str) -> str:
    for record in records:
        if text(record.get("reason")) == reason and text(record.get("summary")):
            return clipped(record.get("summary"))
    for record in records:
        if text(record.get("summary")):
            return clipped(record.get("summary"))
    return reason.replace("_", " ")


def lifecycle_for_group(display_group: str) -> str:
    return DISPLAY_GROUP_LIFECYCLE.get(display_group, "unknown")


def actionability_for_group(display_group: str) -> str:
    return DISPLAY_GROUP_ACTIONABILITY.get(display_group, "inspect")


def presentation_for_reason(reason: str) -> dict[str, str]:
    details = REASON_PRESENTATION.get(reason, REASON_PRESENTATION["unknown_runtime_state"])
    group = details["group"]
    return {
        "presentationGroup": group,
        "presentationLabel": PRESENTATION_GROUP_LABEL.get(group, "Source Unknown"),
        "whyVisible": details["why"],
        "suggestionKind": details["suggestionKind"],
        "suggestedAction": details["suggestedAction"],
        "suggestedCommand": details["suggestedCommand"],
        "suggestionConfidence": details["confidence"],
    }


def aggregate_records(
    records: list[dict[str, Any]],
    *,
    dedupe_key: str,
    aggregation_policy: str = "primary_identity",
    policy_scope: str = "source_identity",
) -> dict[str, Any]:
    reason = best_reason(records)
    display_status, display_group, next_action = REASON_DISPLAY.get(reason, REASON_DISPLAY["unknown_runtime_state"])
    base = first_record(records, "flow", "task") or records[0]
    label_source = base.get("rawLabel") or base.get("label") or base.get("id")
    source_kinds = ordered_source_kinds(records)
    evidence_ids = unique([item for record in records for item in record.get("evidenceIds", [])])
    source_summaries = unique([item for record in records for item in record.get("sourceSummaries", [])])
    age_values = [record.get("lastEventAgeMs") for record in records if isinstance(record.get("lastEventAgeMs"), (int, float))]
    created_values = [record.get("createdAgeMs") for record in records if isinstance(record.get("createdAgeMs"), (int, float))]
    timestamp_values = [record.get("timestampMs") for record in records if isinstance(record.get("timestampMs"), (int, float))]
    summary = best_summary(records, reason)
    first_seen_age = max(created_values) if created_values else None
    last_seen_age = min(age_values) if age_values else None
    presentation = presentation_for_reason(reason)
    return {
        "cardContract": CARD_CONTRACT_VERSION,
        "sourceTruth": SOURCE_TRUTH,
        "sourceProvenance": SOURCE_PROVENANCE,
        "lifecycleState": lifecycle_for_group(display_group),
        "actionability": actionability_for_group(display_group),
        "teardownPolicy": TEARDOWN_POLICY,
        "policyScope": policy_scope,
        "aggregationPolicy": aggregation_policy,
        "kind": "+".join(source_kinds) or text(base.get("kind"), default="unknown"),
        "sourceKind": "+".join(source_kinds),
        "id": dedupe_key,
        "dedupeKey": dedupe_key,
        "label": compact_title(label_source),
        "displayTitle": compact_title(label_source),
        "runtime": text(base.get("runtime"), default="unknown"),
        "status": display_status,
        "displayStatus": display_status,
        "displayGroup": display_group,
        "stateClass": DISPLAY_GROUP_STATE.get(display_group, "unknown"),
        "reason": reason,
        "nextAction": next_action,
        "deliveryStatus": text(base.get("deliveryStatus")),
        "ownerKey": text(base.get("ownerKey")),
        "requesterSessionKey": text(base.get("requesterSessionKey")),
        "childSessionKey": text(base.get("childSessionKey")),
        "runId": text(base.get("runId")),
        "parentFlowId": text(base.get("parentFlowId") or (base.get("id") if text(base.get("sourceKind")) == "flow" else "")),
        "createdAgeMs": first_seen_age,
        "lastEventAgeMs": last_seen_age,
        "firstSeenAgeMs": first_seen_age,
        "lastSeenAgeMs": last_seen_age,
        "timestampMs": max(timestamp_values) if timestamp_values else None,
        "summary": summary,
        "evidenceIds": evidence_ids,
        "sourceKinds": source_kinds,
        "sourceCount": len(records),
        "sourceSummaries": source_summaries,
        **presentation,
        "skeleton": False,
        "skeletonReason": "",
        "suppressed": False,
    }


def secondary_merge_key(card: dict[str, Any]) -> str:
    if "flow" not in set(card.get("sourceKinds") or []):
        return ""
    label = normalize_label(card.get("displayTitle") or card.get("label"))
    owner = text(card.get("ownerKey"))
    runtime = text(card.get("runtime"))
    status = text(card.get("displayStatus"))
    reason = text(card.get("reason"))
    actionability = text(card.get("actionability"))
    delivery = text(card.get("deliveryStatus"))
    if not label or not owner or not runtime or not status or not reason:
        return ""
    return f"logical-flow:{runtime}:{owner}:{label}:{status}:{reason}:{actionability}:{delivery}"


def secondary_merge(cards: list[dict[str, Any]]) -> list[dict[str, Any]]:
    buckets: dict[str, list[dict[str, Any]]] = {}
    out: list[dict[str, Any]] = []
    for card in cards:
        key = secondary_merge_key(card)
        if key:
            buckets.setdefault(key, []).append(card)
        else:
            out.append(card)
    for key, group in buckets.items():
        if len(group) == 1:
            out.extend(group)
            continue
        timestamps = [card.get("timestampMs") for card in group if isinstance(card.get("timestampMs"), (int, float))]
        if timestamps and max(timestamps) - min(timestamps) <= DAY_MS:
            records = [record for card in group for record in card.get("_records", [])]
            merged = aggregate_records(
                records,
                dedupe_key=key,
                aggregation_policy="logical_flow_day_merge",
                policy_scope="local_overlay",
            )
            merged["_records"] = records
            out.append(merged)
        else:
            out.extend(group)
    return out


def pulse_subject(card: dict[str, Any]) -> str:
    values = (
        (
            card.get("logicalSubject"),
            card.get("parentFlowId"),
            card.get("displayTitle"),
            card.get("label"),
            card.get("runId"),
            card.get("childSessionKey"),
            card.get("dedupeKey"),
            card.get("id"),
        )
        if text(card.get("kind")) == "flow"
        else (
            card.get("logicalSubject"),
            card.get("displayTitle"),
            card.get("label"),
            card.get("parentFlowId"),
            card.get("runId"),
            card.get("childSessionKey"),
            card.get("dedupeKey"),
            card.get("id"),
        )
    )
    for value in values:
        subject = normalize_label(value)
        if subject:
            return subject
    return "unknown"


def pulse_group_key(card: dict[str, Any]) -> str:
    presentation = text(card.get("presentationGroup"))
    runtime = text(card.get("runtime"))
    reason = text(card.get("reason"))
    actionability = text(card.get("actionability"))
    next_action = normalize_label(card.get("nextAction"))
    subject = pulse_subject(card)
    if not presentation or not runtime or not reason or not actionability or not subject:
        return ""
    return f"pulse:{presentation}:{runtime}:{reason}:{actionability}:{next_action}:{subject}"


def merge_pulse_group(key: str, group: list[dict[str, Any]]) -> dict[str, Any]:
    ordered = sorted(group, key=card_sort_key)
    representative = dict(ordered[0])
    records = [record for card in ordered for record in card.get("_records", [])]
    evidence_ids = unique([item for card in ordered for item in card.get("evidenceIds", [])])
    source_kinds = unique([item for card in ordered for item in card.get("sourceKinds", [])])
    source_summaries = unique([item for card in ordered for item in card.get("sourceSummaries", [])])
    card_summaries = unique([item for card in ordered for item in [card.get("summary")] if text(item)])
    created_values = [card.get("firstSeenAgeMs") or card.get("createdAgeMs") for card in ordered if isinstance(card.get("firstSeenAgeMs") or card.get("createdAgeMs"), (int, float))]
    last_values = [card.get("lastSeenAgeMs") or card.get("lastEventAgeMs") for card in ordered if isinstance(card.get("lastSeenAgeMs") or card.get("lastEventAgeMs"), (int, float))]
    timestamp_values = [card.get("timestampMs") for card in ordered if isinstance(card.get("timestampMs"), (int, float))]
    source_count = sum(int(card.get("sourceCount") or 0) or 1 for card in ordered)

    representative.update(
        {
            "id": key,
            "dedupeKey": key,
            "logicalGroupKey": key,
            "groupedRecordCount": len(ordered),
            "rawCardCount": len(ordered),
            "sourceCount": source_count,
            "evidenceIds": evidence_ids,
            "sourceKinds": source_kinds,
            "sourceSummaries": unique([*source_summaries, *card_summaries]),
            "groupedEvidenceIds": evidence_ids,
            "groupedSourceKinds": source_kinds,
            "groupedSourceSummaries": unique([*card_summaries, *source_summaries]),
            "firstSeenAgeMs": max(created_values) if created_values else representative.get("firstSeenAgeMs"),
            "createdAgeMs": max(created_values) if created_values else representative.get("createdAgeMs"),
            "lastSeenAgeMs": min(last_values) if last_values else representative.get("lastSeenAgeMs"),
            "lastEventAgeMs": min(last_values) if last_values else representative.get("lastEventAgeMs"),
            "timestampMs": max(timestamp_values) if timestamp_values else representative.get("timestampMs"),
            "aggregationPolicy": text(representative.get("aggregationPolicy"), default="primary_identity") + "+pulse_group",
            "policyScope": "local_overlay",
            "_records": records,
        }
    )
    return representative


def pulse_group(cards: list[dict[str, Any]]) -> list[dict[str, Any]]:
    buckets: dict[str, list[dict[str, Any]]] = {}
    out: list[dict[str, Any]] = []
    for card in cards:
        key = pulse_group_key(card)
        if key:
            buckets.setdefault(key, []).append(card)
        else:
            out.append(card)
    for key, group in buckets.items():
        if len(group) == 1:
            group[0]["groupedRecordCount"] = 1
            out.extend(group)
        else:
            out.append(merge_pulse_group(key, group))
    return out


def canonical_cards_with_counts(cards: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], dict[str, int]]:
    indexes = task_indexes(cards)
    buckets: dict[str, list[dict[str, Any]]] = {}
    for card in cards:
        buckets.setdefault(primary_dedupe_key(card, indexes), []).append(card)
    aggregated = []
    for key, records in buckets.items():
        card = aggregate_records(records, dedupe_key=key)
        card["_records"] = records
        aggregated.append(card)
    merged = secondary_merge(aggregated)
    raw_runtime_card_count = len(merged)
    grouped = pulse_group(merged)
    for card in merged:
        card.pop("_records", None)
    for card in grouped:
        card.pop("_records", None)
    visible_runtime_card_count = len(grouped)
    grouped_runtime_card_count = sum(1 for card in grouped if int(card.get("groupedRecordCount") or 1) > 1)
    return grouped, {
        "rawRuntimeCardCount": raw_runtime_card_count,
        "visibleRuntimeCardCount": visible_runtime_card_count,
        "groupedRuntimeCardCount": grouped_runtime_card_count,
        "hiddenRuntimeCardCount": max(raw_runtime_card_count - visible_runtime_card_count, 0),
    }


def canonical_cards(cards: list[dict[str, Any]]) -> list[dict[str, Any]]:
    grouped, _ = canonical_cards_with_counts(cards)
    return grouped


def apply_runtime_counts(cards: list[dict[str, Any]], counts: dict[str, int]) -> None:
    for card in cards:
        card["rawCardCount"] = counts.get("rawRuntimeCardCount", 0)
        card["visibleCardCount"] = counts.get("visibleRuntimeCardCount", 0)
        card["groupedCardCount"] = counts.get("groupedRuntimeCardCount", 0)
        card["hiddenCardCount"] = counts.get("hiddenRuntimeCardCount", 0)


def list_from_payload(payload: dict[str, Any], key: str) -> list[dict[str, Any]]:
    values = payload.get(key)
    if not isinstance(values, list):
        return []
    return [item for item in values if isinstance(item, dict)]


def build_snapshot(
    payloads: SourcePayloads,
    *,
    limit: int = DEFAULT_LIMIT,
    include_done: bool = False,
    include_stale: bool = True,
) -> dict[str, Any]:
    current = now_ms()
    task_cards = [task_card(item, now=current) for item in list_from_payload(payloads.tasks, "tasks")]
    flow_cards = [flow_card(item, now=current) for item in list_from_payload(payloads.flows, "flows")]
    cards, runtime_counts = canonical_cards_with_counts([*task_cards, *flow_cards, *audit_cards(payloads.audit, now=current)])

    if not include_done:
        cards = [card for card in cards if card.get("stateClass") != "done"]
        runtime_counts = {
            **runtime_counts,
            "rawRuntimeCardCount": sum(int(card.get("groupedRecordCount") or 1) for card in cards),
            "visibleRuntimeCardCount": len(cards),
            "groupedRuntimeCardCount": sum(1 for card in cards if int(card.get("groupedRecordCount") or 1) > 1),
        }
        runtime_counts["hiddenRuntimeCardCount"] = max(runtime_counts["rawRuntimeCardCount"] - runtime_counts["visibleRuntimeCardCount"], 0)

    if not include_stale:
        cards = [
            card
            for card in cards
            if card.get("stateClass") == "active"
            or not isinstance(card.get("lastEventAgeMs"), (int, float))
            or int(card["lastEventAgeMs"]) <= TERMINAL_CARD_VISIBLE_MS
        ]
        runtime_counts = {
            **runtime_counts,
            "visibleRuntimeCardCount": len(cards),
            "groupedRuntimeCardCount": sum(1 for card in cards if int(card.get("groupedRecordCount") or 1) > 1),
        }
        runtime_counts["hiddenRuntimeCardCount"] = max(
            runtime_counts["rawRuntimeCardCount"] - runtime_counts["visibleRuntimeCardCount"], 0
        )

    apply_runtime_counts(cards, runtime_counts)
    for card in cards:
        card["sourceProvenance"] = payloads.provenance
    cards.sort(key=card_sort_key)
    selected = cards[:max(0, limit)]
    runtime_counts["totalVisibleRuntimeCardCount"] = len(cards)
    runtime_counts["visibleRuntimeCardCount"] = len(selected)
    apply_runtime_counts(selected, runtime_counts)
    return {
        "schemaVersion": 1,
        "cardContract": CARD_CONTRACT_VERSION,
        "source": "openclaw-runtime",
        "generatedAt": utc_now(),
        "summary": summarize(cards, runtime_counts=runtime_counts),
        "cards": selected,
        "truncated": len(cards) > len(selected),
        "boundaries": [
            "Read-only snapshot of OpenClaw task, TaskFlow, and audit state.",
            "Live state is read through one short-lived mode=ro SQLite connection; no OpenClaw CLI runs.",
            "Cards represent source-reported runtime facts plus explicit presentation policy.",
            "Cards should disappear when the source stops reporting them; source failure is reported as attention.",
            "Does not prove Discord visual delivery or tmux pane state.",
            "Does not mutate Gateway, tasks, flows, sessions, tmux, or Discord.",
        ],
    }


def card_sort_key(card: dict[str, Any]) -> tuple[int, int, int, str]:
    presentation = text(card.get("presentationGroup"))
    rank = PRESENTATION_RANK.get(presentation)
    if rank is None:
        rank = {"needs_attention": 1, "active": 0, "unknown": 4, "completed": 7}.get(text(card.get("displayGroup")), 8)
    reason_rank = REASON_RANK.get(text(card.get("reason")), 999)
    age = card.get("lastEventAgeMs")
    age_value = int(age) if isinstance(age, (int, float)) else 10**18
    return (rank, reason_rank, age_value, text(card.get("label")).lower())


def summarize(cards: list[dict[str, Any]], *, runtime_counts: dict[str, int] | None = None) -> dict[str, Any]:
    by_state: dict[str, int] = {}
    by_group: dict[str, int] = {}
    by_presentation_group: dict[str, int] = {}
    by_runtime: dict[str, int] = {}
    skeletons = 0
    suppressed = 0
    grouped = 0
    for card in cards:
        state = text(card.get("stateClass"), default="unknown")
        group = text(card.get("displayGroup"), default="unknown")
        presentation_group = text(card.get("presentationGroup"), default="unknown")
        runtime = text(card.get("runtime"), default="unknown")
        by_state[state] = by_state.get(state, 0) + 1
        by_group[group] = by_group.get(group, 0) + 1
        by_presentation_group[presentation_group] = by_presentation_group.get(presentation_group, 0) + 1
        by_runtime[runtime] = by_runtime.get(runtime, 0) + 1
        if card.get("skeleton"):
            skeletons += 1
        if card.get("suppressed"):
            suppressed += 1
        if int(card.get("groupedRecordCount") or 1) > 1:
            grouped += 1
    summary = {
        "total": len(cards),
        "byState": by_state,
        "byGroup": by_group,
        "byPresentationGroup": by_presentation_group,
        "byRuntime": by_runtime,
        "skeletons": skeletons,
        "suppressed": suppressed,
        "grouped": grouped,
    }
    if runtime_counts:
        summary.update(runtime_counts)
    return summary


def render_markdown(snapshot: dict[str, Any]) -> str:
    summary = snapshot.get("summary") or {}
    by_state = summary.get("byState") or {}
    lines = [
        "OpenClaw Runtime Snapshot",
        f"State: {by_state.get('attention', 0)} attention, {by_state.get('active', 0)} active, {by_state.get('unknown', 0)} unknown",
        "",
    ]
    for card in snapshot.get("cards", []):
        label = card.get("displayTitle") or card.get("label") or card.get("id")
        detail = card.get("summary") or card.get("displayStatus") or card.get("status")
        lines.append(f"- [{card.get('displayStatus')}] {card.get('runtime')} `{card.get('dedupeKey') or card.get('id')}` {label}: {detail}")
    if snapshot.get("truncated"):
        lines.append("- snapshot truncated; rerun with a higher limit")
    return "\n".join(lines) + "\n"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Render OpenClaw runtime state as read-only cockpit cards.")
    parser.add_argument("--tasks-json", help="Fixture or pre-captured `openclaw tasks list --json` output")
    parser.add_argument("--flows-json", help="Fixture or pre-captured `openclaw tasks flow list --json` output")
    parser.add_argument("--audit-json", help="Fixture or pre-captured `openclaw tasks audit --json` output")
    parser.add_argument(
        "--state-db",
        help="Explicit OpenClaw state database path (default: $OPENCLAW_STATE_DIR/state/openclaw.sqlite or ~/.openclaw/state/openclaw.sqlite)",
    )
    parser.add_argument("--include-done", action="store_true", help="Include succeeded/cancelled cards")
    parser.add_argument("--include-stale", action="store_true", help="Include non-active cards older than three minutes")
    parser.add_argument("--limit", type=int, default=DEFAULT_LIMIT)
    parser.add_argument(
        "--timeout",
        type=float,
        default=DEFAULT_DB_TIMEOUT_SECONDS,
        help="SQLite busy timeout in seconds for the read-only snapshot connection",
    )
    parser.add_argument("--format", choices=["json", "markdown"], default="json")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        snapshot = build_snapshot(
            load_sources(args),
            limit=args.limit,
            include_done=args.include_done,
            include_stale=args.include_stale,
        )
    except (SnapshotError, sqlite3.Error, OSError) as exc:
        payload = {"schemaVersion": 1, "source": "openclaw-runtime", "ok": False, "error": " ".join(str(exc).split())[:2048]}
        print(json.dumps(payload, indent=2, sort_keys=True))
        print(payload["error"], file=sys.stderr)
        return 1
    if args.format == "markdown":
        sys.stdout.write(render_markdown(snapshot))
    else:
        print(json.dumps(snapshot, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
