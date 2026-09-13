import subprocess

import pytest
from helpers.tmux import cockpit_doctor as doctor


@pytest.mark.parametrize("flags,controlled", [
    ("--monitor-only --control", True),
    ("--control=true --monitor-only", True),
    ("--control=true --control=false", False),
    ("--control=false --control=true", True),
    ("--monitor-only=false", False),
    ("--control=0", False),
    ("--control=T", True),
    ("-- --control", False),
    ("--openclaw-runtime-script /tmp/--control --control=false", False),
])
def test_effective_control_matches_go_flag_semantics(flags, controlled):
    def run(args):
        return subprocess.CompletedProcess(args, 0, "123\n" if args[0] == "tmux" else "openclaw-cockpit --organize --openclaw-runtime " + flags, "")
    check = doctor.check_dashboard_command("example", run)
    assert check.status == ("fail" if controlled else "ok")
    assert check.data["effectiveFlags"]["monitor-only"] is not controlled


@pytest.mark.parametrize("flag", ["--organize=false", "--openclaw-runtime=false"])
def test_false_flags_do_not_pass_by_substring_presence(flag):
    def run(args):
        return subprocess.CompletedProcess(args, 0, "123\n" if args[0] == "tmux" else "openclaw-cockpit --organize --openclaw-runtime " + flag, "")
    assert doctor.check_dashboard_command("example", run).status == "warn"


@pytest.mark.parametrize("flags,enabled", [("--fit-native", True), ("--fit-native=false", False), ("--fit-native=true --fit-native=false", False)])
def test_native_fit_reports_geometry_without_input_authority(flags, enabled):
    def run(args):
        return subprocess.CompletedProcess(args, 0, "123\n" if args[0] == "tmux" else "openclaw-cockpit --organize --openclaw-runtime " + flags, "")
    check = doctor.check_dashboard_command("example", run)
    assert check.status == "ok"
    assert check.data["effectiveFlags"]["fit-native"] is enabled
    assert check.data["effectiveFlags"]["monitor-only"] is True
    assert ("geometry fitting" in check.detail) is enabled
