"""Shared submission readiness, including actual private-socket input capture."""
import argparse
import contextlib
import fcntl
import io
import json
import shlex
import sys
import time
from unittest.mock import patch

import pytest
from helpers.tmux import assignment as a, agent_wall as wall
from helpers.tests.support import disposable_tmux


def prepare(tmp_path, runtime):
    root = tmp_path / 'assignment'
    launch = a.prepare(runtime, root, [runtime], bootstrap=runtime == 'codex')
    (root / 'prompt.md').write_text('ONLY_READY_INPUT')
    return root, launch


def bind(root, launch, *, complete=True):
    a.hook(root, dict(hook_event_name='SessionStart', session_id='native-session'))
    if launch.get('bootstrap_marker') and complete:
        a.hook(root, dict(hook_event_name='Stop', session_id='native-session', turn_id='bootstrap',
                          last_assistant_message=launch['bootstrap_marker']))


@pytest.mark.parametrize('state', ['missing', 'unbound', 'codex_bootstrap', 'malformed', 'nonobject', 'invalid_session'])
def test_unready_shared_boundary_never_dispatches_or_records(tmp_path, state):
    root, launch = prepare(tmp_path, 'codex' if state == 'codex_bootstrap' else 'claude')
    if state == 'missing': (root / 'state.json').unlink()
    elif state == 'codex_bootstrap': bind(root, launch, complete=False)
    elif state == 'malformed': (root / 'state.json').write_text('{')
    elif state == 'nonobject': (root / 'state.json').write_text('[]')
    elif state == 'invalid_session': a._write(root / 'state.json', {'session_id': True})
    assert not a.is_ready(root)
    with patch.object(wall, '_inject_assignment') as inject:
        with pytest.raises(SystemExit, match='not ready.*not submitted'):
            wall.inject_assignment('%1', root)
        inject.assert_not_called()
    assert not (root / 'submission.json').exists()


@pytest.mark.parametrize('runtime', ['claude', 'codex'])
def test_shared_boundary_reads_and_dispatches_under_one_state_lock(tmp_path, runtime):
    root, launch = prepare(tmp_path, runtime)
    bind(root, launch)
    assert a.is_ready(root)
    def dispatch(pane, actual_root):
        assert actual_root == root
        with (root / 'state.lock').open('a') as other:
            with pytest.raises(BlockingIOError):
                fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
    with patch.object(wall, '_inject_assignment', side_effect=dispatch) as inject:
        wall.inject_assignment('%1', root)
        with pytest.raises(SystemExit, match='already submitted'):
            wall.inject_assignment('%1', root)
        assert inject.call_count == 1
    assert a._read(root / 'submission.json')['pane'] == '%1'


@pytest.mark.parametrize('runtime', ['claude', 'codex'])
def test_real_rejected_submission_delivers_zero_bytes_then_ready_works_once(tmp_path, runtime):
    root, launch = prepare(tmp_path, runtime)
    if runtime == 'codex': bind(root, launch, complete=False)
    received, ready = tmp_path / 'received', tmp_path / 'ready'
    # Record every raw PTY input byte, including Enter. No runtime/network used.
    recorder = tmp_path / 'recorder.py'
    recorder.write_text('''import os,sys,tty
from pathlib import Path
tty.setraw(0)
with open(sys.argv[1], 'ab', buffering=0) as out:
 Path(sys.argv[2]).touch()
 while True:
  data=os.read(0,4096)
  if not data: break
  out.write(data)
''')
    with disposable_tmux() as tmux:
        pane = tmux.run('new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'receiver',
                        shlex.join([sys.executable, str(recorder), str(received), str(ready)])).stdout.strip()
        for _ in range(100):
            if ready.exists(): break
            time.sleep(.02)
        assert ready.exists()
        tmux.run('set-option', '-p', '-t', pane, '@oc_launch_id', launch['run_id'])
        identity = tmux.run('display-message', '-p', '-t', pane,
                           '#{pane_id}|#{pane_pid}|#{session_id}|#{window_id}|#{@oc_launch_id}||#{pane_dead}').stdout.strip().split('|')
        a._write(root / 'process.json', {'run_id': launch['run_id'], 'pane_identity': identity})
        args = argparse.Namespace(pane=pane, assignment_run=str(root))
        with patch.object(wall, 'run_tmux', side_effect=tmux.run), contextlib.redirect_stdout(io.StringIO()):
            with pytest.raises(SystemExit, match='not ready'):
                wall.cmd_submit_assignment(args)
            time.sleep(.1)
            assert received.read_bytes() == b''
            assert not (root / 'submission.json').exists()
            bind(root, launch)
            assert wall.cmd_submit_assignment(args) == 0
            for _ in range(100):
                if received.read_bytes().endswith(b'\r'): break
                time.sleep(.02)
            assert received.read_bytes() == b'ONLY_READY_INPUT\r'
            with pytest.raises(SystemExit, match='already submitted'):
                wall.cmd_submit_assignment(args)
            time.sleep(.05)
            assert received.read_bytes() == b'ONLY_READY_INPUT\r'
