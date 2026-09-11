"""Tests for the read-only OpenClaw runtime snapshot adapter.

The SQLite coverage below builds real, disposable databases under
`tempfile.TemporaryDirectory()` rather than mocking `sqlite3`: the behaviour under
test is what SQLite itself does with a `mode=ro` URI, which a mock cannot
falsify. The live database at `~/.openclaw/state/openclaw.sqlite` is never opened,
read, or written by these tests.
"""

from __future__ import annotations

import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from helpers.openclaw_runtime import cockpit_snapshot

FIXTURES = Path(__file__).resolve().parents[1] / "openclaw_runtime/tests/fixtures"
SCRIPT = Path(__file__).resolve().parents[1] / "openclaw_runtime/cockpit_snapshot.py"

# Copied verbatim from OpenClaw 2026.6.6's generated state schema, which the
# installed database matches exactly. Tests assert against the real DDL so a
# schema drift in the product shows up as a failure here.
TASK_RUNS_DDL = """
CREATE TABLE task_runs (
  task_id TEXT NOT NULL PRIMARY KEY,
  runtime TEXT NOT NULL,
  task_kind TEXT,
  source_id TEXT,
  requester_session_key TEXT,
  owner_key TEXT NOT NULL,
  scope_kind TEXT NOT NULL,
  child_session_key TEXT,
  parent_flow_id TEXT,
  parent_task_id TEXT,
  agent_id TEXT,
  run_id TEXT,
  label TEXT,
  task TEXT NOT NULL,
  status TEXT NOT NULL,
  delivery_status TEXT NOT NULL,
  notify_policy TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  started_at INTEGER,
  ended_at INTEGER,
  last_event_at INTEGER,
  cleanup_after INTEGER,
  error TEXT,
  progress_summary TEXT,
  terminal_summary TEXT,
  terminal_outcome TEXT
)
"""
TASK_DELIVERY_STATE_DDL = """
CREATE TABLE task_delivery_state (
  task_id TEXT NOT NULL PRIMARY KEY,
  requester_origin_json TEXT,
  last_notified_event_at INTEGER,
  FOREIGN KEY (task_id) REFERENCES task_runs(task_id) ON DELETE CASCADE
)
"""
FLOW_RUNS_DDL = """
CREATE TABLE flow_runs (
  flow_id TEXT NOT NULL PRIMARY KEY,
  shape TEXT,
  sync_mode TEXT NOT NULL DEFAULT 'managed',
  owner_key TEXT NOT NULL,
  requester_origin_json TEXT,
  controller_id TEXT,
  revision INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  notify_policy TEXT NOT NULL,
  goal TEXT NOT NULL,
  current_step TEXT,
  blocked_task_id TEXT,
  blocked_summary TEXT,
  state_json TEXT,
  wait_json TEXT,
  cancel_requested_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  ended_at INTEGER
)
"""
# Same table with the NOT NULL relaxed, so the documented null-sync_mode fallback
# can be exercised directly and not only through its reachable '' equivalent.
FLOW_RUNS_DDL_NULLABLE_SYNC_MODE = FLOW_RUNS_DDL.replace(
    "sync_mode TEXT NOT NULL DEFAULT 'managed'", "sync_mode TEXT"
)

OWNER = "agent:main:discord:channel:1"
TASK_DEFAULTS: dict[str, object] = {
    "task_id": "task-1",
    "runtime": "acp",
    "task_kind": None,
    "source_id": None,
    "requester_session_key": OWNER,
    "owner_key": OWNER,
    "scope_kind": "session",
    "child_session_key": None,
    "parent_flow_id": None,
    "parent_task_id": None,
    "agent_id": None,
    "run_id": None,
    "label": None,
    "task": "do the thing",
    "status": "running",
    "delivery_status": "not_applicable",
    "notify_policy": "done_only",
    "created_at": 0,
    "started_at": None,
    "ended_at": None,
    "last_event_at": None,
    "cleanup_after": None,
    "error": None,
    "progress_summary": None,
    "terminal_summary": None,
    "terminal_outcome": None,
}
FLOW_DEFAULTS: dict[str, object] = {
    "flow_id": "flow-1",
    "shape": None,
    "sync_mode": "managed",
    "owner_key": OWNER,
    "requester_origin_json": None,
    "controller_id": None,
    "revision": 0,
    "status": "running",
    "notify_policy": "done_only",
    "goal": "flow goal",
    "current_step": None,
    "blocked_task_id": None,
    "blocked_summary": None,
    "state_json": None,
    "wait_json": None,
    "cancel_requested_at": None,
    "created_at": 0,
    "updated_at": 0,
    "ended_at": None,
}


def build_state_db(
    path: Path,
    *,
    task_runs_ddl: str = TASK_RUNS_DDL,
    delivery_ddl: str = TASK_DELIVERY_STATE_DDL,
    flow_runs_ddl: str = FLOW_RUNS_DDL,
    skip_tables: tuple[str, ...] = (),
) -> None:
    con = sqlite3.connect(str(path))
    try:
        for name, ddl in (
            ("task_runs", task_runs_ddl),
            ("task_delivery_state", delivery_ddl),
            ("flow_runs", flow_runs_ddl),
        ):
            if name in skip_tables:
                continue
            con.execute(ddl)
        con.commit()
    finally:
        con.close()


def write_rows(path: Path, *, tasks: tuple[dict, ...] = (), flows: tuple[dict, ...] = ()) -> None:
    con = sqlite3.connect(str(path))
    try:
        for table, defaults, overrides in (
            ("task_runs", TASK_DEFAULTS, tasks),
            ("flow_runs", FLOW_DEFAULTS, flows),
        ):
            for override in overrides:
                unknown = set(override) - set(defaults)
                if unknown:
                    raise AssertionError(f"unknown {table} column(s) in fixture row: {sorted(unknown)}")
                row = {**defaults, **override}
                columns = ", ".join(row)
                placeholders = ", ".join("?" for _ in row)
                con.execute(f"INSERT INTO {table} ({columns}) VALUES ({placeholders})", tuple(row.values()))
        con.commit()
    finally:
        con.close()


def audit_codes(payloads: cockpit_snapshot.SourcePayloads, token: str) -> set[str]:
    return {
        finding["code"]
        for finding in payloads.audit["findings"]
        if finding.get("token") == token
    }


def audit_finding(payloads: cockpit_snapshot.SourcePayloads, token: str, code: str) -> dict:
    for finding in payloads.audit["findings"]:
        if finding.get("token") == token and finding["code"] == code:
            return finding
    raise AssertionError(f"no {code} finding for {token}: {payloads.audit['findings']}")


