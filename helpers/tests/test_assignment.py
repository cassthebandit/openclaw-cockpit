from __future__ import annotations
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tmux'))
import assignment as a


class Guard:
    def __init__(self, held=False):
        self.held = held
        self.stamps = []
    def validate(self):
        return self.held
    def stamp(self, *args, **kwargs):
        self.stamps.append((args, kwargs))


class AssignmentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'run'
        self.prepared = a.prepare('claude', self.root, ['claude'])
        self.payload = dict(hook_event_name='SessionStart', session_id='session')
        self.event(self.payload)
        self.result = Path(self.temp.name) / 'result.md'
        self.result.write_text('Result and verification evidence')
    def event(self, payload):
        with a._locked(self.root):
            return a.accept_event(self.root, payload)
    def finish(self):
        return a.finish(self.root, self.result, 'succeeded')
    def stop(self, marker, **kwargs):
        return dict(hook_event_name='Stop', session_id='session',
                    last_assistant_message='Finished.\n' + marker,
                    background_tasks=[], session_crons=[], **kwargs)
    def test_prepare_is_launch_local_without_bypass_and_cannot_reuse(self):
        self.assertEqual(self.prepared['command'][:2], ['claude', '--settings'])
        self.assertTrue((self.root/'hooks.json').exists())
        with self.assertRaises(FileExistsError):
            a.prepare('claude', self.root, ['claude'])
        second = a.prepare('codex', Path(self.temp.name)/'codex', ['codex'])
        self.assertIn('hooks.Stop=', ' '.join(second['command']))
        self.assertNotIn('--dangerously-bypass-hook-trust', second['command'])
    def test_hook_definition_is_stable_across_launch_roots(self):
        other = a.prepare('claude', Path(self.temp.name)/'other', ['claude'])
        first_hooks = a._read(self.root/'hooks.json')
        other_hooks = a._read(Path(other['run_dir'])/'hooks.json')
        self.assertEqual(first_hooks, other_hooks)
        self.assertNotIn(str(self.root), json.dumps(first_hooks))
    def test_is_bound_requires_actual_runtime_event(self):
        fresh = Path(self.temp.name)/'fresh'
        a.prepare('codex', fresh, ['codex'])
        self.assertFalse(a.is_bound(fresh))
        self.assertTrue(a.is_bound(self.root))
    def test_process_archive_failure_preserves_owned_child(self):
        with mock.patch.object(a.subprocess, 'Popen') as popen, mock.patch.object(a, '_write', side_effect=OSError('disk full')):
            child = popen.return_value
            child.pid = 123
            self.assertEqual(a.supervise(self.root, ['fake'], guard=Guard()), 1)
            child.send_signal.assert_not_called()
            child.wait.assert_called_once()
    def test_codex_bootstrap_requires_native_stop_and_is_not_completion(self):
        root = Path(self.temp.name)/'bootstrap'
        prepared = a.prepare('codex', root, ['codex'], bootstrap=True)
        self.assertIn('Do not use tools', prepared['command'][-1])
        with a._locked(root):
            a.accept_event(root, dict(hook_event_name='SessionStart', session_id='boot'))
        self.assertTrue(a.is_bound(root))
        self.assertFalse(a.is_ready(root))
        payload = dict(hook_event_name='Stop', session_id='boot', turn_id='turn',
                       last_assistant_message=prepared['bootstrap_marker'])
        with a._locked(root):
            self.assertIsNone(a.accept_event(root,payload))
        self.assertTrue(a.is_ready(root))
        self.assertIsNone(a._read(root/'state.json')['pending'])
        self.assertFalse((root/'outcome.json').exists())
    def test_generic_stop_cannot_close(self):
        self.assertIsNone(self.event(self.stop('work continues')))
        self.finish()
        self.assertIsNone(self.event(self.stop('a different final')))
    def test_result_is_snapshot_not_mutable_original(self):
        marker = self.finish()
        self.result.write_text('changed after finish')
        self.assertIsNotNone(self.event(self.stop(marker)))
        saved = a._read(self.root/'state.json')['pending']['result_path']
        self.assertEqual(Path(saved).read_text(), 'Result and verification evidence')
    def test_modified_snapshot_blocks(self):
        marker = self.finish()
        state = a._read(self.root/'state.json')
        Path(state['receipt']['result_path']).write_text('tampered')
        self.assertIsNone(self.event(self.stop(marker)))
        self.assertIn('snapshot', a._read(self.root/'state.json')['blocked_reason'])
    def test_stale_session_and_subagent_stop_rejected(self):
        marker = self.finish()
        payload = self.stop(marker)
        payload['session_id'] = 'other'
        self.assertIsNone(self.event(payload))
        self.assertIsNone(self.event(self.stop(marker, agent_id='child')))
    def test_resumed_activity_and_permission_invalidate_receipt(self):
        for event in a.ACTIVITY:
            marker = self.finish()
            self.event(dict(hook_event_name=event, session_id='session'))
            self.assertIsNone(self.event(self.stop(marker)), event)
    def test_missing_or_live_background_state_blocks(self):
        for key, value in [('background_tasks', None), ('session_crons', None),
                           ('background_tasks', [{'id':'x'}]), ('session_crons', [{'id':'x'}])]:
            marker = self.finish()
            payload = self.stop(marker)
            payload[key] = value
            self.assertIsNone(self.event(payload))
    def test_codex_requires_turn_identity(self):
        launch = a._read(self.root/'launch.json')
        launch['runtime'] = 'codex'
        a._write(self.root/'launch.json', launch)
        marker = self.finish()
        self.assertIsNone(self.event(self.stop(marker)))
        self.assertIsNotNone(self.event(self.stop(marker, turn_id='turn')))
    def test_duplicate_consumed_stop_does_not_rearm(self):
        marker = self.finish()
        nonce = self.event(self.stop(marker))
        state = a._read(self.root/'state.json')
        state['consumed_nonce'] = nonce
        state['pending'] = None
        a._write(self.root/'state.json', state)
        self.assertIsNone(self.event(self.stop(marker)))
    def test_missing_hook_binding_cannot_finish(self):
        state = a._read(self.root/'state.json')
        state['session_id'] = None
        a._write(self.root/'state.json', state)
        with self.assertRaisesRegex(ValueError, 'binding missing'):
            self.finish()
    def test_hook_deadline_revokes_not_authorizes(self):
        marker = self.finish()
        with mock.patch.object(a.time, 'monotonic', side_effect=[0,21]):
            self.assertEqual(a.hook(self.root, self.stop(marker)), {})
        self.assertIsNone(a._read(self.root/'state.json')['pending'])
    def test_snapshot_loss_at_exit_boundary_never_signals(self):
        mark = self.finish()
        self.event(self.stop(mark))
        guard = Guard()
        with mock.patch.object(a.subprocess, 'Popen') as popen, mock.patch.object(a, 'snapshot_valid', side_effect=[True, False]):
            child = popen.return_value
            child.pid = 123
            child.poll.return_value = None
            self.assertEqual(a.supervise(self.root, ['fake'], guard=guard), 1)
            child.send_signal.assert_not_called()
            self.assertTrue(any(args[0] == 'blocked' for args,kwargs in guard.stamps))
    def test_native_zero_exit_without_receipt_is_not_success(self):
        self.assertEqual(a.supervise(self.root, [sys.executable, '-c', 'pass'], guard=Guard()), 1)
        self.assertEqual(a._read(self.root/'outcome.json')['outcome'], 'incomplete')
    def real_child(self, outcome='succeeded', keep_open=False, held=False):
        launch = a._read(self.root/'launch.json')
        launch['keep_open'] = keep_open
        a._write(self.root/'launch.json', launch)
        child = Path(self.temp.name)/'child.py'
        child.write_text('''import sys,json\nfrom pathlib import Path\nsys.path.insert(0, sys.argv[1])\nimport assignment as a\nroot=Path(sys.argv[2])\nmark=a.finish(root,Path(sys.argv[3]),sys.argv[4])\nresponse=a.hook(root,dict(hook_event_name="Stop",session_id="session",last_assistant_message=mark,background_tasks=[],session_crons=[]))\n(root/"hook-return.json").write_text(json.dumps(response))\n''')
        guard = Guard(held=held)
        code = a.supervise(self.root, [sys.executable,str(child),str(Path(a.__file__).parent),str(self.root),str(self.result),outcome], guard=guard)
        return code, guard
    def test_owned_child_exits_only_after_explicit_saved_result_join(self):
        code, guard = self.real_child()
        self.assertEqual(code, 0)
        saved = a._read(self.root/'outcome.json')
        self.assertEqual(saved['outcome'], 'succeeded')
        self.assertEqual(saved['end_reason'], 'managed_assignment_exit')
        self.assertEqual(saved['process_exit_code'], -15)
        self.assertTrue(Path(saved['result_path']).exists())
    def test_failed_assignment_never_becomes_success(self):
        code, guard = self.real_child('failed')
        self.assertEqual(code, 1)
        self.assertTrue(any(args[0]=='failed' for args,kwargs in guard.stamps))
    def test_keep_open_and_holds_acknowledge_without_signal(self):
        code, guard = self.real_child(keep_open=True)
        self.assertEqual(code, 0)
        self.assertFalse(a._read(self.root/'hook-return.json')['continue'])
        self.assertTrue(a._read(self.root/'outcome.json')['retained'])
    def test_legacy_hold_also_retains(self):
        code, guard = self.real_child(held=True)
        self.assertEqual(code, 0)
        self.assertTrue((self.root/'hook-return.json').exists())
    def test_ownership_change_prevents_signal(self):
        guard = Guard()
        guard.stamp = mock.Mock(side_effect=ValueError('replaced'))
        with mock.patch.object(a.subprocess, 'Popen') as popen:
            child = popen.return_value
            child.pid = 123
            child.poll.return_value = None
            self.assertEqual(a.supervise(self.root, ['fake'], guard=guard), 1)
            child.send_signal.assert_not_called()
            child.wait.assert_called_once()

if __name__ == '__main__':
    unittest.main()
