import contextlib
import io
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import patch
import pytest
from helpers.tmux import lifecycle, session_hygiene as hygiene, agent_wall, services
from helpers.tests.support import disposable_tmux, managed


def test_missing_default_and_explicit_missing(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path))
    values, sources = lifecycle.load(environ={})
    assert values["completed_retention_seconds"] == 60
    assert sources["completed_retention_seconds"] == "built-in"
    with pytest.raises(ValueError, match="lifecycle config"):
        lifecycle.load(str(tmp_path / "missing.json"), environ={})


@pytest.mark.parametrize("value", [{"unknown": 2}, {"cleanup_interval_seconds": 0}, {"completed_retention_seconds": -1}, {"max_kills": True}, {"temporary_hold_hours": float("inf")}, {"archive_dir": "relative"}, {"closeout_default": "guess"}, {"inspector_protected_sessions": [1]}])
def test_invalid_config_rejected(value):
    with pytest.raises(ValueError):
        lifecycle.validate(value)


def test_precedence_and_derived_paths(tmp_path):
    path = tmp_path / "lifecycle.json"
    path.write_text(json.dumps({"completed_retention_seconds": 600, "cleanup_interval_seconds": 30}))
    values, sources = lifecycle.load(str(path), environ={"CASS_TMUX_HYGIENE_INTERVAL": "45", "OPENCLAW_COCKPIT_STATE_DIR": str(tmp_path)}, overrides={"cleanup_interval_seconds": 90})
    assert values["completed_retention_seconds"] == 600
    assert values["cleanup_interval_seconds"] == 90
    assert sources["cleanup_interval_seconds"] == "explicit option"
    assert values["archive_dir"] == str(tmp_path / "cleanup-ledger")


@pytest.mark.parametrize("content", ['{} {}', '[]', '{"max_kills": 0}'])
def test_bad_file_stops_apply_before_mutation(tmp_path, content):
    path = tmp_path / "bad.json"; path.write_text(content)
    with patch.object(hygiene, "cmd_apply") as apply:
        with pytest.raises(SystemExit):
            hygiene.main(["apply", "--lifecycle-config", str(path)])
        apply.assert_not_called()


def test_captured_job_retention_and_keep_open(tmp_path):
    now = datetime.now(timezone.utc)
    evidence = tmp_path / "result.md"; evidence.write_text("Completed result")
    p = managed("job", kind="agent", cleanup_policy="kill_on_done", run_root=str(tmp_path), evidence_path=str(evidence), completed_at=(now-timedelta(seconds=120)).isoformat(), completed_retention_seconds="600")
    p.dead = True
    result = hygiene.eligible_managed(p.session, [p], policy="kill-safe", grace=1, now=now)
    assert result["reason"] == "completion_grace_active"
    p.meta["completed_retention_seconds"] = "60"
    result = hygiene.eligible_managed(p.session, [p], policy="kill-safe", grace=600, now=now)
    assert result["action"] == "kill"
    p.meta["keep_open"] = "1"
    assert hygiene.eligible_managed(p.session, [p], policy="kill-safe", grace=0, now=now)["reason"] == "explicit_keep_open"
    with patch.object(hygiene, "list_panes", return_value=[p]):
        assert hygiene.revalidate_target(result, allow_hold=True)[1] == "explicit_keep_open_at_apply"


def test_global_and_subcommand_explicit_options_survive(tmp_path):
    path = tmp_path / "config.json"; path.write_text('{"completed_retention_seconds":600}')
    for argv in (["--grace", "11", "plan"], ["plan", "--grace", "11"]):
        captured = []
        with patch.object(hygiene, "cmd_plan", side_effect=lambda args: captured.append(args) or 0):
            hygiene.main([*argv, "--lifecycle-config", str(path)])
        assert captured[0].grace == 11


def test_service_bad_log_path_never_starts_mutating_child(tmp_path):
    config, _ = lifecycle.load(environ={})
    file = tmp_path / "not-directory"; file.write_text("x")
    config["log_dir"] = str(file)
    with patch.object(services.subprocess, "run") as run:
        with pytest.raises(OSError):
            services.cycle("hygiene", config, None)
        run.assert_not_called()


def test_launch_explicit_choice_wins_over_config(tmp_path):
    path = tmp_path / "config.json"; path.write_text('{"closeout_default":"keep_open","temporary_hold_hours":4}')
    captured = []
    with patch.object(agent_wall, "cmd_spawn_codex", side_effect=lambda args: captured.append(args) or 0):
        agent_wall.main(["--lifecycle-config", str(path), "spawn-codex", "--name", "job", "--prompt-file", "unused", "--close-on-completion", "--cleanup-policy", "manual", "--ttl", "never"])
    args = captured[0]
    assert args.keep_open is False and args.hold_hours == 4
    agent_wall.ensure_cleanup_defaults(args, pane_log=str(tmp_path / "pane.log"), run_root=tmp_path)
    assert args.cleanup_policy == "manual" and args.ttl == "never"


