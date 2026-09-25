"""A plan must not promise removal the executor cannot perform."""
import argparse
from datetime import timedelta
from unittest.mock import patch
import pytest
from helpers.tmux import session_hygiene as h
from helpers.tests.support import managed


@pytest.mark.parametrize('dead', [False, True])
@pytest.mark.parametrize('reason', ['', 'assignment_retained:nonempty_or_unknown_prompt'])
def test_live_completion_reports_owner_blocker(tmp_path, dead, reason):
    evidence = tmp_path / 'result.md'
    evidence.write_text('saved result')
    pane = managed('job', kind='agent', state='done', cleanup_policy='kill_on_done', run_root=str(tmp_path), evidence_path=str(evidence), completed_at=(h.utc_now()-timedelta(hours=1)).isoformat(), end_reason=reason)
    pane.dead = dead
    args = argparse.Namespace(allow_session=[], policy='kill-safe', grace=0, status_file='', override_hold=False)
    with patch.object(h, 'capture_pane_text', return_value='Result saved.\n❯'), patch.object(h, 'list_panes', return_value=[pane]), patch.object(h.adoption, 'protection', return_value=''), patch.object(h.adoption, 'plan', return_value=None):
        rows = h.build_plan(args)
    row = rows[0]
    if dead:
        assert row['action'] == 'kill'
    else:
        assert row['action'] == 'refuse' and row['janitor_state'] == 'cleanup_blocked'
        assert row['reason'] == (reason.removeprefix('assignment_retained:') or 'waiting_for_runtime_exit')
        assert row['tmux_target'] == ''


def test_confirmed_removal_is_not_reported_as_pending():
    args = argparse.Namespace(policy='kill-safe', status_file='')
    row = {'session': 'gone', 'action': 'kill', 'removed': True}
    cycle = h.status_payload([row], args)['last_cycle']
    assert cycle['removed'] == 1 and cycle['kill'] == 0
    row.pop('removed')
    cycle = h.status_payload([row], args)['last_cycle']
    assert cycle['removed'] == 0 and cycle['kill'] == 1
