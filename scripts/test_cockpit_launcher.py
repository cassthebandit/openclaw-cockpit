"""Real launcher recovery against a disposable tmux socket, never the default."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class LauncherRecovery(unittest.TestCase):
    def test_missing_dead_repeat_and_unrelated_window(self):
        self.exercise_recovery(False)

    def test_degraded_helper_recovery(self):
        self.exercise_recovery(True)

    def exercise_recovery(self, helper):
        tmux = shutil.which('tmux')
        if not tmux:
            self.skipTest('tmux unavailable')
        with tempfile.TemporaryDirectory(prefix="p4-", dir="/tmp") as tmp:
            home = Path(tmp)
            bindir = home/'.local/bin'
            bindir.mkdir(parents=True)
            socket = str(home/'tmux.sock')
            wrapper = bindir/'tmux'
            wrapper.write_text(f'#!/bin/bash\nexec "{tmux}" -S "{socket}" "$@"\n')
            wrapper.chmod(0o755)
            binary = bindir/'openclaw-cockpit'
            binary.write_text('#!/bin/bash\nexec sleep 600\n')
            binary.chmod(0o755)
            env = dict(os.environ, HOME=tmp, OPENCLAW_WORKSPACE=str(home/'no-workspace'))
            env.pop('TMUX', None)
            if helper:
                # A helper may launch successfully but return degraded (3).
                helper_path = home/'no-workspace/tools/tmux/start_openclaw_cockpit.sh'
                helper_path.parent.mkdir(parents=True)
                helper_path.write_text('#!/bin/bash\nif tmux list-windows -t =cass-agents -F "#{window_name}" 2>/dev/null | grep -qx dashboard; then\n tmux respawn-pane -k -t =cass-agents:=dashboard "sleep 600"\nelif tmux has-session -t =cass-agents 2>/dev/null; then\n tmux new-window -d -t =cass-agents -n dashboard "sleep 600"\nelse\n tmux new-session -d -s cass-agents -n dashboard "sleep 600"\nfi\nexit 3\n')
                helper_path.chmod(0o755)
            script = str(Path(__file__).with_name('cockpit.command'))
            def run(*args):
                return subprocess.check_output([tmux, '-S', socket, *args], text=True).strip()
            def ensure():
                subprocess.run(['bash', script, '--ensure'], env=env, check=True, capture_output=True, timeout=10)
            try:
                ensure()
                target = '=cass-agents:=dashboard.0'
                first = run('display-message', '-p', '-t', target, '#{pane_pid}')
                ensure()
                self.assertEqual(first, run('display-message', '-p', '-t', target, '#{pane_pid}'))
                run('new-window', '-d', '-t', '=cass-agents', '-n', 'worker', 'sleep 600')
                run('new-window', '-d', '-t', '=cass-agents', '-n', 'dashboard backup', 'sleep 600')
                backup = run('display-message', '-p', '-t', '=cass-agents:=dashboard backup', '#{pane_pid}')
                other = run('display-message', '-p', '-t', '=cass-agents:=worker.0', '#{pane_pid}')
                run('set-option', '-w', '-t', '=cass-agents:=dashboard', 'remain-on-exit', 'on')
                run('respawn-pane', '-k', '-t', target, 'exit 0')
                import time
                for _ in range(50):
                    if run('display-message', '-p', '-t', target, '#{pane_dead}') == '1': break
                    time.sleep(.02)
                ensure()
                self.assertEqual('0', run('display-message', '-p', '-t', target, '#{pane_dead}'))
                self.assertNotEqual(first, run('display-message', '-p', '-t', target, '#{pane_pid}'))
                self.assertEqual(other, run('display-message', '-p', '-t', '=cass-agents:=worker.0', '#{pane_pid}'))
                # Missing dashboard with an unrelated surviving window must recover.
                run('kill-window', '-t', '=cass-agents:=dashboard')
                ensure()
                self.assertEqual('0', run('display-message', '-p', '-t', target, '#{pane_dead}'))
                self.assertEqual(other, run('display-message', '-p', '-t', '=cass-agents:=worker.0', '#{pane_pid}'))
                ensure()
                self.assertEqual(backup, run('display-message', '-p', '-t', '=cass-agents:=dashboard backup', '#{pane_pid}'))
                self.assertEqual(1, run('list-sessions' , '-F', '#{session_name}').count('cass-agents'))
            finally:
                subprocess.run([tmux, '-S', socket, 'kill-server'], capture_output=True)


if __name__ == '__main__':
    unittest.main()