def test_real_disposable_jobs_use_file_retention(tmp_path):
    import subprocess
    import time
    # Outcome timestamps are backdated; no ten-minute wall-clock sleep needed.
    with disposable_tmux() as server:
        run = server.run
        for name, retention in (("short", 60), ("long", 600)):
            config_file = tmp_path / (name + ".json")
            config_file.write_text(json.dumps({"completed_retention_seconds": retention}))
            config, _ = lifecycle.load(str(config_file), environ={})
            pane = run("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", name, "sleep 600").stdout.strip()
            run("set-option", "-w", "-t", pane, "remain-on-exit", "on")
            result = tmp_path / (name + ".md"); result.write_text("Complete saved result")
            metadata = dict(contract_version="1", managed_by="agent_wall", kind="agent", state="done", cleanup_policy="kill_on_done", ttl="never", run_root=str(tmp_path), evidence_path=str(result), completed_at=(datetime.now(timezone.utc)-timedelta(seconds=120)).isoformat(), completed_retention_seconds=str(config["completed_retention_seconds"]))
            for key, value in metadata.items():
                run("set-option", "-p", "-t", pane, "@oc_"+key, value)
            run("respawn-pane", "-k", "-t", pane, "exit 0")
            for _ in range(100):
                if run("display-message", "-p", "-t", pane, "#{pane_dead}").stdout.strip() == "1": break
                time.sleep(.01)
        with patch.object(hygiene, "run_tmux", side_effect=run), contextlib.redirect_stdout(io.StringIO()) as output:
            hygiene.main(["apply", "--json", "--archive-root", str(tmp_path / "archive"), "--status-file", str(tmp_path / "status.json")])
        report = json.loads(output.getvalue())
        assert [row["session"] for row in report["killed"]] == ["short"], report
        assert run("has-session", "-t", "=long", check=False).returncode == 0
        assert run("has-session", "-t", "=short", check=False).returncode != 0


@pytest.mark.parametrize("policy", ["manual", "kill_after_ttl"])
def test_batch_contract_preserves_explicit_policy_and_never_ttl(tmp_path, policy):
    argv = ["spawn", "--name", "batch", "--kind", "batch-worker", "--why-headless", "test batch",
            "--run-root", str(tmp_path), "--progress-path", str(tmp_path / "progress.json"),
            "--cleanup-policy", policy, "--ttl", "never", "--", "true"]
    args = agent_wall.build_parser().parse_args(argv)
    agent_wall.configure_lifecycle_args(args, argv)
    agent_wall.validate_launch_contract(args, generic=True)
    agent_wall.ensure_cleanup_defaults(args, pane_log=str(tmp_path / "pane.log"), run_root=tmp_path)
    assert args.cleanup_policy == policy
    assert args.ttl == "never"


@pytest.mark.parametrize("path", ["/tmp/bad\0path", "/tmp/bad\npath", "~cockpit-user-that-does-not-exist/path"])
def test_invalid_config_paths_are_named_validation_errors(path):
    with pytest.raises(ValueError, match="archive_dir"):
        lifecycle.validate({"archive_dir": path})


def test_child_command_flags_do_not_override_launcher_hold_configuration(tmp_path):
    path = tmp_path / "config.json"
    path.write_text('{"temporary_hold_hours":4}')
    argv = ["--lifecycle-config", str(path), "spawn", "--name", "job", "--hold-reason", "review", "--", "program", "--hold-hours", "99"]
    args = agent_wall.build_parser().parse_args(argv)
    agent_wall.configure_lifecycle_args(args, argv)
    assert args.hold_hours == 4


def test_signaled_service_child_is_not_reported_as_success(tmp_path):
    import subprocess
    config, _ = lifecycle.load(environ={}, overrides={"log_dir": str(tmp_path)})
    with patch.object(services.subprocess, "run", return_value=subprocess.CompletedProcess([], -15)):
        assert services.cycle("hygiene", config, None) == 143


@pytest.mark.parametrize("configured", [False, True])
@pytest.mark.parametrize("reason", ["", "   "])
def test_new_keep_open_is_a_timed_review_not_permanent(tmp_path, configured, reason):
    from helpers.tmux import holds
    path = tmp_path / 'config.json'
    path.write_text(json.dumps({'temporary_hold_hours': 4, 'closeout_default': 'keep_open' if configured else 'close'}))
    argv = ['--lifecycle-config', str(path), 'spawn-claude', '--name', 'review', '--prompt-file', 'unused', '--hold-reason', reason]
    if not configured:
        argv.append('--keep-open')
    args = agent_wall.build_parser().parse_args(argv)
    agent_wall.configure_lifecycle_args(args, argv)
    before = datetime.now(timezone.utc)
    meta = agent_wall.metadata_from_args(args, state=None)
    deadline = datetime.fromisoformat(meta['hold_until'].replace('Z', '+00:00'))
    assert meta['keep_open'] == '0' and meta['hold_reason'] == 'assignment review'
    assert timedelta(hours=3, minutes=59) < deadline - before <= timedelta(hours=4)
    assert holds.active(meta, before)
    assert not holds.active(meta, deadline)
    # Legacy flags retain their old meaning even with an expired date.
    assert holds.active(dict(meta, keep_open='1'), deadline)


def test_indefinite_launch_requires_reason_and_has_no_deadline():
    argv = ['spawn-claude', '--name', 'pin', '--prompt-file', 'unused', '--indefinite']
    args = agent_wall.build_parser().parse_args(argv)
    with pytest.raises(SystemExit, match='requires a nonempty'):
        agent_wall.metadata_from_args(args, state=None)
    args.hold_reason = 'operator pin'
    meta = agent_wall.metadata_from_args(args, state=None)
    assert meta['hold_until'] == '' and meta['keep_open'] == '0'


def test_explicit_renewal_migrates_legacy_bit_to_deadline():
    args = agent_wall.build_parser().parse_args(['keep-open', '--name', 'old', '--hold-reason', 'review', '--hold-hours', '2'])
    with patch.object(agent_wall, 'unique_pane_for_session', return_value='%1'), patch.object(agent_wall, 'read_pane_metadata', return_value={'keep_open':'1'}), patch.object(agent_wall.event_log, 'append'), patch.object(agent_wall, 'set_pane_options') as setter:
        agent_wall.cmd_keep_open(args)
    assert setter.call_args.args[1]['keep_open'] == '0'
    assert setter.call_args.args[1]['hold_until']