class CockpitSnapshotTests(unittest.TestCase):
    def test_live_snapshot_drops_old_non_active_cards(self) -> None:
        now = cockpit_snapshot.now_ms()
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={"tasks": [
                {"taskId": "active", "status": "running", "lastEventAt": now - 600_000},
                {"taskId": "fresh-failure", "status": "failed", "lastEventAt": now - 60_000},
                {"taskId": "old-failure", "status": "failed", "lastEventAt": now - 600_000},
            ]},
            flows={"flows": []},
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads, include_stale=False)

        ids = {card["id"] for card in snapshot["cards"]}
        self.assertIn("task:active", ids)
        self.assertEqual(snapshot["summary"]["byState"], {"attention": 1, "active": 1})
        self.assertEqual(snapshot["summary"]["visibleRuntimeCardCount"], 2)
        self.assertGreaterEqual(snapshot["summary"]["hiddenRuntimeCardCount"], 1)

    def test_build_snapshot_classifies_live_runtime_state(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks=cockpit_snapshot.load_json_file(FIXTURES / "tasks.json"),
            flows=cockpit_snapshot.load_json_file(FIXTURES / "flows.json"),
            audit=cockpit_snapshot.load_json_file(FIXTURES / "audit.json"),
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)

        self.assertEqual(snapshot["source"], "openclaw-runtime")
        self.assertEqual(snapshot["cardContract"], "runtime-card.v1")
        by_state = snapshot["summary"]["byState"]
        self.assertEqual(by_state["attention"], 3)
        self.assertEqual(by_state["active"], 2)
        self.assertEqual(by_state, {"attention": 3, "active": 2})
        self.assertNotIn("task:task-done", {card["id"] for card in snapshot["cards"]})
        by_presentation = snapshot["summary"]["byPresentationGroup"]
        self.assertEqual(by_presentation["current_work"], 2)
        self.assertEqual(by_presentation["delivery_handoff"], 2)
        self.assertEqual(by_presentation["needs_decision"], 1)
        self.assertNotIn("route_health", by_presentation)
        self.assertEqual(snapshot["summary"]["skeletons"], 0)
        self.assertEqual(snapshot["summary"]["suppressed"], 0)

        cards = {card["id"]: card for card in snapshot["cards"]}
        self.assertEqual(cards["task:task-running"]["stateClass"], "active")
        self.assertEqual(cards["task:task-delivery-failed"]["stateClass"], "attention")
        self.assertEqual(cards["task:task-delivery-failed"]["reason"], "delivery_failed")
        self.assertEqual(cards["task:task-delivery-failed"]["presentationGroup"], "delivery_handoff")
        self.assertEqual(cards["task:task-delivery-failed"]["presentationLabel"], "Delivery / Handoff")
        self.assertIn("final delivery", cards["task:task-delivery-failed"]["whyVisible"])
        self.assertEqual(cards["task:task-delivery-failed"]["suggestionKind"], "inspect_delivery")
        self.assertFalse(cards["task:task-delivery-failed"]["skeleton"])
        self.assertFalse(cards["task:task-delivery-failed"]["suppressed"])
        self.assertEqual(cards["task:task-delivery-failed"]["sourceKinds"], ["task", "audit"])
        self.assertEqual(cards["task:task-delivery-failed"]["sourceCount"], 2)
        self.assertEqual(cards["task:task-delivery-failed"]["cardContract"], "runtime-card.v1")
        self.assertEqual(cards["task:task-delivery-failed"]["sourceTruth"], "reported_by_openclaw")
        self.assertEqual(cards["task:task-delivery-failed"]["lifecycleState"], "needs_attention")
        self.assertEqual(cards["task:task-delivery-failed"]["actionability"], "operator_action")
        self.assertEqual(cards["task:task-delivery-failed"]["teardownPolicy"], "drop_when_source_absent")
        self.assertEqual(cards["task:task-delivery-failed"]["policyScope"], "source_identity")
        self.assertEqual(cards["task:task-delivery-failed"]["aggregationPolicy"], "primary_identity")
        self.assertEqual(cards["flow:flow-blocked"]["stateClass"], "attention")
        self.assertEqual(cards["flow:flow-active"]["stateClass"], "active")
        self.assertEqual(cards["flow:flow-active"]["lifecycleState"], "active")
        self.assertEqual(cards["flow:flow-active"]["actionability"], "watch")

    def test_include_done_keeps_terminal_cards(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks=cockpit_snapshot.load_json_file(FIXTURES / "tasks.json"),
            flows=cockpit_snapshot.load_json_file(FIXTURES / "flows.json"),
            audit=cockpit_snapshot.load_json_file(FIXTURES / "audit.json"),
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads, include_done=True)

        cards = {card["id"]: card for card in snapshot["cards"]}
        self.assertIn("task:task-done", cards)
        self.assertEqual(cards["task:task-done"]["lifecycleState"], "resolved")
        self.assertEqual(cards["task:task-done"]["actionability"], "none")

    def test_delivery_failed_audit_rekeys_to_parent_flow(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={
                "tasks": [
                    {
                        "taskId": "child-1",
                        "runtime": "codex",
                        "label": "child worker",
                        "status": "succeeded",
                        "deliveryStatus": "not_applicable",
                        "parentFlowId": "flow-1",
                        "runId": "run-1",
                        "createdAt": 1_000,
                        "lastEventAt": 2_000,
                    }
                ]
            },
            flows={
                "flows": [
                    {
                        "flowId": "flow-1",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "succeeded",
                        "goal": "committee lane",
                        "createdAt": 1_000,
                        "updatedAt": 3_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 0},
                    }
                ]
            },
            audit={
                "findings": [
                    {
                        "kind": "task",
                        "severity": "warn",
                        "code": "delivery_failed",
                        "detail": "terminal update failed",
                        "status": "succeeded",
                        "token": "child-1",
                    }
                ]
            },
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        self.assertEqual(len(snapshot["cards"]), 1)
        card = snapshot["cards"][0]
        self.assertEqual(card["dedupeKey"], "flow:flow-1")
        self.assertEqual(card["reason"], "delivery_failed")
        self.assertEqual(card["displayGroup"], "needs_attention")
        self.assertEqual(card["presentationGroup"], "delivery_handoff")
        self.assertEqual(card["sourceKinds"], ["flow", "task", "audit"])

    def test_secondary_merge_collapses_repeated_logical_flows(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={"tasks": []},
            flows={
                "flows": [
                    {
                        "flowId": "flow-a",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "goal": "backend-corpus-packet-fable",
                        "blockedSummary": "first blocked copy",
                        "createdAt": 1_000,
                        "updatedAt": 5_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 0},
                    },
                    {
                        "flowId": "flow-b",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "goal": "backend-corpus-packet-fable",
                        "blockedSummary": "second blocked copy",
                        "createdAt": 2_000,
                        "updatedAt": 7_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 0},
                    },
                ]
            },
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        self.assertEqual(len(snapshot["cards"]), 1)
        card = snapshot["cards"][0]
        self.assertTrue(card["dedupeKey"].startswith("logical-flow:taskflow:agent:main:discord:channel:1:backend-corpus-packet-fable"))
        self.assertEqual(card["sourceCount"], 2)
        self.assertEqual(card["evidenceIds"], ["flow-a", "flow-b"])
        self.assertEqual(card["policyScope"], "local_overlay")
        self.assertEqual(card["aggregationPolicy"], "logical_flow_day_merge")

    def test_secondary_merge_keeps_far_apart_flows_separate(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={"tasks": []},
            flows={
                "flows": [
                    {
                        "flowId": "flow-a",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "goal": "reused label",
                        "createdAt": 1_000,
                        "updatedAt": 5_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 0},
                    },
                    {
                        "flowId": "flow-b",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "goal": "reused label",
                        "createdAt": 200_000_000,
                        "updatedAt": 200_000_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 0},
                    },
                ]
            },
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        self.assertEqual({card["dedupeKey"] for card in snapshot["cards"]}, {"flow:flow-a", "flow:flow-b"})

    def test_secondary_merge_keeps_different_reasons_separate(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={"tasks": []},
            flows={
                "flows": [
                    {
                        "flowId": "flow-a",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "notifyPolicy": "done_only",
                        "goal": "reused label",
                        "blockedSummary": "needs final delivery",
                        "createdAt": 1_000,
                        "updatedAt": 5_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 0},
                    },
                    {
                        "flowId": "flow-b",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "goal": "reused label",
                        "blockedSummary": "failed child",
                        "createdAt": 2_000,
                        "updatedAt": 7_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 1},
                    },
                ]
            },
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        self.assertEqual({card["dedupeKey"] for card in snapshot["cards"]}, {"flow:flow-a", "flow:flow-b"})
        self.assertEqual({card["reason"] for card in snapshot["cards"]}, {"done_only_no_final", "runtime_failed"})

    def test_pulse_grouping_collapses_repeated_task_failures_without_conflating_source_count(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={
                "tasks": [
                    {
                        "taskId": "qmd-1",
                        "runtime": "qmd",
                        "label": "qmd-sidecar-live-shadow-collector-step06b",
                        "status": "failed",
                        "error": "sidecar exited 1",
                        "createdAt": 1_000,
                        "lastEventAt": 4_000,
                    },
                    {
                        "taskId": "qmd-2",
                        "runtime": "qmd",
                        "label": "qmd-sidecar-live-shadow-collector-step06b",
                        "status": "failed",
                        "error": "sidecar exited 1",
                        "createdAt": 2_000,
                        "lastEventAt": 6_000,
                    },
                    {
                        "taskId": "plugin-1",
                        "runtime": "openclaw",
                        "label": "plugin:memory-core",
                        "status": "failed",
                        "error": "plugin load failed",
                        "createdAt": 3_000,
                        "lastEventAt": 7_000,
                    },
                    {
                        "taskId": "plugin-2",
                        "runtime": "openclaw",
                        "label": "plugin:memory-core",
                        "status": "failed",
                        "error": "plugin load failed",
                        "createdAt": 4_000,
                        "lastEventAt": 8_000,
                    },
                ]
            },
            flows={"flows": []},
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        self.assertEqual(snapshot["summary"]["rawRuntimeCardCount"], 4)
        self.assertEqual(snapshot["summary"]["visibleRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["hiddenRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["groupedRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["grouped"], 2)

        cards = {card["label"]: card for card in snapshot["cards"]}
        qmd = cards["qmd-sidecar-live-shadow-collector-step06b"]
        self.assertEqual(qmd["sourceCount"], 2)
        self.assertEqual(qmd["groupedRecordCount"], 2)
        self.assertEqual(qmd["rawCardCount"], 4)
        self.assertEqual(qmd["visibleCardCount"], 2)
        self.assertEqual(qmd["groupedCardCount"], 2)
        self.assertEqual(qmd["hiddenCardCount"], 2)
        self.assertEqual(qmd["sourceKinds"], ["task"])
        self.assertEqual(qmd["evidenceIds"], ["qmd-2", "qmd-1"])
        self.assertIn("logicalGroupKey", qmd)
        self.assertEqual(qmd["groupedEvidenceIds"], ["qmd-2", "qmd-1"])
        self.assertIn("pulse_group", qmd["aggregationPolicy"])

        plugin = cards["plugin:memory-core"]
        self.assertEqual(plugin["sourceCount"], 2)
        self.assertEqual(plugin["groupedRecordCount"], 2)
        self.assertEqual(plugin["evidenceIds"], ["plugin-2", "plugin-1"])

        self.assertEqual(
            sum(int(card.get("groupedRecordCount") or 1) for card in snapshot["cards"]),
            snapshot["summary"]["rawRuntimeCardCount"],
        )

    def test_pulse_grouping_keeps_distinct_failed_jobs_separate(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={
                "tasks": [
                    {
                        "taskId": "img-1",
                        "runtime": "cli",
                        "label": "Image generation",
                        "status": "failed",
                        "error": "terminated",
                        "childSessionKey": "agent:main:discord:channel:1501624552558694571",
                        "createdAt": 1_000,
                        "lastEventAt": 2_000,
                    },
                    {
                        "taskId": "vid-1",
                        "runtime": "cli",
                        "label": "Video render",
                        "status": "failed",
                        "error": "terminated",
                        "childSessionKey": "agent:main:discord:channel:9999999999999999999",
                        "createdAt": 3_000,
                        "lastEventAt": 4_000,
                    },
                ]
            },
            flows={"flows": []},
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)

        self.assertEqual(snapshot["summary"]["rawRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["visibleRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["groupedRuntimeCardCount"], 0)
        self.assertEqual({card["label"] for card in snapshot["cards"]}, {"Image generation", "Video render"})
        self.assertEqual({card["dedupeKey"] for card in snapshot["cards"]}, {"task:img-1", "task:vid-1"})

    def test_pulse_grouping_keeps_different_next_actions_separate(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={
                "tasks": [
                    {
                        "taskId": "route-1",
                        "runtime": "acp",
                        "label": "gemini lane",
                        "status": "failed",
                        "error": "ACP_TURN_FAILED permission prompt unavailable",
                        "createdAt": 1_000,
                        "lastEventAt": 2_000,
                    },
                    {
                        "taskId": "route-2",
                        "runtime": "acp",
                        "label": "gemini lane",
                        "status": "failed",
                        "error": "launch failed",
                        "createdAt": 3_000,
                        "lastEventAt": 4_000,
                    },
                ]
            },
            flows={"flows": []},
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)

        self.assertEqual(snapshot["summary"]["rawRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["visibleRuntimeCardCount"], 2)
        self.assertEqual(snapshot["summary"]["groupedRuntimeCardCount"], 0)
        self.assertEqual({card["dedupeKey"] for card in snapshot["cards"]}, {"task:route-1", "task:route-2"})

    def test_reason_precedence_prefers_acp_failure_over_done_only_flow(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={
                "tasks": [
                    {
                        "taskId": "child-1",
                        "runtime": "acp",
                        "label": "gemini child",
                        "status": "failed",
                        "error": "ACP_TURN_FAILED permission prompt unavailable",
                        "parentFlowId": "flow-1",
                        "createdAt": 1_000,
                        "lastEventAt": 4_000,
                    }
                ]
            },
            flows={
                "flows": [
                    {
                        "flowId": "flow-1",
                        "ownerKey": "agent:main:discord:channel:1",
                        "status": "blocked",
                        "notifyPolicy": "done_only",
                        "goal": "phase2-committee-gemini",
                        "blockedSummary": "completion ended with progress-only text",
                        "createdAt": 1_000,
                        "updatedAt": 5_000,
                        "taskSummary": {"total": 1, "active": 0, "terminal": 1, "failures": 1},
                    }
                ]
            },
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        card = snapshot["cards"][0]
        self.assertEqual(card["reason"], "acp_turn_failed")
        self.assertEqual(card["displayStatus"], "failed")
        self.assertEqual(card["nextAction"], "inspect ACP route")
        self.assertEqual(card["presentationGroup"], "route_health")
        self.assertEqual(card["presentationLabel"], "Route Health")
        self.assertEqual(card["suggestionKind"], "inspect_route")

    def test_display_title_uses_basename_for_path_labels(self) -> None:
        payloads = cockpit_snapshot.SourcePayloads(
            tasks={
                "tasks": [
                    {
                        "taskId": "task-path",
                        "runtime": "codex",
                        "label": "/tmp/example/runs/thing/result.md",
                        "status": "failed",
                        "createdAt": 1_000,
                        "lastEventAt": 2_000,
                    }
                ]
            },
            flows={"flows": []},
            audit={"findings": []},
        )

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        self.assertEqual(snapshot["cards"][0]["displayTitle"], "result.md")

    def test_cli_outputs_json_from_fixtures(self) -> None:
        cp = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "--tasks-json",
                str(FIXTURES / "tasks.json"),
                "--flows-json",
                str(FIXTURES / "flows.json"),
                "--audit-json",
                str(FIXTURES / "audit.json"),
                "--include-stale",
            ],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        self.assertEqual(cp.returncode, 0, cp.stderr)
        payload = json.loads(cp.stdout)
        self.assertEqual(payload["summary"]["byRuntime"]["acp"], 2)
        self.assertEqual(payload["summary"]["byRuntime"]["taskflow"], 2)


class SqliteSourceTests(unittest.TestCase):
    """Production acquisition: one short-lived read-only SQLite snapshot."""

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = Path(self._tmp.name)
        self.db = self.tmp / "openclaw.sqlite"
        self.now = cockpit_snapshot.now_ms()

    def load(self, *, now: int | None = None) -> cockpit_snapshot.SourcePayloads:
        return cockpit_snapshot.load_sqlite_payloads(
            self.db, timeout=cockpit_snapshot.DEFAULT_DB_TIMEOUT_SECONDS, now=self.now if now is None else now
        )

    def test_sqlite_snapshot_builds_task_flow_delivery_and_audit_cards(self) -> None:
        build_state_db(self.db)
        write_rows(
            self.db,
            tasks=(
                {
                    "task_id": "task-running",
                    "runtime": "acp",
                    "label": "live ACP lane",
                    "status": "running",
                    "run_id": "run-running",
                    "child_session_key": "agent:codex:acp:abc",
                    "created_at": self.now - 60_000,
                    "started_at": self.now - 59_000,
                    "last_event_at": self.now - 30_000,
                    "progress_summary": "working",
                },
                {
                    "task_id": "task-delivery-failed",
                    "runtime": "subagent",
                    "label": "delivery failed lane",
                    "status": "succeeded",
                    "delivery_status": "failed",
                    "notify_policy": "done_only",
                    "created_at": self.now - 120_000,
                    "ended_at": self.now - 45_000,
                    "last_event_at": self.now - 45_000,
                    "cleanup_after": self.now + 600_000,
                    "terminal_summary": "handoff failed",
                },
                {
                    "task_id": "task-child",
                    "runtime": "cli",
                    "label": "flow child",
                    "status": "succeeded",
                    "parent_flow_id": "flow-blocked",
                    "created_at": self.now - 300_000,
                    "ended_at": self.now - 200_000,
                    "last_event_at": self.now - 200_000,
                    "cleanup_after": self.now + 600_000,
                },
            ),
            flows=(
                {
                    "flow_id": "flow-blocked",
                    "status": "blocked",
                    "notify_policy": "state_changes",
                    "goal": "blocked committee lane",
                    "blocked_summary": "waiting on an operator decision",
                    "requester_origin_json": json.dumps({"channel": "discord", "to": "channel:1"}),
                    "created_at": self.now - 300_000,
                    "updated_at": self.now - 100_000,
                },
            ),
        )

        payloads = self.load()

        self.assertEqual(payloads.provenance, cockpit_snapshot.SOURCE_PROVENANCE)
        self.assertIn("mode=ro", payloads.provenance)
        self.assertEqual(payloads.tasks["count"], 3)
        self.assertEqual(payloads.flows["count"], 1)

        # Task records carry the camel-case shape the card builder consumes, and
        # omit-if-blank stays faithful to OpenClaw's row -> record mapping.
        by_id = {task["taskId"]: task for task in payloads.tasks["tasks"]}
        self.assertEqual(by_id["task-running"]["status"], "running")
        self.assertEqual(by_id["task-running"]["runtime"], "acp")
        self.assertEqual(by_id["task-running"]["childSessionKey"], "agent:codex:acp:abc")
        self.assertEqual(by_id["task-running"]["requesterSessionKey"], OWNER)
        self.assertNotIn("terminalSummary", by_id["task-running"])
        self.assertEqual(by_id["task-delivery-failed"]["deliveryStatus"], "failed")

        # Newest-first registry ordering is reconstructed, not incidental.
        self.assertEqual(
            [task["taskId"] for task in payloads.tasks["tasks"]],
            ["task-running", "task-delivery-failed", "task-child"],
        )

        flow = payloads.flows["flows"][0]
        self.assertEqual(flow["flowId"], "flow-blocked")
        self.assertEqual(flow["syncMode"], "managed")
        self.assertEqual(flow["requesterOrigin"]["to"], "channel:1")
        self.assertEqual([task["taskId"] for task in flow["tasks"]], ["task-child"])
        self.assertEqual(flow["taskSummary"]["total"], 1)
        self.assertEqual(flow["taskSummary"]["active"], 0)
        self.assertEqual(flow["taskSummary"]["terminal"], 1)
        self.assertEqual(flow["taskSummary"]["failures"], 0)
        self.assertEqual(flow["taskSummary"]["byRuntime"]["cli"], 1)

        self.assertEqual(
            [(finding["kind"], finding["code"], finding["token"]) for finding in payloads.audit["findings"]],
            [("task", "delivery_failed", "task-delivery-failed")],
        )
        self.assertEqual(payloads.audit["summary"]["byCode"]["delivery_failed"], 1)
        self.assertEqual(payloads.audit["summary"]["combined"], {"total": 1, "errors": 0, "warnings": 1})

        snapshot = cockpit_snapshot.build_snapshot(payloads)
        cards = {card["id"]: card for card in snapshot["cards"]}
        self.assertEqual(set(cards), {"task:task-running", "task:task-delivery-failed", "flow:flow-blocked"})
        self.assertEqual(snapshot["summary"]["byState"], {"active": 1, "attention": 2})
        self.assertEqual(cards["task:task-running"]["stateClass"], "active")
        self.assertEqual(cards["task:task-delivery-failed"]["reason"], "delivery_failed")
        self.assertEqual(cards["task:task-delivery-failed"]["presentationGroup"], "delivery_handoff")
        self.assertEqual(cards["task:task-delivery-failed"]["sourceKinds"], ["task", "audit"])
        self.assertEqual(cards["flow:flow-blocked"]["reason"], "blocked_flow_stale")
        self.assertEqual(cards["flow:flow-blocked"]["sourceKinds"], ["flow", "task"])
        # The reconstructed requesterOrigin reaches the flow card unchanged; the
        # merged card above takes its base record from the linked task, which is
        # existing aggregation behaviour and not part of this repair.
        self.assertEqual(
            cockpit_snapshot.flow_card(flow, now=self.now)["requesterSessionKey"],
            "channel:1",
        )
        for card in snapshot["cards"]:
            self.assertEqual(card["cardContract"], "runtime-card.v1")
            self.assertEqual(card["sourceProvenance"], cockpit_snapshot.SOURCE_PROVENANCE)

    def test_missing_database_fails_visibly(self) -> None:
        with self.assertRaises(cockpit_snapshot.SnapshotError) as ctx:
            self.load()
        self.assertIn("not found", str(ctx.exception))

    def test_missing_table_fails_visibly(self) -> None:
        build_state_db(self.db, skip_tables=("flow_runs",))
        with self.assertRaises(cockpit_snapshot.SnapshotError) as ctx:
            self.load()
        message = str(ctx.exception)
        self.assertIn("missing table(s) flow_runs", message)
        self.assertIn("user_version=", message)

    def test_missing_column_fails_visibly(self) -> None:
        build_state_db(self.db, task_runs_ddl=TASK_RUNS_DDL.replace("  cleanup_after INTEGER,\n", ""))
        with self.assertRaises(cockpit_snapshot.SnapshotError) as ctx:
            self.load()
        message = str(ctx.exception)
        self.assertIn("task_runs is missing required column(s) cleanup_after", message)
        self.assertIn("user_version=", message)

    def test_unknown_extra_column_is_tolerated(self) -> None:
        build_state_db(
            self.db,
            task_runs_ddl=TASK_RUNS_DDL.replace(
                "  terminal_outcome TEXT\n", "  terminal_outcome TEXT,\n  future_column TEXT\n"
            ),
        )
        write_rows(self.db, tasks=({"task_id": "task-a", "created_at": self.now - 1_000},))

        payloads = self.load()

        self.assertEqual([task["taskId"] for task in payloads.tasks["tasks"]], ["task-a"])

    def test_invalid_persisted_enum_fails_visibly(self) -> None:
        build_state_db(self.db)
        write_rows(self.db, tasks=({"task_id": "task-a", "status": "exploded", "created_at": self.now},))
        with self.assertRaises(cockpit_snapshot.SnapshotError) as ctx:
            self.load()
        self.assertIn("invalid persisted task status", str(ctx.exception))

    def test_malformed_json_column_degrades_only_that_field(self) -> None:
        """OpenClaw swallows an unparseable JSON column; so does this reader.

        A corrupt `wait_json` must not blank the whole runtime view, and it must
        not count as blocking metadata that suppresses `missing_linked_tasks`.
        """
        build_state_db(self.db)
        write_rows(
            self.db,
            flows=(
                {
                    "flow_id": "flow-a",
                    "status": "running",
                    "goal": "corrupt sidecar json",
                    "requester_origin_json": "{not json",
                    "state_json": "{also not json",
                    "wait_json": "{still not json",
                    "created_at": self.now - 3_600_000,
                    "updated_at": self.now - 3_600_000,
                },
            ),
        )

        payloads = self.load()

        flow = payloads.flows["flows"][0]
        self.assertNotIn("requesterOrigin", flow)
        self.assertNotIn("stateJson", flow)
        self.assertNotIn("waitJson", flow)
        self.assertIn("missing_linked_tasks", audit_codes(payloads, "flow-a"))
        self.assertEqual(cockpit_snapshot.build_snapshot(payloads)["cardContract"], "runtime-card.v1")

    def test_malformed_fixture_file_fails_visibly(self) -> None:
        broken = self.tmp / "tasks.json"
        broken.write_text("{not json", encoding="utf-8")
        with self.assertRaises(cockpit_snapshot.SnapshotError) as ctx:
            cockpit_snapshot.load_json_file(broken)
        self.assertIn("not valid JSON", str(ctx.exception))

    def test_snapshot_leaves_the_database_byte_identical(self) -> None:
        build_state_db(self.db)
        write_rows(self.db, tasks=({"task_id": "task-a", "created_at": self.now - 1_000},))
        before_bytes = self.db.read_bytes()
        before_stat = self.db.stat()

        self.load()

        after_stat = self.db.stat()
        self.assertEqual(self.db.read_bytes(), before_bytes)
        self.assertEqual(after_stat.st_size, before_stat.st_size)
        self.assertEqual(after_stat.st_mtime_ns, before_stat.st_mtime_ns)
        # A read-only open must not leave journal sidecars behind either.
        self.assertFalse((self.tmp / "openclaw.sqlite-wal").exists())
        self.assertFalse((self.tmp / "openclaw.sqlite-shm").exists())
        self.assertFalse((self.tmp / "openclaw.sqlite-journal").exists())

    def test_snapshot_connection_reports_zero_changes(self) -> None:
        build_state_db(self.db)
        write_rows(self.db, tasks=({"task_id": "task-a", "created_at": self.now - 1_000},))

        con = cockpit_snapshot.open_state_db(self.db, timeout=cockpit_snapshot.DEFAULT_DB_TIMEOUT_SECONDS)
        try:
            con.row_factory = sqlite3.Row
            cockpit_snapshot.validate_state_schema(con, path=self.db)
            con.execute(f"SELECT {', '.join(cockpit_snapshot.TASK_RUN_COLUMNS)} FROM task_runs").fetchall()
            con.execute(f"SELECT {', '.join(cockpit_snapshot.FLOW_RUN_COLUMNS)} FROM flow_runs").fetchall()
            self.assertEqual(con.total_changes, 0)
        finally:
            con.close()

    def test_read_only_connection_refuses_writes(self) -> None:
        build_state_db(self.db)
        con = cockpit_snapshot.open_state_db(self.db, timeout=cockpit_snapshot.DEFAULT_DB_TIMEOUT_SECONDS)
        try:
            for statement in (
                "INSERT INTO task_runs (task_id, runtime, owner_key, scope_kind, task, status,"
                " delivery_status, notify_policy, created_at)"
                " VALUES ('x', 'cli', 'o', 'session', 't', 'running', 'not_applicable', 'done_only', 1)",
                "UPDATE task_runs SET status = 'lost'",
                "DELETE FROM flow_runs",
                "DROP TABLE flow_runs",
                "CREATE TABLE scratch (id TEXT)",
                # A hot-WAL recovery attempt is refused too: mode=ro is what
                # stops this adapter from "fixing" a crashed writer's journal.
                "PRAGMA journal_mode = WAL",
            ):
                with self.subTest(statement=statement.split()[0]):
                    con.rollback()
                    with self.assertRaises(sqlite3.OperationalError) as ctx:
                        con.execute(statement)
                    self.assertIn("readonly", str(ctx.exception).lower())
            con.rollback()
            self.assertEqual(con.execute("PRAGMA journal_mode").fetchone()[0], "delete")
            self.assertEqual(con.total_changes, 0)
        finally:
            con.close()
        self.assertFalse((self.tmp / "openclaw.sqlite-wal").exists())

    def test_resolve_state_db_precedence(self) -> None:
        explicit = self.tmp / "explicit.sqlite"
        self.assertEqual(
            cockpit_snapshot.resolve_state_db(str(explicit), {"OPENCLAW_STATE_DIR": "/ignored"}),
            explicit,
        )
        self.assertEqual(
            cockpit_snapshot.resolve_state_db(None, {"OPENCLAW_STATE_DIR": str(self.tmp)}),
            self.tmp / "state" / "openclaw.sqlite",
        )
        self.assertEqual(
            cockpit_snapshot.resolve_state_db(None, {}),
            Path("~/.openclaw").expanduser() / "state" / "openclaw.sqlite",
        )

    def test_production_source_has_no_subprocess_or_cli_execution_path(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")
        for forbidden in ("import subprocess", "subprocess.", "Popen", "os.system", "os.popen", "os.exec"):
            self.assertNotIn(forbidden, source, f"production adapter must not reach for {forbidden}")
        # The only surviving `openclaw ...` strings are operator guidance printed
        # on cards, never an argv passed to an executor.
        self.assertNotIn('"openclaw",', source)

    def test_cli_reads_a_disposable_state_db_end_to_end(self) -> None:
        build_state_db(self.db)
        write_rows(
            self.db,
            tasks=(
                {
                    "task_id": "task-a",
                    "runtime": "cron",
                    "label": "cron lane",
                    "status": "running",
                    "created_at": self.now - 10_000,
                    "last_event_at": self.now - 5_000,
                },
            ),
        )

        cp = subprocess.run(
            [sys.executable, str(SCRIPT), "--state-db", str(self.db), "--limit", "5", "--format", "json"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        self.assertEqual(cp.returncode, 0, cp.stderr)
        payload = json.loads(cp.stdout)
        self.assertEqual(payload["cardContract"], "runtime-card.v1")
        self.assertEqual([card["id"] for card in payload["cards"]], ["task:task-a"])
        self.assertEqual(payload["cards"][0]["sourceProvenance"], cockpit_snapshot.SOURCE_PROVENANCE)

    def test_cli_reports_a_broken_state_db_visibly(self) -> None:
        build_state_db(self.db, skip_tables=("task_delivery_state",))

        cp = subprocess.run(
            [sys.executable, str(SCRIPT), "--state-db", str(self.db), "--format", "json"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        self.assertEqual(cp.returncode, 1)
        payload = json.loads(cp.stdout)
        self.assertFalse(payload["ok"])
        self.assertIn("missing table(s) task_delivery_state", payload["error"])


class SqliteAuditParityTests(unittest.TestCase):
    """The exact audit traps frozen by the spec and the Fable review.

    Each case is a cheap falsifier: flip one column and the expected finding must
    appear or disappear.
    """

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = Path(self._tmp.name)
        self.now = cockpit_snapshot.now_ms()
        self._db_index = 0

    def load(self, *, tasks=(), flows=(), flow_runs_ddl: str = FLOW_RUNS_DDL) -> cockpit_snapshot.SourcePayloads:
        self._db_index += 1
        db = self.tmp / f"state-{self._db_index}.sqlite"
        build_state_db(db, flow_runs_ddl=flow_runs_ddl)
        write_rows(db, tasks=tasks, flows=flows)
        return cockpit_snapshot.load_sqlite_payloads(
            db, timeout=cockpit_snapshot.DEFAULT_DB_TIMEOUT_SECONDS, now=self.now
        )

    def delivered_failed_task(self, notify_policy: str) -> dict:
        return {
            "task_id": "task-a",
            "status": "succeeded",
            "delivery_status": "failed",
            "notify_policy": notify_policy,
            "created_at": self.now - 120_000,
            "ended_at": self.now - 60_000,
            "last_event_at": self.now - 60_000,
            "cleanup_after": self.now + 600_000,
        }

    def test_silent_notify_policy_suppresses_the_delivery_finding_but_not_the_card(self) -> None:
        loud = self.load(tasks=(self.delivered_failed_task("done_only"),))
        self.assertIn("delivery_failed", audit_codes(loud, "task-a"))

        silent = self.load(tasks=(self.delivered_failed_task("silent"),))
        self.assertNotIn("delivery_failed", audit_codes(silent, "task-a"))
        self.assertEqual(silent.audit["findings"], [])

        # The card still demands attention: delivery status alone classifies it.
        card = cockpit_snapshot.build_snapshot(silent)["cards"][0]
        self.assertEqual(card["id"], "task:task-a")
        self.assertEqual(card["stateClass"], "attention")
        self.assertEqual(card["reason"], "delivery_failed")

    def lost_task(self, cleanup_after: int) -> dict:
        return {
            "task_id": "task-a",
            "status": "lost",
            "created_at": self.now - 7_200_000,
            "ended_at": self.now - 3_600_000,
            "last_event_at": self.now - 3_600_000,
            "cleanup_after": cleanup_after,
        }

    def test_lost_severity_follows_the_effective_cleanup_time(self) -> None:
        retained = self.load(tasks=(self.lost_task(self.now + 3_600_000),))
        finding = audit_finding(retained, "task-a", "lost")
        self.assertEqual(finding["severity"], "warn")
        self.assertEqual(finding["detail"], "task lost its backing session and is retained until cleanupAfter")

        expired = self.load(tasks=(self.lost_task(self.now - 3_600_000),))
        finding = audit_finding(expired, "task-a", "lost")
        self.assertEqual(finding["severity"], "error")
        self.assertEqual(finding["detail"], "task lost its backing session")

    def test_lost_retention_takes_the_earlier_of_persisted_and_status_cleanup(self) -> None:
        """A far-future cleanupAfter cannot outlive the 24h lost retention window."""
        payloads = self.load(
            tasks=(
                {
                    "task_id": "task-a",
                    "status": "lost",
                    "created_at": self.now - 200_000_000,
                    "ended_at": self.now - 200_000_000,
                    "last_event_at": self.now - 200_000_000,
                    "cleanup_after": self.now + 200_000_000,
                },
            )
        )
        self.assertEqual(audit_finding(payloads, "task-a", "lost")["severity"], "error")

    def terminal_task(self, cleanup_after: int | None) -> dict:
        return {
            "task_id": "task-a",
            "status": "succeeded",
            "created_at": self.now - 120_000,
            "ended_at": self.now - 60_000,
            "last_event_at": self.now - 60_000,
            "cleanup_after": cleanup_after,
        }

    def test_missing_cleanup_only_fires_for_terminal_tasks_without_cleanup_after(self) -> None:
        without = self.load(tasks=(self.terminal_task(None),))
        self.assertIn("missing_cleanup", audit_codes(without, "task-a"))

        with_cleanup = self.load(tasks=(self.terminal_task(self.now + 600_000),))
        self.assertNotIn("missing_cleanup", audit_codes(with_cleanup, "task-a"))

    def stale_flow(self, **overrides) -> dict:
        row = {
            "flow_id": "flow-a",
            "status": "running",
            "goal": "single task mirror",
            "created_at": self.now - 3_600_000,
            "updated_at": self.now - 3_600_000,
        }
        row.update(overrides)
        return row

    def test_null_sync_mode_falls_back_through_shape(self) -> None:
        mirrored = self.load(
            flows=(self.stale_flow(sync_mode=None, shape="single_task"),),
            flow_runs_ddl=FLOW_RUNS_DDL_NULLABLE_SYNC_MODE,
        )
        self.assertEqual(mirrored.flows["flows"][0]["syncMode"], "task_mirrored")
        self.assertNotIn("missing_linked_tasks", audit_codes(mirrored, "flow-a"))

        managed = self.load(
            flows=(self.stale_flow(sync_mode=None, shape=None),),
            flow_runs_ddl=FLOW_RUNS_DDL_NULLABLE_SYNC_MODE,
        )
        self.assertEqual(managed.flows["flows"][0]["syncMode"], "managed")
        self.assertIn("missing_linked_tasks", audit_codes(managed, "flow-a"))
        self.assertEqual(audit_finding(managed, "flow-a", "missing_linked_tasks")["severity"], "error")

    def test_blank_sync_mode_uses_the_same_fallback_under_the_canonical_schema(self) -> None:
        """`sync_mode` is NOT NULL in the shipped DDL, but '' reaches the same path."""
        mirrored = self.load(flows=(self.stale_flow(sync_mode="", shape="single_task"),))
        self.assertEqual(mirrored.flows["flows"][0]["syncMode"], "task_mirrored")
        self.assertNotIn("missing_linked_tasks", audit_codes(mirrored, "flow-a"))

        managed = self.load(flows=(self.stale_flow(sync_mode="", shape=None),))
        self.assertEqual(managed.flows["flows"][0]["syncMode"], "managed")
        self.assertIn("missing_linked_tasks", audit_codes(managed, "flow-a"))

    def test_blocking_metadata_suppresses_missing_linked_tasks(self) -> None:
        for column, value in (
            ("blocked_summary", "waiting for review"),
            ("blocked_task_id", "task-a"),
            ("wait_json", json.dumps({"until": "review"})),
        ):
            with self.subTest(column=column):
                payloads = self.load(flows=(self.stale_flow(**{column: value}),))
                self.assertNotIn("missing_linked_tasks", audit_codes(payloads, "flow-a"))

    def cancel_requested_flow(self) -> dict:
        return {
            "flow_id": "flow-a",
            "status": "waiting",
            "goal": "cancel requested lane",
            "cancel_requested_at": self.now - 600_000,
            "created_at": self.now - 900_000,
            "updated_at": self.now - 60_000,
        }

    def linked_task(self, status: str) -> dict:
        return {
            "task_id": "task-a",
            "status": status,
            "parent_flow_id": "flow-a",
            "created_at": self.now - 900_000,
            "last_event_at": self.now - 120_000,
            "cleanup_after": self.now + 600_000,
        }

    def test_cancel_stuck_waits_for_the_last_active_child(self) -> None:
        active = self.load(flows=(self.cancel_requested_flow(),), tasks=(self.linked_task("running"),))
        self.assertNotIn("cancel_stuck", audit_codes(active, "flow-a"))

        settled = self.load(flows=(self.cancel_requested_flow(),), tasks=(self.linked_task("succeeded"),))
        finding = audit_finding(settled, "flow-a", "cancel_stuck")
        self.assertEqual(finding["severity"], "warn")
        # ageMs comes from cancelRequestedAt, not from the flow reference time.
        self.assertEqual(finding["ageMs"], 600_000)

    def test_cancel_stuck_ignores_terminal_flows(self) -> None:
        flow = self.cancel_requested_flow()
        flow["status"] = "cancelled"
        payloads = self.load(flows=(flow,), tasks=(self.linked_task("succeeded"),))
        self.assertNotIn("cancel_stuck", audit_codes(payloads, "flow-a"))

    def test_blocked_task_missing_only_fires_without_the_linked_task(self) -> None:
        flow = {
            "flow_id": "flow-a",
            "status": "blocked",
            "goal": "blocked lane",
            "blocked_task_id": "task-a",
            "created_at": self.now - 300_000,
            "updated_at": self.now - 60_000,
        }
        present = self.load(flows=(flow,), tasks=(self.linked_task("running"),))
        self.assertNotIn("blocked_task_missing", audit_codes(present, "flow-a"))

        absent = self.load(flows=(flow,))
        self.assertIn("blocked_task_missing", audit_codes(absent, "flow-a"))

    def test_stale_thresholds_and_severities(self) -> None:
        cases = (
            ("queued", 600_000 + 1_000, "stale_queued", "warn"),
            ("queued", 600_000 - 60_000, None, None),
            ("running", 1_800_000 + 1_000, "stale_running", "error"),
            ("running", 1_800_000 - 60_000, None, None),
        )
        for status, age, code, severity in cases:
            with self.subTest(status=status, age=age):
                payloads = self.load(
                    tasks=(
                        {
                            "task_id": "task-a",
                            "status": status,
                            "created_at": self.now - age,
                            "last_event_at": self.now - age,
                        },
                    )
                )
                if code is None:
                    self.assertEqual(audit_codes(payloads, "task-a"), set())
                else:
                    self.assertEqual(audit_finding(payloads, "task-a", code)["severity"], severity)

    def test_flow_stale_thresholds_and_severities(self) -> None:
        cases = (
            ("running", "stale_running", "error"),
            ("waiting", "stale_waiting", "warn"),
            ("blocked", "stale_blocked", "warn"),
        )
        for status, code, severity in cases:
            with self.subTest(status=status):
                payloads = self.load(
                    flows=(
                        self.stale_flow(status=status, blocked_summary="held"),
                    )
                )
                self.assertEqual(audit_finding(payloads, "flow-a", code)["severity"], severity)

    def test_errors_sort_ahead_of_warnings_and_older_findings_first(self) -> None:
        payloads = self.load(
            tasks=(
                {
                    "task_id": "task-warn",
                    "status": "queued",
                    "created_at": self.now - 700_000,
                    "last_event_at": self.now - 700_000,
                },
                {
                    "task_id": "task-error-young",
                    "status": "running",
                    "created_at": self.now - 1_900_000,
                    "last_event_at": self.now - 1_900_000,
                },
                {
                    "task_id": "task-error-old",
                    "status": "running",
                    "created_at": self.now - 9_000_000,
                    "last_event_at": self.now - 9_000_000,
                },
            )
        )
        self.assertEqual(
            [finding["token"] for finding in payloads.audit["findings"]],
            ["task-error-old", "task-error-young", "task-warn"],
        )
        self.assertEqual(payloads.audit["summary"]["combined"], {"total": 3, "errors": 2, "warnings": 1})

    def test_timestamp_inconsistencies_are_reported_once_per_record(self) -> None:
        payloads = self.load(
            tasks=(
                {
                    "task_id": "task-a",
                    "status": "running",
                    "created_at": self.now - 60_000,
                    "started_at": self.now - 120_000,
                    "last_event_at": self.now - 30_000,
                },
            ),
            flows=(
                {
                    "flow_id": "flow-a",
                    "status": "blocked",
                    "goal": "backwards clock",
                    "blocked_summary": "held",
                    "created_at": self.now - 60_000,
                    "updated_at": self.now - 120_000,
                },
            ),
        )
        self.assertEqual(
            audit_finding(payloads, "task-a", "inconsistent_timestamps")["detail"],
            "startedAt is earlier than createdAt",
        )
        self.assertEqual(
            audit_finding(payloads, "flow-a", "inconsistent_timestamps")["detail"],
            "updatedAt is earlier than createdAt",
        )
        self.assertEqual(payloads.audit["summary"]["byCode"]["inconsistent_timestamps"], 1)
        self.assertEqual(payloads.audit["summary"]["taskFlows"]["byCode"]["inconsistent_timestamps"], 1)


class FixtureModeTests(unittest.TestCase):
    """Fixture inputs stay independent of any live schema."""

    def test_all_three_fixtures_never_touch_sqlite(self) -> None:
        args = cockpit_snapshot.build_parser().parse_args(
            [
                "--tasks-json",
                str(FIXTURES / "tasks.json"),
                "--flows-json",
                str(FIXTURES / "flows.json"),
                "--audit-json",
                str(FIXTURES / "audit.json"),
                "--state-db",
                "/nonexistent/openclaw.sqlite",
            ]
        )

        payloads = cockpit_snapshot.load_sources(args)

        self.assertEqual(payloads.provenance, cockpit_snapshot.SOURCE_PROVENANCE_FIXTURE)
        self.assertEqual(payloads.tasks["count"], 4)
        self.assertEqual(
            cockpit_snapshot.build_snapshot(payloads)["cards"][0]["sourceProvenance"],
            cockpit_snapshot.SOURCE_PROVENANCE_FIXTURE,
        )

    def test_partial_fixtures_still_require_a_readable_database(self) -> None:
        args = cockpit_snapshot.build_parser().parse_args(
            [
                "--tasks-json",
                str(FIXTURES / "tasks.json"),
                "--state-db",
                "/nonexistent/openclaw.sqlite",
            ]
        )
        with self.assertRaises(cockpit_snapshot.SnapshotError):
            cockpit_snapshot.load_sources(args)

    def test_state_dir_env_is_honoured_by_the_cli(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            (root / "state").mkdir()
            db = root / "state" / "openclaw.sqlite"
            build_state_db(db)
            now = cockpit_snapshot.now_ms()
            write_rows(
                db,
                tasks=({"task_id": "task-env", "status": "running", "created_at": now - 1_000},),
            )

            env = os.environ.copy()
            env["OPENCLAW_STATE_DIR"] = str(root)
            cp = subprocess.run(
                [sys.executable, str(SCRIPT), "--limit", "5", "--format", "json"],
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )

        self.assertEqual(cp.returncode, 0, cp.stderr)
        self.assertEqual([card["id"] for card in json.loads(cp.stdout)["cards"]], ["task:task-env"])


if __name__ == "__main__":
    unittest.main()
