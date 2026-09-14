"""Literal format equality; real tmux verifies expansion, not a fake parser."""
import shutil

import pytest
from helpers.tests.support import disposable_tmux

from helpers.tmux.format_guard import all_equal, literal


@pytest.mark.parametrize('value', ['line\nline', '\r', '\t', '\x00', '\x1b', '\x7f'])
def test_control_characters_refused(value):
    with pytest.raises(ValueError, match='unsafe_guard_control'):
        literal(value)


@pytest.mark.parametrize('name', ['x}', '#{session_id}', 'x,y', '@', 'x:foo'])
def test_variable_syntax_cannot_be_injected(name):
    with pytest.raises(ValueError, match='unsafe_guard_variable'):
        all_equal({name: 'value'})


def test_empty_guard_refused():
    with pytest.raises(ValueError, match='empty_guard'):
        all_equal({})


@pytest.mark.skipif(not shutil.which('tmux'), reason='native tmux unavailable')
def test_native_exact_literals_and_all_contract_fields(tmp_path):
    with disposable_tmux() as server:
        def run(*args):
            return server.run(*args).stdout.rstrip('\n')
        # Isolated disposable process; never the operator server.
        run('new-session', '-d', '-s', 'format-sentinel', 'sleep 300')
        marker = tmp_path / 'unexpected-job'
        values = ['', '#', ',', '{', '}', 'review #10, then {close}',
                  '#{session_name}', '#{E:session_name}', f'#(touch {marker})',
                  'x},1}#{==:1,1}', '## ##{ #} #,', 'Unicode 🦝 é ; $() \\ " \'']
        pane = run('display-message', '-p', '-t', 'format-sentinel', '#{pane_id}')
        for value in values:
            expected = {'pane_id': pane, '@goal': value, '@hold': 'review, {not done} #1'}
            run('set-option', '-p', '-t', pane, '@goal', value)
            run('set-option', '-p', '-t', pane, '@hold', expected['@hold'])
            predicate = all_equal(expected)
            # Exercise the actual command guard, not just display-message.
            assert run('if-shell', '-F', '-t', pane, predicate,
                       'display-message -p EXACT', 'display-message -p CHANGED') == 'EXACT'
            run('set-option', '-p', '-t', pane, '@goal', value + 'changed')
            assert run('if-shell', '-F', '-t', pane, predicate,
                       'display-message -p EXACT', 'display-message -p CHANGED') == 'CHANGED'
            run('set-option', '-p', '-t', pane, '@goal', value)
            run('set-option', '-p', '-t', pane, '@hold', 'changed')
            assert run('if-shell', '-F', '-t', pane, predicate,
                       'display-message -p EXACT', 'display-message -p CHANGED') == 'CHANGED'
        assert not marker.exists(), 'literal command substitution executed'
