"""PR2 regressions: no ambient tmux; apply tests use synthetic snapshots."""
import argparse
import contextlib
import io
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import patch

import pytest
from helpers.tmux import session_hygiene as h
from helpers.tests.support import disposable_tmux, managed


@pytest.fixture
def worker(tmp_path):
    now = datetime.now(timezone.utc)
    (tmp_path / 'result.md').write_text('done')
    p = managed('worker', kind='agent', cleanup_policy='kill_on_done', state='done',
                completed_at=h.isoformat(now - timedelta(minutes=20)), run_root=str(tmp_path),
                evidence_path='result.md')
    p.pid = '123'
    return p


@pytest.mark.parametrize('kind', ['agent', 'smoke', 'detected-agent'])
@pytest.mark.parametrize('text', ['', 'Thinking… esc to interrupt',
    'Worked for 2m\n› approve plan\n1. approve\n2. reject'])
def test_all_automatic_paths_veto_uncertain_or_active(worker, kind, text):
    worker.meta['kind'] = kind
    if kind == 'smoke':
        worker.session = 'oc-vis-smoke-probe'
        worker.meta['cleanup_policy'] = 'smoke'
    if kind == 'detected-agent':
        worker.meta.update(managed_by='tmux_inspector', contract_version='display-only', agent='codex')
    with patch.object(h, 'capture_pane_text', return_value=text):
        item = h.eligible_managed(worker.session, [worker], policy='kill-safe', grace=0, now=h.utc_now())
    assert item['action'] not in ('kill', 'mark')
    assert not h.managed_tui_completion_screen(text)


def test_expired_hold_keeps_approval(worker):
    worker.meta.update(hold_reason='parent review', hold_until=h.isoformat(h.utc_now()-timedelta(hours=1)))
    with patch.object(h, 'capture_pane_text', return_value='Worked for 2m\n› approve plan\n1. approve\n2. reject'):
        assert not h.hold_is_active(worker, h.utc_now())
        item = h.eligible_managed(worker.session, [worker], policy='kill-safe', grace=0, now=h.utc_now())
        assert item['action'] not in ('kill', 'mark')
        assert item['reason'] == 'operator_prompt_active'


def test_quiet_running_never_becomes_completion(worker):
    worker.meta['state'] = 'running'
    worker.meta['completed_at'] = ''
    text = 'quiet partial progress'
    status = {'sessions': {'worker': {'pane_pid': worker.pid, 'tail_hash': h.normalized_tail_hash(text),
              'tail_hash_since': h.isoformat(h.utc_now()-timedelta(days=2))}}}
    with patch.object(h, 'capture_pane_text', return_value=text):
        for marked in [False, True]:
            if marked:
                worker.meta['teardown_marked_at'] = h.isoformat(h.utc_now()-timedelta(hours=1))
            item = h.eligible_managed('worker', [worker], policy='kill-safe', grace=0, now=h.utc_now(), status_state=status)
            assert item['action'] not in ('mark', 'kill')


def test_respawn_before_plan_rejects_old_completion(worker):
    worker.process_started = h.isoformat(h.utc_now()-timedelta(seconds=2))
    with patch.object(h, 'capture_pane_text', return_value='new task output'):
        item = h.eligible_managed('worker', [worker], policy='kill-safe', grace=0, now=h.utc_now())
    assert item['reason'] == 'completion_predates_process'


