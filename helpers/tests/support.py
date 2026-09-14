"""Shared helper-test data and disposable resources, independent of the runner.

Use ordinary functions/context managers from pytest or unittest; lifecycle and
monkeypatch fixtures stay with the test runner that owns them.
"""
import argparse
from datetime import datetime, timedelta, timezone
from helpers.tmux import adoption as a, lifecycle, session_hygiene as hygiene
from helpers.tmux.runtime_adapters import codex as c
from contextlib import contextmanager
from pathlib import Path
import shutil
import subprocess
import tempfile
from unittest import SkipTest


class DisposableTmux:
    def __init__(self, binary, socket):
        self.socket = str(socket)
        self.prefix = [binary, "-u", "-f", "/dev/null", "-S", self.socket]
        # Tests may patch subprocess.run to route production calls. Fixture
        # setup/teardown must keep its own unpatched runner and owned socket.
        self._run = subprocess.run

    def run(self, *args, check=True):
        return self._run([*self.prefix, *args], check=check, capture_output=True,
                         text=True, encoding="utf-8", timeout=10)


@contextmanager
def disposable_tmux():
    binary = shutil.which("tmux")
    if binary is None:
        raise SkipTest("tmux not installed; disposable-server test skipped")
    # macOS's Unix socket limit rules out its usual long TMPDIR paths.
    with tempfile.TemporaryDirectory(prefix="oc-", dir="/tmp") as directory:
        server = DisposableTmux(binary, Path(directory) / "s")
        try:
            yield server
        finally:
            result = server.run("kill-server", check=False)
            if result.returncode and not any(reason in result.stderr for reason in (
                    "No such file or directory", "no server running")):
                raise RuntimeError(f"disposable tmux cleanup failed: {result.stderr}")


def pane(session: str, **meta: str) -> hygiene.Pane:
    old_epoch = str(int(datetime.now(timezone.utc).timestamp()) - 7200)
    return hygiene.Pane(
        session=session,
        server_session_id="$1",
        window_linked="0",
        session_grouped="0",
        window="main",
        pane="%1",
        title="",
        command="zsh",
        path="/tmp",
        created=old_epoch,
        process_started=datetime.fromtimestamp(int(old_epoch), timezone.utc).isoformat(),
        last_activity=old_epoch,
        dead=False,
        dead_status="",
        meta={field: meta.get(field, "") for field in hygiene.OC_FIELDS},
    )



def managed(session: str, **meta: str) -> hygiene.Pane:
    base = {
        "contract_version": "1",
        "managed_by": "agent_wall",
        "kind": "smoke",
        "cleanup_policy": "smoke",
        "state": "done",
        "ttl": "30m",
        "completed_at": (datetime.now(timezone.utc)-timedelta(hours=1)).isoformat(),
    }
    base.update(meta)
    return pane(session, **base)



def fixture(tmp_path, *, dead=False):
    p = pane("temporary")
    p.pid = "321"
    p.dead = dead
    p.dead_status = "0" if dead else ""
    p.server_socket = "/tmp/isolated-adoption-test.sock"
    p.server_pid = "123"
    p.server_started = p.created
    p.session_windows = p.window_panes = "1"
    p.session_attached = p.pane_in_mode = p.pane_input_off = p.pane_pipe = p.pane_synchronized = "0"
    p.remain_on_exit = "on"
    p.window_activity = "1000"
    p.command = "claude.exe"
    config = {**lifecycle.DEFAULTS, "state_dir": str(tmp_path), "status_file": str(tmp_path / "status.json"),
              "archive_dir": str(tmp_path / "archive"), "adopt_existing_exited": True,
              "cleanup_whitelist": [{"exact": p.session}], "live_retirement": "observed",
              "completed_retention_seconds": 0, "teardown_grace_seconds": 0,
              "live_retirement_quiet_seconds": 1}
    args = argparse.Namespace(config=config, policy="kill-safe", grace=0, adopted_grace=0,
                              allow_session=[], override_hold=False, max_kills=1, json=True,
                              archive_root=config["archive_dir"], status_file=config["status_file"],
                              interval=60, write_status=False)
    return p, config, args



def enrollment(tmp_path, p):
    file = tmp_path / "result.md"
    file.write_bytes(b"saved result\x00bytes")
    _, digest = a.result_bytes(str(file))
    return {"release_id": "decision-1", "result_path": str(file), "result_sha256": digest,
            "profile": "claude-2.1.270-direct", "attestation": a.RISK}



def process_fixture(tmp_path):
    p, _, _ = fixture(tmp_path)
    p.command = "node"
    package = "/opt/homebrew/lib/node_modules/@openai/codex"
    native = "/opt/homebrew/lib/node_modules" + c.CODEX_NATIVE_SUFFIX
    rows = {"321": ("100", "node"), "322": ("321", native),
            "323": ("322", c.CUA_BIN + "node_repl"), "324": ("322", c.CUA_BIN + "node"),
            "325": ("324", c.CUA_BIN + "node_repl")}
    paths = {"321": "/opt/homebrew/bin/node", **{pid: command for pid, (_, command) in rows.items() if pid != "321"}}
    args = {"321": "node /opt/homebrew/bin/codex -m gpt-6-astra",
            "322": native, "323": paths["323"],
            "324": paths["324"] + " /tmp/unified-computer-use/26.903.71938/scripts/launch.mjs",
            "325": paths["325"]}
    def ps(argv, **kwargs):
        if argv[1] == "-axo": out = "\n".join(f"{pid} {parent} {command}" for pid, (parent, command) in rows.items())
        elif argv[-1] == "args=": out = args[argv[2]]
        else: out = "Sat Sep 12 15:00:00 2026"
        return subprocess.CompletedProcess(argv, 0, out, "")
    def digest(path, suffix, expected):
        return {"path": package + "/bin/codex.js" if path.endswith("/codex") else path, "sha256": expected}
    return p, rows, paths, args, ps, digest

