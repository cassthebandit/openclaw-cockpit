import json
import multiprocessing
from pathlib import Path
from unittest.mock import patch

import pytest
from helpers.tmux import event_log


def write_events(directory, writer):
    for index in range(20):
        event_log.append('started', session=f'worker-{writer}', identity=f'{writer}-{index}',
                         config={'log_dir': directory})


def test_concurrent_writers_keep_complete_records(tmp_path):
    workers = [multiprocessing.Process(target=write_events, args=(str(tmp_path), i)) for i in range(4)]
    for worker in workers:
        worker.start()
    for worker in workers:
        worker.join(15)
        assert worker.exitcode == 0
    records = [json.loads(line) for line in (tmp_path/'sessions.jsonl').read_text().splitlines()]
    assert len(records) == 80
    assert len({r['identity'] for r in records}) == 80


def test_trim_keeps_newest_complete_records_and_never_exceeds_cap(tmp_path):
    with patch.object(event_log, 'MAX_BYTES', 900):
        write_events(str(tmp_path), 'trim')
        path = tmp_path/'sessions.jsonl'
        assert path.stat().st_size <= 900
        rows = [json.loads(line) for line in path.read_text().splitlines()]
        assert rows[-1]['identity'] == 'trim-19'
        assert len(rows) < 20
        assert path.read_bytes().endswith(b'\n')


def test_logging_failure_propagates(tmp_path):
    with patch.object(event_log.os, 'fsync', side_effect=OSError('disk full')):
        with pytest.raises(OSError, match='disk full'):
            event_log.append('close_attempt', session='worker', identity='id', config={'log_dir': str(tmp_path)})


def test_hygiene_error_controls_do_not_abort_status_logging(tmp_path):
    from helpers.tmux import session_hygiene as h
    from types import SimpleNamespace
    args = SimpleNamespace(config={'log_dir': str(tmp_path)})
    with patch.object(h, 'effective_config', return_value=args.config):
        h.log_event({'session': 'worker', 'reason': 'error\tfrom\x00tmux\x7f',
                     'pane_identity': 'exact'}, args, 'failure', result='failed\toperation')
    record = json.loads((tmp_path/'sessions.jsonl').read_text())
    assert record['reason'] == 'error from tmux '
    assert record['result'] == 'failed operation'


def test_alternating_janitor_policies_do_not_repeat_unchanged_warnings(tmp_path):
    from helpers.tmux import session_hygiene as h
    from helpers.tests.support import fixture
    p, _, args = fixture(tmp_path)
    def cycle(policy, reason, pid='321', log=True):
        args.policy = policy
        p.pid = pid
        item = h.result(session=p.session, action='skip', reason=reason, panes=[p])
        if log:
            h.record_status_changes([item], args)
        h.write_status(args.status_file, h.status_payload([item], args))
    with patch.object(h, 'log_event') as events:
        cycle('smoke', 'not_smoke', log=False)  # Read-only plan is not a logged discovery.
        for _ in range(4):
            cycle('smoke', 'not_smoke')
            cycle('kill-safe', 'hold_reason_active')
        assert [c.args[2] for c in events.call_args_list] == ['discovered', 'cleanup_state', 'cleanup_state']
        cycle('kill-safe', 'hold_released')
        assert events.call_args.args[2] == 'cleanup_state' and events.call_count == 4
        cycle('kill-safe', 'hold_released', pid='999')
        assert [c.args[2] for c in events.call_args_list[-2:]] == ['discovered', 'cleanup_state']


def test_symlink_refused_and_no_unbounded_record(tmp_path):
    elsewhere = tmp_path/'unrelated'
    elsewhere.write_text('original')
    (tmp_path/'sessions.jsonl').symlink_to(elsewhere)
    with pytest.raises(ValueError, match='symlink'):
        event_log.append('close_attempt', session='worker', identity='id', config={'log_dir': str(tmp_path)})
    assert elsewhere.read_text() == 'original'
    with pytest.raises(ValueError, match='controls'):
        event_log.append('failure', session='worker', identity='id', reason='raw\ntranscript', config={'log_dir': str(tmp_path)})


def test_configured_cap_retains_marker_and_typed_records(tmp_path):
    config = {"log_dir": str(tmp_path), "session_log_max_bytes": 65536}
    for i in range(140):
        event_log.append("start", session="s", identity=str(i), reason="running",
                         details={"runtime": "codex", "task_ref": "x" * 1000}, config=config)
    path = tmp_path / "sessions.jsonl"
    rows = [json.loads(line) for line in path.read_text().splitlines()]
    assert path.stat().st_size <= 65536
    assert rows[-1]["identity"] == "139"
    assert any(r["event"] == "history_trimmed" for r in rows)
    assert all(r["schema_version"] == 1 for r in rows)
    assert len({r["event_id"] for r in rows}) == len(rows)


