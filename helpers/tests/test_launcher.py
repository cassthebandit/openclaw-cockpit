#!/usr/bin/env python3
from __future__ import annotations

import argparse
import contextlib
import io
import http.client
import json
import re
import shlex
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock
from pathlib import Path
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tmux"))
import agent_wall


@pytest.fixture(autouse=True)
def isolated_event_log(monkeypatch, tmp_path):
    monkeypatch.setattr(agent_wall, "pane_session_name", lambda pane: "isolated-test-session")
    append = agent_wall.event_log.append
    def write(event, **fields):
        fields["config"] = {"log_dir": str(tmp_path / "events")}
        return append(event, **fields)
    monkeypatch.setattr(agent_wall.event_log, "append", write)


class AgentWallClaudeTests(unittest.TestCase):
    def setUp(self):
        patch = mock.patch.object(agent_wall, "assignment_is_bound", return_value=True)
        patch.start()
        self.addCleanup(patch.stop)

    def test_spawn_claude_parser_defaults_to_fable(self) -> None:
        args = agent_wall.build_parser().parse_args(
            ["spawn-claude", "--name", "claude-default-model", "--prompt-file", "/tmp/prompt.md"]
        )

        self.assertEqual(args.agent, "claude")
        self.assertEqual(args.model, "claude-fable-5-1")
        self.assertEqual(args.effort, "high")

    def test_build_claude_tui_command_uses_visible_mode_and_scrubbed_auth(self) -> None:
        args = argparse.Namespace(
            model="claude-fable-5-1",
            effort="high",
            permission_mode="acceptEdits",
            claude_name="clean-draft-fable",
            name="fallback-name",
            debug_file="/tmp/claude-debug.log",
            claude_arg=[],
        )
        command = agent_wall.build_claude_tui_command(args)

        self.assertEqual(command[:7], ["env", "-u", "ANTHROPIC_API_KEY", "-u", "ANTHROPIC_AUTH_TOKEN", "-u", "ANTHROPIC_BASE_URL"])
        self.assertIn("TERM=xterm-256color", command)
        self.assertIn("ANTHROPIC_BASE_URL", command)
        self.assertIn("claude", command)
        self.assertIn("--model", command)
        self.assertIn("claude-fable-5-1", command)
        self.assertIn("--effort", command)
        self.assertIn("high", command)
        self.assertIn("--permission-mode", command)
        self.assertIn("acceptEdits", command)
        self.assertIn("--debug-file", command)
        self.assertNotIn("-p", command)
        self.assertNotIn("--print", command)
        self.assertNotIn("--output-format", command)
        self.assertNotIn("Build the page.", command)

    def test_spawn_claude_honors_explicit_readiness_marker(self) -> None:
        args = agent_wall.build_parser().parse_args(
            [
                "spawn-claude",
                "--name",
                "claude-custom-ready",
                "--prompt-file",
                "/tmp/prompt.md",
                "--ready-text",
                "Fable",
            ]
        )
        with mock.patch.object(agent_wall, "validate_launch_contract"), mock.patch.object(
            agent_wall, "validate_claude_role_model"
        ), mock.patch.object(agent_wall, "prompt_path_from_args", return_value=Path("/tmp/prompt.md")), mock.patch.object(
            agent_wall, "resolve_run_root", return_value=Path("/tmp/run")
        ), mock.patch.object(agent_wall, "build_claude_tui_command", return_value=["claude"]), mock.patch.object(
            agent_wall, "spawn_tui_session", return_value={}
        ) as spawn:
            agent_wall.cmd_spawn_claude(args)
        self.assertEqual(spawn.call_args.kwargs["ready_text"], "Fable")












    def test_tui_wrapper_records_optional_parallel_contract(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            old_state = agent_wall.STATE_DIR
            try:
                agent_wall.STATE_DIR = Path(tmp)
                wrapper = agent_wall.write_tui_wrapper(
                    "parallel",
                    ["claude"],
                    command_kind="claude_workflow_tui",
                    prompt_file="/tmp/prompt.md",
                    pane_log="/tmp/pane.log",
                    debug_file="/tmp/debug.log",
                    launch_record="/tmp/launch.json",
                    parallel_contract={"execution_class": "parallel", "runtime": "claude"},
                )
                body = wrapper.read_text(encoding="utf-8")
            finally:
                agent_wall.STATE_DIR = old_state

        self.assertIn('"parallel_contract"', body)
        self.assertIn('"execution_class": "parallel"', body)



    def test_build_codex_tui_command_uses_logged_in_home_and_interactive_mode(self) -> None:
        args = argparse.Namespace(
            model="gpt-5.5",
            codex_home="/home/example/.codex",
            ask_for_approval="on-request",
            sandbox="workspace-write",
            cd="/tmp/work",
            add_dir=["/tmp/extra"],
            no_alt_screen=True,
            codex_arg=["--search"],
        )
        command = agent_wall.build_codex_tui_command(args)

        self.assertEqual(command[:4], ["env", "CODEX_HOME=/home/example/.codex", "TERM=xterm-256color", "codex"])
        self.assertIn("--model", command)
        self.assertIn("gpt-5.5", command)
        self.assertIn("--ask-for-approval", command)
        self.assertIn("on-request", command)
        self.assertIn("--sandbox", command)
        self.assertIn("workspace-write", command)
        self.assertIn("--cd", command)
        self.assertIn("/tmp/work", command)
        self.assertIn("--add-dir", command)
        self.assertIn("/tmp/extra", command)
        self.assertIn("--no-alt-screen", command)
        self.assertIn("--search", command)
        self.assertNotIn("exec", command)
        self.assertNotIn("review", command)
        self.assertNotIn("Build the page.", command)

    def test_spawn_codex_parser_defaults_to_astra(self) -> None:
        args = agent_wall.build_parser().parse_args(
            ["spawn-codex", "--name", "codex-default-model", "--prompt-file", "/tmp/prompt.md"]
        )

        self.assertEqual(args.model, "gpt-6-astra")


    def test_build_agy_tui_command_uses_interactive_mode(self) -> None:
        args = argparse.Namespace(
            model="gemini-3.5-flash",
            project="example-project",
            agy_project="agy-review-project",
            new_project=True,
            sandbox=True,
            log_file="/tmp/agy.log",
            add_dir=["/tmp/repo"],
            agy_arg=["--experimental-flag"],
        )
        command = agent_wall.build_agy_tui_command(args)

        self.assertEqual(command[0], "agy")
        self.assertIn("--model", command)
        self.assertIn("gemini-3.5-flash", command)
        self.assertIn("--project", command)
        self.assertIn("agy-review-project", command)
        self.assertIn("--new-project", command)
        self.assertIn("--sandbox", command)
        self.assertIn("--log-file", command)
        self.assertIn("/tmp/agy.log", command)
        self.assertIn("--add-dir", command)
        self.assertIn("/tmp/repo", command)
        self.assertIn("--experimental-flag", command)
        self.assertNotIn("-p", command)
        self.assertNotIn("--print", command)
        self.assertNotIn("--prompt", command)
        self.assertNotIn("--prompt-interactive", command)



















    def test_tui_cleanup_defaults_use_ttl_policy_and_pane_log_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            args = argparse.Namespace(ttl="never", cleanup_policy="manual", evidence_path="")
            pane_log = str(run_root / "logs" / "worker-pane.log")

            agent_wall.ensure_tui_cleanup_defaults(args, pane_log=pane_log, run_root=run_root)

        self.assertEqual(args.ttl, "60s")
        self.assertEqual(args.cleanup_policy, "kill_on_done")
        self.assertEqual(args.evidence_path, "logs/worker-pane.log")

    def test_tui_cleanup_defaults_preserve_explicit_evidence_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            args = argparse.Namespace(ttl="never", cleanup_policy="manual", evidence_path="review.md")

            agent_wall.ensure_tui_cleanup_defaults(args, pane_log=str(run_root / "logs" / "worker.log"), run_root=run_root)

        self.assertEqual(args.ttl, "60s")
        self.assertEqual(args.cleanup_policy, "kill_on_done")
        self.assertEqual(args.evidence_path, "review.md")

    def test_tui_cleanup_defaults_create_evidence_parent_and_reject_escape(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            args = argparse.Namespace(ttl="never", cleanup_policy="manual", evidence_path="outputs/review.md")
            agent_wall.ensure_tui_cleanup_defaults(args, pane_log=str(run_root / "logs" / "worker.log"), run_root=run_root)
            self.assertTrue((run_root / "outputs").is_dir())

            args = argparse.Namespace(ttl="never", cleanup_policy="manual", evidence_path="../escape.md")
            with self.assertRaises(SystemExit):
                agent_wall.ensure_tui_cleanup_defaults(args, pane_log=str(run_root / "logs" / "worker.log"), run_root=run_root)

    def test_tui_geometry_defaults_mark_launch_as_managed(self) -> None:
        args = argparse.Namespace(cols=160, rows=45)

        cols, rows = agent_wall.normalize_tui_geometry(args)

        self.assertEqual((cols, rows), (160, 45))
        self.assertEqual(args.geometry_managed, "1")
        self.assertEqual(args.launch_cols, "160")
        self.assertEqual(args.launch_rows, "45")

    def test_tui_geometry_rejects_non_positive_dimensions(self) -> None:
        for cols, rows in [(0, 45), (160, 0), (-1, 45), (160, -1)]:
            with self.subTest(cols=cols, rows=rows):
                with self.assertRaises(SystemExit):
                    agent_wall.normalize_tui_geometry(argparse.Namespace(cols=cols, rows=rows))

    def test_metadata_from_args_can_leave_state_to_wrapper(self) -> None:
        args = argparse.Namespace(
            kind="agent",
            agent="codex",
            owner="workshop-4",
            project="example-project",
            goal="review",
            run_root="/tmp/run",
            thread_id="",
            session_id="codex-1",
            ttl="15m",
            cleanup_policy="kill_after_ttl",
            evidence_path="logs/codex.log",
            hold_reason="",
            why_headless="",
            progress_path="",
            end_reason="",
            route_failure_reason="",
        )
        values = agent_wall.metadata_from_args(args, state=None)

        self.assertNotIn("state", values)
        self.assertIn("progress_path", values)

    def test_metadata_from_args_includes_managed_geometry_when_present(self) -> None:
        args = argparse.Namespace(
            kind="visible-agent",
            agent="codex",
            owner="workshop-4",
            project="example-project",
            goal="review",
            run_root="/tmp/run",
            thread_id="",
            session_id="codex-1",
            ttl="15m",
            cleanup_policy="kill_after_ttl",
            evidence_path="logs/codex.log",
            hold_reason="",
            why_headless="",
            progress_path="",
            end_reason="",
            route_failure_reason="",
            geometry_managed="1",
            launch_cols="160",
            launch_rows="45",
        )

        values = agent_wall.metadata_from_args(args, state=None)

        self.assertEqual(values["geometry_managed"], "1")
        self.assertEqual(values["launch_cols"], "160")
        self.assertEqual(values["launch_rows"], "45")

    def test_generic_spawn_refuses_visible_agent_runtime_without_batch_contract(self) -> None:
        args = argparse.Namespace(
            kind="agent",
            agent="codex",
            cmd_args=["codex", "exec", "do work"],
            run_root="/tmp/run",
            cleanup_policy="kill_after_ttl",
            why_headless="",
            progress_path="",
        )

        with self.assertRaises(SystemExit):
            agent_wall.validate_launch_contract(args, generic=True)

    def test_batch_worker_requires_reason_and_progress_path(self) -> None:
        args = argparse.Namespace(
            kind="batch-worker",
            agent="codex",
            cmd_args=["codex", "exec", "score fixtures"],
            run_root="/tmp/run",
            cleanup_policy="manual",
            ttl="never",
            why_headless="fresh per-case context and JSON scoring",
            progress_path="progress.jsonl",
        )

        agent_wall.validate_launch_contract(args, generic=True)
        self.assertEqual(args.cleanup_policy, "kill_on_done")
        self.assertEqual(args.ttl, "never")

        args.progress_path = ""
        with self.assertRaises(SystemExit):
            agent_wall.validate_launch_contract(args, generic=True)

    def test_smoke_cleanup_policy_requires_smoke_kind(self) -> None:
        args = argparse.Namespace(
            kind="batch-worker",
            agent="codex",
            cmd_args=["bash", "-lc", "true"],
            run_root="/tmp/run",
            cleanup_policy="smoke",
            ttl="never",
            why_headless="disposable test worker",
            progress_path="progress.jsonl",
        )

        with self.assertRaises(SystemExit) as ctx:
            agent_wall.validate_launch_contract(args, generic=True)

        self.assertIn("cleanup_policy=smoke requires kind=smoke", str(ctx.exception))

    def test_batch_worker_ttl_policy_gets_short_default_ttl(self) -> None:
        args = argparse.Namespace(
            kind="batch-worker",
            agent="claude",
            cmd_args=["claude", "-p", "review"],
            run_root="/tmp/run",
            cleanup_policy="kill_after_ttl",
            ttl="never",
            why_headless="fresh read-only review artifact",
            progress_path="progress.jsonl",
        )

        agent_wall.validate_launch_contract(args, generic=True)

        self.assertEqual(args.cleanup_policy, "kill_after_ttl")
        self.assertEqual(args.ttl, "5m")

    def test_bare_integer_ttl_is_normalized_on_write_path(self) -> None:
        args = argparse.Namespace(
            kind="batch-worker",
            agent="claude",
            cmd_args=["bash", "-lc", "true"],
            run_root="/tmp/run",
            cleanup_policy="kill_after_ttl",
            ttl="7200",
            why_headless="batch check",
            progress_path="progress.jsonl",
        )

        agent_wall.validate_launch_contract(args, generic=True)

        self.assertEqual(args.ttl, "7200s")

    def test_generic_batch_spawn_keeps_batch_cleanup_defaults(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            args = argparse.Namespace(
                name="batch-defaults",
                title="batch defaults",
                kind="batch-worker",
                agent="codex-batch",
                owner="workshop-4",
                project="example-project",
                goal="Verify batch defaults",
                run_root=str(run_root),
                thread_id="",
                session_id="",
                ttl="never",
                cleanup_policy="manual",
                evidence_path="results/summary.json",
                hold_reason="",
                why_headless="noninteractive scoring smoke",
                progress_path="progress.jsonl",
                end_reason="",
                route_failure_reason="",
                cmd_args=["bash", "-lc", "echo ok"],
            )
            calls: list[tuple[object, ...]] = []
            unsets: list[set[str]] = []
            original_state = agent_wall.STATE_DIR
            original_run_tmux = agent_wall.run_tmux
            original_session_exists = agent_wall.session_exists
            original_first_pane = agent_wall.first_pane
            original_unset = agent_wall.unset_pane_options
            try:
                agent_wall.STATE_DIR = run_root / "state"
                agent_wall.session_exists = lambda name: False
                agent_wall.first_pane = lambda target: "%1"
                agent_wall.run_tmux = lambda *call, **kwargs: calls.append(call) or subprocess.CompletedProcess(call, 0, "", "")
                agent_wall.unset_pane_options = lambda pane, keys: unsets.append(set(keys))

                agent_wall.cmd_spawn(args)
            finally:
                agent_wall.STATE_DIR = original_state
                agent_wall.run_tmux = original_run_tmux
                agent_wall.session_exists = original_session_exists
                agent_wall.first_pane = original_first_pane
                agent_wall.unset_pane_options = original_unset

            self.assertEqual(args.cleanup_policy, "kill_on_done")
            self.assertEqual(args.ttl, "never")
            self.assertEqual(args.evidence_path, "results/summary.json")
            self.assertEqual(unsets, [])
            self.assertIn(("set-option", "-p", "-t", "%1", "@oc_cleanup_policy", "kill_on_done"), calls)
            self.assertIn(("set-option", "-p", "-t", "%1", "@oc_ttl", "never"), calls)
            self.assertIn(("set-option", "-p", "-t", "%1", "@oc_evidence_path", "results/summary.json"), calls)

    def test_generic_batch_spawn_defaults_missing_evidence_path_to_pane_log(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            args = argparse.Namespace(
                name="batch-default-evidence",
                title="batch defaults",
                kind="batch-worker",
                agent="codex-batch",
                owner="workshop-4",
                project="example-project",
                goal="Verify batch evidence defaults",
                run_root=str(run_root),
                thread_id="",
                session_id="",
                ttl="never",
                cleanup_policy="manual",
                evidence_path="",
                hold_reason="",
                why_headless="noninteractive scoring smoke",
                progress_path="progress.jsonl",
                end_reason="",
                route_failure_reason="",
                cmd_args=["bash", "-lc", "echo ok"],
            )
            calls: list[tuple[object, ...]] = []
            original_state = agent_wall.STATE_DIR
            original_run_tmux = agent_wall.run_tmux
            original_session_exists = agent_wall.session_exists
            original_first_pane = agent_wall.first_pane
            try:
                agent_wall.STATE_DIR = run_root / "state"
                agent_wall.session_exists = lambda name: False
                agent_wall.first_pane = lambda target: "%1"
                agent_wall.run_tmux = lambda *call, **kwargs: calls.append(call) or subprocess.CompletedProcess(call, 0, "", "")

                agent_wall.cmd_spawn(args)
            finally:
                agent_wall.STATE_DIR = original_state
                agent_wall.run_tmux = original_run_tmux
                agent_wall.session_exists = original_session_exists
                agent_wall.first_pane = original_first_pane

        self.assertEqual(args.evidence_path, "logs/batch-default-evidence-pane.log")
        self.assertIn(("set-option", "-p", "-t", "%1", "@oc_evidence_path", "logs/batch-default-evidence-pane.log"), calls)

    def test_write_generic_wrapper_can_capture_pane_log(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            old_state = agent_wall.STATE_DIR
            try:
                agent_wall.STATE_DIR = Path(tmp)
                wrapper = agent_wall.write_wrapper(
                    "test-generic",
                    ["bash", "-lc", "echo ok"],
                    pane_log=str(Path(tmp) / "logs" / "generic-pane.log"),
                    launch_record=str(Path(tmp) / "logs" / "generic-launch.json"),
                )
                body = wrapper.read_text(encoding="utf-8")
            finally:
                agent_wall.STATE_DIR = old_state

        self.assertIn("tmux pipe-pane -o", body)
        self.assertIn('"command_kind": "generic"', body)
        self.assertIn("bash -lc 'echo ok'", body)
        self.assertIn("@oc_completed_at", body)

    def test_set_state_clears_stale_failure_metadata_for_recovery_states(self) -> None:
        for state in ["running", "waiting", "blocked", "done", "stale"]:
            with self.subTest(state=state):
                calls: list[dict[str, str | None]] = []
                unsets: list[set[str]] = []
                original_unique = agent_wall.unique_pane_for_session
                original_set = agent_wall.set_pane_options
                original_unset = agent_wall.unset_pane_options
                original_read = agent_wall.read_pane_metadata
                try:
                    agent_wall.unique_pane_for_session = lambda name: "%1"
                    agent_wall.set_pane_options = lambda pane, values: calls.append(values)
                    agent_wall.unset_pane_options = lambda pane, keys: unsets.append(set(keys))
                    agent_wall.read_pane_metadata = lambda pane: {}
                    args = argparse.Namespace(
                        name="worker",
                        pane="",
                        state=state,
                        goal="",
                        run_root="",
                        thread_id="",
                        session_id="",
                        exit_code="",
                        end_reason="",
                        route_failure_reason="",
                    )
                    self.assertEqual(agent_wall.cmd_set_state(args), 0)
                finally:
                    agent_wall.unique_pane_for_session = original_unique
                    agent_wall.set_pane_options = original_set
                    agent_wall.unset_pane_options = original_unset
                    agent_wall.read_pane_metadata = original_read

                self.assertEqual(unsets, [{"end_reason", "route_failure_reason"}])
                if state in {"done", "stale"}:
                    self.assertIn("completed_at", calls[0])
                if state == "done":
                    self.assertEqual(calls[0]["exit_code"], "0")
                    self.assertEqual(calls[0]["end_reason"], "operator_set_state")


    def test_set_state_failed_preserves_failure_metadata(self) -> None:
        calls: list[dict[str, str | None]] = []
        unsets: list[set[str]] = []
        original_unique = agent_wall.unique_pane_for_session
        original_set = agent_wall.set_pane_options
        original_unset = agent_wall.unset_pane_options
        original_read = agent_wall.read_pane_metadata
        try:
            agent_wall.unique_pane_for_session = lambda name: "%1"
            agent_wall.set_pane_options = lambda pane, values: calls.append(values)
            agent_wall.unset_pane_options = lambda pane, keys: unsets.append(set(keys))
            agent_wall.read_pane_metadata = lambda pane: {}
            args = argparse.Namespace(
                name="worker",
                pane="",
                state="failed",
                goal="",
                run_root="",
                thread_id="",
                session_id="",
                exit_code="1",
                end_reason="route_failure",
                route_failure_reason="route unavailable",
            )
            self.assertEqual(agent_wall.cmd_set_state(args), 0)
        finally:
            agent_wall.unique_pane_for_session = original_unique
            agent_wall.set_pane_options = original_set
            agent_wall.unset_pane_options = original_unset
            agent_wall.read_pane_metadata = original_read

        self.assertEqual(unsets, [])
        self.assertEqual(calls[0]["end_reason"], "route_failure")
        self.assertEqual(calls[0]["route_failure_reason"], "route unavailable")

    def _degraded_args(self, **overrides: object) -> argparse.Namespace:
        base: dict[str, object] = {
            "name": "lane",
            "pane": "",
            "state": "failed",
            "goal": "",
            "run_root": "",
            "thread_id": "",
            "session_id": "",
            "exit_code": "1",
            "end_reason": "quota_exhausted",
            "route_failure_reason": "",
        }
        base.update(overrides)
        return argparse.Namespace(**base)

    def _run_set_state_with_meta(self, meta: dict[str, str], args: argparse.Namespace) -> dict[str, object]:
        original_unique = agent_wall.unique_pane_for_session
        original_set = agent_wall.set_pane_options
        original_unset = agent_wall.unset_pane_options
        original_read = agent_wall.read_pane_metadata
        buffer = io.StringIO()
        try:
            agent_wall.unique_pane_for_session = lambda name: "%7"
            agent_wall.set_pane_options = lambda pane, values: None
            agent_wall.unset_pane_options = lambda pane, keys: None
            agent_wall.read_pane_metadata = lambda pane: meta
            with contextlib.redirect_stdout(buffer):
                self.assertEqual(agent_wall.cmd_set_state(args), 0)
        finally:
            agent_wall.unique_pane_for_session = original_unique
            agent_wall.set_pane_options = original_set
            agent_wall.unset_pane_options = original_unset
            agent_wall.read_pane_metadata = original_read
        return json.loads(buffer.getvalue())

    def test_set_state_failed_writes_degraded_artifact_for_missing_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            report = self._run_set_state_with_meta(
                {"run_root": tmp, "evidence_path": "logs/lane.log"},
                self._degraded_args(),
            )
            evidence = Path(tmp) / "logs" / "lane.log"
            self.assertEqual(report["degraded_evidence"]["status"], "written")
            self.assertEqual(report["degraded_evidence"]["path"], str(evidence.resolve()))
            content = evidence.read_text(encoding="utf-8")
        self.assertIn("DEGRADED RESULT", content)
        self.assertIn("quota_exhausted", content)
        self.assertIn("state: failed", content)

    def test_set_state_blocked_fills_empty_evidence_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "lane.log"
            evidence.write_text("", encoding="utf-8")
            report = self._run_set_state_with_meta(
                {"run_root": tmp, "evidence_path": "lane.log"},
                self._degraded_args(state="blocked", end_reason="quota_blocked"),
            )
            self.assertEqual(report["degraded_evidence"]["status"], "written")
            content = evidence.read_text(encoding="utf-8")
        self.assertIn("state: blocked", content)
        self.assertIn("quota_blocked", content)

    def test_set_state_failed_never_overwrites_real_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "lane.log"
            evidence.write_text("real lane output\n", encoding="utf-8")
            report = self._run_set_state_with_meta(
                {"run_root": tmp, "evidence_path": "lane.log"},
                self._degraded_args(),
            )
            self.assertEqual(report["degraded_evidence"]["status"], "kept")
            self.assertEqual(evidence.read_text(encoding="utf-8"), "real lane output\n")

    def test_set_state_failed_refuses_degraded_write_outside_run_root(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            report = self._run_set_state_with_meta(
                {"run_root": tmp, "evidence_path": "../escape.log"},
                self._degraded_args(),
            )
            self.assertEqual(report["degraded_evidence"]["status"], "skipped")
            self.assertEqual(report["degraded_evidence"]["reason"], "evidence_outside_run_root")
            self.assertFalse((Path(tmp).parent / "escape.log").exists())

    def test_set_state_failed_skips_degraded_write_for_relative_run_root(self) -> None:
        report = self._run_set_state_with_meta(
            {"run_root": "memory/runs/somewhere", "evidence_path": "lane.log"},
            self._degraded_args(),
        )
        self.assertEqual(report["degraded_evidence"]["status"], "skipped")
        self.assertEqual(report["degraded_evidence"]["reason"], "relative_run_root")

    def test_set_state_done_does_not_write_degraded_artifact(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            report = self._run_set_state_with_meta(
                {"run_root": tmp, "evidence_path": "lane.log"},
                self._degraded_args(state="done", end_reason="", exit_code=""),
            )
            self.assertNotIn("degraded_evidence", report)
            self.assertFalse((Path(tmp) / "lane.log").exists())

    def test_release_hold_does_not_pre_mark_for_teardown(self) -> None:
        calls: list[dict[str, str | None]] = []
        original_unique = agent_wall.unique_pane_for_session
        original_read = agent_wall.read_pane_metadata
        original_set = agent_wall.set_pane_options
        original_unset_hold = agent_wall.unset_hold_reason
        try:
            agent_wall.unique_pane_for_session = lambda name: "%1"
            agent_wall.read_pane_metadata = lambda pane: {"hold_reason": "parent-review-after-build"}
            agent_wall.set_pane_options = lambda pane, values: calls.append(values)
            agent_wall.unset_hold_reason = lambda pane: None
            args = argparse.Namespace(
                name="worker",
                pane="",
                end_reason="",
                dry_run=False,
                evidence_captured=True,
                allow_hygiene_after_release=True,
            )
            self.assertEqual(agent_wall.cmd_release_hold(args), 0)
        finally:
            agent_wall.unique_pane_for_session = original_unique
            agent_wall.read_pane_metadata = original_read
            agent_wall.set_pane_options = original_set
            agent_wall.unset_hold_reason = original_unset_hold

        # Hygiene is the single marking authority: release stamps completion
        # evidence only and leaves marking to the janitor's next cycle.
        self.assertEqual(set(calls[0]), {"updated_at"})
        self.assertNotIn("janitor_state", calls[0])
        self.assertNotIn("teardown_reason", calls[0])
        self.assertNotIn("teardown_marked_at", calls[0])

    def test_unset_pane_options_refuses_non_allowlisted_fields(self) -> None:
        with self.assertRaises(SystemExit):
            agent_wall.unset_pane_options("%1", {"goal"})

    def test_annotate_does_not_clear_metadata(self) -> None:
        calls: list[dict[str, str | None]] = []
        unsets: list[set[str]] = []
        original_unique = agent_wall.unique_pane_for_session
        original_set = agent_wall.set_pane_options
        original_unset = agent_wall.unset_pane_options
        try:
            agent_wall.unique_pane_for_session = lambda name: "%1"
            agent_wall.set_pane_options = lambda pane, values: calls.append(values)
            agent_wall.unset_pane_options = lambda pane, keys: unsets.append(set(keys))
            args = argparse.Namespace(
                name="worker",
                pane="",
                kind="agent",
                agent="codex",
                owner="workshop-4",
                project="example-project",
                goal="annotate",
                state="running",
                run_root="/tmp/run",
                thread_id="",
                session_id="",
                ttl="never",
                evidence_path="",
                hold_reason="",
                why_headless="",
                progress_path="",
                end_reason="",
                route_failure_reason="",
            )
            self.assertEqual(agent_wall.cmd_annotate(args), 0)
        finally:
            agent_wall.unique_pane_for_session = original_unique
            agent_wall.set_pane_options = original_set
            agent_wall.unset_pane_options = original_unset

        self.assertEqual(unsets, [])
        self.assertEqual(calls[0]["managed_by"], "manual_adopt")

    def test_sanitize_tmux_option_value_removes_field_breaking_controls(self) -> None:
        self.assertEqual(agent_wall.sanitize_tmux_option_value("one\ttwo\nthree\r\0four"), "one two three  four")

    def test_smoke_state_cycle_exercises_attention_buckets(self) -> None:
        got = [agent_wall.smoke_state(i) for i in range(1, 7)]
        self.assertEqual(got, ["running", "waiting", "blocked", "failed", "done", "running"])
        failed = agent_wall.smoke_command(4)
        self.assertIn("@oc_state failed", failed[-1])
        self.assertIn("exit 1", failed[-1])
        waiting = agent_wall.smoke_command(2)
        self.assertIn("@oc_state waiting", waiting[-1])

    def test_display_annotation_is_not_cleanup_managed(self) -> None:
        args = argparse.Namespace(
            kind="agent",
            agent="fable",
            owner="workshop-4",
            project="clean-draft",
            goal="Continue Tranche 1",
            state="running",
            run_root="/tmp/run",
            thread_id="",
            session_id="fable-3",
            ttl="never",
            evidence_path="",
            hold_reason="raw continuation",
            why_headless="",
            progress_path="",
            end_reason="",
            route_failure_reason="",
        )
        values = agent_wall.display_annotation_from_args(args)

        self.assertEqual(values["contract_version"], "display-only")
        self.assertEqual(values["managed_by"], "manual_adopt")
        self.assertEqual(values["cleanup_policy"], "manual")
        self.assertNotEqual(values["managed_by"], "agent_wall")

    def test_service_display_annotation_uses_service_contract(self) -> None:
        args = argparse.Namespace(
            kind="service",
            agent="service",
            owner="Example",
            project="cameras",
            goal="RTSP stream bridge",
            state="running",
            run_root="",
            thread_id="",
            session_id="",
            ttl="never",
            evidence_path="",
            hold_reason="service card; cleanup is manual",
            why_headless="",
            progress_path="",
            end_reason="",
            route_failure_reason="",
        )
        values = agent_wall.display_annotation_from_args(args)

        self.assertEqual(values["contract_version"], "service-card.v1")
        self.assertEqual(values["managed_by"], "manual_adopt")
        self.assertEqual(values["cleanup_policy"], "manual")

    def test_unique_pane_for_session_refuses_ambiguous_session(self) -> None:
        original = agent_wall.session_pane_rows
        try:
            agent_wall.session_pane_rows = lambda name: [
                {"session": "multi", "window_index": "0", "window_name": "a", "pane_index": "0", "pane": "%1", "title": "", "command": "zsh"},
                {"session": "multi", "window_index": "1", "window_name": "b", "pane_index": "0", "pane": "%2", "title": "", "command": "zsh"},
            ]
            with self.assertRaises(SystemExit):
                agent_wall.unique_pane_for_session("multi")
        finally:
            agent_wall.session_pane_rows = original

    def test_wait_for_command_fails_closed_on_timeout(self) -> None:
        original_run_tmux = agent_wall.run_tmux
        try:
            agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(args, 0, "zsh\n", "")
            with self.assertRaises(SystemExit):
                agent_wall.wait_for_command("%1", "claude", timeout=0.01)
        finally:
            agent_wall.run_tmux = original_run_tmux

    def test_wait_for_log_text_fails_closed_on_timeout(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(SystemExit):
                agent_wall.wait_for_log_text(Path(tmp) / "missing.log", "Claude Code", timeout=0.01)


    def test_verify_visible_contract_accepts_wrapper_child_runtime(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("OpenAI Codex\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "run-codex_tui.sh", "1", "agent_wall", "visible-agent", "codex", "kill_after_ttl", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: ["bash /tmp/run-codex_tui.sh", "codex --model gpt-5.5"]
                proof = agent_wall.verify_visible_contract(
                    argparse.Namespace(name="codex-lane"),
                    pane="%1",
                    pane_log_path=log,
                    launch_record=str(launch),
                    command_kind="codex_tui",
                )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

        self.assertEqual(proof["runtime"], "codex")
        self.assertEqual(proof["runtime_process"], "codex --model gpt-5.5")

    def test_verify_visible_contract_rejects_shell_only_runtime(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("ready\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "zsh", "1", "agent_wall", "visible-agent", "codex", "kill_after_ttl", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: ["zsh", "bash /tmp/run-codex_tui.sh"]
                with self.assertRaises(agent_wall.VisibleContractError):
                    agent_wall.verify_visible_contract(
                        argparse.Namespace(name="codex-lane"),
                        pane="%1",
                        pane_log_path=log,
                        launch_record=str(launch),
                        command_kind="codex_tui",
                    )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

    def test_verify_visible_contract_rejects_wrapper_path_as_runtime_proof(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            wrapper = Path(tmp) / "agent-wall" / "codex" / "run-codex_tui.sh"
            wrapper.parent.mkdir(parents=True)
            wrapper.write_text("#!/usr/bin/env bash\n", encoding="utf-8")
            log.write_text("OpenAI Codex\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "bash", "1", "agent_wall", "visible-agent", "codex", "kill_after_ttl", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: [f"bash {wrapper}"]
                with self.assertRaises(agent_wall.VisibleContractError):
                    agent_wall.verify_visible_contract(
                        argparse.Namespace(name="codex"),
                        pane="%1",
                        pane_log_path=log,
                        launch_record=str(launch),
                        command_kind="codex_tui",
                        wrapper_path=wrapper,
                    )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

    def test_verify_visible_contract_rejects_detected_agent_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("Claude Code\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "101", "claude", "1", "tmux_inspector", "detected-agent", "claude", "manual", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: ["claude --model claude-opus-4-8"]
                with self.assertRaises(agent_wall.VisibleContractError):
                    agent_wall.verify_visible_contract(
                        argparse.Namespace(name="claude-lane"),
                        pane="%2",
                        pane_log_path=log,
                        launch_record=str(launch),
                        command_kind="claude_tui",
                    )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

    def test_verify_visible_contract_rejects_manual_adopted_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("OpenAI Codex\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "codex", "display-only", "manual_adopt", "visible-agent", "codex", "manual", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: ["codex --model gpt-5.5"]
                with self.assertRaises(agent_wall.VisibleContractError):
                    agent_wall.verify_visible_contract(
                        argparse.Namespace(name="codex-lane"),
                        pane="%1",
                        pane_log_path=log,
                        launch_record=str(launch),
                        command_kind="codex_tui",
                    )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

    def test_verify_visible_contract_accepts_fable_agent_with_claude_runtime(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("Claude Code\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "run-claude_tui.sh", "1", "agent_wall", "visible-agent", "fable", "kill_after_ttl", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: ["node /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/cli.js"]
                proof = agent_wall.verify_visible_contract(
                    argparse.Namespace(name="fable-lane"),
                    pane="%1",
                    pane_log_path=log,
                    launch_record=str(launch),
                    command_kind="claude_tui",
                )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

        self.assertEqual(proof["runtime"], "claude")

    def test_verify_visible_contract_accepts_agy_runtime(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("? for shortcuts\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "run-agy_tui.sh", "1", "agent_wall", "visible-agent", "agy", "kill_after_ttl", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: ["agy --model gemini-3.5-flash"]
                proof = agent_wall.verify_visible_contract(
                    argparse.Namespace(name="agy-lane"),
                    pane="%1",
                    pane_log_path=log,
                    launch_record=str(launch),
                    command_kind="agy_tui",
                )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree

        self.assertEqual(proof["runtime"], "agy")


    def test_spawn_tui_session_uses_managed_tmux_geometry(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            prompt = run_root / "prompt.md"
            prompt.write_text("do work", encoding="utf-8")
            args = argparse.Namespace(
                name="codex-geometry-lane",
                title="",
                prompt_file=str(prompt),
                run_root=str(run_root),
                pane_log="",
                kind="visible-agent",
                agent="codex",
                owner="Example",
                project="example-project",
                goal="verify geometry",
                thread_id="",
                session_id="",
                ttl="never",
                cleanup_policy="manual",
                evidence_path="",
                hold_reason="",
                why_headless="",
                progress_path="",
                end_reason="",
                route_failure_reason="",
                ready_text="",
                cols=132,
                rows=37,
            )
            calls: list[tuple[object, ...]] = []
            original_state = agent_wall.STATE_DIR
            original_session_exists = agent_wall.session_exists
            original_run_tmux = agent_wall.run_tmux
            original_first_pane = agent_wall.first_pane
            original_wait = agent_wall.wait_for_log_activity
            original_verify = agent_wall.verify_visible_contract
            original_inject = agent_wall.inject_assignment
            try:
                agent_wall.STATE_DIR = run_root / "state"
                agent_wall.session_exists = lambda name: False
                agent_wall.first_pane = lambda target: "%1"
                agent_wall.run_tmux = lambda *call, **kwargs: calls.append(call) or subprocess.CompletedProcess(call, 0, "", "")
                agent_wall.wait_for_log_activity = lambda path: Path(path).write_text("OpenAI Codex\n", encoding="utf-8")
                agent_wall.verify_visible_contract = lambda *v_args, **v_kwargs: {"runtime": "codex"}
                agent_wall.inject_assignment = lambda *i_args, **i_kwargs: None

                agent_wall.spawn_tui_session(
                    args,
                    command=["codex"],
                    command_kind="codex_tui",
                    window_name="codex",
                )
            finally:
                agent_wall.STATE_DIR = original_state
                agent_wall.session_exists = original_session_exists
                agent_wall.run_tmux = original_run_tmux
                agent_wall.first_pane = original_first_pane
                agent_wall.wait_for_log_activity = original_wait
                agent_wall.verify_visible_contract = original_verify
                agent_wall.inject_assignment = original_inject

        self.assertIn(("new-session", "-d", "-x", "132", "-y", "37", "-s", "codex-geometry-lane", "-n", "codex", str(run_root / "state" / "codex-geometry-lane" / "run-codex_tui.sh")), calls)
        self.assertIn(("set-option", "-p", "-t", "%1", "@oc_geometry_managed", "1"), calls)
        self.assertIn(("set-option", "-p", "-t", "%1", "@oc_launch_cols", "132"), calls)
        self.assertIn(("set-option", "-p", "-t", "%1", "@oc_launch_rows", "37"), calls)

    def test_spawn_tui_session_preserves_session_on_verifier_failure(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            prompt = run_root / "prompt.md"
            prompt.write_text("do work", encoding="utf-8")
            args = argparse.Namespace(
                name="bad-codex-lane",
                title="",
                prompt_file=str(prompt),
                run_root=str(run_root),
                pane_log="",
                kind="visible-agent",
                agent="codex",
                owner="Example",
                project="example-project",
                goal="verify cleanup",
                thread_id="",
                session_id="",
                ttl="never",
                cleanup_policy="manual",
                evidence_path="",
                hold_reason="",
                why_headless="",
                progress_path="",
                end_reason="",
                route_failure_reason="",
                ready_text="",
            )
            calls: list[tuple[object, ...]] = []
            original_state = agent_wall.STATE_DIR
            original_session_exists = agent_wall.session_exists
            original_run_tmux = agent_wall.run_tmux
            original_first_pane = agent_wall.first_pane
            original_wait = agent_wall.wait_for_log_activity
            original_verify = agent_wall.verify_visible_contract
            original_set = agent_wall.set_pane_options
            try:
                agent_wall.STATE_DIR = run_root / "state"
                agent_wall.session_exists = lambda name: False
                agent_wall.first_pane = lambda target: "%1"
                agent_wall.run_tmux = lambda *call, **kwargs: calls.append(call) or subprocess.CompletedProcess(call, 0, "", "")
                agent_wall.wait_for_log_activity = lambda path: Path(path).write_text("shell only\n", encoding="utf-8")
                agent_wall.verify_visible_contract = lambda *v_args, **v_kwargs: (_ for _ in ()).throw(
                    agent_wall.VisibleContractError("no runtime child")
                )
                agent_wall.set_pane_options = lambda pane, values: calls.append(("set_pane_options", pane, values))

                with self.assertRaises(SystemExit):
                    agent_wall.spawn_tui_session(
                        args,
                        command=["bash"],
                        command_kind="codex_tui",
                        window_name="codex",
                    )
            finally:
                agent_wall.STATE_DIR = original_state
                agent_wall.session_exists = original_session_exists
                agent_wall.run_tmux = original_run_tmux
                agent_wall.first_pane = original_first_pane
                agent_wall.wait_for_log_activity = original_wait
                agent_wall.verify_visible_contract = original_verify
                agent_wall.set_pane_options = original_set

        self.assertNotIn(("kill-session", "-t", "=bad-codex-lane"), calls)
        self.assertNotIn(("kill-session", "-t", "=other-session"), calls)

    def test_spawn_tui_session_preserves_session_on_readiness_timeout(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_root = Path(tmp).resolve()
            prompt = run_root / "prompt.md"
            prompt.write_text("do work", encoding="utf-8")
            args = argparse.Namespace(
                name="timeout-codex-lane",
                title="",
                prompt_file=str(prompt),
                run_root=str(run_root),
                pane_log="",
                kind="visible-agent",
                agent="codex",
                owner="Example",
                project="example-project",
                goal="verify timeout cleanup",
                thread_id="",
                session_id="",
                ttl="never",
                cleanup_policy="manual",
                evidence_path="",
                hold_reason="",
                why_headless="",
                progress_path="",
                end_reason="",
                route_failure_reason="",
                ready_text="",
            )
            calls: list[tuple[object, ...]] = []
            original_state = agent_wall.STATE_DIR
            original_session_exists = agent_wall.session_exists
            original_run_tmux = agent_wall.run_tmux
            original_first_pane = agent_wall.first_pane
            original_wait = agent_wall.wait_for_log_activity
            original_set = agent_wall.set_pane_options
            original_inject = agent_wall.inject_assignment
            injected: list[bool] = []
            try:
                agent_wall.STATE_DIR = run_root / "state"
                agent_wall.session_exists = lambda name: False
                agent_wall.first_pane = lambda target: "%1"
                agent_wall.run_tmux = lambda *call, **kwargs: calls.append(call) or subprocess.CompletedProcess(call, 0, "", "")
                agent_wall.wait_for_log_activity = lambda path: (_ for _ in ()).throw(SystemExit("no output"))
                agent_wall.set_pane_options = lambda pane, values: calls.append(("set_pane_options", pane, values))
                agent_wall.inject_prompt = lambda *args, **kwargs: injected.append(True)

                with self.assertRaises(SystemExit):
                    agent_wall.spawn_tui_session(
                        args,
                        command=["codex"],
                        command_kind="codex_tui",
                        window_name="codex",
                    )
            finally:
                agent_wall.STATE_DIR = original_state
                agent_wall.session_exists = original_session_exists
                agent_wall.run_tmux = original_run_tmux
                agent_wall.first_pane = original_first_pane
                agent_wall.wait_for_log_activity = original_wait
                agent_wall.set_pane_options = original_set
                agent_wall.inject_assignment = original_inject

        self.assertNotIn(("kill-session", "-t", "=timeout-codex-lane"), calls)
        failure_updates = [call for call in calls if call[:2] == ("set_pane_options", "%1")]
        self.assertEqual(failure_updates[-1][2]["end_reason"], "readiness_timeout")
        self.assertEqual(failure_updates[-1][2]["state"], "waiting")
        self.assertNotIn("completed_at", failure_updates[-1][2])
        self.assertIn("no output", failure_updates[-1][2]["route_failure_reason"])
        self.assertEqual(injected, [])


    def test_process_tree_commands_parses_descendants(self) -> None:
        def runner(args: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
            return subprocess.CompletedProcess(args, 0, "100 1 bash wrapper\n101 100 codex --model gpt-5.5\n102 101 helper\n", "")

        commands = agent_wall.process_tree_commands("100", runner=runner)

        self.assertEqual(commands, ["bash wrapper", "codex --model gpt-5.5", "helper"])


DIRECT_CLAUDE_PROCESS = "claude --model claude-opus-5 --effort xhigh"
# Verified on this host: /opt/homebrew/bin/claude resolves to this entrypoint.
RESOLVED_CLAUDE_ENTRYPOINT = "/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe"
RESOLVED_CLAUDE_PROCESS = f"{RESOLVED_CLAUDE_ENTRYPOINT} --model claude-opus-5"
NODE_CLAUDE_PROCESS = "node /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/cli.js"
# A `claude` token that only ever appears in argument paths, never in the
# executable position. Runtime proof must not accept it.
UNRELATED_PATH_COLLISION_PROCESS = (
    "node /opt/tools/bundle.js --workspace /home/example/projects/claude-code/notes --log /tmp/claude.log"
)


class FakeTmuxServer:
    """Stateful stand-in for the tmux CLI surface agent_wall drives.

    Sessions, panes and @oc_* pane options live in real state here, so a value
    the launcher writes at launch is the value the verifier reads back — the
    Opus role in `@oc_agent` round-trips instead of being asserted twice from
    the same variable. The server also plays the part of the launch wrapper it
    is handed: on session creation it writes the pane log and launch record the
    real wrapper writes, which is what makes readiness and the evidence gates
    testable without stubbing the verifier.
    """

    FIELD_RE = re.compile(r"#\{([^}]+)\}")

    def __init__(
        self,
        *,
        pane_log: Path,
        launch_record: Path,
        ready_text: str = "Claude Code",
        process_tree: list[str] | None = None,
        pane_dead: str = "0",
        write_pane_log: bool = True,
        write_launch_record: bool = True,
        empty_launch_record: bool = False,
        truncate_pane_log_at_verify: bool = False,
    ) -> None:
        self.pane_log = pane_log
        self.launch_record = launch_record
        self.ready_text = ready_text
        self.process_tree = process_tree if process_tree is not None else []
        self.pane_dead = pane_dead
        self.write_pane_log = write_pane_log
        self.write_launch_record = write_launch_record
        self.empty_launch_record = empty_launch_record
        self.truncate_pane_log_at_verify = truncate_pane_log_at_verify
        self.sessions: dict[str, dict[str, str]] = {}
        self.wrappers: dict[str, str] = {}
        self.calls: list[tuple[str, ...]] = []
        self.events: list[str] = []
        self.killed: list[str] = []
        self._next_pane = 1

    # --- helpers -----------------------------------------------------------
    def pane_state(self, pane_id: str) -> dict[str, str] | None:
        for pane in self.sessions.values():
            if pane["pane_id"] == pane_id:
                return pane
        return None

    def expand(self, fmt: str, pane: dict[str, str]) -> str:
        return self.FIELD_RE.sub(lambda match: pane.get(match.group(1), ""), fmt)

    def process_tree_commands(self, pid: str) -> list[str]:
        self.events.append("process_tree")
        return list(self.process_tree)

    # --- tmux surface ------------------------------------------------------
    def __call__(self, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        self.calls.append(args)
        self.events.append(args[0])
        handler = getattr(self, "_tmux_" + args[0].replace("-", "_"), None)
        if handler is None:
            return subprocess.CompletedProcess(args, 0, "", "")
        return handler(list(args))

    def _ok(self, args: list[str], stdout: str = "") -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess(args, 0, stdout, "")

    def _tmux_has_session(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        name = args[args.index("-t") + 1].lstrip("=")
        return subprocess.CompletedProcess(args, 0 if name in self.sessions else 1, "", "")

    def _tmux_new_session(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        name = args[args.index("-s") + 1]
        window = args[args.index("-n") + 1]
        pane_id = f"%{self._next_pane}"
        self._next_pane += 1
        self.wrappers[name] = args[-1]
        self.sessions[name] = {
            "session_name": name,
            "window_index": "0",
            "window_name": window,
            "pane_index": "0",
            "pane_id": pane_id,
            "pane_title": "",
            "pane_current_command": "bash",
            "pane_dead": self.pane_dead,
            "pane_dead_status": "",
            "pane_pid": "4242",
        }
        self._run_wrapper_side_effects()
        wrapper = Path(args[-1]).read_text()
        for line in wrapper.splitlines():
            if 'supervise --run-dir' not in line:
                continue
            words = shlex.split(line)
            root = Path(words[words.index('--run-dir') + 1])
            launch = json.loads((root / 'launch.json').read_text())
            self.sessions[name].update(session_id='$1', window_id='@1', **{'@oc_launch_id': launch['run_id']})
            identity = [pane_id, '4242', '$1', '@1', launch['run_id'], '', self.pane_dead]
            (root / 'process.json').write_text(json.dumps({'run_id': launch['run_id'], 'pane_identity': identity}))
        return self._ok(args)

    def _run_wrapper_side_effects(self) -> None:
        """What the real run-claude_tui.sh does before the runtime starts."""
        self.pane_log.parent.mkdir(parents=True, exist_ok=True)
        self.launch_record.parent.mkdir(parents=True, exist_ok=True)
        if self.write_pane_log:
            self.pane_log.write_text(self.ready_text + "\n", encoding="utf-8")
        else:
            self.pane_log.write_text("", encoding="utf-8")
        if self.write_launch_record:
            self.launch_record.write_text("" if self.empty_launch_record else '{"command_kind": "claude_tui"}\n', encoding="utf-8")

    def _tmux_list_panes(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        fmt = args[args.index("-F") + 1]
        if "-a" in args:
            panes = list(self.sessions.values())
        else:
            target = args[args.index("-t") + 1].lstrip("=").split(":")[0]
            panes = [pane for name, pane in self.sessions.items() if name == target]
        return self._ok(args, "".join(self.expand(fmt, pane) + "\n" for pane in panes))

    def _tmux_if_shell(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        pane = self.pane_state(args[args.index('-t') + 1]) or {}
        tests = re.findall(r"#\{==:#\{([^}]+)\},([^}]+)\}", args[-3])
        command = args[-2] if all(pane.get(field, '') == expected for field,expected in tests) else args[-1]
        if command.startswith('display-message -p COCKPIT_'):
            return self._ok(args, command.split()[-1])
        return self(*shlex.split(command))

    def _tmux_display_message(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        if self.truncate_pane_log_at_verify and self.pane_log.exists():
            # Readiness has already passed; the evidence disappears underneath
            # the verifier, which is exactly the case the pane-log gate exists
            # to catch independently of readiness.
            self.pane_log.write_text("", encoding="utf-8")
        pane_id = args[args.index("-t") + 1]
        pane = self.pane_state(pane_id)
        if pane is None:
            return subprocess.CompletedProcess(args, 1, "", f"can't find pane {pane_id}")
        return self._ok(args, self.expand(args[-1], pane))

    def _tmux_select_pane(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        pane = self.pane_state(args[args.index("-t") + 1])
        if pane is not None and "-T" in args:
            pane["pane_title"] = args[args.index("-T") + 1]
        return self._ok(args)

    def _tmux_set_option(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        pane = self.pane_state(args[args.index("-t") + 1])
        if pane is None:
            return subprocess.CompletedProcess(args, 1, "", "no such pane")
        key = args[-1] if "-u" in args else args[-2]
        if "-u" in args:
            pane.pop(key, None)
        else:
            pane[key] = args[-1]
        return self._ok(args)

    def _tmux_kill_session(self, args: list[str]) -> subprocess.CompletedProcess[str]:
        target = args[args.index("-t") + 1]
        self.killed.append(target)
        self.sessions.pop(target.lstrip("="), None)
        return self._ok(args)


class ClaudeTuiFullPathTests(unittest.TestCase):
    """Parser-derived, non-stubbed `claude_tui` launches against a stateful tmux.

    `verify_visible_contract` runs for real in every case here; only the ps(1)
    boundary is supplied by the fixture, because a real Claude process cannot be
    started inside a unit test.
    """

    def setUp(self) -> None:
        patch = mock.patch.object(agent_wall, "assignment_is_bound", return_value=True)
        patch.start()
        self.addCleanup(patch.stop)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.run_root = Path(self.tmp.name).resolve()
        self.prompt = self.run_root / "prompt.md"
        self.prompt.write_text("do the work\n", encoding="utf-8")

    def launch(
        self,
        *,
        name: str = "claude-lane",
        extra_args: list[str] | None = None,
        **server_kwargs: object,
    ) -> tuple[FakeTmuxServer, dict | None, SystemExit | None]:
        pane_log = self.run_root / "logs" / f"{name}-pane.log"
        launch_record = self.run_root / "logs" / f"{name}-claude_tui-launch.json"
        server = FakeTmuxServer(pane_log=pane_log, launch_record=launch_record, **server_kwargs)
        argv = [
            "spawn-claude",
            "--name",
            name,
            "--prompt-file",
            str(self.prompt),
            "--run-root",
            str(self.run_root),
            *(extra_args or []),
        ]
        args = agent_wall.build_parser().parse_args(argv)

        original = (
            agent_wall.run_tmux,
            agent_wall.process_tree_commands,
            agent_wall.STATE_DIR,
            agent_wall.READINESS_TIMEOUT_SECONDS,
        )
        stdout = io.StringIO()
        failure: SystemExit | None = None
        payload: dict | None = None
        try:
            agent_wall.run_tmux = server
            agent_wall.process_tree_commands = server.process_tree_commands
            agent_wall.STATE_DIR = self.run_root / "state"
            agent_wall.READINESS_TIMEOUT_SECONDS = 0.6
            with contextlib.redirect_stdout(stdout):
                try:
                    args.func(args)
                except SystemExit as exc:
                    failure = exc
        finally:
            (
                agent_wall.run_tmux,
                agent_wall.process_tree_commands,
                agent_wall.STATE_DIR,
                agent_wall.READINESS_TIMEOUT_SECONDS,
            ) = original
        if failure is None:
            payload = json.loads(stdout.getvalue())
        return server, payload, failure

    def assertPreCreationRefusal(self, server: FakeTmuxServer, failure: SystemExit | None) -> None:
        self.assertIsNotNone(failure)
        self.assertEqual(server.sessions, {})
        self.assertNotIn("new-session", [call[0] for call in server.calls])
        self.assertEqual(server.killed, [])
        self.assertNotIn("load-buffer", server.events)

    def assertFailedSessionPreserved(self, server: FakeTmuxServer, failure: SystemExit | None, name: str = "claude-lane") -> None:
        self.assertIsNotNone(failure)
        self.assertEqual(server.killed, [])
        self.assertIn(name, server.sessions)
        self.assertNotIn("load-buffer", server.events)
        self.assertNotIn("paste-buffer", server.events)
        self.assertNotIn("send-keys", server.events)

    # --- positives ---------------------------------------------------------
    def test_default_claude_launch_verifies_and_injects(self) -> None:
        server, payload, failure = self.launch(process_tree=["bash /state/run-claude_tui.sh", DIRECT_CLAUDE_PROCESS])

        self.assertIsNone(failure)
        assert payload is not None
        self.assertEqual(payload["visible_proof"]["runtime"], "claude")
        self.assertEqual(payload["visible_proof"]["metadata"]["agent"], "claude")
        self.assertEqual(payload["visible_proof"]["runtime_process"], DIRECT_CLAUDE_PROCESS)
        # The role survived a real write/read round-trip through pane metadata.
        self.assertEqual(server.pane_state(payload["pane"])["@oc_agent"], "claude")
        # Readiness, then verification, then injection — in that order.
        self.assertLess(server.events.index("process_tree"), server.events.index("load-buffer"))
        self.assertIn("send-keys", server.events)
        # The wrapper the launcher wrote really does own both evidence files.
        wrapper_body = Path(server.wrappers["claude-lane"]).read_text(encoding="utf-8")
        self.assertIn(str(server.pane_log), wrapper_body)
        self.assertIn(str(server.launch_record), wrapper_body)

    def test_untrusted_startup_saves_task_without_submitting(self) -> None:
        with mock.patch.object(agent_wall, 'assignment_is_bound', return_value=False):
            server, payload, failure = self.launch(process_tree=[DIRECT_CLAUDE_PROCESS])
        self.assertIsNone(failure)
        self.assertFalse(payload['prompt_submitted'])
        self.assertIn('setup_required', payload)
        self.assertIn('submit-assignment', payload['submit_command'])
        self.assertTrue(Path(payload['prompt_file']).is_file())
        self.assertNotIn('load-buffer', server.events)
        self.assertNotIn('paste-buffer', server.events)
        self.assertNotIn('send-keys', server.events)

    def test_generic_claude_role_accepts_opus_model(self) -> None:
        _, payload, failure = self.launch(
            extra_args=["--agent", "claude"], process_tree=[DIRECT_CLAUDE_PROCESS]
        )
        self.assertIsNone(failure)
        assert payload is not None
        self.assertEqual(payload["visible_proof"]["metadata"]["agent"], "claude")

    def test_generic_claude_role_accepts_fable_model(self) -> None:
        _, payload, failure = self.launch(
            extra_args=["--agent", "claude", "--model", "claude-fable-5-1", "--effort", "high"],
            process_tree=["claude --model claude-fable-5-1 --effort high"],
        )
        self.assertIsNone(failure)
        assert payload is not None
        self.assertEqual(payload["visible_proof"]["metadata"]["agent"], "claude")

    def test_explicit_fable_role_stays_distinguishable(self) -> None:
        server, payload, failure = self.launch(
            extra_args=["--agent", "fable", "--model", "claude-fable-5-1", "--effort", "high"],
            process_tree=["claude --model claude-fable-5-1 --effort high"],
        )
        self.assertIsNone(failure)
        assert payload is not None
        self.assertEqual(payload["visible_proof"]["metadata"]["agent"], "fable")
        self.assertEqual(server.pane_state(payload["pane"])["@oc_agent"], "fable")

    def test_resolved_claude_code_entrypoint_verifies(self) -> None:
        _, payload, failure = self.launch(process_tree=["bash /state/run-claude_tui.sh", RESOLVED_CLAUDE_PROCESS])
        self.assertIsNone(failure)
        assert payload is not None
        self.assertEqual(payload["visible_proof"]["runtime_process"], RESOLVED_CLAUDE_PROCESS)

    def test_node_package_entrypoint_verifies(self) -> None:
        _, payload, failure = self.launch(process_tree=[NODE_CLAUDE_PROCESS])
        self.assertIsNone(failure)
        assert payload is not None
        self.assertEqual(payload["visible_proof"]["runtime_process"], NODE_CLAUDE_PROCESS)

    # --- pre-creation refusals --------------------------------------------
    def test_non_claude_model_is_refused_before_session_creation(self) -> None:
        server, _, failure = self.launch(
            extra_args=["--agent", "claude", "--model", "gpt-5.6-sol"], process_tree=[DIRECT_CLAUDE_PROCESS]
        )
        self.assertPreCreationRefusal(server, failure)
        self.assertIn("claude-", str(failure))

    def test_wrong_role_is_refused_before_session_creation(self) -> None:
        server, _, failure = self.launch(
            extra_args=["--agent", "codex"], process_tree=[DIRECT_CLAUDE_PROCESS]
        )
        self.assertPreCreationRefusal(server, failure)

    def test_opus_role_with_fable_model_is_refused_before_session_creation(self) -> None:
        server, _, failure = self.launch(
            extra_args=["--agent", "opus", "--model", "claude-fable-5"],
            process_tree=["claude --model claude-fable-5"],
        )
        self.assertPreCreationRefusal(server, failure)
        self.assertIn("claude-opus-", str(failure))

    def test_fable_role_with_opus_model_is_refused_before_session_creation(self) -> None:
        server, _, failure = self.launch(
            extra_args=["--agent", "fable", "--model", "claude-opus-4-6"], process_tree=[DIRECT_CLAUDE_PROCESS]
        )
        self.assertPreCreationRefusal(server, failure)
        self.assertIn("claude-fable-", str(failure))

    def test_stale_fable_model_is_refused_before_session_creation(self) -> None:
        server, _, failure = self.launch(
            extra_args=["--agent", "fable", "--model", "claude-fable-5", "--effort", "high"],
            process_tree=["claude --model claude-fable-5 --effort high"],
        )
        self.assertPreCreationRefusal(server, failure)
        self.assertIn("claude-fable-5-1", str(failure))

    def test_wrong_fable_effort_is_refused_before_session_creation(self) -> None:
        server, _, failure = self.launch(
            extra_args=["--agent", "fable", "--model", "claude-fable-5-1", "--effort", "xhigh"],
            process_tree=["claude --model claude-fable-5-1 --effort xhigh"],
        )
        self.assertPreCreationRefusal(server, failure)
        self.assertIn("--effort high", str(failure))

    def test_role_model_refusal_never_derives_a_model(self) -> None:
        args = agent_wall.build_parser().parse_args(
            ["spawn-claude", "--name", "x", "--prompt-file", str(self.prompt), "--agent", "fable", "--model", "claude-opus-4-6"]
        )
        with self.assertRaises(SystemExit):
            agent_wall.validate_claude_role_model(args)
        self.assertEqual(args.model, "claude-opus-4-6")
        self.assertEqual(args.agent, "fable")

    # --- post-creation fail-closed ----------------------------------------
    def test_missing_runtime_process_cleans_up_exact_session(self) -> None:
        server, _, failure = self.launch(process_tree=["bash /state/run-claude_tui.sh", "zsh"])
        self.assertFailedSessionPreserved(server, failure)

    def test_wrong_runtime_process_cleans_up_exact_session(self) -> None:
        server, _, failure = self.launch(process_tree=["codex --model gpt-5.6-sol"])
        self.assertFailedSessionPreserved(server, failure)

    def test_unrelated_path_token_collision_fails_closed(self) -> None:
        server, _, failure = self.launch(process_tree=[UNRELATED_PATH_COLLISION_PROCESS])
        self.assertFailedSessionPreserved(server, failure)

    def test_empty_readiness_evidence_fails_before_verification(self) -> None:
        server, _, failure = self.launch(write_pane_log=False, process_tree=[DIRECT_CLAUDE_PROCESS])
        self.assertFailedSessionPreserved(server, failure)
        # Readiness gates verification: the verifier never even looked.
        self.assertNotIn("process_tree", server.events)

    def test_zero_byte_pane_log_fails_verification_independently(self) -> None:
        server, _, failure = self.launch(
            truncate_pane_log_at_verify=True, process_tree=[DIRECT_CLAUDE_PROCESS]
        )
        self.assertFailedSessionPreserved(server, failure)
        self.assertIn("pane log missing or empty", str(failure))

    def test_missing_launch_record_fails_verification_independently(self) -> None:
        server, _, failure = self.launch(write_launch_record=False, process_tree=[DIRECT_CLAUDE_PROCESS])
        self.assertFailedSessionPreserved(server, failure)
        self.assertIn("launch record missing or empty", str(failure))

    def test_zero_byte_launch_record_fails_verification_independently(self) -> None:
        server, _, failure = self.launch(empty_launch_record=True, process_tree=[DIRECT_CLAUDE_PROCESS])
        self.assertFailedSessionPreserved(server, failure)
        self.assertIn("launch record missing or empty", str(failure))

    def test_dead_pane_fails_verification(self) -> None:
        server, _, failure = self.launch(pane_dead="1", process_tree=[DIRECT_CLAUDE_PROCESS])
        self.assertFailedSessionPreserved(server, failure)
        self.assertIn("is dead", str(failure))


class RuntimePositionMatchingTests(unittest.TestCase):
    def test_executable_position_tokens_skip_env_wrapper(self) -> None:
        command = (
            "env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN TERM=xterm-256color "
            "claude --model claude-opus-5"
        )
        self.assertEqual(agent_wall.executable_position_tokens(command), ["claude"])

    def test_executable_position_tokens_include_interpreter_script(self) -> None:
        self.assertEqual(
            agent_wall.executable_position_tokens(NODE_CLAUDE_PROCESS),
            ["node", "/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/cli.js"],
        )

    def test_direct_and_resolved_entrypoints_still_match(self) -> None:
        for command in [
            DIRECT_CLAUDE_PROCESS,
            RESOLVED_CLAUDE_PROCESS,
            NODE_CLAUDE_PROCESS,
            "env -u ANTHROPIC_API_KEY TERM=xterm-256color claude --model claude-opus-5",
            "/opt/homebrew/bin/claude",
        ]:
            with self.subTest(command=command):
                self.assertTrue(agent_wall.command_text_mentions_runtime(command, "claude"))

    def test_interpreter_option_operand_is_not_the_executed_script(self) -> None:
        # `--require` consumes the following token, so the loader path is an
        # option value; the executed script is the one after it.
        command = "node --require /tmp/claude/loader.js /opt/app/main.js"
        self.assertEqual(
            agent_wall.executable_position_tokens(command),
            ["node", "/opt/app/main.js"],
        )
        self.assertFalse(agent_wall.command_text_mentions_runtime(command, "claude"))
        # Same shape in the short form.
        self.assertEqual(
            agent_wall.executable_position_tokens("node -r /tmp/claude/loader.js /opt/app/main.js"),
            ["node", "/opt/app/main.js"],
        )

    def test_env_option_operand_is_not_the_wrapped_executable(self) -> None:
        # `-P` consumes the following token, so the wrapped executable is
        # /usr/bin/other, not the alternate search path that contains `claude`.
        command = "env -P /tmp/claude /usr/bin/other --flag"
        self.assertEqual(agent_wall.executable_position_tokens(command), ["/usr/bin/other"])
        self.assertFalse(agent_wall.command_text_mentions_runtime(command, "claude"))

    def test_option_terminator_and_unknown_arity_fail_closed(self) -> None:
        # `--` ends option parsing: the next token really is the executable.
        self.assertEqual(
            agent_wall.executable_position_tokens("env -i -- /usr/bin/other --claude"),
            ["/usr/bin/other"],
        )
        # An option of undeterminable arity yields no runtime proof rather than
        # a guess about which token is executed.
        self.assertEqual(
            agent_wall.executable_position_tokens("env --unsupported-flag /tmp/claude/bin/claude"),
            [],
        )
        # An attached value does not make an unknown option safe to skip.
        for command, expected_positions in [
            ("node --definitely-unsupported=value /tmp/claude/cli.js", ["node"]),
            ("env --definitely-unsupported=value /usr/bin/claude", []),
        ]:
            with self.subTest(command=command):
                self.assertEqual(
                    agent_wall.executable_position_tokens(command),
                    expected_positions,
                )
                self.assertFalse(agent_wall.command_text_mentions_runtime(command, "claude"))
        # Inline code executes no script path, so nothing after it is an entrypoint.
        self.assertEqual(
            agent_wall.executable_position_tokens("node --eval require('/tmp/claude/x.js')"),
            ["node"],
        )

    def test_runtime_token_only_in_arguments_is_rejected(self) -> None:
        for command in [
            UNRELATED_PATH_COLLISION_PROCESS,
            "rg --files /home/example/projects/claude-code",
            "python3 /tmp/tool.py --config /opt/claude/conf.json",
            "bash /tmp/run-claude_tui.sh",
            "zsh",
            "node --require /tmp/claude/loader.js /opt/app/main.js",
            "env -P /tmp/claude /usr/bin/other --flag",
            "python3 -m tool --config /opt/claude/conf.json",
        ]:
            with self.subTest(command=command):
                self.assertFalse(agent_wall.command_text_mentions_runtime(command, "claude"))

    def test_other_runtimes_keep_their_positive_shapes(self) -> None:
        self.assertTrue(agent_wall.command_text_mentions_runtime("codex --model gpt-5.6-sol", "codex"))
        self.assertTrue(agent_wall.command_text_mentions_runtime("agy --model gemini-3.5-flash", "agy"))
        self.assertFalse(agent_wall.command_text_mentions_runtime("node /srv/codex-notes/build.js --out /tmp/codex", "codex"))

    def test_opus_is_an_allowed_claude_role(self) -> None:
        self.assertEqual(agent_wall.allowed_agents_for_runtime("claude"), {"claude", "fable", "opus"})
        self.assertEqual(agent_wall.allowed_agents_for_runtime("codex"), {"codex"})

    def test_verifier_still_rejects_foreign_role_on_claude_runtime(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "pane.log"
            launch = Path(tmp) / "launch.json"
            log.write_text("Claude Code\n", encoding="utf-8")
            launch.write_text("{}\n", encoding="utf-8")
            original_run_tmux = agent_wall.run_tmux
            original_tree = agent_wall.process_tree_commands
            try:
                agent_wall.run_tmux = lambda *args, **kwargs: subprocess.CompletedProcess(
                    args,
                    0,
                    agent_wall.TMUX_FIELD_SEP.join(["0", "100", "claude", "1", "agent_wall", "visible-agent", "codex", "kill_on_done", "logs/pane.log"])
                    + "\n",
                    "",
                )
                agent_wall.process_tree_commands = lambda pid: [DIRECT_CLAUDE_PROCESS]
                with self.assertRaises(agent_wall.VisibleContractError):
                    agent_wall.verify_visible_contract(
                        argparse.Namespace(name="claude-lane"),
                        pane="%1",
                        pane_log_path=log,
                        launch_record=str(launch),
                        command_kind="claude_tui",
                    )
            finally:
                agent_wall.run_tmux = original_run_tmux
                agent_wall.process_tree_commands = original_tree


class AgentWallReleaseHoldTests(unittest.TestCase):
    def _release_args(self, **overrides: object) -> argparse.Namespace:
        base = {
            "name": "",
            "pane": "%12",
            "end_reason": "",
            "dry_run": False,
            "evidence_captured": False,
            "allow_hygiene_after_release": False,
        }
        base.update(overrides)
        return argparse.Namespace(**base)

    def test_release_log_uses_launch_identity(self) -> None:
        self.assertIn("launch_id", agent_wall.OC_FIELDS)
        with mock.patch.object(agent_wall, "read_pane_metadata", return_value={"launch_id": "stable-launch", "hold_reason": "review"}), \
             mock.patch.object(agent_wall, "unset_hold_reason"), \
             mock.patch.object(agent_wall, "set_pane_options"), \
             mock.patch.object(agent_wall.event_log, "append") as append:
            with contextlib.redirect_stdout(io.StringIO()):
                agent_wall.cmd_release_hold(self._release_args(evidence_captured=True, allow_hygiene_after_release=True))
        self.assertEqual(len(append.call_args_list), 2)
        self.assertTrue(all(call.kwargs["identity"] == "stable-launch" for call in append.call_args_list))
        self.assertTrue(all(call.kwargs["session"] == "isolated-test-session" for call in append.call_args_list))

    def test_session_exists_uses_exact_name_not_prefix(self) -> None:
        with mock.patch.object(agent_wall, "run_tmux", return_value=subprocess.CompletedProcess([], 1, "", "")) as run:
            self.assertFalse(agent_wall.session_exists("worker"))
        run.assert_called_once_with("has-session", "-t", "=worker", check=False)

    def test_release_hold_requires_exact_target(self) -> None:
        with self.assertRaises(SystemExit):
            agent_wall.cmd_release_hold(self._release_args(name="", pane=""))

    def test_release_hold_ambiguous_session_is_rejected(self) -> None:
        original_rows = agent_wall.session_pane_rows
        try:
            agent_wall.session_pane_rows = lambda name: [
                {"pane": "%1", "window_index": "0", "window_name": "w", "pane_index": "0", "title": "a", "command": "zsh"},
                {"pane": "%2", "window_index": "0", "window_name": "w", "pane_index": "1", "title": "b", "command": "zsh"},
            ]
            with self.assertRaises(SystemExit):
                agent_wall.cmd_release_hold(self._release_args(name="ambiguous", pane=""))
        finally:
            agent_wall.session_pane_rows = original_rows

    def test_release_hold_dry_run_reports_plan_without_writing(self) -> None:
        set_calls: list[dict[str, str | None]] = []
        unset_hold_calls: list[str] = []
        original_read = agent_wall.read_pane_metadata
        original_set = agent_wall.set_pane_options
        original_unset_hold = agent_wall.unset_hold_reason
        buffer = io.StringIO()
        try:
            agent_wall.read_pane_metadata = lambda pane: {"state": "running", "hold_reason": "evidence pending"}
            agent_wall.set_pane_options = lambda pane, values: set_calls.append(values)
            agent_wall.unset_hold_reason = lambda pane: unset_hold_calls.append(pane)
            with contextlib.redirect_stdout(buffer):
                self.assertEqual(agent_wall.cmd_release_hold(self._release_args(dry_run=True)), 0)
        finally:
            agent_wall.read_pane_metadata = original_read
            agent_wall.set_pane_options = original_set
            agent_wall.unset_hold_reason = original_unset_hold

        self.assertEqual(set_calls, [])
        self.assertEqual(unset_hold_calls, [])
        report = json.loads(buffer.getvalue())
        self.assertFalse(report["applied"])
        self.assertTrue(report["dry_run"])
        self.assertTrue(report["had_hold_reason"])
        self.assertFalse(report["becomes_hygiene_eligible"])
        self.assertTrue(report["cleanup_rechecked"])
        self.assertEqual(report["planned_unset"], ["@oc_hold_reason"])
        self.assertEqual(set(report["planned_set"]), {"updated_at"})
        self.assertFalse(any(report["side_effects"].values()))

    def test_release_hold_real_requires_confirmation_flags(self) -> None:
        original_read = agent_wall.read_pane_metadata
        try:
            agent_wall.read_pane_metadata = lambda pane: {"hold_reason": "evidence pending"}
            with self.assertRaises(SystemExit):
                agent_wall.cmd_release_hold(self._release_args(dry_run=False, evidence_captured=True, allow_hygiene_after_release=False))
            with self.assertRaises(SystemExit):
                agent_wall.cmd_release_hold(self._release_args(dry_run=False, evidence_captured=False, allow_hygiene_after_release=True))
        finally:
            agent_wall.read_pane_metadata = original_read


    def test_release_hold_never_kills_or_applies_hygiene(self) -> None:
        calls: list[tuple[object, ...]] = []
        original_read = agent_wall.read_pane_metadata
        original_run_tmux = agent_wall.run_tmux
        buffer = io.StringIO()
        try:
            agent_wall.read_pane_metadata = lambda pane: {"hold_reason": "evidence pending"}
            agent_wall.run_tmux = lambda *call, **kwargs: calls.append(call) or subprocess.CompletedProcess(call, 0, "", "")
            with contextlib.redirect_stdout(buffer):
                self.assertEqual(
                    agent_wall.cmd_release_hold(
                        self._release_args(
                            dry_run=False,
                            evidence_captured=True,
                            allow_hygiene_after_release=True,
                        )
                    ),
                    0,
                )
        finally:
            agent_wall.read_pane_metadata = original_read
            agent_wall.run_tmux = original_run_tmux

        flat = [" ".join(str(part) for part in call) for call in calls]
        for entry in flat:
            self.assertNotIn("kill", entry)
            self.assertNotIn("session_hygiene", entry)
            self.assertNotIn("break-pane", entry)
            self.assertNotIn("detach", entry)
        # The only tmux mutations are the single hold unset and metadata set-options.
        self.assertIn(("set-option", "-p", "-u", "-t", "%12", "@oc_hold_reason"), calls)
        self.assertFalse(any("@oc_state" in call or "@oc_exit_code" in call or "@oc_completed_at" in call for call in calls))



class AssignmentSubmissionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.launch_id = '12345678-1234-1234-1234-123456789abc'
        (self.root/'launch.json').write_text(json.dumps({'run_id': self.launch_id}))
        (self.root/'process.json').write_text(json.dumps({'run_id':self.launch_id,
             'pane_identity':['%1','4242','$1','@1',self.launch_id,'','0']}))
        (self.root/'prompt.md').write_text('Only the intended worker may receive this task.')
        self.server = FakeTmuxServer(pane_log=self.root/'log', launch_record=self.root/'record')
        self.server.sessions['worker'] = {'pane_id':'%1','pane_pid':'4242','session_id':'$1',
            'window_id':'@1','pane_dead':'0','@oc_launch_id':self.launch_id}
    def submit(self):
        with mock.patch.object(agent_wall, 'run_tmux', self.server), mock.patch.object(agent_wall.time, 'sleep'), contextlib.redirect_stdout(io.StringIO()):
            return agent_wall.cmd_submit_assignment(argparse.Namespace(pane='%1',assignment_run=str(self.root)))
    def test_stale_submit_refuses_respawn_even_when_launch_metadata_survives(self):
        self.server.sessions['worker']['pane_pid'] = '5252'
        with self.assertRaisesRegex(SystemExit, 'identity changed'):
            self.submit()
        self.assertNotIn('paste-buffer', self.server.events)
        self.assertNotIn('send-keys', self.server.events)
        self.assertFalse((self.root/'submission.json').exists())
    def test_replacement_between_paste_and_enter_refuses_enter(self):
        def replace(args):
            self.server.sessions['worker']['pane_pid'] = '5252'
            return self.server._ok(args)
        self.server._tmux_paste_buffer = replace
        with self.assertRaisesRegex(SystemExit, 'identity changed'):
            self.submit()
        self.assertIn('paste-buffer', self.server.events)
        self.assertNotIn('send-keys', self.server.events)
    def test_dead_submit_refused(self):
        self.server.sessions['worker']['pane_dead'] = '1'
        with self.assertRaisesRegex(SystemExit, 'identity changed'):
            self.submit()
        self.assertNotIn('paste-buffer', self.server.events)
    def test_initial_submission_is_one_shot(self):
        self.assertEqual(self.submit(),0)
        with self.assertRaisesRegex(SystemExit, 'already submitted'):
            self.submit()
        self.assertEqual(self.server.events.count('send-keys'),1)
    def test_missing_record_is_visible_refusal(self):
        (self.root/'process.json').unlink()
        with self.assertRaisesRegex(SystemExit, 'prompt not submitted'):
            self.submit()
        self.assertNotIn('load-buffer', self.server.events)

if __name__ == "__main__":
    unittest.main()
