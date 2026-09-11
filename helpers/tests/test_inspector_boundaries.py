"""Inspector guards tested against disposable tmux server mutations."""
import argparse
import subprocess
import tempfile
from pathlib import Path
from unittest.mock import patch

import pytest
from helpers.tmux import tmux_inspector as inspector


@pytest.fixture
def server():
    with tempfile.TemporaryDirectory(prefix="cockpit-inspect-", dir="/tmp") as tmp:
        sock = str(Path(tmp) / "s")
        def run(*args, check=True):
            return subprocess.run(["tmux", "-S", sock, *args], capture_output=True, text=True, check=check)
        try:
            pane = run("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "example", "sleep 600").stdout.strip()
            yield run, pane
        finally:
            run("kill-server", check=False)


@pytest.mark.parametrize("field,value", [("managed_by", "agent_wall"), ("contract_version", "1"),
    ("cleanup_policy", "kill_on_done"), ("owner", "launcher"), ("run_root", "/tmp/example")])
def test_partial_claim_after_observation_blocks_actual_mutation(server, field, value):
    run, pane = server
    with patch.object(inspector, "run_tmux", side_effect=run):
        observed = inspector.list_panes()
    changed = False
    def interleave(*args, **kwargs):
        nonlocal changed
        if not changed and args[0] == "if-shell":
            run("set-option", "-p", "-t", pane, "@oc_" + field, value)
            changed = True
        return run(*args, **kwargs)
    with patch.object(inspector, "list_panes", return_value=observed), patch.object(inspector, "run_tmux", side_effect=interleave):
        events = inspector.annotate_once(argparse.Namespace(stale_seconds=0, protect_session=[], dry_run=False))
    assert events[0]["applied"] is False
    assert run("show-option", "-p", "-v", "-t", pane, "@oc_" + field).stdout.strip() == value
    assert run("show-option", "-p", "-v", "-t", pane, "@oc_state", check=False).returncode != 0


def test_claim_between_inspector_writes_is_preserved(server):
    run, pane = server
    calls = 0
    def interleave(*args, **kwargs):
        nonlocal calls
        calls += 1
        if calls == 2:
            run("set-option", "-p", "-t", pane, "@oc_managed_by", "agent_wall")
            run("set-option", "-p", "-t", pane, "@oc_contract_version", "1")
        return run(*args, **kwargs)
    with patch.object(inspector, "run_tmux", side_effect=interleave):
        applied, _ = inspector.set_pane_options(pane, {"contract_version": "display-only", "managed_by": "tmux_inspector", "state": "idle"})
    assert not applied
    assert run("show-option", "-p", "-v", "-t", pane, "@oc_managed_by").stdout.strip() == "agent_wall"
    assert run("show-option", "-p", "-v", "-t", pane, "@oc_contract_version").stdout.strip() == "1"


def test_format_and_quote_bearing_values_are_literal(server):
    run, pane = server
    literal = "quote ' ; #{pane_id} #() \\ text"
    with patch.object(inspector, "run_tmux", side_effect=run):
        assert inspector.set_pane_options(pane, {"goal": literal})[0]
    assert run("show-option", "-p", "-v", "-t", pane, "@oc_goal").stdout.rstrip("\n") == literal


def test_oversize_or_framing_control_refused_before_any_write():
    for value in ["x" * 4097, "bad\x1ffield"]:
        with patch.object(inspector, "run_tmux") as run:
            assert not inspector.set_pane_options("%1", {"goal": value})[0]
        run.assert_not_called()


def test_respawn_after_scan_does_not_receive_old_annotation(server):
    run, pane = server
    with patch.object(inspector, "run_tmux", side_effect=run):
        observed = inspector.list_panes()
    run("respawn-pane", "-k", "-t", pane, "sleep 601")
    with patch.object(inspector, "list_panes", return_value=observed), patch.object(inspector, "run_tmux", side_effect=run):
        events = inspector.annotate_once(argparse.Namespace(stale_seconds=0, protect_session=[], dry_run=False))
    assert events[0]["applied"] is False
    assert run("show-option", "-p", "-v", "-t", pane, "@oc_managed_by", check=False).returncode != 0