@pytest.mark.parametrize("value", [True, 0, 65535, 1.5, "10000000", None, 2147483648])
def test_session_cap_rejects_invalid_values(value):
    from helpers.tmux import lifecycle
    with pytest.raises(ValueError, match="session_log_max_bytes"):
        lifecycle.validate({"session_log_max_bytes": value})


def test_typed_details_reject_payloads_and_wrong_types(tmp_path):
    for detail in [{"exit_code": True}, {"prompt": "private"}, {"action_outcome": "probably"}]:
        with pytest.raises(ValueError):
            event_log.append("exit", session="s", identity="id", details=detail,
                             config={"log_dir": str(tmp_path)})
    assert not (tmp_path / "sessions.jsonl").exists()


def test_identity_is_never_silently_truncated(tmp_path):
    with pytest.raises(ValueError, match="size limit"):
        event_log.append("start", session="s", identity="x"*2049, config={"log_dir":str(tmp_path)})


def test_reader_preserves_global_coverage_with_identity_filter(tmp_path, capsys):
    path = tmp_path / "sessions.jsonl"
    path.write_text('{"event":"history_trimmed","schema_version":1}\n{"identity":"a"}\ninvalid\n')
    assert event_log.main([str(path), "--identity", "a"]) == 1
    result = json.loads(capsys.readouterr().out)
    assert result["history_complete"] is False
    assert result["invalid_lines"] == [3]
    assert len(result["events"]) == 1


def write_trimmed_events(directory, writer):
    for index in range(90):
        event_log.append("start", session=f"worker-{writer}", identity=f"{writer}-{index}",
                         config={"log_dir": directory, "session_log_max_bytes": 65536},
                         details={"task_ref": "x" * 1000})


def test_concurrent_trim_keeps_valid_unique_records_and_coverage(tmp_path):
    workers = [multiprocessing.Process(target=write_trimmed_events, args=(str(tmp_path), i)) for i in range(4)]
    for worker in workers:
        worker.start()
    for worker in workers:
        worker.join(20)
        assert worker.exitcode == 0
    path = tmp_path / "sessions.jsonl"
    rows = [json.loads(line) for line in path.read_text().splitlines()]
    assert path.stat().st_size <= 65536
    assert path.read_bytes().endswith(b"\n")
    assert len({row["event_id"] for row in rows}) == len(rows)
    assert any(row["event"] == "history_trimmed" for row in rows)
    assert any(row["identity"].endswith("-89") for row in rows)


def test_incomplete_tail_refuses_append_without_changing_history(tmp_path):
    path = tmp_path / "sessions.jsonl"
    path.write_bytes(b'{"broken":')
    with pytest.raises(ValueError, match="unterminated"):
        event_log.append("start", session="s", identity="i", config={"log_dir": str(tmp_path)})
    assert path.read_bytes() == b'{"broken":'


def producer_fixture(tmp_path):
    from helpers.tmux import session_hygiene as h
    from types import SimpleNamespace
    item = h.result(session="worker", action="kill", reason="managed_kill_on_done", panes=[])
    item["pane_identity"] = "exact"
    args = SimpleNamespace(config={"log_dir": str(tmp_path)})
    return h, item, args


def test_action_result_does_not_leak_into_cleanup_observation(tmp_path):
    h, item, args = producer_fixture(tmp_path)
    with patch.object(h, "effective_config", return_value=args.config), patch.object(h, "ledger_paths", return_value=[]):
        h.write_ledger_event(item, [], args, event="kill_attempt")
        copied = dict(item)
        h.write_ledger_event(item, [], args, event="kill_result", kill_returncode=0)
        h.log_event(copied, args, "cleanup_state", result="removed")
    rows = [json.loads(line) for line in (tmp_path / "sessions.jsonl").read_text().splitlines()]
    assert [r["details"].get("action_outcome") for r in rows] == ["attempted", "succeeded", None]
    assert rows[0]["details"]["action_id"] == rows[1]["details"]["action_id"]
    assert "action_id" not in rows[2]["details"]
    assert "_log_action_id" not in item and "_log_action_id" not in copied


