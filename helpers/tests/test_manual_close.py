import contextlib
import io
import json
import subprocess
import tempfile
from unittest.mock import patch

import pytest
from helpers.tmux import session_hygiene as h


@pytest.fixture
def server(tmp_path, monkeypatch):
    for name in ("SOCKET_PATH", "STATE_ROOT", "ACTIVE_IDLE_MARK_SECONDS", "TEARDOWN_GRACE_SECONDS", "FAILED_VISIBLE_SECONDS"):
        monkeypatch.setattr(h, name, getattr(h, name))
    directory = tempfile.TemporaryDirectory(prefix='close-', dir='/tmp')
    sock = str(h.Path(directory.name)/'s')
    def run(*args, check=True):
        return subprocess.run(['tmux', '-u', '-S', sock, *args], capture_output=True, text=True, check=check)
    run('new-session', '-d', '-s', 'target', 'sleep 600')
    run('new-session', '-d', '-s', 'sentinel', 'sleep 600')
    cfg = tmp_path/'config.json'
    cfg.write_text(json.dumps({'state_dir': str(tmp_path/'state'), 'log_dir': str(tmp_path/'state/logs'),
                               'status_file': str(tmp_path/'state/status.json')}))
    try:
        yield run, sock, cfg
    finally:
        run('kill-server', check=False)
        directory.cleanup()


def invoke(sock, cfg, *args):
    with contextlib.redirect_stdout(io.StringIO()) as out:
        code = h.main(['--socket-path', sock, '--lifecycle-config', str(cfg), 'kill-session', '--name', 'target', *args])
    return code, json.loads(out.getvalue())


def test_manual_preview_then_exact_close_keeps_sentinel(server):
    run, sock, cfg = server
    code, preview = invoke(sock, cfg)
    assert code == 0 and not preview['removed']
    assert run('has-session', '-t', '=target', check=False).returncode == 0
    # Use the exact resolved status path rather than guessing a state layout.
    resolved, _ = h.lifecycle.load(str(cfg), environ={})
    status = h.Path(resolved['status_file'])
    h.write_status(status, {'adoptions': {preview['identity']: {'attempt': {'id': 'consumed'}}}})
    with patch.object(h, 'status_lock', wraps=h.status_lock) as lock:
        code, closed = invoke(sock, cfg, '--execute', '--identity', preview['identity'])
    lock.assert_called_once()
    assert code == 0 and closed['removed'] and not closed['surviving_processes']
    assert run('has-session', '-t', '=sentinel', check=False).returncode == 0
    assert h.Path(closed['metadata_path']).is_file()
    events = [json.loads(s) for s in (cfg.parent/'state/logs/sessions.jsonl').read_text().splitlines()]
    assert [e['event'] for e in events] == ['manual_close_attempt', 'manual_close_result']
    assert preview['identity'] not in h.load_status(status)['adoptions']


@pytest.mark.parametrize('change', ['hold', 'identity', 'archive_failure', 'log_failure', 'service', 'attach'])
def test_manual_close_preserves_changed_or_unarchivable_target(server, change):
    run, sock, cfg = server
    _, preview = invoke(sock, cfg)
    if change == 'hold':
        run('set-option', '-p', '-t', '=target:', '@oc_hold_reason', 'keep')
    elif change == 'identity':
        run('kill-session', '-t', '=target')
        run('new-session', '-d', '-s', 'target', 'sleep 600')
    elif change == 'service':
        run('set-option', '-p', '-t', '=target:', '@oc_kind', 'service')
    with contextlib.ExitStack() as stack:
        if change == 'archive_failure':
            stack.enter_context(patch.object(h, 'archive_cleanup', side_effect=OSError('disk full')))
        elif change == 'log_failure':
            stack.enter_context(patch.object(h, 'write_ledger_event', side_effect=OSError('disk full')))
        elif change == 'attach':
            original = h.list_panes
            def attached():
                panes = original()
                for pane in panes:
                    if pane.session == 'target': pane.session_attached = '1'
                return panes
            stack.enter_context(patch.object(h, 'list_panes', side_effect=attached))
        with pytest.raises((SystemExit, OSError, ValueError)):
            invoke(sock, cfg, '--execute', '--identity', preview['identity'])
    assert run('has-session', '-t', '=target', check=False).returncode == 0
    assert run('has-session', '-t', '=sentinel', check=False).returncode == 0


def test_late_hold_after_log_prevents_manual_close(server):
    run, sock, cfg = server
    _, preview = invoke(sock, cfg)
    original = h.write_ledger_event
    def late_hold(*args, **kwargs):
        original(*args, **kwargs)
        run('set-option', '-p', '-t', '=target:', '@oc_hold_reason', 'new hold')
    with patch.object(h, 'write_ledger_event', side_effect=late_hold), pytest.raises(SystemExit):
        invoke(sock, cfg, '--execute', '--identity', preview['identity'])
    assert run('has-session', '-t', '=target', check=False).returncode == 0
