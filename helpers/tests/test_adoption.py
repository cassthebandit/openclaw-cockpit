"""Policy/authority tests; synthetic observations are not native profile proof."""
import argparse
import contextlib
import io
import json
import subprocess
from datetime import timedelta
from unittest.mock import patch

import pytest
from helpers.tmux import adoption as a, lifecycle, session_hygiene as h
from test_hygiene import pane


def fixture(tmp_path, *, dead=False):
    p = pane("temporary")
    p.pid = "321"
    p.dead = dead
    p.dead_status = "0" if dead else ""
    p.server_socket = "/tmp/isolated-adoption-test.sock"
    p.server_pid = "123"
    p.server_started = p.created
    p.session_windows = p.window_panes = "1"
    p.session_attached = p.pane_in_mode = p.pane_input_off = p.pane_pipe = p.pane_synchronized = "0"
    p.remain_on_exit = "on"
    p.window_activity = "1000"
    p.command = "claude.exe"
    config = {**lifecycle.DEFAULTS, "state_dir": str(tmp_path), "status_file": str(tmp_path / "status.json"),
              "archive_dir": str(tmp_path / "archive"), "adopt_existing_exited": True,
              "cleanup_whitelist": [{"exact": p.session}], "live_retirement": "observed",
              "completed_retention_seconds": 0, "teardown_grace_seconds": 0,
              "live_retirement_quiet_seconds": 1}
    args = argparse.Namespace(config=config, policy="kill-safe", grace=0, adopted_grace=0,
                              allow_session=[], override_hold=False, max_kills=1, json=True,
                              archive_root=config["archive_dir"], status_file=config["status_file"],
                              interval=60, write_status=False)
    return p, config, args


def enrollment(tmp_path, p):
    file = tmp_path / "result.md"
    file.write_bytes(b"saved result\x00bytes")
    _, digest = a.result_bytes(str(file))
    return {"release_id": "decision-1", "result_path": str(file), "result_sha256": digest,
            "profile": "claude-2.1.270-direct", "attestation": a.RISK}


@pytest.mark.parametrize("value", [True, {}, ["x"], [{"exact": ""}], [{"glob": "a?"}],
    [{"exact": "a", "glob": "b"}], [{"regex": "x"}], [{"exact": "x\x7f"}], [{"exact": "x" * 257}]])
def test_selector_validation(value):
    with pytest.raises(ValueError):
        lifecycle.validate({"cleanup_whitelist": value})


def test_duplicate_selector_keys(tmp_path):
    path = tmp_path / "config.json"
    path.write_text('{"cleanup_blacklist":[{"exact":"a","exact":"b"}]}')
    with pytest.raises(ValueError, match="duplicate"):
        lifecycle.load(str(path), environ={})


def test_matching():
    assert a.matches("a[*]", [{"exact": "a[*]"}])
    assert a.matches("abc", [{"glob": "a*c"}])
    assert not a.matches("Abc", [{"glob": "a*c"}])
    assert not a.selected("a", lifecycle.DEFAULTS)
    assert a.selected("a", {**lifecycle.DEFAULTS, "existing_session_mode": "all-except-protected"})


@pytest.mark.parametrize("change,reason", [("blacklist", "blacklist_protected"), ("hold", "hold_reason_active"),
    ("launch", "managed_launch_owns_closeout"), ("manual", "manual_policy_protected"),
    ("keep", "explicit_keep_open"), ("service", "service_protected"), ("self", "self_identity_unknown_or_protected")])
def test_preservation(tmp_path, change, reason):
    p, config, _ = fixture(tmp_path)
    if change == "blacklist": config["cleanup_blacklist"] = [{"glob": "*"}]
    elif change == "hold": p.meta["hold_reason"] = "old hold"
    elif change == "launch": p.meta["launch_id"] = "partial-contract"
    elif change == "manual": p.meta["cleanup_policy"] = "manual"
    elif change == "keep": p.meta["keep_open"] = "1"
    elif change == "service": p.meta["kind"] = "service"
    else: p.self_ancestor = "1"
    assert a.protection([p], config, adoption=True) == reason


