"""Synthetic OS boundaries; real two-session matching is separate evidence."""
import hashlib
import json
from pathlib import Path
import subprocess
from unittest.mock import patch

import pytest

from helpers.tmux import assignment_recovery as r, agent_wall as wall
from test_adoption import fixture


def setup(tmp_path, *, receipt=False):
    pane, _, _ = fixture(tmp_path)
    root = tmp_path / "run" / "assignments" / ("a" * 32); root.mkdir(parents=True)
    run_id = "11111111-1111-1111-1111-111111111111"
    pane.meta.update(run_root=str(root.parent.parent), launch_id=run_id)
    pane.command = "bash"
    original = [pane.pane, pane.pid, pane.server_session_id, "@1", run_id, "old hold", "0"]
    launch = {"run_id": run_id, "runtime": "claude", "keep_open": False}
    state = {"generation": 26, "session_id": "native-session", "activity": "PreToolUse",
             "receipt": None, "pending": None, "blocked_reason": None}
    process = {"run_id": run_id, "supervisor_pid": 322, "child_pid": 323, "pane_identity": original}
    result = root.parent.parent / "result.md"; result.write_text("Complete reviewed report")
    if receipt:
        state["consumed_nonce"] = "nonce"
        snapshot = root / "result-nonce.bin"; snapshot.write_bytes(result.read_bytes())
        (root / "outcome.json").write_text(json.dumps({"run_id": run_id, "session_id": state["session_id"],
            "generation": 26, "retained": True, "nonce": "nonce", "outcome": "succeeded",
            "result_path": str(snapshot), "result_sha256": hashlib.sha256(snapshot.read_bytes()).hexdigest()}))
    for name, content in (("launch", launch), ("state", state), ("process", process)):
        (root / (name + ".json")).write_text(json.dumps(content))
    (root / "state.lock").touch()
    helper = tmp_path / "helpers/tmux/assignment.py"; helper.parent.mkdir(parents=True)
    helper.write_text("fake pinned old supervisor")
    python = tmp_path / "python3.14"; python.touch()
    argv = [str(python), str(helper), "supervise", "--run-dir", str(root), "--", "claude"]
    with patch.object(wall, "STATE_DIR", tmp_path / "scripts"):
        wrapper = wall.write_tui_wrapper("recovery", argv, command_kind="claude_tui",
            prompt_file=str(root / "prompt.md"), pane_log=str(tmp_path / "pane.log"),
            debug_file="", launch_record=str(tmp_path / "launch.json"),
            exit_label="claude TUI", launch_id=run_id)
    rows = {pane.pid: ("100", "bash"), "322": (pane.pid, str(python)), "323": ("322", "claude")}
    args = {pane.pid: "bash " + str(wrapper), "322": " ".join(argv), "323": "claude"}
    births = {key: "Sat Sep 12 12:00:00 2026" for key in rows}
    paths = {pane.pid: "/bin/bash", "322": str(python), "323": "/opt/npm/@anthropic-ai/claude-code/bin/claude.exe"}
    def run(argv, **kwargs):
        if argv[0] == "tmux": output = "|".join(original[:5])
        elif argv[1] == "-axo": output = "\n".join(f"{pid} {parent} {command}" for pid, (parent, command) in rows.items())
        elif argv[-1] == "args=": output = args[argv[2]]
        else: output = births[argv[2]]
        return subprocess.CompletedProcess(argv, 0, output, "")
    return pane, root, wrapper, result, rows, args, paths, births, run, helper


@pytest.fixture
def environment(tmp_path):
    items = setup(tmp_path)
    with patch.object(r.subprocess, "run", side_effect=items[8]), \
         patch.object(r, "SUPERVISOR_SHA256", hashlib.sha256(items[9].read_bytes()).hexdigest()), \
         patch.object(r._adoption().native_runtimes.sys, "platform", "darwin"), \
         patch.object(r.claude, "code_directory_hash", return_value=r.NATIVE_PROFILES["claude"]["cdhash"]):
        yield items


def enrolled(items):
    pane, root, wrapper, result, _, _, paths, *_ = items
    return r.enroll(pane, root, wrapper, result, owner_reviewed_result=True,
                    allow_missing_receipt=True, process_path=lambda pid: paths[pid])


