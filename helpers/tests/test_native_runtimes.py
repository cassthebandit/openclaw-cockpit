"""Recorded native screens + simulated OS trees. End-to-end proof is separate."""
import subprocess
from pathlib import Path
from unittest.mock import patch

import pytest
from helpers.tmux import adoption as a, native_runtimes as n, session_hygiene as h
from helpers.tmux.runtime_adapters import codex as c
from test_adoption import fixture, enrollment

FIXTURES = Path(__file__).parent / "fixtures"
CODEX = n.PROFILES["codex-0.153.4-npm"]


def test_recorded_codex_empty_placeholder_and_typed_lookalike():
    assert c.prompt_reason(h, CODEX, (FIXTURES / "codex-completed.ansi").read_text()) == ""
    assert c.prompt_reason(h, CODEX, (FIXTURES / "codex-typed-placeholder.ansi").read_text()) == "nonempty_or_unknown_prompt"


@pytest.mark.parametrize("change,reason", [
    ("version", "unsupported_runtime_version_or_unknown"),
    ("startup", "completed_prompt_unavailable"),
    ("draft", "nonempty_or_unknown_prompt"),
    ("multiline", "nonempty_or_unknown_prompt"),
    ("plain_placeholder", "nonempty_or_unknown_prompt"),
    ("approval", "active_turn_approval_or_question"),
    ("active", "active_turn_approval_or_question"),
    ("background", "background_work_visible"),
    ("queued", "nonempty_or_unknown_prompt"),
])
def test_codex_prompt_refusals(change, reason):
    text = (FIXTURES / "codex-completed.ansi").read_text()
    placeholder = "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
    if change == "version": text = text.replace("(v0.153.4)", "(v0.999.0)")
    elif change == "startup": text = text.replace("\x1b[1;2m›", "user")
    elif change == "draft": text = text.replace(placeholder, "› draft")
    elif change == "multiline": text = text.replace(placeholder, "›\nsecond line")
    elif change == "plain_placeholder": text = text.replace(placeholder, "› Ask Codex to do anything")
    elif change == "approval": text = text.replace(placeholder, "approval required\n" + placeholder)
    elif change == "active": text = text.replace(placeholder, "esc to interrupt\n" + placeholder)
    elif change == "background": text = text.replace(placeholder, "1 background terminal\n" + placeholder)
    elif change == "queued": text = text.replace(placeholder, placeholder + "\nQueued message: continue")
    assert c.prompt_reason(h, CODEX, text) == reason


def test_codex_requires_raw_capture_not_normalized_display(tmp_path):
    p, _, _ = fixture(tmp_path)
    p.command = "node"
    record = enrollment(tmp_path, p)
    record["profile"] = "codex-0.153.4-npm"
    text = (FIXTURES / "codex-completed.ansi").read_text()
    with patch.object(a, "runtime", return_value=({"pid": "322"}, "")), \
         patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, text, "")) as capture:
        observed, reason = a.observe(h, p, record)
        assert observed and not reason
        assert "-e" in capture.call_args.args


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


@pytest.mark.parametrize("change", ["", "no_helpers", "code_host", "code_host_child", "unknown_child", "extra_nested", "duplicate_helper", "repl_args", "native_path", "node_path", "script", "malformed_inventory"])
def test_codex_actual_process_matcher(tmp_path, change):
    p, rows, paths, args, ps, digest = process_fixture(tmp_path)
    if change == "no_helpers":
        for pid in ["323", "324", "325"]: del rows[pid]
    elif change in {"code_host", "code_host_child"}:
        host = paths["322"] + "-code-mode-host"
        rows["326"] = ("322", host); paths["326"] = host; args["326"] = host
        if change == "code_host_child": rows["327"] = ("326", "/bin/sh")
    elif change == "unknown_child": rows["326"] = ("322", "/bin/sleep"); paths["326"] = "/bin/sleep"; args["326"] = "sleep 60"
    elif change == "extra_nested": rows["326"] = ("325", "/bin/sh")
    elif change == "duplicate_helper": rows["326"] = ("322", c.CUA_BIN + "node_repl"); paths["326"] = c.CUA_BIN + "node_repl"; args["326"] = paths["326"]
    elif change == "repl_args": args["323"] += " --evaluate anything"
    elif change == "native_path": paths["322"] = "/tmp/codex"
    elif change == "node_path": paths["321"] = "/bin/bash"
    elif change == "script":
        def digest(*_): raise ValueError("changed launcher")
    elif change == "malformed_inventory":
        def ps(*args, **kwargs): return subprocess.CompletedProcess([], 0, "broken row", "")
    with patch.object(c.subprocess, "run", side_effect=ps), patch.object(c, "script_digest", side_effect=digest), \
         patch.object(n.sys, "platform", "darwin"):
        instance, reason = n.runtime(p, CODEX, {}, process_path=lambda pid: paths[pid], legacy_wrapper=a.legacy_wrapper)
    if change in {"", "no_helpers", "code_host"}:
        assert not reason and instance["pid"] == "322"
        assert len(instance["processes"]) == {"": 5, "no_helpers": 2, "code_host": 6}[change]
    else:
        assert reason and not instance