def test_inspector_manual_and_presupervisor_are_adoptable(tmp_path):
    p, config, _ = fixture(tmp_path)
    p.meta.update(managed_by="tmux_inspector", cleanup_policy="manual")
    assert not a.protection([p], config, adoption=True)
    p.meta.update(managed_by="agent_wall", cleanup_policy="kill_on_done")
    assert not a.protection([p], config, adoption=True)


@pytest.mark.parametrize("field", ["server_pid", "server_started", "server_socket", "session", "pid", "created", "pane"])
def test_identity_binds_incarnation(tmp_path, field):
    p, _, _ = fixture(tmp_path)
    old = a.identity(p)
    setattr(p, field, getattr(p, field) + "2")
    assert a.identity(p) != old


def test_dead_retention_plain_plan_does_not_write(tmp_path):
    p, config, args = fixture(tmp_path, dead=True)
    config["completed_retention_seconds"] = 30
    with patch.object(h, "list_panes", return_value=[p]), contextlib.redirect_stdout(io.StringIO()):
        h.cmd_plan(args)
    assert not (tmp_path / "status.json").exists()
    with patch.object(h, "list_panes", return_value=[p]), contextlib.redirect_stdout(io.StringIO()):
        args.write_status = True
        h.cmd_plan(args)
    state = h.load_status(args.status_file)
    record = a.record_for(state, p)
    assert record["dead_observed_at"]
    planned = a.plan(h, [p], args, state, config, h.utc_now() + timedelta(seconds=31))
    assert planned["action"] == "kill"
    assert planned["state"] == "exited_unknown_outcome"
    assert not p.meta["completed_at"]


def test_default_never_enrolls(tmp_path):
    p, _, args = fixture(tmp_path)
    assert a.plan(h, [p], args, {}, lifecycle.DEFAULTS, h.utc_now()) is None


@pytest.mark.parametrize("text,reason", [("Claude Code v2.1.270\n✻ Worked for 1s\n────────\n❯ draft\n────────", "nonempty_or_unknown_prompt"),
    ("Claude Code v2.1.270\n✻ Worked for 1s\napproval required\n────────\n❯\n────────", "active_turn_approval_or_question"),
    ("Claude Code v2.1.270\n✻ Worked for 1s\nesc to interrupt\n────────\n❯\n────────", "active_turn_approval_or_question"),
    ("Claude Code v9.0\n✻ Worked for 1s\n────────\n❯\n────────", "unsupported_runtime_version_or_unknown")])
def test_prompt_vetoes(tmp_path, text, reason):
    p, _, _ = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    with patch.object(a, "runtime", return_value=({"pid": p.pid}, "")), \
         patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, text, "")):
        assert a.observe(h, p, record)[1] == reason


def test_quiet_gap_activity_invalidates_release(tmp_path):
    p, config, args = fixture(tmp_path)
    now = h.utc_now()
    record = enrollment(tmp_path, p)
    record.update(baseline={"raw": "stable"}, last_observed=h.isoformat(now), quiet_since=h.isoformat(now))
    state = {"adoptions": {a.identity(p): record}}
    with patch.object(a, "observe", return_value=({"raw": "stable"}, "")):
        item = a.plan(h, [p], args, state, config, now + timedelta(seconds=2))
        assert item["action"] == "request_exit"
        gap = a.plan(h, [p], args, state, config, now + timedelta(seconds=500))
        assert gap["action"] == "skip"
    with patch.object(a, "observe", return_value=({"raw": "changed"}, "")):
        item = a.plan(h, [p], args, state, config, now + timedelta(seconds=2))
    assert item["adoption_record"]["invalidated"] == "new_activity_invalidated_release"


def test_consumption_survives_other_writers_and_discovery_gap(tmp_path):
    p, _, args = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    record["attempt"] = {"id": "consumed", "archive_path": "/tmp/pre-exit"}
    h.write_status(args.status_file, {"sessions": {}, "adoptions": {a.identity(p): record}})
    for policy in ("smoke", "kill-safe"):
        args.policy = policy
        h.write_status(args.status_file, h.status_payload([], args))
    state = h.load_status(args.status_file)
    assert a.record_for(state, p)["attempt"]["id"] == "consumed"
    assert a.plan(h, [p], args, state, args.config, h.utc_now())["reason"] == "shutdown_unconfirmed"
    p.command = "bash"
    assert a.plan(h, [p], args, state, args.config, h.utc_now())["reason"] == "retained_exited_to_shell"