def test_explicit_authority_and_no_invented_receipt(environment):
    pane, root, wrapper, result, _, _, paths, *_ = environment
    with pytest.raises(ValueError, match="reviewed_result"):
        r.enroll(pane, root, wrapper, result)
    with pytest.raises(ValueError, match="missing_receipt"):
        r.enroll(pane, root, wrapper, result, owner_reviewed_result=True)
    record = enrolled(environment)
    assert record["snapshot"]["classification"] == "missing_native_receipt"
    assert not (root / "outcome.json").exists()
    assert r.profile(record)["proof_pending"] is False
    observed, reason = r.observe(pane, record, process_path=lambda pid: paths[pid])
    assert not reason and observed["runtime"]["native"]["pid"] == "323"


@pytest.mark.parametrize("change", ["generation", "session", "pending", "receipt", "blocked", "activity", "launch",
    "result", "wrapper", "helper", "supervisor", "child", "extra", "birth", "pane", "lock"])
def test_each_bound_change_refuses(environment, change):
    pane, root, wrapper, result, rows, args, paths, births, _, helper = environment
    record = enrolled(environment)
    if change in {"generation", "session", "pending", "receipt", "blocked", "activity"}:
        state = json.loads((root / "state.json").read_text())
        key, value = {"generation": ("generation", 27), "session": ("session_id", "replacement"),
            "pending": ("pending", {"new": True}), "receipt": ("receipt", {"new": True}),
            "blocked": ("blocked_reason", "approval"), "activity": ("activity", "PermissionRequest")}[change]
        state[key] = value; (root / "state.json").write_text(json.dumps(state))
    elif change == "launch": pane.meta["launch_id"] = "replacement"
    elif change == "result": result.write_text("changed")
    elif change == "wrapper": wrapper.write_text(wrapper.read_text() + "exec bash\n")
    elif change == "helper": helper.write_text("changed helper")
    elif change == "supervisor": args["322"] = args["322"].replace("supervise", "different")
    elif change == "child": rows["323"] = ("999", "claude")
    elif change == "extra": rows["324"] = ("322", "sleep")
    elif change == "birth": births["322"] = "new process birth"
    elif change == "pane": pane.created = "999999999"
    elif change == "lock": (root / "state.lock").rename(root / "old.lock"); (root / "state.lock").touch()
    observed, reason = r.observe(pane, record, process_path=lambda pid: paths[pid])
    assert not observed and reason


def test_final_guard_uses_original_lock_without_writing(environment):
    pane, root, _, _, _, _, paths, *_ = environment
    record = enrolled(environment)
    before = (root / "state.json").read_bytes()
    with r.final_guard(record):
        assert not r.observe(pane, record, process_path=lambda pid: paths[pid])[1]
        with pytest.raises(BlockingIOError):
            with r.final_guard(record):
                pytest.fail("another fd must not acquire the already-held lock")
    assert (root / "state.json").read_bytes() == before
    (root / "state.lock").rename(root / "old.lock")
    (root / "state.lock").symlink_to(root / "old.lock")
    with pytest.raises(OSError):
        with r.final_guard(record):
            pytest.fail("symlink lock accepted")


def test_retained_real_receipt_requires_matching_report(tmp_path):
    items = setup(tmp_path, receipt=True)
    pane, root, wrapper, result, _, _, paths, _, run, helper = items
    with patch.object(r.subprocess, "run", side_effect=run), \
         patch.object(r, "SUPERVISOR_SHA256", hashlib.sha256(helper.read_bytes()).hexdigest()), \
         patch.object(r._adoption().native_runtimes.sys, "platform", "darwin"), \
         patch.object(r.claude, "code_directory_hash", return_value=r.NATIVE_PROFILES["claude"]["cdhash"]):
        record = r.enroll(pane, root, wrapper, result, owner_reviewed_result=True, process_path=lambda pid: paths[pid])
        assert record["snapshot"]["classification"] == "retained_native_completion"
        result.write_text("different report")
        assert r.observe(pane, record, process_path=lambda pid: paths[pid])[1]


def test_missing_cli_flags_are_validation_errors(environment):
    from argparse import Namespace
    with pytest.raises(ValueError, match='fresh_enrollment_pane'):
        r.enrollment(Namespace(), None)
    for missing in ('assignment_dir', 'wrapper', 'result'):
        values = dict(_enrollment_pane=environment[0], assignment_dir=str(environment[1]),
                      wrapper=str(environment[2]), result=str(environment[3]))
        values.pop(missing)
        with pytest.raises(ValueError, match='requires_assignment_dir_wrapper_and_result'):
            r.enrollment(Namespace(**values), None)