@pytest.mark.parametrize('change', ['pid', 'policy', 'kind', 'active', 'capture_failure'])
@pytest.mark.parametrize('during_archive', [False, True])
def test_apply_rechecks_before_and_after_archive(worker, tmp_path, change, during_archive):
    calls = []
    text = ['completed worker output']
    count = [0]
    def mutate():
        if change == 'pid': worker.pid = '456'
        elif change == 'policy': worker.meta['cleanup_policy'] = 'manual'
        elif change == 'kind': worker.meta['kind'] = 'service'
        elif change == 'active': text[0] = 'Thinking… esc to interrupt'
        else: text[0] = ''
    def panes():
        count[0] += 1
        if count[0] == 2 and not during_archive: mutate()
        return [worker]
    def archive(*args):
        if during_archive: mutate()
        return {}
    args = argparse.Namespace(policy='kill-safe', grace=0, allow_session=[], override_hold=False,
            max_kills=1, json=True, archive_root=str(tmp_path/'archive'), status_file='')
    with patch.object(h, 'list_panes', side_effect=panes), patch.object(h, 'capture_pane_text', side_effect=lambda *a, **k:text[0]), \
         patch.object(h, 'archive_cleanup', side_effect=archive), patch.object(h, 'write_ledger_event'), \
         patch.object(h, 'run_tmux', side_effect=lambda *a, **k:calls.append(a)), contextlib.redirect_stdout(io.StringIO()) as output:
        h.cmd_apply(args)
    result = json.loads(output.getvalue())
    assert not result['killed']
    assert result['refused']
    assert not calls


def test_normal_dead_completed_cleanup_archives_first(worker, tmp_path):
    worker.dead = True
    worker.dead_status = '0'
    events = []
    args = argparse.Namespace(policy='kill-safe', grace=0, allow_session=[], override_hold=False,
        max_kills=1, json=True, archive_root=str(tmp_path/'archive'), status_file='')
    import subprocess
    def run(*a, **k):
        events.append(a[0]); return subprocess.CompletedProcess(a, 0, '', '')
    with patch.object(h, 'list_panes', return_value=[worker]), \
         patch.object(h, 'archive_cleanup', side_effect=lambda *a:events.append('archive') or {}), \
         patch.object(h, 'write_ledger_event'), patch.object(h, 'run_tmux', side_effect=run), contextlib.redirect_stdout(io.StringIO()):
        h.cmd_apply(args)
    assert events == ['archive', 'if-shell']

@pytest.mark.parametrize("kind,state", [("smoke", "done"), ("agent", "failed"), ("agent", "blocked")])
def test_fallback_terminal_metadata_predates_respawn(worker, kind, state):
    worker.process_started = h.isoformat(h.utc_now()-timedelta(seconds=2))
    worker.meta.update(kind=kind, state=state, completed_at="", updated_at=h.isoformat(h.utc_now()-timedelta(hours=1)))
    if kind == "smoke":
        worker.meta["cleanup_policy"] = "smoke"
        worker.session = "oc-vis-smoke-probe"
    with patch.object(h, "capture_pane_text", return_value="new task output"):
        item = h.eligible_managed(worker.session, [worker], policy="kill-safe", grace=0, now=h.utc_now())
    assert item["action"] == "refuse"


def test_status_drops_predecessor_pid_baseline(worker):
    prior = {"sessions": {"worker": {"pane_id": worker.pane, "pane_created": worker.created,
             "pane_pid": "old", "tail_hash": "old", "tail_hash_since": "old"}}}
    item = h.result(session="worker", action="skip", reason="active", panes=[worker])
    with patch.object(h, "load_status", return_value=prior):
        row = h.status_payload([item], argparse.Namespace(status_file=""))["sessions"]["worker"]
    assert row["tail_hash"] == ""
    assert row["pane_pid"] == worker.pid


def test_real_tmux_respawn_changes_incarnation(tmp_path):
    import time
    with disposable_tmux() as server:
        run = server.run
        run('new-session', '-d', '-s', 'fixture', 'echo "Thinking… esc to interrupt"; exec sleep 60')
        with patch.object(h, 'run_tmux', side_effect=run):
            first = h.list_panes()[0]
            assert first.pid and first.process_started
            planned = h.result(session='fixture', action='kill', reason='fixture', panes=[first])
            run('respawn-pane', '-k', '-t', '=fixture:0.0', 'echo "new task"; exec sleep 60')
            for _ in range(50):
                after = h.list_panes()[0]
                if after.pid != first.pid: break
                time.sleep(.02)
            assert after.pid != first.pid
            assert h.revalidate_target(planned, allow_hold=False)[1] == 'pane_identity_changed_at_apply'