def test_archive_empty_capture_and_failure(tmp_path):
    p, _, args = fixture(tmp_path, dead=True)
    item = h.result(session=p.session, action="kill", reason="dead", panes=[p])
    with patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, "", "")):
        assert h.archive_cleanup(item, [p], args)["capture_count"] == 1
    with patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 1, "", "failed")):
        with pytest.raises(RuntimeError, match="capture_failed"):
            h.archive_cleanup(item, [p], args)


def test_attempt_persisted_before_send_and_never_replayed(tmp_path):
    p, config, args = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    now = h.utc_now()
    evidence = {"stable": True}
    record.update(baseline=evidence, quiet_since=h.isoformat(now-timedelta(seconds=10)), last_observed=h.isoformat(now))
    h.write_status(args.status_file, {"sessions": {}, "adoptions": {a.identity(p): record}})
    sent = []
    def send(*_):
        assert a.record_for(h.load_status(args.status_file), p)["attempt"]
        sent.append(True)
        raise OSError("ambiguous transport")
    with patch.object(h, "list_panes", return_value=[p]), patch.object(a, "observe", return_value=(evidence, "")), \
         patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, "scrollback", "")), \
         patch.object(h, "guarded_native_exit", side_effect=send), contextlib.redirect_stdout(io.StringIO()):
        h.cmd_apply(args)
        h.cmd_apply(args)
    assert sent == [True]
    saved = a.record_for(h.load_status(args.status_file), p)
    assert saved["attempt"]
    assert (tmp_path / "archive").exists()


def test_blacklist_vetoes_exact_allow(tmp_path):
    p, config, args = fixture(tmp_path, dead=True)
    config["cleanup_blacklist"] = [{"glob": "*"}]
    args.allow_session = [p.session]
    args.override_hold = True
    with patch.object(h, "list_panes", return_value=[p]):
        assert h.build_plan(args)[0]["reason"] == "blacklist_protected"


def test_release_dry_run_and_hold_preservation(tmp_path):
    p, config, args = fixture(tmp_path)
    enrollment(tmp_path, p)
    args.identity = a.identity(p)
    args.result = str(tmp_path / "result.md")
    args.reason = "finished temporary work"
    args.profile = "claude-2.1.270-direct"
    args.dry_run = True
    with patch.object(h, "list_panes", return_value=[p]), patch.object(a, "observe", return_value=({"stable": True}, "")), \
         contextlib.redirect_stdout(io.StringIO()):
        h.cmd_release(args)
        assert not (tmp_path / "status.json").exists()
        p.meta["hold_reason"] = "retain"
        with pytest.raises(ValueError, match="hold_reason_active"):
            h.cmd_release(args)
    assert p.meta["hold_reason"] == "retain"
    assert not p.meta["completed_at"]


@pytest.mark.parametrize("footer,reason", [("1 shell · ↓ to manage", "background_work_visible"), ("? for shortcuts", "")])
def test_current_footer_and_historical_activity(tmp_path, footer, reason):
    p, _, _ = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    text = ("Claude Code v2.1.270\n❯ previous turn\nesc to interrupt (old prose)\n"
            "✻ Worked for 1s · done\n────────\n❯\n────────\n" + footer)
    with patch.object(a, "runtime", return_value=({"pid": p.pid}, "")), \
         patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, text, "")):
        assert a.observe(h, p, record)[1] == reason


def test_failed_capture_and_missing_activity_are_unknown(tmp_path):
    p, _, _ = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    with patch.object(a, "runtime", return_value=({}, "")), \
         patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 1, "", "denied")):
        assert a.observe(h, p, record)[1] == "capture_observation_failed"
        p.window_activity = ""
        assert a.observe(h, p, record)[1] == "activity_observation_unavailable"


