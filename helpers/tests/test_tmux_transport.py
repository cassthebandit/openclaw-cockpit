"""Real production framing outside tmux, independent of ambient UTF-8 locale."""
import os
from pathlib import Path
import subprocess
import tempfile

import pytest
from helpers.tmux import agent_wall, cockpit_doctor, session_hygiene, tmux_inspector


@pytest.mark.parametrize("locale_mode", ["unset", "C"])
def test_production_pane_readers_preserve_framing_and_unicode(monkeypatch, locale_mode):
    native_run = subprocess.run
    with tempfile.TemporaryDirectory(prefix="cockpit-transport-", dir="/tmp") as temporary:
        socket = str(Path(temporary) / "socket")
        def fixture(*args, check=True):
            return native_run(["tmux", "-u", "-S", socket, *args], check=check,
                              capture_output=True, text=True, encoding="utf-8")
        try:
            pane = fixture("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "framing", "sleep 60").stdout.strip()
            title = "Unicode café • pipe|literal"
            fixture("select-pane", "-t", pane, "-T", title)
            for key in ("TMUX", "TERM", "LC_ALL", "LC_CTYPE", "LANG"):
                monkeypatch.delenv(key, raising=False)
            if locale_mode == "C":
                monkeypatch.setenv("LC_ALL", "C")
            observed_commands = []
            def route_only_socket(argv, **kwargs):
                if argv and argv[0] == "tmux":
                    # Do not replace the production wrapper or supply -u here:
                    # missing transport flags must fail this regression.
                    observed_commands.append(argv)
                    argv = [argv[0], "-S", socket, *argv[1:]]
                return native_run(argv, **kwargs)
            monkeypatch.setattr(subprocess, "run", route_only_socket)
            raw = agent_wall.run_tmux("list-panes", "-a", "-F", "#{session_name}\x1f#{pane_id}").stdout
            assert raw == "framing\x1f" + pane + "\n", repr(raw)
            assert agent_wall.pane_rows("framing")[0]["title"] == title
            assert session_hygiene.list_panes()[0].title == title
            assert tmux_inspector.list_panes()[0].title == title
            rows, errors = cockpit_doctor.tmux_model_lane_rows()
            assert not errors
            assert rows[0]["title"] == title
            assert observed_commands and all(command[1] == "-u" for command in observed_commands)
        finally:
            fixture("kill-server", check=False)


@pytest.mark.parametrize("reader,args", [(agent_wall.pane_rows, ("framing",)),
                                        (session_hygiene.list_panes, ()),
                                        (tmux_inspector.list_panes, ()),
                                        (cockpit_doctor.tmux_model_lane_rows, ())])
def test_invalid_transport_utf8_is_refused_not_replaced(tmp_path, monkeypatch, reader, args):
    executable = tmp_path / "tmux"
    executable.write_text("#!/bin/sh\nprintf '\\377\\n'\n", encoding="ascii")
    executable.chmod(0o700)
    monkeypatch.setenv("PATH", str(tmp_path) + os.pathsep + os.environ.get("PATH", ""))
    with pytest.raises(UnicodeDecodeError):
        reader(*args)
