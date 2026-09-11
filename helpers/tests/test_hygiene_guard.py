"""PR4 action-boundary falsifiers; all real tmux calls use a disposable socket."""
import argparse
import contextlib
import io
import json
import subprocess
import tempfile
import time
from pathlib import Path
from unittest.mock import patch

import pytest
from helpers.tmux import session_hygiene as h
from test_hygiene import managed


def test_same_second_completion_is_ambiguous(tmp_path):
    p = managed('probe', state='done', completed_at='2026-09-10T10:00:00Z')
    p.process_started = '2026-09-10T10:00:00Z'
    with patch.object(h, 'capture_pane_text', return_value='plain old output'):
        result = h.eligible_managed('probe', [p], policy='kill-safe', grace=0, now=h.utc_now())
    assert result['action'] != 'kill'
    assert result['reason'] == 'completion_predates_process'


@pytest.mark.parametrize('change', ['pid', 'hold', 'policy', 'live'])
def test_final_ledger_mutation_cannot_reach_tmux(tmp_path, change):
    p = managed('probe', kind='agent', state='done', cleanup_policy='kill_on_done',
                completed_at='2026-09-10T10:00:00Z', run_root=str(tmp_path), evidence_path='result.md')
    p.dead = True
    (tmp_path/'result.md').write_text('done')
    item = h.result(session='probe', action='kill', reason='test', panes=[p], tmux_target='=probe')
    def ledger(*a, **kw):
        if change == 'pid': p.pid = '999'
        elif change == 'hold': p.meta['hold_reason'] = 'new hold'
        elif change == 'policy': p.meta['cleanup_policy'] = 'manual'
        else: p.dead = False
    args = argparse.Namespace(policy='kill-safe', grace=0, allow_session=[], override_hold=False,
        max_kills=1, json=True, archive_root=str(tmp_path), status_file='')
    with patch.object(h, 'build_plan', return_value=[item]), patch.object(h, 'list_panes', return_value=[p]), \
         patch.object(h, 'archive_cleanup', return_value={}), patch.object(h, 'write_ledger_event', side_effect=ledger), \
         patch.object(h, 'capture_pane_text', return_value='active work'), patch.object(h, 'run_tmux') as run, \
         contextlib.redirect_stdout(io.StringIO()) as output:
        h.cmd_apply(args)
    assert not json.loads(output.getvalue())['killed']
    run.assert_not_called()


@pytest.mark.parametrize('change', ['', 'respawn', 'hold', 'extra_window', 'policy', 'link_after', 'rename', 'recreate'])
def test_real_guard(change):
    with tempfile.TemporaryDirectory(prefix='p4-', dir='/tmp') as tmp:
        sock = str(Path(tmp)/'s')
        def run(*args, check=True):
            return subprocess.run(['tmux', '-u', '-S', sock, *args], capture_output=True, text=True, check=check)
        try:
            pane_id = run('new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'probe', 'sleep 600').stdout.strip()
            run('set-option', '-w', '-t', pane_id, 'remain-on-exit', 'on')
            run('respawn-pane', '-k', '-t', pane_id, 'exit 0')
            for _ in range(50):
                if run('display-message','-p','-t',pane_id,'#{pane_dead}').stdout.strip() == '1': break
                time.sleep(.02)
            with patch.object(h, 'run_tmux', side_effect=run):
                p = h.list_panes()[0]
                # Another current session must not steal the format context.
                run('new-session', '-d', '-s', 'unrelated', 'sleep 600')
                if change == 'respawn': run('respawn-pane', '-t', pane_id, 'sleep 600')
                elif change == 'hold': run('set-option','-p','-t',pane_id,'@oc_hold_reason','new hold')
                elif change == 'extra_window': run('new-window','-d','-t','probe','sleep 600')
                elif change == 'link_after': run('new-session', '-d', '-s', 'linked', '-t', 'probe')
                elif change == 'rename': run('rename-session', '-t', '=probe', 'renamed')
                elif change == 'recreate':
                    # Keep the server alive so a new same-name session gets
                    # a distinct immutable ID on this server.
                    run('new-session', '-d', '-s', 'keeper', 'sleep 600')
                    run('kill-session', '-t', '=probe')
                    run('new-session', '-d', '-s', 'probe', 'sleep 600')
                elif change == 'policy': run('set-option','-p','-t',pane_id,'@oc_cleanup_policy','manual')
                result = h.guarded_dead_retirement(p)
            assert run('has-session', '-t', '=unrelated', check=False).returncode == 0
            exists = run('has-session', '-t', '=renamed' if change == 'rename' else '=probe', check=False).returncode == 0
            if change == 'link_after':
                assert run('has-session', '-t', '=linked', check=False).returncode == 0
            assert exists == bool(change), (change, result, exists)
            assert (result.returncode == 0) == (not change)
        finally:
            run('kill-server', check=False)


def test_existing_linked_sessions_are_both_retained():
    with tempfile.TemporaryDirectory(prefix='linked-', dir='/tmp') as tmp:
        def run(*args, check=True):
            return subprocess.run(['tmux','-u','-S',tmp+'/s',*args],capture_output=True,text=True,check=check)
        try:
            run('new-session','-d','-s','original','sleep 600')
            run('set-option','-w','-t','original','remain-on-exit','on')
            run('respawn-pane','-k','-t','original','exit 0')
            run('new-session','-d','-s','linked','-t','original')
            for _ in range(50):
                if run('display-message','-p','-t','original','#{pane_dead}').stdout.strip()=='1':break
                time.sleep(.02)
            with patch.object(h,'run_tmux',side_effect=run):
                panes=h.list_panes()
                assert len(panes)==2
                assert panes[0].server_session_id != panes[1].server_session_id
                for pane in panes:
                    assert h.guarded_dead_retirement(pane).returncode != 0
            assert run('has-session','-t','=original',check=False).returncode == 0
            assert run('has-session','-t','=linked',check=False).returncode == 0
        finally:run('kill-server',check=False)


def test_comma_metadata_never_archives_on_repeated_apply(tmp_path):
    p=managed('probe',kind='agent',state='done',cleanup_policy='kill_on_done',goal='a, b',
              completed_at='2026-09-10T10:00:00Z',run_root=str(tmp_path),evidence_path='result.md')
    p.dead=True
    (tmp_path/'result.md').write_text('done')
    args=argparse.Namespace(policy='kill-safe',grace=0,allow_session=[],override_hold=False,
        max_kills=1,json=True,archive_root=str(tmp_path),status_file='')
    with patch.object(h,'list_panes',return_value=[p]), patch.object(h,'archive_cleanup') as archive, \
         patch.object(h,'write_ledger_event') as ledger, contextlib.redirect_stdout(io.StringIO()) as output:
        h.cmd_apply(args)
        h.cmd_apply(args)
    archive.assert_not_called()
    ledger.assert_not_called()
    assert output.getvalue().count('unsafe_guard_literal') >= 2