@pytest.mark.parametrize("event,error,expected", [
    ("kill_result", "closure_verification_unavailable", "unknown"),
    ("kill_result", "closure_unconfirmed", "unknown"),
    ("kill_result", "tmux_guard_changed", "failed"),
    ("exit_request_result", "send failed", "unknown"),
    ("exit_request_result", "tmux_guard_changed", "failed"),
])
def test_producer_correlates_actions_and_preserves_uncertainty(tmp_path, event, error, expected):
    h, item, args = producer_fixture(tmp_path)
    attempt = "exit_request_prepared" if event.startswith("exit_request") else "kill_attempt"
    with patch.object(h, "effective_config", return_value=args.config), patch.object(h, "ledger_paths", return_value=[]):
        h.write_ledger_event(item, [], args, event=attempt)
        h.write_ledger_event(item, [], args, event=event, kill_returncode=1, kill_stderr=error)
    rows = [json.loads(line) for line in (tmp_path / "sessions.jsonl").read_text().splitlines()]
    assert rows[0]["details"]["action_id"] == rows[1]["details"]["action_id"]
    assert rows[1]["details"]["action_outcome"] == expected
    assert rows[1]["details"]["error_code"]
    assert rows[1]["details"]["retryable"] is None
    if event.startswith("exit_request"):
        assert rows[1]["component"] == "adoption"
        assert rows[1]["details"]["action"] == "native_exit_request"


def test_observed_failure_has_typed_code_and_blocker(tmp_path):
    h, item, args = producer_fixture(tmp_path)
    item.update(action="refuse", reason="archive_failed:disk unavailable")
    with patch.object(h, "effective_config", return_value=args.config):
        h.log_event(item, args, "cleanup_state", result="refuse")
    row = json.loads((tmp_path / "sessions.jsonl").read_text())
    assert row["details"]["error_code"] == "archive_failed"
    assert row["details"]["blocking_conditions"] == ["archive_failed"]
    assert "action_outcome" not in row["details"]


def test_trim_amortizes_writes_and_retains_exact_newest_suffix(tmp_path):
    config = {"log_dir": str(tmp_path), "session_log_max_bytes": 65536}
    path = tmp_path / "sessions.jsonl"
    trimmed_at = []
    with patch.object(event_log.os, "replace", wraps=event_log.os.replace) as replace:
        for index in range(800):
            before = replace.call_count
            event_log.append("start", session="s", identity=str(index), config=config)
            assert path.stat().st_size <= 65536
            rows = [json.loads(line) for line in path.read_text().splitlines()]
            events = [row for row in rows if row["event"] != "history_trimmed"]
            assert [int(row["identity"]) for row in events] == list(range(index + 1 - len(events), index + 1))
            assert sum(row["event"] == "history_trimmed" for row in rows) == bool(trimmed_at or replace.call_count)
            if replace.call_count != before:
                trimmed_at.append(index)
                assert path.stat().st_size <= 65536 * 3 // 4
    assert len(trimmed_at) >= 3  # Exercise repeated trims, not just the first.
    assert all(right - left >= 40 for left, right in zip(trimmed_at, trimmed_at[1:]))
    assert len(trimmed_at) < 20  # Old behavior trims on every post-cap append.


def test_minimum_cap_has_headroom_for_maximum_record(tmp_path):
    config = {"log_dir": str(tmp_path), "session_log_max_bytes": 65536}
    fields = dict(session="s" * 2048, identity="i" * 2048, reason="r" * 2048,
                  details={"task_ref": ""}, reason_code="large_record", config=config)
    event_log.append("start", **fields)
    path = tmp_path / "sessions.jsonl"
    fields["details"]["task_ref"] = "x" * (event_log.MAX_RECORD_BYTES - path.stat().st_size)
    assert len(fields["details"]["task_ref"]) <= 2048
    with patch.object(event_log.os, "replace", wraps=event_log.os.replace) as replace:
        for _ in range(30):
            before = replace.call_count
            event_log.append("start", **fields)
            assert path.stat().st_size <= 65536
            assert len(path.read_bytes().splitlines(keepends=True)[-1]) == event_log.MAX_RECORD_BYTES
            if replace.call_count != before:
                event_log.append("start", **fields)
                assert replace.call_count == before + 1
                assert path.stat().st_size <= 65536
    assert replace.call_count >= 3


def test_failed_trim_replace_preserves_original_and_removes_temporary(tmp_path):
    config = {"log_dir": str(tmp_path), "session_log_max_bytes": 65536}
    path = tmp_path / "sessions.jsonl"
    for _ in range(40):
        event_log.append("start", session="s", identity="i", details={"task_ref": "x" * 1000}, config=config)
    original = path.read_bytes()
    # Force the trim path without relying on an incidental serialized length.
    with patch.object(event_log, "MAX_BYTES", len(original)), \
         patch.object(event_log.os, "replace", side_effect=OSError("replace failed")):
        with pytest.raises(OSError, match="replace failed"):
            event_log.append("start", session="s", identity="i", config={"log_dir": str(tmp_path)})
    assert path.read_bytes() == original
    assert not list(tmp_path.glob(".sessions-*"))
