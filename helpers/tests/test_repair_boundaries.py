"""Regressions for the committee's demonstrated launcher/closeout boundaries."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from helpers.tmux import agent_wall as wall
from helpers.tmux import assignment
from helpers.tests import test_assignment as fixture


class WrapperBoundaryTests(unittest.TestCase):
    def test_literal_paths_and_private_outputs_in_both_wrappers(self):
        for tui in (False, True):
            for existing in (False, True):
                with self.subTest(tui=tui, existing=existing), tempfile.TemporaryDirectory() as tmp:
                    root = Path(tmp)
                    # Substitution would create the marker in cwd if evaluated.
                    logs = root / "space ' quote $(touch INJECTED) `touch INJECTED`"
                    pane, record, debug = [logs / name for name in ('pane.log', 'launch.json', 'debug.log')]
                    if existing:
                        logs.mkdir()
                        for file in (pane, record, debug):
                            file.write_text('old')
                            file.chmod(0o644)
                    stub = root / 'tmux'
                    stub.write_text('#!' + sys.executable + '\nimport os,sys,subprocess\n'
                        'if sys.argv[1:2] == ["pipe-pane"] and "-o" in sys.argv:\n'
                        ' subprocess.run(["bash", "-c", sys.argv[-1]], input=b"literal output", check=True)\n')
                    stub.chmod(0o700)
                    with mock.patch.object(wall, 'STATE_DIR', root / 'state'):
                        if tui:
                            wrapper = wall.write_tui_wrapper('fixture', ['true'], command_kind='fixture',
                                prompt_file='', pane_log=str(pane), launch_record=str(record), debug_file=str(debug))
                        else:
                            wrapper = wall.write_wrapper('fixture', ['true'], pane_log=str(pane), launch_record=str(record))
                    env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'], 'TMUX_PANE': '%1'}
                    result = subprocess.run(['bash', str(wrapper)], cwd=root, env=env, capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertFalse((root / 'INJECTED').exists())
                    self.assertEqual(pane.read_text(), 'literal output')
                    for file in ((pane, record, debug) if tui else (pane, record)):
                        self.assertEqual(file.stat().st_mode & 0o777, 0o600)
                    if not existing:
                        self.assertEqual(logs.stat().st_mode & 0o777, 0o700)
                    self.assertEqual(json.loads(record.read_text())['argv'], ['true'])

    def test_hold_bounds_and_display_annotation(self):
        for hours in (0, -1, float('nan'), float('inf'), 8761):
            with self.subTest(hours=hours), self.assertRaises(SystemExit):
                wall.hold_deadline(argparse.Namespace(hold_reason='review', hold_hours=hours))
        self.assertTrue(wall.hold_deadline(argparse.Namespace(hold_reason='review', hold_hours=24)))
        for kind in ('viewer', 'service', 'runtime'):
            args = wall.build_parser().parse_args(['annotate', '--pane', '%1', '--kind', kind])
            with mock.patch.object(wall, 'run_tmux') as tmux, mock.patch.object(wall, 'set_pane_options') as stamp:
                wall.cmd_annotate(args)
            tmux.assert_any_call('set-option', '-p', '-u', '-t', '%1', '@oc_agent')
            self.assertEqual(stamp.call_args.args[1]['agent'], '')



class SupervisorBoundaryTests(unittest.TestCase):
    setUp = fixture.AssignmentTests.setUp
    event = fixture.AssignmentTests.event
    finish = fixture.AssignmentTests.finish
    stop = fixture.AssignmentTests.stop
    def test_blocked_stamps_only_on_change(self):
        state = assignment._read(self.root / 'state.json')
        state['blocked_reason'] = 'unchanged'
        assignment._write(self.root / 'state.json', state)
        guard = fixture.Guard()
        with mock.patch.object(assignment.subprocess, 'Popen') as popen, mock.patch.object(assignment.time, 'sleep'):
            child = popen.return_value
            child.pid = 123
            child.poll.side_effect = [None] * 6 + [0]
            child.returncode = 0
            assignment.supervise(self.root, ['fake'], guard=guard)
        self.assertEqual(sum(args == ('blocked', 'unchanged') for args, _ in guard.stamps), 1)

    def test_delayed_shutdown_releases_lock_and_preserves_result(self):
        self.event(self.stop(self.finish()))
        guard = fixture.Guard()
        now = [100.0]
        state = assignment._read(self.root / 'state.json')
        state['pending']['deadline'] = 110
        assignment._write(self.root / 'state.json', state)
        checked = []
        def sleep(_):
            with (self.root / 'state.lock').open('a') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                checked.append(assignment._read(self.root / 'state.json').get('consumed_nonce'))
            now[0] += 6
        with mock.patch.object(assignment.subprocess, 'Popen') as popen, mock.patch.object(assignment.time, 'monotonic', side_effect=lambda: now[0]), mock.patch.object(assignment.time, 'sleep', side_effect=sleep):
            child = popen.return_value
            child.pid = 123
            child.poll.side_effect = [None] * 4 + [0]
            child.returncode = 0
            self.assertEqual(assignment.supervise(self.root, ['fake'], guard=guard), 0)
            child.send_signal.assert_called_once()
            child.wait.assert_not_called()
        self.assertTrue(all(checked))
        result = assignment._read(self.root / 'outcome.json')
        self.assertEqual(result['outcome'], 'succeeded')
        self.assertEqual(result['shutdown_status'], 'exited')
        self.assertEqual(sum(args[0] == 'blocked' for args, _ in guard.stamps), 1)

    def test_session_replacement_reports_without_rebinding(self):
        self.finish()
        self.event(dict(hook_event_name='SessionStart', session_id='replacement'))
        state = assignment._read(self.root / 'state.json')
        self.assertEqual(state['session_id'], 'session')
        self.assertIn('runtime session replaced', state['blocked_reason'])
        self.assertIsNone(state['receipt'])
