"""Backlog-specific native identities and real composer observations."""
from pathlib import Path
import subprocess
from unittest.mock import patch

import pytest

from helpers.tmux import adoption as a, native_runtimes as n, session_hygiene as h
from helpers.tmux.runtime_adapters import claude, codex
from helpers.tests.support import fixture, enrollment
from helpers.tests.support import process_fixture

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.mark.parametrize("filename,reason", [("claude270-empty.ansi", ""),
    ("claude270-typed.ansi", "nonempty_or_unknown_prompt")])
def test_real_claude_composer_preserves_typed_drafts(filename, reason):
    assert claude.composer_reason((FIXTURES / filename).read_text()) == reason


@pytest.mark.parametrize("change", ["", "plain", "reset_inside", "extra_line", "footer", "background"])
def test_dim_suggestion_not_typed_input(change):
    raw = (FIXTURES / "claude270-empty.ansi").read_text()
    raw = raw.replace("❯\u00a0\n", "\x1b[39m❯\u00a0\x1b[2mReview complete.\x1b[0m\n")
    assert "\x1b[2mReview complete." in raw
    if change == "plain": raw = raw.replace("\x1b[2mReview complete.\x1b[0m", "Review complete.")
    elif change == "reset_inside": raw = raw.replace("Review complete.", "Review\x1b[0m complete.")
    elif change == "extra_line": raw = raw.replace("Review complete.\x1b[0m", "Review complete.\x1b[0m\nsecond draft line")
    elif change == "footer": raw += "\nunknown dialog"
    elif change == "background": raw += "\n1 shell still running"
    assert bool(claude.composer_reason(raw)) == bool(change)


@pytest.mark.parametrize("runtime_name", ["claude", "codex"])
@pytest.mark.parametrize("older", [False, True])
def test_only_exact_old_templates(tmp_path, runtime_name, older):
    text = (FIXTURES / "legacy-claude-wrapper.sh").read_text()
    if runtime_name == "codex":
        text = text.replace("claude_tui", "codex_tui").replace('"claude"', '"codex"').replace(" claude ", " codex ").replace("\nclaude ", "\ncodex ").replace("claude TUI", "codex TUI")
    if older:
        start = text.index("# A new CLI invocation")
        end = text.index("done\n", start) + 5
        text = text[:start] + text[end:]
    wrapper = tmp_path / "wrapper.sh"; wrapper.write_text(text)
    assert claude.legacy_wrapper(str(wrapper), a.result_bytes, runtime_name)
    wrapper.write_text(text.replace("set +e", "set +e\necho extra"))
    with pytest.raises(ValueError, match="unsupported_wrapper_template"):
        claude.legacy_wrapper(str(wrapper), a.result_bytes, runtime_name)


@pytest.mark.parametrize("change", ["", "extra_child", "wrong_shell", "changed_wrapper", "wrong_code"])
def test_codex_bash_not_generic_traversal(tmp_path, change):
    p, rows, paths, args, ps, digest = process_fixture(tmp_path)
    p.command = "bash"; p.pid = "320"
    rows["320"] = ("100", "bash"); paths["320"] = "/bin/bash"; args["320"] = "bash /tmp/wrapper.sh"
    rows["321"] = ("320", "node")
    if change == "extra_child": rows["329"] = ("320", "sleep")
    elif change == "wrong_shell": paths["320"] = "/tmp/bash"
    record = {"wrapper_path": "/tmp/wrapper.sh", "wrapper_sha256": "expected"}
    profile = codex.PROFILES["codex-0.153.4-legacy-bash"]
    with patch.object(codex.subprocess, "run", side_effect=ps), patch.object(codex, "script_digest", side_effect=digest), \
         patch.object(claude, "code_directory_hash", return_value="changed" if change == "wrong_code" else profile["cdhash"]), \
         patch.object(n.sys, "platform", "darwin"):
        instance, reason = n.runtime(p, profile, record, process_path=lambda pid: paths[pid],
            legacy_wrapper=lambda *args: "changed" if change == "changed_wrapper" else "expected")
    assert bool(reason) == bool(change)
    if not change:
        assert instance["processes"][0]["pid"] == "320" and instance["pid"] == "322"