def test_corrupt_state_never_reenrolls(tmp_path):
    p, config, args = fixture(tmp_path)
    (tmp_path / "status.json").write_text('{"status_version":1,"adoptions":')
    state = h.load_status(args.status_file)
    assert a.plan(h, [p], args, state, config, h.utc_now())["reason"] == "owner_release_required"


def test_activity_seen_at_final_boundary_invalidates_release(tmp_path):
    p, _, _ = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    item = {"adoption_record": record}
    refused = h.apply_refusal(item, "adoption_eligibility_changed_at_apply")
    assert refused["adoption_record"]["invalidated"]


def test_native_guard_contains_all_receiver_protections(tmp_path):
    p, _, _ = fixture(tmp_path)
    with patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, "", "")) as run:
        h.guarded_native_exit(p, a.PROFILES["claude-2.1.270-direct"])
    args = run.call_args.args
    assert args[:4] == ("if-shell", "-F", "-t", "$1:")
    assert "send-keys -t %1 C-d C-d" in args
    for key in ("socket_path", "pid", "start_time", "pane_pid", "pane_dead", "pane_pipe", "pane_input_off", "session_attached", "pane_in_mode", "synchronize-panes", "remain-on-exit", "@oc_launch_id"):
        assert "#{" + key + "}" in args[4]


def test_result_symlink_and_empty_refused(tmp_path):
    path = tmp_path / "empty"
    path.write_bytes(b"")
    with pytest.raises(ValueError): a.result_bytes(str(path))
    path.write_bytes(b"result")
    link = tmp_path / "link"
    link.symlink_to(path)
    with pytest.raises(OSError): a.result_bytes(str(link))


