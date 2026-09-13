from __future__ import annotations
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
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
        self.prepared = a.prepare('claude', self.root, ['claude'], event_config={'log_dir': str(Path(self.temp.name) / 'events')})
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
        child.write_text('''import sys,json,os,signal
from pathlib import Path
from types import SimpleNamespace
root=Path(sys.argv[2])
def fail(reason):
    (root/"fixture-error.json").write_text(json.dumps({"reason":reason}))
    os._exit(86)
signal.signal(signal.SIGALRM, lambda *_: fail("fixture watchdog expired before state handoff"))
signal.alarm(30)
sys.path.insert(0, sys.argv[1])
import assignment as a
# This integration proves receipt/state/owned-process handoff, not elapsed lease
# timing (covered by test_hook_deadline_revokes_not_authorizes). A fixed logical
# lease clock prevents host scheduling/fsync latency from selecting that branch.
a.time=SimpleNamespace(**{name:getattr(a.time,name) for name in ("time","sleep","gmtime","strftime")},monotonic=lambda:0)
mark=a.finish(root,Path(sys.argv[3]),sys.argv[4])
response=a.hook(root,dict(hook_event_name="Stop",session_id="session",last_assistant_message=mark,background_tasks=[],session_crons=[]))
(root/"hook-return.json").write_text(json.dumps(response))
if response.get("continue") is not False:
    fail("hook returned without acknowledging completion")
state=a._read(root/"state.json")
outcome=a._read(root/"outcome.json")
if state.get("consumed_nonce")!=outcome.get("nonce") or not a.snapshot_valid(outcome):
    fail("acknowledgment lacks consumed receipt and valid saved result")
if sys.argv[5]=="retained":
    if outcome.get("retained") is not True:
        fail("retained fixture was not actually retained")
    signal.alarm(0)
else:
    # An interactive runtime remains alive after its final answer. Only its
    # owner may terminate it; do not race the supervisor by exiting voluntarily.
    signal.pause()
''')
        guard = Guard(held=held or keep_open)
        clock = SimpleNamespace(**{name:getattr(a.time,name) for name in ("time","sleep","gmtime","strftime")}, monotonic=lambda:0)
        with mock.patch.object(a, "time", clock):
            code = a.supervise(self.root, [sys.executable,str(child),str(Path(a.__file__).parent),str(self.root),str(self.result),outcome,
                                        "retained" if held or keep_open else "close"], guard=guard)
        if (self.root/"fixture-error.json").exists():
            self.fail("real-child fixture failed: " + (self.root/"fixture-error.json").read_text())
        # Keep diagnosis state in assertion output instead of deleting the only
        # evidence when TemporaryDirectory cleanup runs after a failing gate.
        saved = a._read(self.root/"outcome.json")
        state = a._read(self.root/"state.json")
        self.assertEqual(saved.get("outcome"), outcome, json.dumps({"code":code,"outcome":saved,"state":state}))
        self.assertEqual(state.get("consumed_nonce"), saved.get("nonce"), json.dumps(state))
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
    def file_completion(self, outcome="succeeded"):
        request = {"run_id": self.prepared["run_id"], "outcome": outcome, "result": "Full review result"}
        content = json.dumps(request)
        payload = dict(session_id="session", tool_name="Write", tool_use_id="write-1",
                       tool_input={"file_path": str(self.root / "completion.json"), "content": content})
        self.event(dict(payload, hook_event_name="PreToolUse"))
        (self.root / "completion.json").write_text(content)
        self.event(dict(payload, hook_event_name="PostToolUse"))
        return "COCKPIT_FILE_FINISHED:" + self.prepared["run_id"], payload

    def test_shellless_write_and_matching_final_complete(self):
        mark, _ = self.file_completion()
        self.assertIsNone(self.event(self.stop("still working")))
        self.assertIsNotNone(self.event(self.stop(mark)))
        receipt = a._read(self.root / "state.json")["pending"]
        self.assertEqual(Path(receipt["result_path"]).read_text(), "Full review result")

    def test_shellless_failed_assignment_preserves_outcome(self):
        mark, _ = self.file_completion("failed")
        self.assertIsNotNone(self.event(self.stop(mark)))
        self.assertEqual(a._read(self.root / "state.json")["pending"]["outcome"], "failed")

    def test_shellless_later_tools_and_failed_write_invalidate(self):
        for event in ("PreToolUse", "PostToolUseFailure", "PermissionRequest", "UserPromptSubmit"):
            mark, payload = self.file_completion()
            self.event(dict(session_id="session", hook_event_name=event))
            self.event(dict(payload, hook_event_name="PostToolUse"))
            self.assertIsNone(self.event(self.stop(mark)), event)

    def test_shellless_unmatched_or_subagent_write_cannot_complete(self):
        request = {"run_id": self.prepared["run_id"], "outcome": "succeeded", "result": "old"}
        (self.root / "completion.json").write_text(json.dumps(request))
        self.event(dict(session_id="session", hook_event_name="PostToolUse", tool_name="Write", tool_use_id="old"))
        self.assertIsNone(a._read(self.root / "state.json")["receipt"])
        mark, payload = self.file_completion()
        self.event(dict(session_id="session", hook_event_name="UserPromptSubmit"))
        self.event(dict(payload, hook_event_name="PreToolUse", agent_id="child"))
        self.event(dict(payload, hook_event_name="PostToolUse", agent_id="child"))
        self.assertIsNone(self.event(self.stop(mark)))

    def retained_child(self, *, invalidate=False, tamper=False):
        # Advance actual supervisor state deterministically; native-process proof
        # lives in real_child tests and the disposable installed-runtime run.
        self.event(self.stop(self.finish()))
        guard = Guard(held=True)
        owner = self
        class Child:
            pid = 123
            returncode = 0
            polls = 0
            def poll(inner):
                inner.polls += 1
                if inner.polls == 1:
                    return None
                if inner.polls == 2:
                    assert a._read(owner.root / "state.json").get("consumed_nonce")
                    if invalidate:
                        owner.event(dict(session_id="session", hook_event_name="UserPromptSubmit"))
                    if tamper:
                        saved = a._read(owner.root / "outcome.json")
                        Path(saved["result_path"]).write_text("changed")
                    guard.held = False
                    return None
                return inner.returncode
            def send_signal(inner, sig):
                inner.returncode = -sig
            def wait(inner):
                return inner.returncode
        with mock.patch.object(a.subprocess, "Popen", return_value=Child()), mock.patch.object(a.time, "sleep"):
            code = a.supervise(self.root, ["fake"], guard=guard)
        return code, a._read(self.root / "outcome.json")

    def test_retained_completion_closes_after_hold_release(self):
        code, saved = self.retained_child()
        self.assertEqual(code, 0)
        self.assertEqual(saved["process_exit_code"], -15)
        self.assertFalse(saved["retained"])

    def test_retained_completion_resumed_work_does_not_close(self):
        code, saved = self.retained_child(invalidate=True)
        self.assertEqual(code, 1)
        self.assertEqual(saved["outcome"], "incomplete")
        self.assertEqual(saved["process_exit_code"], 0)

    def test_retained_result_tamper_blocks_close(self):
        code, _ = self.retained_child(tamper=True)
        self.assertEqual(code, 1)
        self.assertTrue((self.root / "closeout-error.json").exists())

    def test_retained_composer_refusal_preserves_saved_completion(self):
        mark = self.finish()
        self.event(self.stop(mark))
        guard = Guard(held=True)
        guard.delayed_close_reason = lambda runtime: "nonempty_or_unknown_prompt"
        owner = self
        def release(*args):
            guard.held = False
        with mock.patch.object(a.subprocess, "Popen") as popen, mock.patch.object(a.time, "sleep", side_effect=release):
            child = popen.return_value
            child.pid = 123
            child.poll.side_effect = [None, None, 0]
            child.returncode = 0
            self.assertEqual(a.supervise(self.root, ["fake"], guard=guard), 0)
            child.send_signal.assert_not_called()
        self.assertEqual(a._read(self.root / "outcome.json")["outcome"], "succeeded")

    def test_late_hold_during_attempt_log_prevents_signal(self):
        self.event(self.stop(self.finish()))
        guard = Guard()
        logged = []
        def append(event, **kwargs):
            logged.append((event, kwargs))
            if event == "exit_attempt":
                guard.held = True
        with mock.patch.object(a.event_log, "append", side_effect=append), mock.patch.object(a.subprocess, "Popen") as popen, mock.patch.object(a.time, "sleep"):
            child = popen.return_value
            child.pid = 123
            child.poll.side_effect = [None, 0]
            child.returncode = 0
            self.assertEqual(a.supervise(self.root, ["fake"], guard=guard), 0)
            child.send_signal.assert_not_called()
        saved = a._read(self.root / "outcome.json")
        self.assertTrue(saved["retained"])
        self.assertEqual(saved["retention_reason"], "hold_active")

    def test_late_snapshot_change_during_attempt_log_prevents_signal(self):
        self.event(self.stop(self.finish()))
        def append(event, **kwargs):
            if event == "exit_attempt":
                Path(a._read(self.root / "outcome.json")["result_path"]).write_text("changed")
        with mock.patch.object(a.event_log, "append", side_effect=append), mock.patch.object(a.subprocess, "Popen") as popen:
            child = popen.return_value
            child.pid = 123
            child.poll.return_value = None
            self.assertEqual(a.supervise(self.root, ["fake"], guard=Guard()), 1)
            child.send_signal.assert_not_called()

    def test_delayed_final_checks_retry_after_transient_refusal(self):
        for obstruction in ("attached", "nonempty_or_unknown_prompt"):
            # Re-arm a genuine receipt for each independent trial.
            self.event(dict(hook_event_name="UserPromptSubmit", session_id="session"))
            self.event(self.stop(self.finish()))
            guard = Guard(held=True)
            guard.reason = ""
            guard.delayed_close_reason = mock.Mock(side_effect=lambda runtime: guard.reason)
            attempts = []
            polls = 0
            def poll():
                nonlocal polls
                polls += 1
                if polls == 2:
                    guard.held = False
                if polls == 3:
                    saved = a._read(self.root / "outcome.json")
                    self.assertTrue(saved["retained"])
                    self.assertEqual(saved["shutdown_status"], "retained")
                    guard.reason = ""
                return None if polls <= 3 else -15
            def append(event, **kwargs):
                if event == "exit_attempt":
                    attempts.append(event)
                    if len(attempts) == 1:
                        guard.reason = obstruction
            with mock.patch.object(a.event_log, "append", side_effect=append), mock.patch.object(a.subprocess, "Popen") as popen, mock.patch.object(a.time, "sleep") as sleep:
                child = popen.return_value
                child.pid = 123
                child.poll.side_effect = poll
                child.returncode = -15
                self.assertEqual(a.supervise(self.root, ["fake"], guard=guard), 0)
                child.send_signal.assert_called_once_with(a.signal.SIGTERM)
                self.assertGreaterEqual(guard.delayed_close_reason.call_count, 4)
                self.assertIn(mock.call(1.0), sleep.call_args_list)

    def test_start_log_failure_does_not_launch_inert_runtime(self):
        with mock.patch.object(a.event_log, "append", side_effect=OSError("log full")), mock.patch.object(a.subprocess, "Popen") as popen:
            self.assertEqual(a.supervise(self.root, ["fake"], guard=Guard()), 1)
            popen.assert_not_called()
        self.assertIn("start log failed", a._read(self.root / "closeout-error.json")["reason"])

    def test_completion_log_failure_does_not_publish_success(self):
        self.event(self.stop(self.finish()))
        def append(event, **kwargs):
            if event == "completion":
                raise OSError("log full")
        with mock.patch.object(a.event_log, "append", side_effect=append), mock.patch.object(a.subprocess, "Popen") as popen:
            child = popen.return_value
            child.pid = 123
            child.poll.return_value = None
            self.assertEqual(a.supervise(self.root, ["fake"], guard=Guard()), 1)
            child.send_signal.assert_not_called()
        self.assertFalse((self.root / "outcome.json").exists())
        self.assertIsNone(a._read(self.root / "state.json").get("consumed_nonce"))

    def test_subagent_session_start_does_not_replace_owner(self):
        mark = self.finish()
        self.event(dict(hook_event_name="SessionStart", session_id="child", agent_id="child"))
        self.assertIsNotNone(self.event(self.stop(mark)))

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