def test_old_profiles_require_native_proof(tmp_path):
    p, _, _ = fixture(tmp_path)
    record = enrollment(tmp_path, p)
    for key, profile in n.PROFILES.items():
        if not profile.get("proof_pending"):
            continue
        record["profile"] = key
        with patch.object(a, "runtime") as runtime:
            assert a.observe(h, p, record)[1] == "runtime_profile_unproven:" + key
            runtime.assert_not_called()


@pytest.mark.parametrize("change", ["", "hash", "hash_unavailable", "extra_child", "wrong_wrapper"])
def test_claude_unlinked_image_requires_kernel_identity(tmp_path, change):
    p, _, _ = fixture(tmp_path); p.command = "bash"
    profile = claude.PROFILES["claude-2.1.268-legacy-bash"]
    def ps(argv, **kwargs):
        if argv[1] == "-axo":
            out = "321 100 bash\n322 321 claude"
            if change == "extra_child": out += "\n323 322 sleep"
        elif argv[-1] == "args=": out = "bash /tmp/wrapper.sh"
        else: out = "Sat Sep 12 2026"
        return subprocess.CompletedProcess(argv, 0, out, "")
    def path(pid):
        if pid == "321": return "/bin/bash"
        raise OSError(3, "image no longer has a pathname")
    record = {"wrapper_path": "/tmp/wrapper.sh", "wrapper_sha256": "expected"}
    with patch.object(claude.subprocess, "run", side_effect=ps), \
         patch.object(claude, "code_directory_hash", return_value="wrong" if change == "hash" else profile["cdhash"],
                      side_effect=OSError("unavailable") if change == "hash_unavailable" else None):
        observed, reason = claude.runtime(p, profile, record, process_path=path,
            legacy_wrapper=lambda *args: "wrong" if change == "wrong_wrapper" else "expected")
    assert bool(reason) == bool(change)
    if not change:
        assert observed["executable"] == "" and observed["code_directory_hash"] == profile["cdhash"]


def test_codex_native_final_divider_excludes_historic_tool_words():
    raw = (FIXTURES / "codex-completed.ansi").read_text()
    raw = raw.replace("MULTIRUNTIME_FIXTURE_DONE\n", "Old tool: ctrl+t to view transcript\n" + "─" * 30 + "\n• Final result saved.\n" + "─" * 30 + "\n")
    assert not codex.prompt_reason(h, codex.PROFILES["codex-0.153.4-npm"], raw)
    placeholder = "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
    raw = raw.replace(placeholder, "• Working (esc to interrupt)\n" + placeholder)
    assert codex.prompt_reason(h, codex.PROFILES["codex-0.153.4-npm"], raw)


def test_pinned_old_image_can_outlive_its_scrollback_banner():
    raw = "\n• Final result saved.\n" + "─" * 30 + "\n\n\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m\n\ngpt-6-astra high · /tmp/probe\n"
    assert not codex.prompt_reason(h, codex.PROFILES["codex-0.153.4-legacy-bash"], raw)
    assert codex.prompt_reason(h, codex.PROFILES["codex-0.153.4-npm"], raw)
    raw = (FIXTURES / "claude270-empty.ansi").read_text()
    raw = "\n".join(line for line in raw.splitlines() if "Claude Code" not in h.strip_ansi(line))
    assert not claude.prompt_reason(h, claude.PROFILES["claude-2.1.268-legacy-bash"], raw)
    assert claude.prompt_reason(h, claude.PROFILES["claude-2.1.270-direct"], raw)


@pytest.mark.parametrize('draft', ['', 'Please continue', '\nsecond line'])
@pytest.mark.parametrize('footer', ['⏵⏵ auto mode on (shift+tab to cycle) · ← for agents', '⏵⏵ auto mode on (shift+tab to cycle)', 'unknown dialog', '1 agent still running'])
def test_observed_auto_mode_footer_requires_empty_composer(draft, footer):
    raw = '\x1b[38;5;244m' + '─' * 70 + ' job ─\n\x1b[39m❯\u00a0' + draft + '\n' + '─' * 80 + '\n\x1b[38;5;220m  ' + footer + '\x1b[39m\n'
    reason = claude.composer_reason(raw)
    assert bool(reason) == bool(draft or not footer.startswith('⏵⏵'))
