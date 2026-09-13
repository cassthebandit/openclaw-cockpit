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
    from test_adoption import fixture
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
