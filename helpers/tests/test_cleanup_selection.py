"""Legacy display evidence is not selection authority; opt-in keeps exact guards."""
import contextlib
import io
import json
import time
from unittest.mock import patch

import pytest
from helpers.tmux import session_hygiene as h
from helpers.tests.support import disposable_tmux, fixture


@pytest.mark.parametrize('enabled,selected,dead', [
    (False, False, True), (True, False, True), (False, True, True),
    (True, True, True), (True, True, False),
])
def test_full_plan_requires_explicit_dead_adoption_selection(tmp_path, enabled, selected, dead):
    p, config, args = fixture(tmp_path, dead=dead)
    p.meta.update(contract_version='display-only', managed_by='tmux_inspector',
                  kind='detected-agent', cleanup_policy='manual', agent='codex')
    root = tmp_path / 'runs' / 'finished'
    root.mkdir(parents=True)
    (root / 'RESULT.md').write_text('finished result')
    p.path = str(root)
    config.update(adopt_existing_exited=enabled, live_retirement='off',
                  cleanup_whitelist=[{'exact': p.session}] if selected else [])
    with patch.object(h, 'STATE_ROOT', tmp_path), patch.object(h, 'list_panes', return_value=[p]), \
         patch.object(h, 'capture_pane_text', return_value='OpenAI Codex\nGoal achieved\n› next task'):
        item, = h.build_plan(args)
    assert (item['action'] == 'kill') == (enabled and selected and dead)
    if item['action'] == 'kill':
        assert item['policy_source'] == 'adopted_dead'
    else:
        assert item['reason'] in {'unmanaged_or_incomplete_contract', 'not_selected', 'live_retirement_off'}


@pytest.mark.parametrize('replace', [False, True])
def test_real_opted_in_cleanup_revalidates_identity_after_archive(tmp_path, replace):
    _, config, args = fixture(tmp_path)
    config.update(live_retirement='off', log_dir=str(tmp_path / 'logs'))
    with disposable_tmux() as tmux:
        tmux.run('new-session', '-d', '-s', 'sentinel', 'sleep 60')
        pane = tmux.run('new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'temporary', 'sleep 60').stdout.strip()
        tmux.run('set-option', '-w', '-t', pane, 'remain-on-exit', 'on')
        tmux.run('respawn-pane', '-k', '-t', pane, 'exit 0')
        for _ in range(100):
            observed = tmux.run('display-message', '-p', '-t', pane, '#{pane_dead}:#{pane_dead_status}').stdout.strip()
            if observed.startswith('1:'): break
            time.sleep(.02)
        assert observed.startswith('1:'), observed
        archived = h.archive_cleanup
        def archive_then_replace(*positional, **keywords):
            result = archived(*positional, **keywords)
            if replace:
                tmux.run('respawn-pane', '-t', pane, 'sleep 60')
            return result
        with patch.object(h, 'run_tmux', side_effect=tmux.run), \
             patch.object(h, 'archive_cleanup', side_effect=archive_then_replace) as archive, \
             contextlib.redirect_stdout(io.StringIO()) as output:
            h.cmd_apply(args)
        result = json.loads(output.getvalue())
        assert tmux.run('has-session', '-t', '=sentinel', check=False).returncode == 0
        exists = tmux.run('has-session', '-t', '=temporary', check=False).returncode == 0
        if observed != '1:0':
            # Unsupported dead exit telemetry must fail closed, not count as kill proof.
            assert exists and not result['killed'] and not archive.called
            assert any(i['reason'] == 'dead_exit_metadata_unavailable' for i in result['skipped'])
        elif replace:
            assert archive.called and exists and not result['killed']
            assert any('identity_changed' in i['reason'] for i in result['refused'])
        else:
            assert archive.called and not exists and len(result['killed']) == 1
            assert list((tmp_path / 'archive').glob('*/metadata.json'))
