"""Real production framing outside tmux, independent of ambient UTF-8 locale."""
import os
from pathlib import Path
import subprocess
import tempfile

import pytest
from helpers.tmux import agent_wall, assignment, cockpit_doctor, session_hygiene, tmux_inspector


@pytest.mark.parametrize("locale_mode", ["unset", "C"])
@pytest.mark.parametrize("escape_controls", [False, True])
def test_production_pane_readers_preserve_framing_and_unicode(monkeypatch, locale_mode, escape_controls):
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
                result = native_run(argv, **kwargs)
                if escape_controls and argv[0] == "tmux":
                    # Older Linux tmux prints format controls as visible octal
                    # escapes even with -u (hosted CI observed literal \\037).
                    result.stdout = result.stdout.replace("\x1f", r"\037").replace("\t", r"\011")
                return result
            monkeypatch.setattr(subprocess, "run", route_only_socket)
            sep = agent_wall.TMUX_FIELD_SEP
            raw = agent_wall.run_tmux("list-panes", "-a", "-F", sep.join(["#{session_name}", "#{pane_id}"])).stdout
            assert raw == "framing" + sep + pane + "\n", repr(raw)
            assert agent_wall.pane_rows("framing")[0]["title"] == title
            assert session_hygiene.list_panes()[0].title == title
            assert tmux_inspector.list_panes()[0].title == title
            rows, errors = cockpit_doctor.tmux_model_lane_rows()
            assert not errors
            assert rows[0]["title"] == title
            # Also cover doctor's three-field display-message reads.
            dashboard = cockpit_doctor.check_dashboard(pane)
            assert "not OpenClaw Cockpit" in dashboard.detail
            assert "malformed" not in dashboard.detail
            fixture("set-option", "-p", "-t", pane, "@oc_launch_id", "transport-launch")
            fixture("set-option", "-p", "-t", pane, "@oc_hold_reason", "review | evidence")
            monkeypatch.setenv("TMUX_PANE", pane)
            guard = assignment.TmuxGuard("transport-launch")
            assert guard.initial[5] == "review | evidence"
            assert guard.validate() is True
            fixture("set-option", "-p", "-t", pane, "@oc_hold_reason", "collision" + sep + "hold")
            with pytest.raises(ValueError):
                guard.validate()
            fixture("select-pane", "-t", pane, "-T", "collision" + sep + "data")
            assert agent_wall.pane_rows("framing") == []
            assert session_hygiene.list_panes() == []
            assert tmux_inspector.list_panes() == []
            rows, errors = cockpit_doctor.tmux_model_lane_rows()
            assert rows == [] and errors
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


def test_literal_octal_text_is_not_decoded(monkeypatch):
    title = r"literal\037text\011tabs"
    fields = ["session", "0", "window", "0", "%0", title, "sleep"]
    monkeypatch.setattr(agent_wall, "run_tmux", lambda *args, **kwargs:
                        subprocess.CompletedProcess(args, 0, agent_wall.TMUX_FIELD_SEP.join(fields) + "\n", ""))
    assert agent_wall.pane_rows("session")[0]["title"] == title
