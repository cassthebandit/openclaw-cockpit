"""Real launcher recovery against a disposable tmux socket, never the default."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class LauncherRecovery(unittest.TestCase):
    def test_missing_dead_repeat_and_unrelated_window(self):
        tmux = shutil.which('tmux')
        if not tmux:
            self.skipTest('tmux unavailable')
        with tempfile.TemporaryDirectory() as tmp:
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
                self.assertEqual(1, run('list-sessions', '-F', '#{session_name}').count('cass-agents'))
            finally:
                subprocess.run([tmux, '-S', socket, 'kill-server'], capture_output=True)


if __name__ == '__main__':
    unittest.main()