def test_codex_observation_binds_helper_replacement(tmp_path):
    p, rows, paths, args, ps, digest = process_fixture(tmp_path)
    with patch.object(c.subprocess, "run", side_effect=ps), patch.object(c, "script_digest", side_effect=digest), patch.object(n.sys, "platform", "darwin"):
        first, _ = n.runtime(p, CODEX, {}, process_path=lambda pid: paths[pid], legacy_wrapper=a.legacy_wrapper)
        rows["326"] = rows.pop("323"); paths["326"] = paths.pop("323"); args["326"] = args.pop("323")
        second, _ = n.runtime(p, CODEX, {}, process_path=lambda pid: paths[pid], legacy_wrapper=a.legacy_wrapper)
    assert first != second


def test_script_content_and_path_both_required(tmp_path):
    import hashlib
    path = tmp_path / "launch.mjs"
    path.write_text("tested launcher")
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    assert c.script_digest(str(path), "/launch.mjs", digest)["sha256"] == digest
    path.write_text("changed launcher")
    with pytest.raises(ValueError): c.script_digest(str(path), "/launch.mjs", digest)
    with pytest.raises(ValueError): c.script_digest(str(path), "/different.mjs", hashlib.sha256(path.read_bytes()).hexdigest())


@pytest.mark.parametrize("profile", list(n.PROFILES))
@pytest.mark.parametrize("protection,reason", [("hold", "hold_reason_active"), ("blacklist", "blacklist_protected"), ("attached", "attached_copy_mode_or_unknown")])
def test_common_policy_not_replaced_by_adapter(tmp_path, profile, protection, reason):
    p, config, args = fixture(tmp_path)
    p.command = n.PROFILES[profile]["command"]
    record = enrollment(tmp_path, p); record["profile"] = profile
    if protection == "hold": p.meta["hold_reason"] = "human review"
    elif protection == "blacklist": config["cleanup_blacklist"] = [{"exact": p.session}]
    else: p.session_attached = "1"
    with patch.object(a, "observe") as observe:
        item = a.plan(h, [p], args, {"adoptions": {a.identity(p): record}}, config, h.utc_now())
    assert item["reason"] == reason
    observe.assert_not_called()


@pytest.mark.parametrize("name", ["codex-active.ansi", "codex-approval.ansi", "codex-interrupted.ansi"])
def test_recorded_real_activity_and_permission_dialog_are_preserved(name):
    text = (FIXTURES / name).read_text()
    assert c.prompt_reason(h, CODEX, text)


def test_contributor_adapter_requires_no_cleanup_dispatch_change():
    from types import SimpleNamespace
    profile = {"platform": "darwin", "adapter": "example", "capture_ansi": False,
               "command": "example", "version": "1", "keys": ("C-d",), "proof": "fixture"}
    module = SimpleNamespace(ADAPTER_ID="example", PROFILES={"example-1": profile},
                             runtime=lambda *a, **k: ({"pid": "42"}, ""),
                             prompt_reason=lambda *a: "")
    adapters, profiles = n.registry([module])
    with patch.object(n, "ADAPTERS", adapters), patch.object(n.sys, "platform", "darwin"):
        pane = SimpleNamespace(process_started="birth", command="example")
        assert n.runtime(pane, profiles["example-1"], {}, process_path=None, legacy_wrapper=None) == ({"pid": "42"}, "")
        assert n.prompt_reason(h, profiles["example-1"], "screen") == ""
    with pytest.raises(ValueError): n.registry([module, module])
    module.PROFILES["example-1"] = {**profile, "adapter": "different"}
    with pytest.raises(ValueError): n.registry([module])


