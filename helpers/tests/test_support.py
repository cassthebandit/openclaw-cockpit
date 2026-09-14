"""Native proof that fixture ownership survives failures and ignores config."""
import os
from pathlib import Path
import time

import pytest

from helpers.tests.support import disposable_tmux


@pytest.mark.parametrize("started", [False, True])
def test_disposable_server_cleanup_on_exception_preserves_other_server(tmp_path, monkeypatch, started):
    # A config that would visibly contaminate every new test server if loaded.
    home = tmp_path / "home"
    home.mkdir()
    (home / ".tmux.conf").write_text("set-option -g @fixture_operator_config loaded\n")
    monkeypatch.setenv("HOME", str(home))
    pid = None
    with disposable_tmux() as sentinel:
        sentinel.run("new-session", "-d", "-s", "sentinel", "sleep 60")
        with pytest.raises(RuntimeError, match="setup failed"):
            with disposable_tmux() as server:
                socket = Path(server.socket)
                assert socket != Path(sentinel.socket) and len(str(socket)) < 100
                assert server.prefix[2:4] == ["-f", "/dev/null"]
                if started:
                    server.run("new-session", "-d", "-s", "owned", "sleep 60")
                    pid = int(server.run("display-message", "-p", "#{pid}").stdout)
                    assert not server.run("show-option", "-gqv", "@fixture_operator_config").stdout.strip()
                raise RuntimeError("setup failed")
        assert not socket.parent.exists()
        if pid is not None:
            deadline = time.monotonic() + 3
            while True:
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    break
                assert time.monotonic() < deadline, "owned tmux process survived cleanup"
                time.sleep(.01)
        assert sentinel.run("has-session", "-t", "=sentinel").returncode == 0
