"""Built dashboard regression on a private tmux socket; no provider or live jobs."""
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


class NativeFeedback(unittest.TestCase):
    def test_binary_feedback_and_shutdown(self):
        real_tmux = shutil.which("tmux")
        self.assertIsNotNone(real_tmux, "required native integration needs tmux")
        root = Path(__file__).resolve().parents[1]
        with tempfile.TemporaryDirectory(prefix="native-feedback-", dir="/tmp") as tmp:
            directory = Path(tmp)
            wrapper = directory / "tmux"
            wrapper.write_text(f'#!/bin/sh\nexec {shlex.quote(real_tmux)} -S {shlex.quote(tmp + "/socket")} -f /dev/null "$@"\n')
            wrapper.chmod(0o755)
            binary = directory / "cockpit"
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/openclaw-cockpit"], cwd=root, check=True, timeout=120, capture_output=True)
            mode = directory / "mode"
            mode.write_text("idle")
            config = directory / "config.json"
            config.write_text(json.dumps({"expanded_groups": ["Completed Agent Runs"], "stale_threshold": "1h"}))

            def tm(*args):
                return subprocess.check_output([str(wrapper), *args], text=True, timeout=5).strip()

            def wait_for(check):
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    if check():
                        return
                    time.sleep(.1)
                self.fail("dashboard did not reach expected state:\n" + tm("capture-pane", "-p", "-t", "wall"))

            try:
                program = root / "internal/ui/testdata/native_feedback.py"
                tm("new-session", "-d", "-s", "worker", "-x", "160", "-y", "45", shlex.join(["python3", str(program), str(mode)]))
                for key, value in {"kind": "visible-agent", "agent": "claude", "contract_version": "1", "managed_by": "agent_wall", "state": "waiting"}.items():
                    tm("set-option", "-p", "-t", "worker", "@oc_" + key, value)
                pid = tm("display-message", "-p", "-t", "worker", "#{pane_pid}")
                args = [str(binary), "--config", str(config), "--tmux", str(wrapper), "--organize", "--monitor-only", "--fit-native", "--interval", "100ms", "--exclude-session", "wall"]
                tm("new-session", "-d", "-s", "wall", "-x", "360", "-y", "24", "exec " + shlex.join(args))
                tm("set-option", "-p", "-t", "wall", "remain-on-exit", "on")
                for state in ("idle", "working", "idle"):
                    mode.write_text(state)
                    expected = "Completed Agent Runs  1" if state == "idle" else "Active Agents  1"
                    wait_for(lambda: expected in tm("capture-pane", "-p", "-t", "wall"))
                    # Many actual refresh/capture cycles, not one successful screenshot.
                    for _ in range(12):
                        time.sleep(.15)
                        screen = tm("capture-pane", "-p", "-t", "wall")
                        self.assertIn(expected, screen)
                        width, height = map(int, tm("display-message", "-p", "-t", "worker", "#{pane_width} #{pane_height}").split())
                        self.assertGreaterEqual(width, 160)
                        self.assertGreaterEqual(height, 45)
                        self.assertEqual(pid, tm("display-message", "-p", "-t", "worker", "#{pane_pid}"))
                dashboard_pid = int(tm("display-message", "-p", "-t", "wall", "#{pane_pid}"))
                os.kill(dashboard_pid, signal.SIGTERM)
                wait_for(lambda: tm("display-message", "-p", "-t", "wall", "#{pane_dead}") == "1")
                self.assertEqual("160x45", tm("display-message", "-p", "-t", "worker", "#{pane_width}x#{pane_height}"))
                self.assertEqual("", tm("show-options", "-wqv", "-t", "worker", "@cockpit_native_size_owner"))
            finally:
                subprocess.run([str(wrapper), "kill-server"], capture_output=True, timeout=5)