@pytest.mark.parametrize("trial", range(10))
def test_real_dead_adoption_preserves_sentinel(tmp_path, trial):
    """Real production discovery/archive/dead guard, isolated nondefault socket."""
    import tempfile
    import time
    with tempfile.TemporaryDirectory(prefix="adopt-native-", dir="/tmp") as temporary:
        socket = temporary + "/s"
        def run(*args, check=True):
            return subprocess.run(["tmux", "-u", "-S", socket, *args], capture_output=True,
                                  text=True, check=check, timeout=5)
        try:
            run("new-session", "-d", "-s", "sentinel", "sleep 60")
            target = run("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "temporary", "trap '' HUP; sleep 1; exit 0").stdout.strip()
            run("set-option", "-p", "-t", target, "remain-on-exit", "on")
            # Use an explicit normal-exit fixture, independent of PTY hangup.
            # Missing/signal-only exit metadata must still be retained.
            for _ in range(150):
                observed = run("display-message", "-p", "-t", target, "#{pane_dead}:#{pane_dead_status}").stdout.strip()
                if observed == "1:0":
                    break
                time.sleep(.02)
            assert observed == "1:0", run("display-message", "-p", "-t", target, "#{pane_dead}:#{pane_dead_status}:#{pane_dead_signal}").stdout
            _, _, args = fixture(tmp_path)
            args.config["live_retirement"] = "off"
            with patch.object(h, "run_tmux", side_effect=run), contextlib.redirect_stdout(io.StringIO()) as output:
                h.cmd_apply(args)
            assert len(json.loads(output.getvalue())["killed"]) == 1, output.getvalue()
            assert run("has-session", "-t", "=temporary", check=False).returncode != 0
            assert run("has-session", "-t", "=sentinel", check=False).returncode == 0
            assert list((tmp_path / "archive").glob("*/metadata.json"))
            assert not h.load_status(args.status_file)["adoptions"]
        finally:
            run("kill-server", check=False)


def test_status_lock_serializes_process_writers(tmp_path):
    # Independent Python processes both perform read/modify/write under the
    # same adjacent lock, as release/revoke/cycle entry points do.
    import os
    import sys
    from pathlib import Path
    path = str(tmp_path / "status.json")
    source = '''import sys,time
from helpers.tmux import session_hygiene as h
with h.status_lock(sys.argv[1]):
 state=h.load_status(sys.argv[1])
 state.setdefault("adoptions",{})[sys.argv[2]]={"attempt":{"id":sys.argv[2]}}
 time.sleep(.05)
 h.write_status(sys.argv[1],state)
'''
    processes = [subprocess.Popen([sys.executable, "-c", source, path, name],
                                 cwd=Path(__file__).resolve().parents[2],
                                 env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
                 for name in ("release", "cycle")]
    for process in processes:
        assert process.wait(timeout=10) == 0
    assert set(h.load_status(path)["adoptions"]) == {"release", "cycle"}


def test_observation_capacity_applies_within_one_cycle(tmp_path):
    from dataclasses import replace
    p, config, args = fixture(tmp_path, dead=True)
    config["existing_session_mode"] = "all-except-protected"
    panes = [replace(p, session=f"dead-{i:03}", pane=f"%{i}", server_session_id=f"${i}") for i in range(a.MAX_RECORDS + 1)]
    with patch.object(h, "list_panes", return_value=panes):
        plan = h.build_plan(args)
    assert plan[-1]["reason"] == "adoption_record_capacity_reached"
    assert len(h.status_payload(plan, args)["adoptions"]) == a.MAX_RECORDS


@pytest.mark.parametrize("changed", ["", "shell", "extra_child", "script"])
def test_legacy_profile_runtime_receiver_entry(tmp_path, changed):
    """Exercise the actual matcher/runtime entry, with OS inventory simulated.

    Parent integration supplies real CLI proof; this catches branch reachability
    and identity/tree regressions that mocking observe/runtime would hide.
    """
    from pathlib import Path
    p, _, _ = fixture(tmp_path)
    p.command = "bash"
    script = tmp_path / "run-claude_tui.sh"
    script.write_bytes((Path(__file__).parent / "fixtures/legacy-claude-wrapper.sh").read_bytes())
    record = {"wrapper_path": str(script), "wrapper_sha256": a.legacy_wrapper(str(script))}
    if changed == "script": script.write_text(script.read_text() + "exec bash\n")
    rows = "321 100 bash\n322 321 claude\n"
    if changed == "shell": rows = "321 100 bash\n"
    if changed == "extra_child": rows += "323 322 sleep\n"
    def ps(argv, **kwargs):
        if argv[1] == "-axo": output = rows
        elif argv[-1] == "args=": output = "bash " + str(script)
        else: output = "Sat Sep 12 15:11:32 2026"
        return subprocess.CompletedProcess(argv, 0, output, "")
    def executable(pid):
        return "/bin/bash" if pid == "321" else "/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe"
    with patch.object(a.native_runtimes.ADAPTERS["claude"].subprocess, "run", side_effect=ps), patch.object(a, "process_path", side_effect=executable), \
         patch.object(a.native_runtimes.sys, "platform", "darwin"):
        instance, reason = a.runtime(p, a.PROFILES["claude-2.1.270-legacy-bash"], record)
    if changed:
        assert reason and not instance
    else:
        assert reason == "" and instance["pid"] == "322" and instance["wrapper_sha256"]


@pytest.mark.parametrize("runtime_reason,expected", [("", "shutdown_unconfirmed"),
    ("runtime_child_missing", "retained_exited_to_shell"), ("process_inventory_unavailable", "shutdown_unconfirmed")])
def test_waiting_bash_wrapper_is_not_a_shell_successor(tmp_path, runtime_reason, expected):
    p, config, args = fixture(tmp_path)
    p.command = "bash"
    record = enrollment(tmp_path, p)
    record.update(profile="claude-2.1.270-legacy-bash", attempt={"id": "consumed"})
    with patch.object(a, "runtime", return_value=({}, runtime_reason)):
        item = a.plan(h, [p], args, {"adoptions": {a.identity(p): record}}, config, h.utc_now())
    assert item["action"] == "skip" and item["reason"] == expected


@pytest.mark.parametrize("profile", list(a.PROFILES))
def test_unproven_platform_refused_before_native_observation(tmp_path, profile):
    p, _, _ = fixture(tmp_path)
    with patch.object(a.native_runtimes.sys, "platform", "linux"), patch.object(a.native_runtimes.ADAPTERS["claude"].subprocess, "run") as run:
        assert a.runtime(p, a.PROFILES[profile], {})[1] == "runtime_platform_unproven:linux"
    run.assert_not_called()