def test_recovery_dead_plan_requires_original_binding_and_attempt(environment, tmp_path):
    from helpers.tmux import adoption as a, session_hygiene as h
    pane = environment[0]
    recovery = enrolled(environment)
    _, config, args = fixture(tmp_path)
    record = {'profile': r.PROFILE_NAMES['claude'], 'assignment_recovery': recovery}
    state = {'adoptions': {a.identity(pane): record}}
    pane.dead = True; pane.dead_status = '0'
    item = a.plan(h, [pane], args, state, config, h.utc_now())
    assert item['action'] == 'skip'
    record['attempt'] = {'archive_path': str(tmp_path / 'saved')}
    with patch.object(h, 'dead_retirement_refusal', return_value=''):
        assert a.plan(h, [pane], args, state, config, h.utc_now())['action'] == 'kill'
    pane.meta['launch_id'] = 'replacement'
    assert a.plan(h, [pane], args, state, config, h.utc_now()) is None


def release_args(items, tmp_path, *flags):
    from helpers.tmux import adoption as a, session_hygiene as h
    pane, root, wrapper, result, *_ = items
    _, config, _ = fixture(tmp_path)
    args = h.build_parser().parse_args(['release-existing', '--identity', a.identity(pane),
        '--result', str(result), '--reason', 'reviewed old report', '--profile', r.PROFILE_NAMES['claude'],
        '--wrapper', str(wrapper), '--assignment-dir', str(root), '--owner-reviewed-result',
        '--allow-missing-receipt', '--attest-no-background-work', *flags])
    args.config = config
    args.status_file = str(tmp_path / 'release-status.json')
    args.archive_root = str(tmp_path / 'release-archive')
    return args


@pytest.fixture
def release_environment(environment):
    from helpers.tmux import adoption as a, session_hygiene as h
    raw = 'Claude Code v2.1.269\n✻ Worked for 1s\n────────\n❯\n────────\n? for shortcuts\n'
    with patch.object(a, 'process_path', side_effect=lambda pid: environment[6][pid]), \
         patch.object(h, 'list_panes', return_value=[environment[0]]), \
         patch.object(h, 'run_tmux', return_value=subprocess.CompletedProcess([], 0, raw, '')):
        yield environment


@pytest.mark.parametrize('dry', [False, True])
def test_full_release_recovery_prepares_original_baseline(release_environment, tmp_path, dry):
    from helpers.tmux import adoption as a, session_hygiene as h
    args = release_args(release_environment, tmp_path, '--prepare', *(['--dry-run'] if dry else []))
    files_before = {str(p): p.read_bytes() for p in tmp_path.rglob('*') if p.is_file()}
    archive = tmp_path / 'mock-archive'; archive.mkdir()
    with patch.object(h, 'archive_cleanup', return_value={'archive_path': str(archive)}) as save, \
         patch.object(h, 'write_ledger_event') as ledger, patch.object(h, 'log_event') as log:
        assert h.cmd_release(args) == 0
    if dry:
        save.assert_not_called(); ledger.assert_not_called(); log.assert_not_called()
        assert {str(p): p.read_bytes() for p in tmp_path.rglob('*') if p.is_file()} == files_before
    else:
        assert save.call_count == 1
        record = h.load_status(args.status_file)['adoptions'][args.identity]
        nested = record['assignment_recovery']
        assert nested['snapshot']['pane_identity'] == a.identity(release_environment[0])
        assert record['baseline']['runtime']['runtime'] == nested['baseline_runtime']
        assert record['baseline']['runtime']['assignment'] == nested['snapshot']
        assert record['profile'] == r.PROFILE_NAMES['claude']


@pytest.mark.parametrize('change,reason', [('missing', 'requires_assignment_dir'),
    ('authority', 'requires_reviewed_result'), ('mismatch', 'profile_runtime_mismatch'),
    ('legacy', 'managed_launch_owns_closeout')])
def test_full_release_recovery_rejects_wrong_authority(release_environment, tmp_path, change, reason):
    from helpers.tmux import session_hygiene as h
    args = release_args(release_environment, tmp_path, '--dry-run')
    if change == 'missing': del args.assignment_dir
    elif change == 'authority': del args.owner_reviewed_result
    elif change == 'mismatch': args.profile = r.PROFILE_NAMES['codex']
    else:
        args.profile = 'claude-2.1.270-direct'
        del args.wrapper
    with patch.object(h, 'write_status') as write, patch.object(h, 'log_event') as log:
        with pytest.raises(ValueError, match=reason): h.cmd_release(args)
    write.assert_not_called(); log.assert_not_called()


