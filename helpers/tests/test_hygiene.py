#!/usr/bin/env python3
from __future__ import annotations

import argparse
import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tmux"))
from helpers.tmux import session_hygiene as hygiene


def pane(session: str, **meta: str) -> hygiene.Pane:
    old_epoch = str(int(datetime.now(timezone.utc).timestamp()) - 7200)
    return hygiene.Pane(
        session=session,
        server_session_id="$1",
        window_linked="0",
        session_grouped="0",
        window="main",
        pane="%1",
        title="",
        command="zsh",
        path="/tmp",
        created=old_epoch,
        process_started=datetime.fromtimestamp(int(old_epoch), timezone.utc).isoformat(),
        last_activity=old_epoch,
        dead=False,
        dead_status="",
        meta={field: meta.get(field, "") for field in hygiene.OC_FIELDS},
    )


def managed(session: str, **meta: str) -> hygiene.Pane:
    base = {
        "contract_version": "1",
        "managed_by": "agent_wall",
        "kind": "smoke",
        "cleanup_policy": "smoke",
        "state": "done",
        "ttl": "30m",
        "completed_at": (datetime.now(timezone.utc)-timedelta(hours=1)).isoformat(),
    }
    base.update(meta)
    return pane(session, **base)


class SessionHygieneTests(unittest.TestCase):
    def setUp(self) -> None:
        # Synthetic pane IDs must never read the operator's default server.
        capture = patch.object(hygiene, "capture_pane_text", return_value="completed worker output")
        capture.start()
        self.addCleanup(capture.stop)

    def test_incomplete_metadata_no_kill(self) -> None:
        item = hygiene.eligible_managed(
            "plain",
            [pane("plain", cleanup_policy="kill_on_done")],
            policy="kill-safe",
            grace=300,
            now=datetime.now(timezone.utc),
        )
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "unmanaged_or_incomplete_contract")

    def test_inspector_display_only_session_is_not_cleanup_managed(self) -> None:
        item = hygiene.eligible_managed(
            "detected",
            [
                pane(
                    "detected",
                    contract_version="display-only",
                    managed_by="tmux_inspector",
                    kind="detected-agent",
                    state="done",
                    cleanup_policy="manual",
                )
            ],
            policy="kill-safe",
            grace=300,
            now=datetime.now(timezone.utc),
        )
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "unmanaged_or_incomplete_contract")

    def test_completed_inspector_codex_tui_can_be_cleaned_after_idle_grace(self) -> None:
        original_capture = hygiene.capture_pane_text
        original_root = hygiene.STATE_ROOT
        try:
            with tempfile.TemporaryDirectory() as tmp:
                hygiene.STATE_ROOT = Path(tmp)
                run_root = Path(tmp) / "runs/codex-done"
                run_root.mkdir(parents=True)
                (run_root / "COMPLETION_AUDIT.md").write_text("complete\n", encoding="utf-8")
                p = pane(
                    "cd-legacy-exit-goal",
                    contract_version="display-only",
                    managed_by="tmux_inspector",
                    kind="detected-agent",
                    agent="codex",
                    state="running",
                    cleanup_policy="manual",
                )
                p.path = str(run_root)
                p.command = "node"
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\n\n  gpt-5.5 xhigh · Goal achieved (13m)\n› Find and fix a bug\n"
                item = hygiene.eligible_managed(
                    "cd-legacy-exit-goal",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                    adopted_grace=3600,
                )
        finally:
            hygiene.capture_pane_text = original_capture
            hygiene.STATE_ROOT = original_root
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "adopted_codex_goal_achieved")
        self.assertEqual(item["policy_source"], "adopted_codex")
        self.assertTrue(item["evidence_path"].endswith("COMPLETION_AUDIT.md"))

    def test_manual_adopted_codex_tui_is_not_auto_cleaned(self) -> None:
        p = pane(
            "manual-codex",
            contract_version="display-only",
            managed_by="manual_adopt",
            kind="detected-agent",
            agent="codex",
            state="running",
            cleanup_policy="manual",
        )
        original_capture = hygiene.capture_pane_text
        try:
            hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\n\n  gpt-5.5 xhigh · Goal achieved (13m)\n› Find and fix a bug\n"
            item = hygiene.eligible_managed(
                "manual-codex",
                [p],
                policy="kill-safe",
                grace=300,
                now=datetime.now(timezone.utc),
                adopted_grace=0,
            )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "unmanaged_or_incomplete_contract")

    def test_completed_inspector_codex_tui_respects_hold_reason(self) -> None:
        p = pane(
            "held-codex",
            contract_version="display-only",
            managed_by="tmux_inspector",
            kind="detected-agent",
            agent="codex",
            state="running",
            cleanup_policy="manual",
            hold_reason="review first",
        )
        item = hygiene.eligible_managed(
            "held-codex",
            [p],
            policy="kill-safe",
            grace=300,
            now=datetime.now(timezone.utc),
            adopted_grace=0,
        )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active_adopted")

    def test_inspector_codex_without_completion_marker_is_not_cleaned(self) -> None:
        original_capture = hygiene.capture_pane_text
        original_root = hygiene.STATE_ROOT
        try:
            with tempfile.TemporaryDirectory() as tmp:
                hygiene.STATE_ROOT = Path(tmp)
                run_root = Path(tmp) / "runs/active-codex"
                run_root.mkdir(parents=True)
                (run_root / "RESULT.md").write_text("some result\n", encoding="utf-8")
                p = pane(
                    "active-codex",
                    contract_version="display-only",
                    managed_by="tmux_inspector",
                    kind="detected-agent",
                    agent="codex",
                    state="running",
                    cleanup_policy="manual",
                )
                p.path = str(run_root)
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\nWorking...\n"
                item = hygiene.eligible_managed(
                    "active-codex",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                    adopted_grace=0,
                )
        finally:
            hygiene.capture_pane_text = original_capture
            hygiene.STATE_ROOT = original_root
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "adopted_codex_not_complete")

    def test_inspector_codex_completion_requires_final_artifact_evidence(self) -> None:
        original_capture = hygiene.capture_pane_text
        original_root = hygiene.STATE_ROOT
        try:
            with tempfile.TemporaryDirectory() as tmp:
                hygiene.STATE_ROOT = Path(tmp)
                run_root = Path(tmp) / "runs/no-final"
                run_root.mkdir(parents=True)
                p = pane(
                    "no-evidence-codex",
                    contract_version="display-only",
                    managed_by="tmux_inspector",
                    kind="detected-agent",
                    agent="codex",
                    state="running",
                    cleanup_policy="manual",
                )
                p.path = str(run_root)
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\n\n  gpt-5.5 xhigh · Goal achieved (13m)\n› Find and fix a bug\n"
                item = hygiene.eligible_managed(
                    "no-evidence-codex",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                    adopted_grace=0,
                )
        finally:
            hygiene.capture_pane_text = original_capture
            hygiene.STATE_ROOT = original_root
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "adopted_evidence_missing")

    def test_inspector_codex_completion_requires_fresh_final_artifact(self) -> None:
        original_capture = hygiene.capture_pane_text
        original_root = hygiene.STATE_ROOT
        try:
            with tempfile.TemporaryDirectory() as tmp:
                hygiene.STATE_ROOT = Path(tmp)
                run_root = Path(tmp) / "runs/stale-final"
                run_root.mkdir(parents=True)
                evidence = run_root / "REPORT.md"
                evidence.write_text("old report\n", encoding="utf-8")
                old_mtime = int(datetime.now(timezone.utc).timestamp()) - 7200
                os.utime(evidence, (old_mtime, old_mtime))
                p = pane(
                    "stale-evidence-codex",
                    contract_version="display-only",
                    managed_by="tmux_inspector",
                    kind="detected-agent",
                    agent="codex",
                    state="running",
                    cleanup_policy="manual",
                )
                p.path = str(run_root)
                p.created = str(int(datetime.now(timezone.utc).timestamp()) - 3600)
                p.last_activity = p.created
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\n\n  gpt-5.5 xhigh · Goal achieved (13m)\n› Find and fix a bug\n"
                item = hygiene.eligible_managed(
                    "stale-evidence-codex",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                    adopted_grace=0,
                )
        finally:
            hygiene.capture_pane_text = original_capture
            hygiene.STATE_ROOT = original_root
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "adopted_evidence_stale")

    def test_inspector_codex_completion_waits_for_session_grace(self) -> None:
        p = pane(
            "fresh-codex",
            contract_version="display-only",
            managed_by="tmux_inspector",
            kind="detected-agent",
            agent="codex",
            state="running",
            cleanup_policy="manual",
        )
        p.created = str(int(datetime.now(timezone.utc).timestamp()))
        item = hygiene.eligible_managed(
            "fresh-codex",
            [p],
            policy="kill-safe",
            grace=300,
            now=datetime.now(timezone.utc),
            adopted_grace=3600,
        )
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "adopted_completion_grace_active")

    def test_inspector_codex_completion_waits_for_idle_grace(self) -> None:
        p = pane(
            "busy-codex",
            contract_version="display-only",
            managed_by="tmux_inspector",
            kind="detected-agent",
            agent="codex",
            state="running",
            cleanup_policy="manual",
        )
        p.last_activity = str(int(datetime.now(timezone.utc).timestamp()))
        item = hygiene.eligible_managed(
            "busy-codex",
            [p],
            policy="kill-safe",
            grace=300,
            now=datetime.now(timezone.utc),
            adopted_grace=3600,
        )
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "adopted_completion_idle_grace_active")

    def test_inspector_codex_completion_marker_must_be_in_visible_tail(self) -> None:
        original_capture = hygiene.capture_pane_text
        original_root = hygiene.STATE_ROOT
        try:
            with tempfile.TemporaryDirectory() as tmp:
                hygiene.STATE_ROOT = Path(tmp)
                run_root = Path(tmp) / "runs/old-history"
                run_root.mkdir(parents=True)
                (run_root / "SUMMARY.md").write_text("summary\n", encoding="utf-8")
                p = pane(
                    "codex-with-old-history",
                    contract_version="display-only",
                    managed_by="tmux_inspector",
                    kind="detected-agent",
                    agent="codex",
                    state="running",
                    cleanup_policy="manual",
                )
                p.path = str(run_root)
                hygiene.capture_pane_text = lambda pane, lines=240: (
                    "OpenAI Codex\n"
                    "  gpt-5.5 xhigh · Goal achieved (13m)\n"
                    "› Find and fix a bug\n"
                    "new prompt line\n"
                    "working line 1\nworking line 2\nworking line 3\nworking line 4\nworking line 5\nworking line 6\nworking line 7\n"
                )
                item = hygiene.eligible_managed(
                    "codex-with-old-history",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                    adopted_grace=0,
                )
        finally:
            hygiene.capture_pane_text = original_capture
            hygiene.STATE_ROOT = original_root
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "adopted_codex_not_complete")

    def test_managed_smoke_kill(self) -> None:
        item = hygiene.eligible_managed(
            "oc-vis-smoke-01",
            [managed("oc-vis-smoke-01")],
            policy="smoke",
            grace=300,
            now=datetime.now(timezone.utc),
        )
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "managed_smoke_complete")

    def test_running_managed_tui_with_fresh_evidence_gets_marked_for_teardown(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "changes-summary.md"
                evidence.write_text("done\n", encoding="utf-8")
                p = managed(
                    "codex-tui-done",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="15m",
                    run_root=tmp,
                    evidence_path="changes-summary.md",
                    started_at=(datetime.now(timezone.utc) - timedelta(hours=2)).isoformat().replace("+00:00", "Z"),
                    completed_at="",
                )
                p.command = "node"
                old_epoch = str(int(datetime.now(timezone.utc).timestamp()) - 7200)
                p.created = old_epoch
                p.last_activity = old_epoch
                old_mtime = int(datetime.now(timezone.utc).timestamp()) - 3600
                os.utime(evidence, (old_mtime, old_mtime))
                hygiene.capture_pane_text = lambda pane, lines=240: (
                    "OpenAI Codex\n"
                    "No workflow process is running now.\n"
                    "─ Worked for 5m 47s ─────────────────────────\n"
                    "› Use /skills to list available skills\n"
                    "\n"
                    "  gpt-5.5 high · ~/projects/clean-draft\n"
                )

                item = hygiene.eligible_managed(
                    "codex-tui-done",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "managed_tui_completed")

    def test_managed_tui_completion_markers_include_fable_verbs(self) -> None:
        for text in [
            "✻ Sautéed for 15m 37s\n❯ ",
            "✻ Sauteed for 15m 37s\n❯ ",
            "✻ Crunched for 13m 43s\n│ › ",
            "✻ Baked for 17m 19s\n❯ ",
        ]:
            with self.subTest(text=text):
                self.assertTrue(hygiene.managed_tui_completion_screen(text))

    def test_running_managed_codex_tui_waiting_on_background_terminal_stays_running(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "changes-summary.md"
                evidence.write_text("done\n", encoding="utf-8")
                p = managed(
                    "codex-tui-active",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="15m",
                    run_root=tmp,
                    evidence_path="changes-summary.md",
                    completed_at="",
                )
                p.command = "node"
                hygiene.capture_pane_text = lambda pane, lines=240: (
                    "OpenAI Codex\n"
                    "• Waiting for background terminal (9m 36s • esc to interrupt)\n"
                    "› Find and fix a bug in @filename\n"
                    "\n"
                    "  gpt-5.5 xhigh · ~/projects/clean-draft\n"
                )

                item = hygiene.eligible_managed(
                    "codex-tui-active",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "state_not_cleanable:running")

    def test_running_active_session_does_not_require_evidence_yet(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                p = managed(
                    "active-no-evidence-yet",
                    kind="visible-agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="10m",
                    run_root=tmp,
                    evidence_path="missing-report.md",
                    hold_reason="parent review",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: (
                    "OpenAI Codex\n"
                    "• Working (6m 46s • esc to interrupt) · 1 background terminal running\n"
                    "› Explain this codebase\n"
                )
                item = hygiene.eligible_managed(
                    "active-no-evidence-yet",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")

    def test_running_managed_codex_tui_requires_fresh_evidence(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "changes-summary.md"
                evidence.write_text("old\n", encoding="utf-8")
                p = managed(
                    "codex-tui-stale-evidence",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="15m",
                    run_root=tmp,
                    evidence_path="changes-summary.md",
                    completed_at="",
                )
                p.command = "node"
                p.created = str(int(datetime.now(timezone.utc).timestamp()) - 1800)
                p.last_activity = p.created
                old_mtime = int(datetime.now(timezone.utc).timestamp()) - 3600
                os.utime(evidence, (old_mtime, old_mtime))
                hygiene.capture_pane_text = lambda pane, lines=240: (
                    "OpenAI Codex\n"
                    "─ Worked for 5m 47s ─────────────────────────\n"
                    "› Use /skills to list available skills\n"
                    "\n"
                    "  gpt-5.5 high · ~/projects/clean-draft\n"
                )

                item = hygiene.eligible_managed(
                    "codex-tui-stale-evidence",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=datetime.now(timezone.utc),
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "state_not_cleanable:running")

    def test_hold_reason_refuses_without_override(self) -> None:
        item = hygiene.eligible_managed(
            "held",
            [managed("held", hold_reason="parent review")],
            policy="smoke",
            grace=300,
            now=datetime.now(timezone.utc),
        )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")

    def test_hold_reason_blocks_completed_teardown_mark(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "worker.log"
            evidence.write_text("done\n", encoding="utf-8")
            p = managed(
                "held-done",
                kind="visible-agent",
                cleanup_policy="kill_after_ttl",
                state="done",
                ttl="10m",
                run_root=tmp,
                evidence_path="worker.log",
                completed_at=(datetime.now(timezone.utc) - timedelta(minutes=20)).isoformat().replace("+00:00", "Z"),
                hold_reason="parent review",
            )
            item = hygiene.eligible_managed("held-done", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_hold_reason_protects_live_running_session(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "worker.log"
                evidence.write_text("progress\n", encoding="utf-8")
                p = managed(
                    "held-live",
                    kind="visible-agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="10m",
                    run_root=tmp,
                    evidence_path="worker.log",
                    hold_reason="parent review",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\nWorking...\n• Waiting for background terminal (1m • esc to interrupt)\n"
                item = hygiene.eligible_managed("held-live", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_hold_reason_blocks_marked_teardown_kill(self) -> None:
        # Migration rule (refuse-at-kill): a mark stamped under pre-hold-fix
        # rules becomes inert once a hold is observed; the pane is never killed.
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "worker.log"
                evidence.write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                p = managed(
                    "held-marked",
                    kind="visible-agent",
                    cleanup_policy="kill_after_ttl",
                    state="done",
                    ttl="10m",
                    run_root=tmp,
                    evidence_path="worker.log",
                    completed_at=(now - timedelta(minutes=20)).isoformat().replace("+00:00", "Z"),
                    teardown_marked_at=(now - timedelta(minutes=60)).isoformat().replace("+00:00", "Z"),
                    teardown_reason="completed_idle_teardown",
                    hold_reason="parent review",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\nWorked for 5m\n› done\n"
                item = hygiene.eligible_managed("held-marked", [p], policy="kill-safe", grace=300, now=now)
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_held_marked_pane_refuses_within_grace_without_countdown(self) -> None:
        # A held+marked conflict must never render as a clean countdown.
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "worker.log"
            evidence.write_text("done\n", encoding="utf-8")
            now = datetime.now(timezone.utc)
            p = managed(
                "held-marked-grace",
                kind="visible-agent",
                cleanup_policy="kill_after_ttl",
                state="done",
                ttl="10m",
                run_root=tmp,
                evidence_path="worker.log",
                completed_at=(now - timedelta(minutes=20)).isoformat().replace("+00:00", "Z"),
                teardown_marked_at=(now - timedelta(seconds=30)).isoformat().replace("+00:00", "Z"),
                teardown_reason="completed_idle_teardown",
                hold_reason="parent review",
            )
            item = hygiene.eligible_managed("held-marked-grace", [p], policy="kill-safe", grace=300, now=now)
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")
        self.assertNotIn("kill_not_before", item)

    def test_override_hold_allows_marked_teardown_kill(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "worker.log"
                evidence.write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                p = managed(
                    "held-marked-override",
                    kind="visible-agent",
                    cleanup_policy="kill_after_ttl",
                    state="done",
                    ttl="10m",
                    run_root=tmp,
                    evidence_path="worker.log",
                    completed_at=(now - timedelta(minutes=20)).isoformat().replace("+00:00", "Z"),
                    teardown_marked_at=(now - timedelta(minutes=60)).isoformat().replace("+00:00", "Z"),
                    teardown_reason="completed_idle_teardown",
                    hold_reason="parent review",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: "OpenAI Codex\nWorked for 5m\n› done\n"
                item = hygiene.eligible_managed(
                    "held-marked-override", [p], policy="kill-safe", grace=300, now=now, override_hold=True
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "completed_idle_teardown")

    def test_hold_reason_blocks_live_idle_mark_when_due(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                text = "quiet delivered tail with nothing new\n"
                tail_hash = hygiene.normalized_tail_hash(text)
                p = managed(
                    "held-idle-due",
                    kind="visible-agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="10m",
                    run_root=tmp,
                    evidence_path="worker.log",
                    hold_reason="parent review",
                )
                stable_since = (now - timedelta(seconds=hygiene.ACTIVE_IDLE_MARK_SECONDS + 60)).isoformat().replace("+00:00", "Z")
                status = {"sessions": {"held-idle-due": {"tail_hash": tail_hash, "tail_hash_since": stable_since}}}
                hygiene.capture_pane_text = lambda pane, lines=240: text
                item = hygiene.eligible_managed(
                    "held-idle-due", [p], policy="kill-safe", grace=300, now=now, status_state=status
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")
        self.assertEqual(item["tail_hash"], tail_hash)

    def test_hold_reason_blocks_failed_cleanup_after_visible_grace(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            Path(tmp, "worker.log").write_text("failed\n", encoding="utf-8")
            p = managed(
                "held-failed",
                kind="agent",
                cleanup_policy="kill_on_done",
                state="failed",
                ttl="never",
                run_root=tmp,
                evidence_path="worker.log",
                updated_at=(datetime.now(timezone.utc) - timedelta(seconds=hygiene.FAILED_VISIBLE_SECONDS + 100)).isoformat().replace("+00:00", "Z"),
                completed_at="",
                hold_reason="investigate failure",
            )
            p.dead = True
            p.dead_status = "1"
            item = hygiene.eligible_managed("held-failed", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_hold_reason_override_allows_cleanup(self) -> None:
        item = hygiene.eligible_managed(
            "oc-vis-smoke-held",
            [managed("oc-vis-smoke-held", hold_reason="parent review")],
            policy="smoke",
            grace=300,
            now=datetime.now(timezone.utc),
            override_hold=True,
        )
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "managed_smoke_complete")

    def test_smoke_cleanup_requires_smoke_prefix(self) -> None:
        item = hygiene.eligible_managed(
            "not-a-smoke-session",
            [managed("not-a-smoke-session")],
            policy="smoke",
            grace=300,
            now=datetime.now(timezone.utc),
        )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "smoke_policy_without_smoke_prefix")

    def test_invalid_ttl_refuses_kill_after_ttl(self) -> None:
        p = managed(
            "worker",
            kind="agent",
            cleanup_policy="kill_after_ttl",
            state="done",
            ttl="banana",
        )
        item = hygiene.eligible_managed("worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "invalid_ttl")

    def test_multi_pane_refused(self) -> None:
        item = hygiene.eligible_managed(
            "multi",
            [managed("multi"), managed("multi")],
            policy="smoke",
            grace=300,
            now=datetime.now(timezone.utc),
        )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "multi_pane_session_refused")

    def test_dead_running_pane_effective_state(self) -> None:
        p = managed("crashed", state="running")
        p.dead = True
        p.dead_status = "9"
        self.assertEqual(hygiene.effective_state(p), "failed")

    def test_dead_done_nonzero_effective_state_fails_closed(self) -> None:
        p = managed("crashed-after-metadata-done", state="done")
        p.dead = True
        p.dead_status = "7"
        self.assertEqual(hygiene.effective_state(p), "failed")

    def test_exact_allow_session_overrides_unowned(self) -> None:
        original = hygiene.list_panes
        try:
            hygiene.list_panes = lambda: [pane("unowned")]
            args = argparse.Namespace(policy="kill-safe", grace=300, allow_session=["unowned"])
            plan = hygiene.build_plan(args)
        finally:
            hygiene.list_panes = original
        self.assertEqual(plan[0]["action"], "kill")
        self.assertEqual(plan[0]["reason"], "exact_allow_session")

    def test_exact_allow_session_respects_hold_without_override(self) -> None:
        original = hygiene.list_panes
        try:
            hygiene.list_panes = lambda: [managed("held", hold_reason="parent synthesis")]
            args = argparse.Namespace(policy="kill-safe", grace=300, allow_session=["held"], override_hold=False)
            plan = hygiene.build_plan(args)
        finally:
            hygiene.list_panes = original
        self.assertEqual(plan[0]["action"], "refuse")
        self.assertEqual(plan[0]["reason"], "hold_reason_active_allow_session")

    def test_completion_grace_blocks_kill_on_done(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "worker.log"
            evidence.write_text("done\n", encoding="utf-8")
            p = managed(
                "worker",
                kind="agent",
                cleanup_policy="kill_on_done",
                state="done",
                ttl="never",
                run_root=tmp,
                evidence_path="worker.log",
                completed_at=(datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed("worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
            self.assertEqual(item["action"], "skip")
            self.assertEqual(item["reason"], "completion_grace_active")

    def test_evidence_must_be_nonempty_regular_file_under_run_root(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            empty = Path(tmp) / "empty.log"
            empty.write_text("", encoding="utf-8")
            p = managed(
                "worker",
                kind="agent",
                cleanup_policy="kill_on_done",
                state="done",
                run_root=tmp,
                evidence_path="empty.log",
                completed_at=(datetime.now(timezone.utc) - timedelta(seconds=400)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed("worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
            self.assertEqual(item["action"], "kill")
            self.assertEqual(item["evidence_warning"], "evidence_empty")

            p.meta["evidence_path"] = "."
            item = hygiene.eligible_managed("worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
            self.assertEqual(item["evidence_warning"], "evidence_not_regular_file")

            p.meta["evidence_path"] = "../outside.log"
            item = hygiene.eligible_managed("worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
            self.assertEqual(item["evidence_warning"], "evidence_outside_run_root")

    def test_dead_cleanable_pane_can_use_updated_at_completion_fallback(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "worker.log"
            evidence.write_text("failed\n", encoding="utf-8")
            p = managed(
                "worker",
                kind="agent",
                cleanup_policy="kill_on_done",
                state="failed",
                ttl="never",
                run_root=tmp,
                evidence_path="worker.log",
                updated_at=(datetime.now(timezone.utc) - timedelta(seconds=1000)).isoformat().replace("+00:00", "Z"),
                completed_at="",
            )
            p.dead = True
            p.dead_status = "1"
            item = hygiene.eligible_managed("worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
            self.assertEqual(item["action"], "kill")
            self.assertEqual(item["reason"], "failed_visible_grace_expired")

    def test_cleanup_eligibility_matrix_stays_unchanged_for_truth_cleanup(self) -> None:
        now = datetime.now(timezone.utc)
        old = (now - timedelta(seconds=1000)).isoformat().replace("+00:00", "Z")
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            evidence = root / "worker.log"
            evidence.write_text("done\n", encoding="utf-8")
            cases = [
                (
                    "managed-kill-on-done",
                    managed(
                        "managed-kill-on-done",
                        kind="agent",
                        cleanup_policy="kill_on_done",
                        state="done",
                        ttl="never",
                        run_root=str(root),
                        evidence_path="worker.log",
                        completed_at=old,
                    ),
                    "kill",
                    "terminal_live_grace_expired",
                ),
                (
                    "manual-policy",
                    managed(
                        "manual-policy",
                        kind="agent",
                        cleanup_policy="manual",
                        state="done",
                        run_root=str(root),
                        evidence_path="worker.log",
                        completed_at=old,
                    ),
                    "skip",
                    "policy_manual",
                ),
                (
                    "display-only",
                    pane(
                        "display-only",
                        contract_version="display-only",
                        managed_by="manual_adopt",
                        kind="detected-agent",
                        cleanup_policy="manual",
                        state="done",
                        run_root=str(root),
                        evidence_path="worker.log",
                    ),
                    "skip",
                    "unmanaged_or_incomplete_contract",
                ),
                (
                    "dead-nonzero-with-evidence",
                    managed(
                        "dead-nonzero-with-evidence",
                        kind="agent",
                        cleanup_policy="kill_on_done",
                        state="done",
                        ttl="never",
                        run_root=str(root),
                        evidence_path="worker.log",
                        completed_at=old,
                    ),
                    "kill",
                    "failed_visible_grace_expired",
                ),
                (
                    "artifact-present",
                    managed(
                        "artifact-present",
                        kind="agent",
                        cleanup_policy="kill_after_ttl",
                        state="done",
                        ttl="15m",
                        run_root=str(root),
                        evidence_path="worker.log",
                        completed_at=old,
                    ),
                    "kill",
                    "terminal_live_grace_expired",
                ),
            ]
            for name, p, want_action, want_reason in cases:
                with self.subTest(name=name):
                    if name == "dead-nonzero-with-evidence":
                        p.dead = True
                        p.dead_status = "1"
                    item = hygiene.eligible_managed(name, [p], policy="kill-safe", grace=300, now=now)
                    self.assertEqual(item["action"], want_action)
                    self.assertEqual(item["reason"], want_reason)

    def test_default_main_uses_plan_defaults(self) -> None:
        original = hygiene.list_panes
        try:
            hygiene.list_panes = lambda: [pane("plain")]
            self.assertEqual(hygiene.main([]), 0)
        finally:
            hygiene.list_panes = original

    def test_list_panes_warns_on_malformed_rows(self) -> None:
        original = hygiene.run_tmux
        malformed = "too few fields\n"
        try:
            hygiene.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(args, 0, malformed, "")
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                panes = hygiene.list_panes()
        finally:
            hygiene.run_tmux = original
        self.assertEqual(panes, [])
        self.assertIn("skipped 1 malformed", stderr.getvalue())

    def test_bare_integer_ttl_is_seconds(self) -> None:
        self.assertEqual(hygiene.parse_ttl("7200"), (7200, "ok"))

    def test_blocked_live_session_uses_updated_at_age_fallback(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "worker.log"
            evidence.write_text("blocked\n", encoding="utf-8")
            p = managed(
                "blocked-worker",
                kind="agent",
                cleanup_policy="kill_after_ttl",
                state="blocked",
                ttl="7200",
                run_root=tmp,
                evidence_path="worker.log",
                completed_at="",
                updated_at=(datetime.now(timezone.utc) - timedelta(seconds=1000)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed("blocked-worker", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "failed_visible_grace_expired")

    def test_teardown_mark_grace_supersedes_ttl(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "worker.log"
            evidence.write_text("done\n", encoding="utf-8")
            now = datetime.now(timezone.utc)
            p = managed(
                "marked-worker",
                kind="agent",
                cleanup_policy="kill_after_ttl",
                state="done",
                ttl="180m",
                run_root=tmp,
                evidence_path="worker.log",
                completed_at=(now - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
                teardown_marked_at=(now - timedelta(minutes=11)).isoformat().replace("+00:00", "Z"),
                teardown_reason="hold_released",
            )
            with patch.object(hygiene, "capture_pane_text", return_value="new output after mark"):
                item = hygiene.eligible_managed("marked-worker", [p], policy="kill-safe", grace=300, now=now)
        self.assertEqual(item["action"], "cancel_mark")
        self.assertEqual(item["reason"], "meaningful_output_after_teardown_mark")

    def test_meaningful_output_cancels_teardown_mark(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                p = managed(
                    "marked-active",
                    kind="agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
                )
                status = {"sessions": {"marked-active": {"tail_hash": "old-hash"}}}
                hygiene.capture_pane_text = lambda pane, lines=240: "new useful output\nstill working\n"
                item = hygiene.eligible_managed("marked-active", [p], policy="kill-safe", grace=300, now=now, status_state=status)
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "cancel_mark")
        self.assertEqual(item["reason"], "meaningful_output_after_teardown_mark")

    def test_marked_pane_missing_sidecar_baseline_resumed_output_does_not_kill(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                p = managed(
                    "marked-missing-baseline",
                    kind="agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(seconds=hygiene.TEARDOWN_GRACE_SECONDS + 60))
                    .isoformat()
                    .replace("+00:00", "Z"),
                    teardown_reason="managed_tui_completed",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: "new useful output\nstill working\n"
                item = hygiene.eligible_managed(
                    "marked-missing-baseline",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now,
                    status_state={"sessions": {}},
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "cancel_mark")
        self.assertEqual(item["reason"], "meaningful_output_after_teardown_mark")
        self.assertEqual(item["tail_hash"], hygiene.normalized_tail_hash("new useful output\nstill working\n"))

    def test_marked_done_pane_missing_baseline_live_screen_cancels(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                live_screen = "OpenAI Codex\nworking on follow-up\nesc to interrupt\n"
                p = managed(
                    "marked-done-but-live",
                    kind="agent",
                    cleanup_policy="kill_after_ttl",
                    state="done",
                    run_root=tmp,
                    evidence_path="worker.log",
                    completed_at=(now - timedelta(minutes=20)).isoformat().replace("+00:00", "Z"),
                    teardown_marked_at=(now - timedelta(seconds=hygiene.TEARDOWN_GRACE_SECONDS + 60))
                    .isoformat()
                    .replace("+00:00", "Z"),
                    teardown_reason="managed_tui_completed",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: live_screen
                item = hygiene.eligible_managed(
                    "marked-done-but-live",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now,
                    status_state={"sessions": {}},
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "cancel_mark")
        self.assertEqual(item["reason"], "meaningful_output_after_teardown_mark")
        self.assertEqual(item["tail_hash"], hygiene.normalized_tail_hash(live_screen))

    def test_marked_pane_missing_baseline_rearms_static_completion_before_kill(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                completion = (
                    "─ Worked for 5m 47s ─────────────────────────\n"
                    "› Use /skills to list available skills\n"
                )
                p = managed(
                    "marked-static-missing-baseline",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(seconds=hygiene.TEARDOWN_GRACE_SECONDS + 60))
                    .isoformat()
                    .replace("+00:00", "Z"),
                    teardown_reason="managed_tui_completed",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: completion
                first = hygiene.eligible_managed(
                    "marked-static-missing-baseline",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now,
                    status_state={"sessions": {}},
                )
                self.assertEqual(first["action"], "skip")
                self.assertEqual(first["reason"], "teardown_baseline_missing")
                self.assertEqual(first["janitor_state"], "marked_for_teardown")
                self.assertEqual(first["tail_hash"], hygiene.normalized_tail_hash(completion))
                args = argparse.Namespace(status_file="", policy="kill-safe", interval=0)
                status = hygiene.status_payload([first], args)
                second = hygiene.eligible_managed(
                    "marked-static-missing-baseline",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now + timedelta(seconds=30),
                    status_state=status,
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(second["action"], "kill")
        self.assertEqual(second["reason"], "managed_tui_completed")

    def test_marked_pane_rearmed_baseline_cancels_if_work_resumes_next_cycle(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                completion = (
                    "─ Worked for 5m 47s ─────────────────────────\n"
                    "› Use /skills to list available skills\n"
                )
                p = managed(
                    "marked-rearmed-then-active",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(seconds=hygiene.TEARDOWN_GRACE_SECONDS + 60))
                    .isoformat()
                    .replace("+00:00", "Z"),
                    teardown_reason="managed_tui_completed",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: completion
                first = hygiene.eligible_managed(
                    "marked-rearmed-then-active",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now,
                    status_state={"sessions": {}},
                )
                args = argparse.Namespace(status_file="", policy="kill-safe", interval=0)
                status = hygiene.status_payload([first], args)
                hygiene.capture_pane_text = lambda pane, lines=240: "new useful output\nstill working\n"
                second = hygiene.eligible_managed(
                    "marked-rearmed-then-active",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now + timedelta(seconds=30),
                    status_state=status,
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(second["action"], "cancel_mark")
        self.assertEqual(second["reason"], "meaningful_output_after_teardown_mark")

    def test_marked_pane_empty_capture_after_grace_never_kills(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                p = managed(
                    "marked-empty-capture",
                    kind="agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(seconds=hygiene.TEARDOWN_GRACE_SECONDS + 60))
                    .isoformat()
                    .replace("+00:00", "Z"),
                    teardown_reason="managed_tui_completed",
                )
                hygiene.capture_pane_text = lambda pane, lines=240: ""
                item = hygiene.eligible_managed(
                    "marked-empty-capture",
                    [p],
                    policy="kill-safe",
                    grace=300,
                    now=now,
                    status_state={
                        "sessions": {
                            "marked-empty-capture": {
                                "tail_hash": "previous-hash",
                                "tail_hash_since": hygiene.isoformat(now - timedelta(minutes=10)),
                            }
                        }
                    },
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "cancel_mark")
        self.assertEqual(item["reason"], "completion_not_proven")

    def test_cosmetic_churn_does_not_cancel_teardown_mark(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                text = "gpt-5.5 high · 12k tokens · 20:00\n/clear to save tokens\n"
                tail_hash = hygiene.normalized_tail_hash(text)
                p = managed(
                    "marked-cosmetic",
                    kind="agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
                )
                status = {"sessions": {"marked-cosmetic": {"tail_hash": tail_hash}}}
                hygiene.capture_pane_text = lambda pane, lines=240: "gpt-5.5 high · 13k tokens · 20:01\n/clear to save tokens\n"
                item = hygiene.eligible_managed("marked-cosmetic", [p], policy="kill-safe", grace=300, now=now, status_state=status)
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["reason"], "teardown_baseline_missing")

    def test_stale_precompletion_baseline_does_not_cancel_mark(self) -> None:
        # The true production loop: the sidecar baseline was recorded before
        # the completion screen appeared, so the marked pane's capture hash
        # differs from the baseline every cycle. The capture still shows the
        # completion screen, so the mark must be retained, not cancelled.
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                pre_completion = "• Working (2m 10s • esc to interrupt)\n› building the artifact\n"
                completion = (
                    "─ Worked for 5m 47s ─────────────────────────\n"
                    "› Use /skills to list available skills\n"
                )
                p = managed(
                    "loop-victim",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="15m",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
                    teardown_reason="managed_tui_completed",
                )
                status = {"sessions": {"loop-victim": {"tail_hash": hygiene.normalized_tail_hash(pre_completion)}}}
                hygiene.capture_pane_text = lambda pane, lines=240: completion
                item = hygiene.eligible_managed(
                    "loop-victim", [p], policy="kill-safe", grace=300, now=now, status_state=status
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "managed_tui_completed")

    def test_statically_completed_pane_retains_mark_across_cycles_to_kill(self) -> None:
        # Mark carries the mark-time normalized baseline; a static completion
        # screen then holds the mark across repeated cycles until the grace
        # expires and the pane becomes kill-eligible.
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                evidence = Path(tmp) / "worker.log"
                evidence.write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                completion = (
                    "─ Worked for 5m 47s ─────────────────────────\n"
                    "› Use /skills to list available skills\n"
                )
                p = managed(
                    "stable-complete",
                    kind="codex-build",
                    agent="codex",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="15m",
                    run_root=tmp,
                    evidence_path="worker.log",
                    completed_at="",
                )
                p.command = "node"
                old_epoch = str(int(now.timestamp()) - 7200)
                p.created = old_epoch
                p.last_activity = old_epoch
                old_mtime = int(now.timestamp()) - 3600
                os.utime(evidence, (old_mtime, old_mtime))
                hygiene.capture_pane_text = lambda pane, lines=240: completion
                item = hygiene.eligible_managed("stable-complete", [p], policy="kill-safe", grace=300, now=now)
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["reason"], "managed_tui_completed")

    def test_operator_prompt_after_mark_cancels_teardown(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                prompt = "Would you like to proceed?\n1. approve\n2. reject\n"
                p = managed(
                    "marked-prompt",
                    kind="agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    run_root=tmp,
                    evidence_path="worker.log",
                    teardown_marked_at=(now - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
                )
                hygiene.capture_pane_text = lambda pane, lines=240: prompt
                item = hygiene.eligible_managed("marked-prompt", [p], policy="kill-safe", grace=300, now=now)
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "cancel_mark")
        self.assertEqual(item["reason"], "operator_prompt_after_teardown_mark")
        self.assertEqual(item["tail_hash"], hygiene.normalized_tail_hash("Would you like to proceed?\n1. approve\n2. reject\n"))

    def test_evidence_refusal_rows_carry_cleanup_blocked_state(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            empty = Path(tmp) / "empty.log"
            empty.write_text("", encoding="utf-8")
            p = managed(
                "blocked-evidence",
                kind="agent",
                cleanup_policy="kill_on_done",
                state="done",
                run_root=tmp,
                evidence_path="empty.log",
                completed_at=(datetime.now(timezone.utc) - timedelta(seconds=400)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed("blocked-evidence", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        self.assertEqual(item["action"], "kill")
        self.assertEqual(item["evidence_warning"], "evidence_empty")
        self.assertEqual(item["janitor_state"], "archiving_non_evidence")
        args = argparse.Namespace(status_file="", policy="kill-safe", interval=0)
        payload = hygiene.status_payload([item], args)
        self.assertEqual(payload["sessions"]["blocked-evidence"]["janitor_state"], "archiving_non_evidence")

    def test_structural_refusals_publish_cleanup_blocked_state(self) -> None:
        p = managed("bad-ttl", kind="agent", cleanup_policy="kill_after_ttl", state="done", ttl="banana")
        item = hygiene.eligible_managed("bad-ttl", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc))
        self.assertEqual(item["action"], "refuse")
        args = argparse.Namespace(status_file="", policy="kill-safe", interval=0)
        payload = hygiene.status_payload([item], args)
        self.assertEqual(payload["sessions"]["bad-ttl"]["janitor_state"], "cleanup_blocked")

    def test_session_name_reuse_does_not_inherit_tail_hash(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
                now = datetime.now(timezone.utc)
                text = "quiet delivered tail with nothing new\n"
                tail_hash = hygiene.normalized_tail_hash(text)
                p = managed(
                    "reused-name",
                    kind="visible-agent",
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="10m",
                    run_root=tmp,
                    evidence_path="worker.log",
                )
                stable_since = (now - timedelta(seconds=hygiene.ACTIVE_IDLE_MARK_SECONDS + 60)).isoformat().replace("+00:00", "Z")
                old_row = {
                    "tail_hash": tail_hash,
                    "tail_hash_since": stable_since,
                    "pane_created": str(int(p.created) - 5000),
                    "pane_id": "%99",
                }
                status = {"sessions": {"reused-name": old_row}}
                hygiene.capture_pane_text = lambda pane, lines=240: text
                item = hygiene.eligible_managed(
                    "reused-name", [p], policy="kill-safe", grace=300, now=now, status_state=status
                )
                self.assertEqual(item["action"], "skip", "stale identity row must not make a fresh pane mark-due")
                self.assertEqual(item["tail_hash"], tail_hash)
                # Same identity preserves observation, but quiet output is not completion.
                matching_row = dict(old_row, pane_created=p.created, pane_id=p.pane)
                status = {"sessions": {"reused-name": matching_row}}
                item = hygiene.eligible_managed(
                    "reused-name", [p], policy="kill-safe", grace=300, now=now, status_state=status
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "skip")
        self.assertEqual(item["tail_hash"], tail_hash)

    def test_status_payload_does_not_inherit_tail_hash_across_pane_identity(self) -> None:
        p = managed("reused-name", state="running", cleanup_policy="kill_after_ttl", ttl="10m")
        item = hygiene.result(session="reused-name", action="skip", reason="state_not_cleanable:running", panes=[p], policy_source="managed")
        item["janitor_state"] = "active"
        with tempfile.TemporaryDirectory() as tmp:
            status_path = Path(tmp) / "status.json"
            stale = {
                "status_version": hygiene.STATUS_VERSION,
                "sessions": {
                    "reused-name": {
                        "tail_hash": "stale-hash-from-dead-pane",
                        "tail_hash_since": "2026-07-09T00:00:00Z",
                        "pane_created": str(int(p.created) - 5000),
                        "pane_id": "%99",
                    }
                },
            }
            status_path.write_text(json.dumps(stale), encoding="utf-8")
            args = argparse.Namespace(status_file=str(status_path), policy="kill-safe", interval=0)
            payload = hygiene.status_payload([item], args)
        row = payload["sessions"]["reused-name"]
        self.assertEqual(row["tail_hash"], "")
        self.assertEqual(row["pane_id"], p.pane)
        self.assertEqual(row["pane_created"], p.created)

    def test_timer_constants_match_contract_reset(self) -> None:
        self.assertEqual(hygiene.ACTIVE_IDLE_MARK_SECONDS, 180)
        self.assertEqual(hygiene.TEARDOWN_GRACE_SECONDS, 60)
        self.assertEqual(hygiene.FAILED_VISIBLE_SECONDS, 180)

    def test_mark_kill_not_before_uses_teardown_grace(self) -> None:
        now = datetime.now(timezone.utc).replace(microsecond=0)
        item = hygiene.mark_item("timer-check", [managed("timer-check")], "completed_idle_teardown", now)
        expected = now + timedelta(seconds=hygiene.TEARDOWN_GRACE_SECONDS)
        self.assertEqual(item["kill_not_before"], hygiene.isoformat(expected))

    def test_status_file_atomic_schema_round_trip(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "status.json"
            payload = {"sessions": {"worker": {"janitor_state": "active"}}}
            hygiene.write_status(path, payload)
            loaded = hygiene.load_status(path)
        self.assertEqual(loaded["status_version"], hygiene.STATUS_VERSION)
        self.assertIn("generated_at", loaded)
        self.assertEqual(loaded["sessions"]["worker"]["janitor_state"], "active")

    def test_plan_and_list_do_not_write_status_without_explicit_opt_in(self) -> None:
        original = hygiene.build_plan
        try:
            item = hygiene.result(
                session="dry-run",
                action="skip",
                reason="test",
                panes=[managed("dry-run", cleanup_policy="kill_after_ttl", state="running")],
                policy_source="managed",
            )
            hygiene.build_plan = lambda args: [item]
            with tempfile.TemporaryDirectory() as tmp:
                plan_status = Path(tmp) / "plan-status.json"
                args = argparse.Namespace(
                    json=False,
                    status_file=str(plan_status),
                    policy="kill-safe",
                    interval=0,
                    write_status=False,
                )
                with contextlib.redirect_stdout(io.StringIO()):
                    hygiene.cmd_plan(args)
                self.assertFalse(plan_status.exists())
                args.write_status = True
                with contextlib.redirect_stdout(io.StringIO()):
                    hygiene.cmd_plan(args)
                self.assertTrue(plan_status.exists())

                list_status = Path(tmp) / "list-status.json"
                args.status_file = str(list_status)
                args.write_status = False
                with contextlib.redirect_stdout(io.StringIO()):
                    hygiene.cmd_list(args)
                self.assertFalse(list_status.exists())
        finally:
            hygiene.build_plan = original

    def test_archive_falls_back_to_central_ledger_root(self) -> None:
        original_run = hygiene.run_tmux
        original_archive_base = hygiene.archive_base_for
        try:
            with tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                bad_parent = root / "not-a-dir"
                bad_parent.write_text("blocks mkdir\n", encoding="utf-8")
                p = managed("worker", run_root=str(root), evidence_path="worker.log")
                (root / "worker.log").write_text("done\n", encoding="utf-8")
                item = hygiene.result(session="worker", action="kill", reason="test", panes=[p], policy_source="managed", tmux_target="=worker")
                hygiene.archive_base_for = lambda item, panes, args: bad_parent / ".tmux-cleanup"
                hygiene.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(args, 0, "pane text\n", "")
                archive = hygiene.archive_cleanup(item, [p], argparse.Namespace(archive_root=str(root / "central")))
        finally:
            hygiene.run_tmux = original_run
            hygiene.archive_base_for = original_archive_base
        self.assertTrue(archive["archive_fallback"])
        self.assertIn("not-a-dir", archive["archive_fallback_from"])

    def test_apply_archives_and_ledgers_before_kill(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            evidence = root / "worker.log"
            evidence.write_text("done\n", encoding="utf-8")
            p = managed(
                "worker",
                kind="agent",
                cleanup_policy="kill_on_done",
                state="done",
                run_root=str(root),
                evidence_path="worker.log",
                completed_at=(datetime.now(timezone.utc) - timedelta(seconds=400)).isoformat().replace("+00:00", "Z"),
            )
            p.dead = True
            calls: list[str] = []
            original_list = hygiene.list_panes
            original_run = hygiene.run_tmux
            try:
                hygiene.list_panes = lambda: [p]

                def fake_run_tmux(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
                    if args[0] == "capture-pane":
                        calls.append("capture")
                        return subprocess.CompletedProcess(args, 0, "api_key=secret-value\nok\n", "")
                    if args[0] == "if-shell":
                        calls.append("kill")
                        hygiene.list_panes = lambda: []
                        return subprocess.CompletedProcess(args, 0, "", "")
                    raise AssertionError(f"unexpected tmux call: {args}")

                hygiene.run_tmux = fake_run_tmux
                args = argparse.Namespace(
                    policy="kill-safe",
                    grace=0,
                    allow_session=[],
                    override_hold=False,
                    max_kills=10,
                    json=True,
                    archive_root=str(root / "central"),
                )
                stdout = io.StringIO()
                with contextlib.redirect_stdout(stdout):
                    self.assertEqual(hygiene.cmd_apply(args), 0)
            finally:
                hygiene.list_panes = original_list
                hygiene.run_tmux = original_run

            self.assertEqual(calls, ["capture", "kill"])
            result = json.loads(stdout.getvalue())
            self.assertEqual(result["killed"][0]["session"], "worker")
            archive = Path(result["killed"][0]["archive_path"])
            self.assertTrue((archive / "metadata.json").exists())
            capture_text = next(archive.glob("*.log")).read_text(encoding="utf-8")
            self.assertIn("api_key=<redacted>", capture_text)
            central_ledger = root / "central" / "cleanup.jsonl"
            run_ledger = root / ".tmux-cleanup" / "cleanup.jsonl"
            self.assertTrue(central_ledger.exists())
            self.assertTrue(run_ledger.exists())
            events = [json.loads(line)["event"] for line in central_ledger.read_text(encoding="utf-8").splitlines()]
            self.assertEqual(events, ["kill_attempt", "kill_result"])


def held_terminal_pane(session: str, tmp: str, **meta: str) -> hygiene.Pane:
    """A managed pane that would be cleanup-eligible if it were not held."""
    Path(tmp, "worker.log").write_text("evidence\n", encoding="utf-8")
    base = {
        "kind": "visible-agent",
        "agent": "opus",
        "run_root": tmp,
        "evidence_path": "worker.log",
        "hold_reason": "parent review",
    }
    base.update(meta)
    return managed(session, **base)


class SessionHygieneHoldGateTests(unittest.TestCase):
    """Every terminal cleanup path must consult the hold gate, not just one."""

    def test_hold_reason_blocks_virtual_completion_kill(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                p = held_terminal_pane(
                    "held-virtual",
                    tmp,
                    cleanup_policy="kill_after_ttl",
                    state="running",
                    ttl="15m",
                    started_at=(datetime.now(timezone.utc) - timedelta(hours=2)).isoformat().replace("+00:00", "Z"),
                )
                old_mtime = int(datetime.now(timezone.utc).timestamp()) - 3600
                os.utime(Path(tmp, "worker.log"), (old_mtime, old_mtime))
                hygiene.capture_pane_text = lambda pane, lines=240: (
                    "Claude Code\n─ Worked for 5m 47s ──────────\n❯ \n"
                )
                item = hygiene.eligible_managed(
                    "held-virtual", [p], policy="kill-safe", grace=300, now=datetime.now(timezone.utc)
                )
        finally:
            hygiene.capture_pane_text = original_capture
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_hold_reason_blocks_kill_on_done(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            p = held_terminal_pane(
                "held-kill-on-done",
                tmp,
                kind="agent",
                cleanup_policy="kill_on_done",
                state="done",
                ttl="never",
                completed_at=(datetime.now(timezone.utc) - timedelta(minutes=30)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed(
                "held-kill-on-done", [p], policy="kill-safe", grace=0, now=datetime.now(timezone.utc)
            )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_hold_reason_blocks_ttl_expired_kill(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            p = held_terminal_pane(
                "held-ttl",
                tmp,
                kind="agent",
                cleanup_policy="kill_after_ttl",
                state="blocked",
                ttl="1m",
                completed_at=(datetime.now(timezone.utc) - timedelta(hours=1)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed(
                "held-ttl", [p], policy="kill-safe", grace=0, now=datetime.now(timezone.utc)
            )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_hold_reason_blocks_stale_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            p = held_terminal_pane(
                "held-stale",
                tmp,
                kind="agent",
                cleanup_policy="kill_after_ttl",
                state="stale",
                ttl="1m",
                completed_at=(datetime.now(timezone.utc) - timedelta(hours=1)).isoformat().replace("+00:00", "Z"),
            )
            item = hygiene.eligible_managed(
                "held-stale", [p], policy="kill-safe", grace=0, now=datetime.now(timezone.utc)
            )
        self.assertEqual(item["action"], "refuse")
        self.assertEqual(item["reason"], "hold_reason_active")
        self.assertEqual(item["janitor_state"], "protected")

    def test_every_hold_gate_uses_one_refusal_code(self) -> None:
        # The refusal code operators and Cockpit read must not vary by branch.
        codes = set()
        with tempfile.TemporaryDirectory() as tmp:
            for state, cleanup, ttl in [
                ("done", "kill_on_done", "never"),
                ("failed", "kill_on_done", "never"),
                ("stale", "kill_after_ttl", "1m"),
                ("blocked", "kill_after_ttl", "1m"),
            ]:
                p = held_terminal_pane(
                    f"held-{state}",
                    tmp,
                    kind="agent",
                    cleanup_policy=cleanup,
                    state=state,
                    ttl=ttl,
                    completed_at=(datetime.now(timezone.utc) - timedelta(hours=1)).isoformat().replace("+00:00", "Z"),
                )
                item = hygiene.eligible_managed(
                    f"held-{state}", [p], policy="kill-safe", grace=0, now=datetime.now(timezone.utc)
                )
                self.assertEqual(item["action"], "refuse", state)
                codes.add(item["reason"])
        self.assertEqual(codes, {"hold_reason_active"})


class SessionHygieneOverrideScopeTests(unittest.TestCase):
    def test_override_hold_requires_explicit_allow_session(self) -> None:
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            with self.assertRaises(SystemExit) as caught:
                hygiene.main(["plan", "--override-hold"])
        self.assertEqual(caught.exception.code, 2)
        self.assertIn("--override-hold requires", stderr.getvalue())

    def test_override_hold_allows_only_named_session_in_plan(self) -> None:
        original = hygiene.list_panes
        try:
            with tempfile.TemporaryDirectory() as tmp:
                fixtures = [
                    held_terminal_pane("held-a", tmp, kind="agent", cleanup_policy="kill_on_done", state="done"),
                    held_terminal_pane("held-b", tmp, kind="agent", cleanup_policy="kill_on_done", state="done"),
                ]
                hygiene.list_panes = lambda: fixtures
                args = argparse.Namespace(
                    policy="kill-safe",
                    grace=0,
                    allow_session=["held-a"],
                    override_hold=True,
                )
                plan = {item["session"]: item for item in hygiene.build_plan(args)}
        finally:
            hygiene.list_panes = original
        self.assertEqual(plan["held-a"]["action"], "kill")
        self.assertEqual(plan["held-a"]["reason"], "exact_allow_session")
        self.assertEqual(plan["held-b"]["action"], "refuse")
        self.assertEqual(plan["held-b"]["reason"], "hold_reason_active")
        self.assertEqual(plan["held-b"]["janitor_state"], "protected")

    def test_apply_override_hold_kills_only_named_session(self) -> None:
        original_list = hygiene.list_panes
        original_run = hygiene.run_tmux
        killed_targets: list[str] = []
        try:
            with tempfile.TemporaryDirectory() as tmp:
                fixtures = [
                    held_terminal_pane("held-a", tmp, kind="agent", cleanup_policy="kill_on_done", state="done"),
                    held_terminal_pane("held-b", tmp, kind="agent", cleanup_policy="kill_on_done", state="done"),
                ]
                hygiene.list_panes = lambda: fixtures

                def fake_run_tmux(*call: str, check: bool = True) -> subprocess.CompletedProcess[str]:
                    if call[0] == "capture-pane":
                        return subprocess.CompletedProcess(call, 0, "held pane output\n", "")
                    if call[0] == "kill-session":
                        killed_targets.append(call[-1])
                        return subprocess.CompletedProcess(call, 0, "", "")
                    raise AssertionError(f"unexpected tmux call: {call}")

                hygiene.run_tmux = fake_run_tmux
                args = argparse.Namespace(
                    policy="kill-safe",
                    grace=0,
                    allow_session=["held-a"],
                    override_hold=True,
                    max_kills=10,
                    json=True,
                    archive_root=str(Path(tmp) / "central"),
                )
                stdout = io.StringIO()
                with contextlib.redirect_stdout(stdout):
                    self.assertEqual(hygiene.cmd_apply(args), 0)
                outcome = json.loads(stdout.getvalue())
        finally:
            hygiene.list_panes = original_list
            hygiene.run_tmux = original_run

        self.assertEqual(outcome["killed"], [])
        self.assertEqual(killed_targets, [])
        self.assertEqual([item["session"] for item in outcome["refused"]], ["held-a", "held-b"])
        self.assertEqual(outcome["refused"][-1]["reason"], "hold_reason_active")


class SessionHygieneApplyRevalidationTests(unittest.TestCase):
    """Apply must re-read live state immediately before it acts."""

    @staticmethod
    def two_snapshots(before: list[hygiene.Pane], after: list[hygiene.Pane]):
        # build_plan takes the first snapshot; every apply-time revalidation
        # takes the second, so the fixture models a real between-cycle change.
        state = {"calls": 0}

        def lister() -> list[hygiene.Pane]:
            state["calls"] += 1
            return list(before) if state["calls"] == 1 else list(after)

        return lister

    def run_apply(
        self,
        before: list[hygiene.Pane],
        after: list[hygiene.Pane],
        *,
        archive_root: Path,
        status_file: str = "",
        allow_session: list[str] | None = None,
    ) -> tuple[dict, list[tuple[str, ...]]]:
        calls: list[tuple[str, ...]] = []
        original_list = hygiene.list_panes
        original_run = hygiene.run_tmux
        try:
            hygiene.list_panes = self.two_snapshots(before, after)

            def fake_run_tmux(*call: str, check: bool = True) -> subprocess.CompletedProcess[str]:
                calls.append(call)
                if call[0] == "capture-pane":
                    return subprocess.CompletedProcess(call, 0, "pane output\n", "")
                return subprocess.CompletedProcess(call, 0, "", "")

            hygiene.run_tmux = fake_run_tmux
            args = argparse.Namespace(
                policy="kill-safe",
                grace=0,
                allow_session=list(allow_session or []),
                override_hold=False,
                max_kills=10,
                json=True,
                archive_root=str(archive_root),
                status_file=status_file,
            )
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                self.assertEqual(hygiene.cmd_apply(args), 0)
            return json.loads(stdout.getvalue()), calls
        finally:
            hygiene.list_panes = original_list
            hygiene.run_tmux = original_run

    def killable_pane(self, tmp: str, session: str = "worker") -> hygiene.Pane:
        Path(tmp, "worker.log").write_text("done\n", encoding="utf-8")
        return managed(
            session,
            kind="agent",
            cleanup_policy="kill_on_done",
            state="done",
            ttl="never",
            run_root=tmp,
            evidence_path="worker.log",
            completed_at=(datetime.now(timezone.utc) - timedelta(seconds=400)).isoformat().replace("+00:00", "Z"),
        )

    def markable_pane(self, tmp: str, session: str = "worker") -> hygiene.Pane:
        Path(tmp, "worker.log").write_text("progress\n", encoding="utf-8")
        return managed(
            session,
            kind="visible-agent",
            cleanup_policy="kill_after_ttl",
            state="running",
            ttl="15m",
            run_root=tmp,
            evidence_path="worker.log",
        )

    def test_apply_refuses_kill_when_hold_appears_after_planning(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            central = Path(tmp) / "central"
            before = self.killable_pane(tmp)
            after = self.killable_pane(tmp)
            after.meta["hold_reason"] = "parent review"
            outcome, calls = self.run_apply([before], [after], archive_root=central)

            self.assertEqual(outcome["killed"], [])
            # The plan really did reach a kill decision before apply stopped it.
            self.assertEqual(outcome["refused"][0]["tmux_target"], "=worker")
            self.assertEqual(outcome["refused"][0]["reason"], "hold_reason_active_at_apply")
            self.assertEqual(outcome["refused"][0]["janitor_state"], "protected")
            # No archive capture, no ledger, no kill.
            self.assertEqual([call[0] for call in calls if call[0] != "capture-pane"], [])
            self.assertFalse((central / "cleanup.jsonl").exists())
            self.assertFalse((Path(tmp) / ".tmux-cleanup").exists())

    def test_apply_refuses_kill_when_pane_identity_changes_after_planning(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            central = Path(tmp) / "central"
            before = self.killable_pane(tmp)
            after = self.killable_pane(tmp)
            after.pane = "%97"
            after.created = str(int(after.created) + 30)
            outcome, calls = self.run_apply([before], [after], archive_root=central)

            self.assertEqual(outcome["killed"], [])
            self.assertEqual(outcome["refused"][0]["tmux_target"], "=worker")
            self.assertEqual(outcome["refused"][0]["reason"], "pane_identity_changed_at_apply")
            self.assertEqual([call[0] for call in calls if call[0] != "capture-pane"], [])
            self.assertFalse((central / "cleanup.jsonl").exists())

    @staticmethod
    def two_pane_session(session: str = "multi") -> list[hygiene.Pane]:
        primary = pane(session)
        primary.pane = "%1"
        secondary = pane(session)
        secondary.pane = "%2"
        secondary.window = "second"
        secondary.created = str(int(secondary.created) + 5)
        return [primary, secondary]

    def test_apply_refuses_exact_allowlisted_kill_when_only_secondary_pane_is_replaced(self) -> None:
        """Same pane count and an unchanged primary pane is not enough proof."""
        with tempfile.TemporaryDirectory() as tmp:
            central = Path(tmp) / "central"
            before = self.two_pane_session()
            after = self.two_pane_session()
            after[1].pane = "%9"
            after[1].created = str(int(after[1].created) + 60)

            outcome, calls = self.run_apply(
                before, after, archive_root=central, allow_session=["multi"]
            )

            refused = outcome["refused"][0]
            # The plan really did reach an exact-allowlist kill decision, and
            # the primary pane and pane count both survived unchanged.
            self.assertEqual(refused["tmux_target"], "=multi")
            self.assertEqual(refused["pane_id"], "%1")
            self.assertEqual(refused["pane_count"], 2)
            self.assertEqual(refused["reason"], "pane_identity_changed_at_apply")
            # Zero mark, zero capture/archive, zero ledger kill attempt, zero kill.
            self.assertEqual(outcome["marked"], [])
            self.assertEqual(outcome["killed"], [])
            self.assertEqual([call[0] for call in calls if call[0] != "capture-pane"], [])
            self.assertFalse(central.exists())
            self.assertFalse((central / "cleanup.jsonl").exists())
            self.assertFalse((Path(tmp) / ".tmux-cleanup").exists())

    def test_apply_retains_multi_pane_even_when_allowlisted(self) -> None:
        """PR4 only automatically retires single exited panes."""
        with tempfile.TemporaryDirectory() as tmp:
            central = Path(tmp) / "central"
            outcome, calls = self.run_apply(
                self.two_pane_session(),
                self.two_pane_session(),
                archive_root=central,
                allow_session=["multi"],
            )

            self.assertEqual(outcome["killed"], [])
            self.assertNotIn(("kill-session", "-t", "=multi"), calls)
            self.assertFalse((central / "cleanup.jsonl").exists())

    def test_apply_refuses_kill_when_session_disappears_after_planning(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            central = Path(tmp) / "central"
            outcome, calls = self.run_apply([self.killable_pane(tmp)], [], archive_root=central)

            self.assertEqual(outcome["killed"], [])
            self.assertEqual(outcome["refused"][0]["reason"], "panes_missing_at_apply")
            self.assertEqual([call[0] for call in calls if call[0] != "capture-pane"], [])
            self.assertFalse((central / "cleanup.jsonl").exists())

    def mark_status_file(self, tmp: str, session: str, pane: hygiene.Pane, tail_hash: str) -> str:
        stable_since = (
            datetime.now(timezone.utc) - timedelta(seconds=hygiene.ACTIVE_IDLE_MARK_SECONDS + 60)
        ).isoformat().replace("+00:00", "Z")
        path = Path(tmp) / "status.json"
        path.write_text(
            json.dumps(
                {
                    "status_version": hygiene.STATUS_VERSION,
                    "sessions": {
                        session: {
                            "tail_hash": tail_hash,
                            "tail_hash_since": stable_since,
                            "pane_id": pane.pane,
                            "pane_created": pane.created,
                        }
                    },
                }
            ),
            encoding="utf-8",
        )
        return str(path)

    def test_apply_refuses_mark_when_hold_appears_after_planning(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                text = "quiet delivered tail with nothing new\n"
                hygiene.capture_pane_text = lambda pane, lines=240: text
                before = self.markable_pane(tmp)
                after = self.markable_pane(tmp)
                after.meta["hold_reason"] = "parent review"
                status_file = self.mark_status_file(tmp, "worker", before, hygiene.normalized_tail_hash(text))

                outcome, calls = self.run_apply(
                    [before], [after], archive_root=Path(tmp) / "central", status_file=status_file
                )

                self.assertEqual(outcome["marked"], [])
                self.assertEqual(outcome["killed"], [])
                self.assertEqual(outcome["skipped"][0]["action"], "skip")
                self.assertEqual([call for call in calls if call[0] == "set-option"], [])
        finally:
            hygiene.capture_pane_text = original_capture

    def test_apply_refuses_mark_when_pane_identity_changes_after_planning(self) -> None:
        original_capture = hygiene.capture_pane_text
        try:
            with tempfile.TemporaryDirectory() as tmp:
                text = "quiet delivered tail with nothing new\n"
                hygiene.capture_pane_text = lambda pane, lines=240: text
                before = self.markable_pane(tmp)
                after = self.markable_pane(tmp)
                after.pane = "%97"
                after.created = str(int(after.created) + 30)
                status_file = self.mark_status_file(tmp, "worker", before, hygiene.normalized_tail_hash(text))

                outcome, calls = self.run_apply(
                    [before], [after], archive_root=Path(tmp) / "central", status_file=status_file
                )

                self.assertEqual(outcome["marked"], [])
                self.assertEqual([call for call in calls if call[0] == "set-option"], [])
        finally:
            hygiene.capture_pane_text = original_capture


if __name__ == "__main__":
    unittest.main()