@pytest.mark.parametrize("change", ["", "draft", "multiline", "version", "approval", "interrupt", "background", "empty_turn"])
@pytest.mark.parametrize("version,filename", [("1.2.0", "agy-completed.ansi"), ("1.2.2", "agy-1.2.2-completed.ansi")])
def test_agy_completed_and_pending_states(change, version, filename):
    text = (FIXTURES / filename).read_text()
    if change == "draft": text = text.replace("\n>\n", "\n> draft\n")
    elif change == "multiline": text = text.replace("\n>\n", "\n>\n draft\n")
    elif change == "version": text = text.replace("CLI " + version, "CLI 9.0.0")
    elif change in {"approval", "interrupt", "background"}:
        text = text.replace("  AGY_ADAPTER_DONE", {"approval": "  permission required", "interrupt": "  Interrupted", "background": "  1 background task"}[change])
    elif change == "empty_turn": text = text.replace("  AGY_ADAPTER_DONE", "")
    assert bool(n.prompt_reason(h, {**n.PROFILES["agy-1.2.2-direct"], "version": version}, text)) == bool(change)


@pytest.mark.parametrize("change", ["", "child", "build", "binding", "conflict", "home", "birth", "inventory"])
def test_agy_process_and_configuration_identity(tmp_path, change):
    import hashlib
    from helpers.tmux.runtime_adapters import agy
    p, _, _ = fixture(tmp_path); p.command = "agy"
    binary = tmp_path / "agy"; binary.write_bytes(b"tested native fixture")
    home = tmp_path / "home"; directory = home / ".gemini/antigravity-cli"; directory.mkdir(parents=True)
    bindings = {"cli.exit": ["ctrl+d"]}
    if change == "binding": bindings["cli.exit"] = ["ctrl+x"]
    if change == "conflict": bindings["prompt.submit"] = ["ctrl+d"]
    import json
    (directory / "keybindings.json").write_text(json.dumps(bindings))
    def ps(argv, **kwargs):
        if argv[1] == "-axo":
            out = "321 100 agy" + ("\n322 321 sleep" if change == "child" else "")
            if change == "inventory": out = "unreadable"
        elif argv[1] == "eww": out = "agy HOME=" + ("/different" if change == "home" else str(home))
        else: out = "" if change == "birth" else "Sat Sep 12 2026"
        return subprocess.CompletedProcess(argv, 0, out, "")
    digest = "wrong" if change == "build" else hashlib.sha256(binary.read_bytes()).hexdigest()
    with patch.object(agy.subprocess, "run", side_effect=ps), patch.object(agy, "BINARY_SHA256", digest), patch.object(agy.Path, "home", return_value=home):
        evidence, reason = agy.runtime(p, n.PROFILES["agy-1.2.2-direct"], {}, process_path=lambda pid: str(binary), legacy_wrapper=None)
    assert bool(reason) == bool(change)
    if not change:
        assert evidence["sha256"] == digest and evidence["keybindings_sha256"]


def test_agy_redraw_requires_explicit_owner_sampling_attestation(tmp_path):
    p, _, _ = fixture(tmp_path); p.command = "agy"
    record = enrollment(tmp_path, p); record["profile"] = "agy-1.2.2-direct"
    text = (FIXTURES / "agy-1.2.2-completed.ansi").read_text()
    with patch.object(a, "runtime", return_value=({"pid": p.pid}, "")), patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, text, "")):
        assert a.observe(h, p, record)[1] == "screen_sampling_attestation_required"
        record["screen_sampling"] = True
        first, reason = a.observe(h, p, record); assert not reason
        p.window_activity = "999999"
        second, reason = a.observe(h, p, record); assert not reason and first == second
    with patch.object(a, "runtime", return_value=({"pid": p.pid}, "")), patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, text.replace("  AGY_ADAPTER_DONE", "  Different result"), "")):
        changed, reason = a.observe(h, p, record)
        assert not reason and changed != first


def test_default_observation_still_binds_output_timestamp(tmp_path):
    p, _, _ = fixture(tmp_path); p.command = "node"
    record = enrollment(tmp_path, p); record["profile"] = "codex-0.153.4-npm"
    text = (FIXTURES / "codex-completed.ansi").read_text()
    with patch.object(a, "runtime", return_value=({"pid": p.pid}, "")), patch.object(h, "run_tmux", return_value=subprocess.CompletedProcess([], 0, text, "")):
        first, reason = a.observe(h, p, record); assert not reason
        p.window_activity = "999999"
        second, reason = a.observe(h, p, record); assert not reason
        assert first != second
