#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tmux"))
from helpers.tmux import cockpit_doctor as doctor


def model_row(
    *,
    session: str = "worker",
    window: str = "main",
    pane: str = "%1",
    title: str = "",
    command: str = "zsh",
    dead: str = "0",
    pid: str = "100",
    contract_version: str = "1",
    managed_by: str = "",
    kind: str = "",
    agent: str = "",
    cleanup_policy: str = "",
    evidence_path: str = "",
    why_headless: str = "",
    progress_path: str = "",
) -> str:
    return doctor.TMUX_FIELD_SEP.join(
        [
            session,
            window,
            pane,
            title,
            command,
            dead,
            pid,
            contract_version,
            managed_by,
            kind,
            agent,
            cleanup_policy,
            evidence_path,
            why_headless,
            progress_path,
        ]
    )


class DoctorTests(unittest.TestCase):
    def test_overall_status(self) -> None:
        self.assertEqual(doctor.overall([doctor.ok("a")]), "ok")
        self.assertEqual(doctor.overall([doctor.ok("a"), doctor.warn("b")]), "warn")
        self.assertEqual(doctor.overall([doctor.ok("a"), doctor.fail("b")]), "fail")

    def test_dashboard_requires_cockpit_command(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            return subprocess.CompletedProcess(args, 0, "zsh\t0\t123\n", "")

        check = doctor.check_dashboard("cockpit:dashboard.0", runner)
        self.assertEqual(check.status, "fail")
        self.assertIn("not OpenClaw Cockpit", check.detail)

    def test_dashboard_accepts_openclaw_cockpit_command(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            return subprocess.CompletedProcess(args, 0, "openclaw-cockpit\t0\t123\n", "")

        check = doctor.check_dashboard("cockpit:dashboard.0", runner)
        self.assertEqual(check.status, "ok")

    def test_hygiene_plan_warns_on_pending_or_refused_sessions(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            payload = [{"session": "done-worker", "action": "kill"}, {"session": "held", "action": "refuse"}]
            return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

        check = doctor.check_hygiene_plan(runner)
        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["killable"], ["done-worker"])
        self.assertEqual(check.data["refused"], ["held"])

    def test_hygiene_plan_warns_on_detected_work_attention(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            payload = [
                {
                    "session": "raw-worker",
                    "action": "skip",
                    "reason": "unmanaged_or_incomplete_contract",
                    "kind": "detected-work",
                },
                {
                    "session": "service",
                    "action": "skip",
                    "reason": "unmanaged_or_incomplete_contract",
                    "kind": "service",
                },
            ]
            return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

        check = doctor.check_hygiene_plan(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["attention"], ["raw-worker"])

    def test_janitor_command_requires_both_apply_cycles(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "display-message", "-p"]:
                return subprocess.CompletedProcess(args, 0, "22\n", "")
            if args[:2] == ["ps", "-p"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    "bash -c python3 tools/tmux/session_hygiene.py apply --policy smoke --json --status-file status.json; "
                    "python3 tools/tmux/session_hygiene.py apply --policy kill-safe --json --status-file status.json\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 1, "", "unexpected")

        check = doctor.check_janitor_command("cockpit-hygiene", runner)
        self.assertEqual(check.status, "ok")

    def test_hygiene_log_cycle_requires_smoke_and_kill_safe_markers(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "janitor.log"
            log.write_text("=== 2026-07-03T00:00:00Z smoke ===\n{}\n=== 2026-07-03T00:00:00Z kill-safe ===\n{}\n", encoding="utf-8")

            check = doctor.check_hygiene_log_cycle(log)

        self.assertEqual(check.status, "ok")

    def test_writable_check(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            check = doctor.check_writable(Path(tmp) / "ledger", "ledger_root")
        self.assertEqual(check.status, "ok")

    def test_runtime_snapshot_warns_on_attention(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            payload = {"cardContract": "runtime-card.v1", "summary": {"total": 2, "byState": {"attention": 1, "active": 1}}}
            return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

        check = doctor.check_openclaw_runtime_snapshot(doctor.DEFAULT_RUNTIME_SNAPSHOT_SCRIPT, runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["attention"], 1)

    def test_model_lane_contract_warns_on_detected_opus_committee(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(
                        session="cd-opus-committee-1608",
                        title="cd-opus-committee-1608",
                        command="zsh",
                        managed_by="tmux_inspector",
                        kind="detected-agent",
                        agent="committee",
                    )
                    + "\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "detected_model_lane_without_agent_wall_contract")

    def test_model_lane_contract_warns_on_raw_child_runtime(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="raw-codex", command="zsh", pid="200") + "\n", "")
            if args[:3] == ["ps", "-ww", "-axo"]:
                return subprocess.CompletedProcess(args, 0, "200 1 zsh\n201 200 codex --model gpt-5.5\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "raw_model_lane_without_agent_wall_contract")

    def test_model_lane_contract_warns_on_raw_node_fronted_claude_runtime(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="scratch", command="zsh", pid="205") + "\n", "")
            if args[:3] == ["ps", "-ww", "-axo"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    "205 1 zsh\n206 205 node /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/cli.js\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "raw_model_lane_without_agent_wall_contract")
        self.assertEqual(check.data["warnings"][0]["window"], "main")

    def test_model_lane_contract_warns_on_incomplete_agent_wall_batch(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(
                        session="bad-batch",
                        command="bash",
                        pid="210",
                        managed_by="agent_wall",
                        kind="batch-worker",
                        agent="codex-batch",
                        cleanup_policy="kill_on_done",
                        evidence_path="summary.json",
                        progress_path="progress.jsonl",
                    )
                    + "\n",
                    "",
                )
            if args[:3] == ["ps", "-ww", "-axo"]:
                return subprocess.CompletedProcess(args, 0, "210 1 bash\n211 210 codex exec review\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "managed_model_lane_with_incomplete_contract")

    def test_model_lane_contract_warns_on_unrecognized_manager_runtime(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(session="bad-manager", command="zsh", pid="220", managed_by="somebody_else", kind="worker") + "\n",
                    "",
                )
            if args[:3] == ["ps", "-ww", "-axo"]:
                return subprocess.CompletedProcess(args, 0, "220 1 zsh\n221 220 agy --model gemini-3.5-flash\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "unrecognized_manager_model_lane")

    def test_model_lane_contract_does_not_warn_raw_name_only_runtime(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="claude-docs", title="claude notes", command="zsh") + "\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_does_not_warn_suspended_editor_argument(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="notes", command="zsh", pid="230") + "\n", "")
            if args[:3] == ["ps", "-ww", "-axo"]:
                return subprocess.CompletedProcess(args, 0, "230 1 zsh\n231 230 vim claude-notes.md\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_warns_on_runtime_child_under_editor_foreground(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="mixed-pane", command="vim", pid="235") + "\n", "")
            if args[:3] == ["ps", "-ww", "-axo"]:
                return subprocess.CompletedProcess(args, 0, "235 1 vim notes.md\n236 235 claude --model claude-opus-4-8\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "raw_model_lane_without_agent_wall_contract")

    def test_command_runtime_matcher_does_not_match_non_interpreter_arguments(self) -> None:
        self.assertFalse(doctor.command_mentions_runtime_executable("python3 eval.py --model claude-opus"))
        self.assertTrue(doctor.command_mentions_runtime_executable("node /opt/homebrew/bin/claude"))
        self.assertFalse(doctor.command_mentions_runtime_executable("vim claude-notes.md"))

    def test_model_lane_contract_warns_on_manual_adopt_and_autodetect(self) -> None:
        rows = "\n".join(
            [
                model_row(session="manual-codex", command="zsh", managed_by="manual_adopt", kind="detected-agent", agent="codex"),
                model_row(session="auto-agy", command="zsh", managed_by="autodetect", kind="detected-work", agent="agy"),
            ]
        )

        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, rows + "\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual([item["reason"] for item in check.data["warnings"]], ["detected_model_lane_without_agent_wall_contract"] * 2)

    def test_model_lane_contract_warns_on_dead_runtime_process_without_cleanup(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="dead-codex", command="codex", dead="1") + "\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "stale_model_lane_without_cleanup_contract")

    def test_model_lane_contract_ignores_weak_words_without_runtime(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, model_row(session="committee-review", title="committee review", command="zsh") + "\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_excludes_editor_viewing_agy_skill(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(session="read-docs", title="skills/runtime-antigravity-operate/references/general-cli/PROCEDURE.md", command="less", agent="agy") + "\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_excludes_viewer_reading_committee_docs(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(session="read-committee", title="committee-v2/SYNTHESIS.md", command="cat", agent="opus") + "\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_accepts_valid_visible_and_batch_lanes(self) -> None:
        visible = model_row(
            session="codex-visible",
            command="codex",
            managed_by="agent_wall",
            kind="visible-agent",
            agent="codex",
            cleanup_policy="kill_after_ttl",
            evidence_path="logs/codex.log",
        )
        batch = model_row(
            session="codex-batch",
            command="bash",
            managed_by="agent_wall",
            kind="batch-worker",
            agent="codex-batch",
            cleanup_policy="kill_on_done",
            evidence_path="summary.json",
            why_headless="machine-readable scoring",
            progress_path="progress.jsonl",
        )

        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, visible + "\n" + batch + "\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_rejects_visible_contract_without_version(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(
                        session="fake-visible",
                        command="codex",
                        contract_version="",
                        managed_by="agent_wall",
                        kind="visible-agent",
                        agent="codex",
                        cleanup_policy="kill_after_ttl",
                        evidence_path="logs/codex.log",
                    )
                    + "\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertEqual(check.data["warnings"][0]["reason"], "managed_model_lane_with_incomplete_contract")

    def test_model_lane_contract_excludes_services(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(
                    args,
                    0,
                    model_row(session="example-service", command="agy", kind="service", agent="agy") + "\n",
                    "",
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "ok")

    def test_model_lane_contract_malformed_rows_warn_not_crash(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["tmux", "list-panes", "-a"]:
                return subprocess.CompletedProcess(args, 0, "too few fields\n" + model_row(session="valid-visible", command="codex", managed_by="agent_wall", kind="visible-agent", agent="codex", cleanup_policy="kill_after_ttl", evidence_path="logs/codex.log") + "\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_model_lane_contracts(runner)

        self.assertEqual(check.status, "warn")
        self.assertIn("malformed", check.detail)

    def test_binary_identity_dirty_build_is_parity_failure(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["go", "version", "-m"]:
                return subprocess.CompletedProcess(
                    args, 0, "bin: go1.26.4\n\tbuild\tvcs.revision=abc123def456\n\tbuild\tvcs.modified=true\n", ""
                )
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_binary_identity(sys.executable, "/nonexistent-source", runner)

        self.assertEqual(check.status, "fail")
        self.assertIn("dirty", check.detail)
        self.assertNotIn("not found", check.detail)

    def test_binary_identity_revision_mismatch_is_parity_failure(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:

            def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
                if args[:3] == ["go", "version", "-m"]:
                    return subprocess.CompletedProcess(
                        args, 0, "bin: go1.26.4\n\tbuild\tvcs.revision=aaaa111122223333\n\tbuild\tvcs.modified=false\n", ""
                    )
                if args[:2] == ["git", "-C"]:
                    return subprocess.CompletedProcess(args, 0, "bbbb444455556666\n", "")
                return subprocess.CompletedProcess(args, 0, "", "")

            check = doctor.check_binary_identity(sys.executable, tmp, runner)

        self.assertEqual(check.status, "fail")
        self.assertIn("does not match", check.detail)

    def test_binary_identity_match_is_ok(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:

            def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
                if args[:3] == ["go", "version", "-m"]:
                    return subprocess.CompletedProcess(
                        args, 0, "bin: go1.26.4\n\tbuild\tvcs.revision=cccc777788889999\n\tbuild\tvcs.modified=false\n", ""
                    )
                if args[:2] == ["git", "-C"]:
                    return subprocess.CompletedProcess(args, 0, "cccc777788889999\n", "")
                return subprocess.CompletedProcess(args, 0, "", "")

            check = doctor.check_binary_identity(sys.executable, tmp, runner)

        self.assertEqual(check.status, "ok")

    def test_binary_identity_without_vcs_metadata_is_unverified_not_ok(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            if args[:3] == ["go", "version", "-m"]:
                return subprocess.CompletedProcess(args, 0, "bin: go1.26.4\n\tmod\tsomething\t(devel)\n", "")
            return subprocess.CompletedProcess(args, 0, "", "")

        check = doctor.check_binary_identity(sys.executable, "/nonexistent-source", runner)

        self.assertEqual(check.status, "warn")
        self.assertIn("unverified", check.detail)

    def test_binary_identity_missing_binary_is_distinct_from_parity(self) -> None:
        check = doctor.check_binary_identity("/definitely/not/a/binary", "/nonexistent-source")

        self.assertEqual(check.status, "fail")
        self.assertIn("not found", check.detail)

    def test_read_binary_build_identity_parses_go_version_output(self) -> None:
        def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
            return subprocess.CompletedProcess(
                args,
                0,
                "bin: go1.26.4\n"
                "\tpath\texample\n"
                "\tbuild\tvcs.revision=deadbeef\n"
                "\tbuild\tvcs.modified=false\n"
                "\tbuild\tvcs.time=2026-08-12T00:00:00Z\n",
                "",
            )

        identity, err = doctor.read_binary_build_identity(sys.executable, runner)

        self.assertEqual(err, "")
        self.assertEqual(identity["vcs.revision"], "deadbeef")
        self.assertEqual(identity["vcs.modified"], "false")

    def test_run_checks_with_fake_runner(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "janitor.log"
            status = Path(tmp) / "status.json"
            inspector_log = Path(tmp) / "inspector.log"
            log.write_text("=== now smoke ===\n{}\n=== now kill-safe ===\n{}\n", encoding="utf-8")
            status.write_text('{"status_version":1,"generated_at":"2099-01-01T00:00:00Z","sessions":{}}\n', encoding="utf-8")
            inspector_log.write_text("ok\n", encoding="utf-8")
            ledger = Path(tmp) / "ledger"

            def runner(args: list[str]) -> subprocess.CompletedProcess[str]:
                if args[-1] == "--help":
                    return subprocess.CompletedProcess(args, 0, "help\n", "")
                if args[:3] == ["go", "version", "-m"]:
                    return subprocess.CompletedProcess(
                        args, 0, "bin: go1.26.4\n\tbuild\tvcs.revision=feedface0123\n\tbuild\tvcs.modified=false\n", ""
                    )
                if args[:2] == ["git", "-C"]:
                    return subprocess.CompletedProcess(args, 0, "feedface0123\n", "")
                if args[:3] == ["tmux", "display-message", "-p"]:
                    if args[4] == "cockpit:dashboard.0":
                        if args[5] == "#{pane_pid}":
                            return subprocess.CompletedProcess(args, 0, "11\n", "")
                        return subprocess.CompletedProcess(args, 0, "openclaw-cockpit\t0\t11\n", "")
                    if args[5] == "#{pane_pid}":
                        return subprocess.CompletedProcess(args, 0, "22\n", "")
                    return subprocess.CompletedProcess(args, 0, "bash\t0\t22\n", "")
                if args[:2] == ["ps", "-p"]:
                    if args[2] == "11":
                        return subprocess.CompletedProcess(
                            args, 0, "openclaw-cockpit --monitor-only --organize --openclaw-runtime\n", ""
                        )
                    return subprocess.CompletedProcess(
                        args,
                        0,
                        "bash -c python3 tools/tmux/session_hygiene.py apply --policy smoke --json --status-file status.json; "
                        "python3 tools/tmux/session_hygiene.py apply --policy kill-safe --json --status-file status.json\n",
                        "",
                    )
                if args[:2] == ["tmux", "has-session"]:
                    return subprocess.CompletedProcess(args, 0, "", "")
                if args[-3:] == ["plan", "--policy", "kill-safe"] or "session_hygiene.py" in " ".join(args):
                    return subprocess.CompletedProcess(args, 0, "[]", "")
                if len(args) >= 2 and args[1].endswith("cockpit_snapshot.py"):
                    payload = {
                        "cardContract": "runtime-card.v1",
                        "summary": {"total": 0, "byState": {"attention": 0, "active": 0, "unknown": 0}},
                    }
                    return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")
                return subprocess.CompletedProcess(args, 0, "", "")

            args = argparse.Namespace(
                binary=sys.executable,
                source=tmp,
                wall_target="cockpit:dashboard.0",
                janitor_session="cockpit-hygiene",
                inspector_session="cockpit-inspector",
                hygiene_log=str(log),
                hygiene_status=str(status),
                inspector_log=str(inspector_log),
                ledger_root=str(ledger),
                runtime_snapshot_script=str(doctor.DEFAULT_RUNTIME_SNAPSHOT_SCRIPT),
                max_log_age=180,
                max_inspector_log_age=30,
            )
            checks = doctor.run_checks(args, runner)
        self.assertEqual(doctor.overall(checks), "ok")


if __name__ == "__main__":
    unittest.main()