def test_request_exit_reuses_original_lock_only_during_final_observe_and_eof(release_environment, tmp_path):
    from helpers.tmux import adoption as a, session_hygiene as h
    args = release_args(release_environment, tmp_path)
    with patch.object(h, 'log_event'):
        assert h.cmd_release(args) == 0
    record = h.load_status(args.status_file)['adoptions'][args.identity]
    pane = release_environment[0]
    archive = tmp_path / 'request-archive'; archive.mkdir()
    item = h.result(session=pane.session, action='request_exit', reason='observed', panes=[pane], policy_source='adopted_observed')
    item.update(adoption_identity=args.identity, adoption_record=record)
    events = []
    observed_lock = []
    original_observe = a.observe
    def observe(*values, **kwargs):
        try:
            with r.final_guard(record['assignment_recovery']): pass
        except BlockingIOError:
            observed_lock.append(True)
        else:
            observed_lock.append(False)
        return original_observe(*values, **kwargs)
    def check_unlocked(*unused, **kwargs):
        with r.final_guard(record['assignment_recovery']): pass
        events.append(kwargs['event'])
    def eof(target, profile):
        with pytest.raises(BlockingIOError):
            with r.final_guard(record['assignment_recovery']): pytest.fail('lock not held at EOF')
        saved = h.load_status(args.status_file)['adoptions'][args.identity]
        assert saved['attempt']['archive_path'] == str(archive)
        assert (archive / 'released-result.bin').read_bytes() == release_environment[3].read_bytes()
        events.append('eof')
        return subprocess.CompletedProcess([], 0, '', '')
    with patch.object(h, 'revalidate_target', return_value=([pane], 'ok')), \
         patch.object(h, 'archive_cleanup', return_value={'archive_path': str(archive)}), \
         patch.object(h, 'write_ledger_event', side_effect=check_unlocked), \
         patch.object(a, 'observe', side_effect=observe), \
         patch('time.sleep', side_effect=AssertionError('must not wait in native exit request')), \
         patch.object(h, 'guarded_native_exit', side_effect=eof):
        applied = h.request_exit(item, args)
    assert applied['reason'] == 'native_exit_requested'
    assert events == ['exit_request_prepared', 'eof', 'exit_request_result']
    assert observed_lock == [False, True]
    with r.final_guard(record['assignment_recovery']): pass


@pytest.mark.parametrize('refusal', ['busy_lock', 'tmux_guard'])
def test_known_no_send_does_not_burn_an_exit_attempt(release_environment, tmp_path, refusal):
    import contextlib
    from helpers.tmux import session_hygiene as h
    args = release_args(release_environment, tmp_path)
    with patch.object(h, 'log_event'):
        assert h.cmd_release(args) == 0
    record = h.load_status(args.status_file)['adoptions'][args.identity]
    pane = release_environment[0]
    archive = tmp_path / 'request-archive'; archive.mkdir()
    item = h.result(session=pane.session, action='request_exit', reason='observed', panes=[pane], policy_source='adopted_observed')
    item.update(adoption_identity=args.identity, adoption_record=record)
    guard = r.final_guard(record['assignment_recovery']) if refusal == 'busy_lock' else contextlib.nullcontext()
    with guard, patch.object(h, 'revalidate_target', return_value=([pane], 'ok')), \
         patch.object(h, 'archive_cleanup', return_value={'archive_path': str(archive)}), \
         patch.object(h, 'write_ledger_event'), \
         patch.object(h, 'guarded_native_exit', return_value=subprocess.CompletedProcess([], 1, '', 'tmux_guard_changed')) as send:
        applied = h.request_exit(item, args)
    saved = h.load_status(args.status_file)['adoptions'][args.identity]
    assert not saved.get('attempt')
    if refusal == 'busy_lock':
        send.assert_not_called()
        assert applied['reason'] == 'recovery_lock_busy'
        assert not applied['adoption_record'].get('invalidated')
        assert not saved.get('invalidated')
    else:
        send.assert_called_once()
        assert applied['reason'] == saved['invalidated'] == 'tmux_guard_changed'
        assert saved['previous_attempt']['archive_path'] == str(archive)
