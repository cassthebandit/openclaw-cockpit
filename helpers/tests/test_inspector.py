#!/usr/bin/env python3

import argparse
import contextlib
import io
import subprocess
import unittest
from pathlib import Path

from helpers.tmux import tmux_inspector as inspector


def pane(session: str, command: str = "zsh", start_command: str = "", path: str = "/tmp", **meta: str) -> inspector.Pane:
    return inspector.Pane(
        session=session,
        window="main",
        pane="%1",
        title="",
        command=command,
        start_command=start_command,
        path=path,
        dead=False,
        dead_status="",
        last_activity=1,
        meta={field: meta.get(field, "") for field in inspector.OC_FIELDS},
    )


class TmuxInspectorTests(unittest.TestCase):
    def test_codex_pane_gets_display_only_agent_metadata(self) -> None:
        p = pane("raw-codex", command="codex", path="/tmp/example/runs/demo")
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected=set())

        self.assertTrue(got.eligible)
        self.assertEqual(got.values["contract_version"], "display-only")
        self.assertEqual(got.values["managed_by"], "tmux_inspector")
        self.assertEqual(got.values["kind"], "detected-agent")
        self.assertEqual(got.values["agent"], "codex")
        self.assertEqual(got.values["cleanup_policy"], "manual")
        self.assertNotEqual(got.values["managed_by"], "agent_wall")
        self.assertNotIn("evidence_path", got.values)
        self.assertNotIn("completed_at", got.values)

    def test_service_pane_gets_service_card_metadata(self) -> None:
        p = pane("example-service", command="nginx", path="/tmp/example/config/service")
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected=set())

        self.assertTrue(got.eligible)
        self.assertEqual(got.values["contract_version"], "service-card.v1")
        self.assertEqual(got.values["managed_by"], "tmux_inspector")
        self.assertEqual(got.values["kind"], "detected-service")
        self.assertEqual(got.values["agent"], "")
        self.assertEqual(got.values["cleanup_policy"], "manual")

    def test_http_server_start_command_gets_viewer_metadata(self) -> None:
        p = pane(
            "example-site",
            command="Python",
            start_command='cd /tmp/example-site && exec python3 -m http.server 4173 --bind 0.0.0.0',
            path="/tmp/example/projects/example-site",
            agent="work",
            kind="detected-work",
        )
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected=set())

        self.assertTrue(got.eligible)
        self.assertEqual(got.values["kind"], "detected-viewer")
        self.assertEqual(got.values["agent"], "")

    def test_managed_agent_wall_pane_is_skipped(self) -> None:
        p = pane("managed", command="codex", contract_version="1", managed_by="agent_wall", kind="visible-agent", cleanup_policy="kill_after_ttl")
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected=set())

        self.assertFalse(got.eligible)
        self.assertEqual(got.reason, "managed_contract")

    def test_manual_display_annotation_is_not_overwritten(self) -> None:
        p = pane("manual", command="codex", contract_version="display-only", managed_by="manual_adopt", kind="agent", cleanup_policy="manual")
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected=set())

        self.assertFalse(got.eligible)
        self.assertEqual(got.reason, "manual_display_annotation")

    def test_protected_session_is_skipped(self) -> None:
        p = pane("cockpit", command="openclaw-cockpit")
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected={"cockpit"})

        self.assertFalse(got.eligible)
        self.assertEqual(got.reason, "protected_session")

    def test_dead_nonzero_raw_pane_is_failed_without_cleanup_contract(self) -> None:
        p = pane("dead-codex", command="codex")
        p.dead = True
        p.dead_status = "1"
        got = inspector.classify_pane(p, stale_seconds=0, now="2026-07-03T00:00:00Z", now_epoch=100, protected=set())

        self.assertTrue(got.eligible)
        self.assertEqual(got.values["state"], "failed")
        self.assertEqual(got.values["cleanup_policy"], "manual")

    def test_scan_path_does_not_write_tmux_options(self) -> None:
        p = pane("raw-codex", command="codex")
        original_list = inspector.list_panes
        original_run_tmux = inspector.run_tmux
        try:
            inspector.list_panes = lambda: [p]

            def fake_run_tmux(*args, **kwargs):
                raise AssertionError(f"scan should not write tmux options: {args}")

            inspector.run_tmux = fake_run_tmux
            args = argparse.Namespace(stale_seconds=0, protect_session=[])
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(inspector.cmd_scan(args), 0)
        finally:
            inspector.list_panes = original_list
            inspector.run_tmux = original_run_tmux

    def test_annotate_only_writes_changed_display_values(self) -> None:
        p = pane("raw-codex", command="codex")
        calls = []
        original_list = inspector.list_panes
        original_run_tmux = inspector.run_tmux
        try:
            inspector.list_panes = lambda: [p]
            inspector.run_tmux = lambda *args, **kwargs: calls.append(args) or subprocess.CompletedProcess(args, 0, "", "")
            args = argparse.Namespace(stale_seconds=0, protect_session=[], dry_run=False)

            events = inspector.annotate_once(args)
        finally:
            inspector.list_panes = original_list
            inspector.run_tmux = original_run_tmux

        self.assertEqual(len(events), 1)
        self.assertTrue(events[0]["changed"])
        self.assertTrue(any("@oc_contract_version display-only" in call[5] for call in calls))
        self.assertTrue(any("@oc_cleanup_policy manual" in call[5] for call in calls))
        self.assertNotIn(("set-option", "-p", "-t", "%1", "@oc_managed_by", "agent_wall"), calls)

    def test_annotate_does_not_rewrite_only_for_updated_at_churn(self) -> None:
        p = pane(
            "raw-codex",
            command="codex",
            contract_version="display-only",
            managed_by="tmux_inspector",
            kind="detected-agent",
            agent="codex",
            owner="autodetect",
            project="",
            goal="Detected codex tmux session",
            state="running",
            ttl="never",
            cleanup_policy="manual",
            updated_at="2026-07-03T00:00:00Z",
        )
        calls = []
        original_list = inspector.list_panes
        original_run_tmux = inspector.run_tmux
        try:
            inspector.list_panes = lambda: [p]
            inspector.run_tmux = lambda *args, **kwargs: calls.append(args) or subprocess.CompletedProcess(args, 0, "", "")
            args = argparse.Namespace(stale_seconds=0, protect_session=[], dry_run=False)

            events = inspector.annotate_once(args)
        finally:
            inspector.list_panes = original_list
            inspector.run_tmux = original_run_tmux

        self.assertEqual(len(events), 1)
        self.assertFalse(events[0]["changed"])
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
