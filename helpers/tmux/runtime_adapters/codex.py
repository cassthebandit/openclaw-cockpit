"""Codex native process and prompt recognition; no terminal effects."""
from __future__ import annotations

import hashlib
import re
import shlex
import subprocess
from pathlib import Path

PROFILES = {}
PROFILES["codex-0.153.4-npm"] = {
    "platform": "darwin", "adapter": "codex", "capture_ansi": True,
    "command": "node", "version": "0.153.4", "keys": ("C-d",),
    "proof": "native Codex 0.153.4 macOS fixtures 2026-09-12: retained single EOF and typed-input hazard; integration evidence accompanies this profile",
}
PROFILES["codex-0.153.4-legacy-bash"] = {
    **PROFILES["codex-0.153.4-npm"], "wrapper": True, "command": "bash",
    "cdhash": "9227334847123dd04d3369b05b27769de1987426",
    "proof": "native Codex153.4 old Bash fixture 2026-09-13: completed prompt, single EOF, wrapper done/exit0/footer, all bound processes gone",
}
CODEX_LAUNCHER_SHA256 = "61b0194f3bb6534439c8d26a3ed57d0805f84b884588b761795323eeb92fcf70"
CUA_LAUNCHER_SHA256 = "a50b66879f7b72e45ab6fbaad77eff14a87680a946135f410c121b9b166a2597"
CUA_BIN = "/Applications/ChatGPT.app/Contents/Resources/cua_node/bin/"
CODEX_NATIVE_SUFFIX = "/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex"


def script_digest(path, suffix, expected):
    resolved = Path(path).resolve(strict=True)
    if not str(resolved).endswith(suffix) or not resolved.is_file() or resolved.stat().st_size > 65536:
        raise ValueError("unsupported_runtime_script")
    digest = hashlib.sha256(resolved.read_bytes()).hexdigest()
    if digest != expected:
        raise ValueError("unsupported_runtime_script")
    return {"path": str(resolved), "sha256": digest}


def runtime(p, profile, record, *, process_path, legacy_wrapper):
    """Recognize the npm launcher/native CLI and the tested optional CUA tree.

    Helper recognition is NOT proof of helper idleness. Observed mode still
    requires owner attestation and visible-state checks, and binds every process
    and launcher script in the observation so replacements invalidate release.
    """
    try:
        cp = subprocess.run(["ps", "-axo", "pid=,ppid=,comm="], capture_output=True,
                            text=True, check=True, timeout=5)
        rows = {}
        for line in cp.stdout.splitlines():
            fields = line.strip().split(maxsplit=2)
            if len(fields) != 3 or not fields[0].isdigit() or not fields[1].isdigit():
                return {}, "process_inventory_unavailable"
            rows[fields[0]] = (fields[1], fields[2])
        def children(pid):
            return sorted((child for child, (parent, _) in rows.items() if parent == pid), key=int)
        def argv(pid):
            return shlex.split(subprocess.run(["ps", "-p", pid, "-o", "args="], capture_output=True,
                                             text=True, check=True, timeout=5).stdout.strip())
        def instance(pid):
            path = process_path(pid)
            birth = subprocess.run(["ps", "-p", pid, "-o", "lstart="], capture_output=True,
                                   text=True, check=True, timeout=5).stdout.strip()
            if not birth or not Path(path).is_absolute():
                raise ValueError("runtime_birth_or_executable_unavailable")
            return {"pid": pid, "parent": rows[pid][0], "birth": birth, "executable": path}
        launcher_pid = p.pid
        wrappers = []
        wrapper_hash = ""
        if profile.get("wrapper"):
            wrapper = record.get("wrapper_path", "")
            wrapper_hash = legacy_wrapper(wrapper, "codex")
            if record.get("wrapper_sha256") != wrapper_hash:
                return {}, "wrapper_changed"
            observed = instance(p.pid)
            if observed["executable"] != "/bin/bash" or rows[p.pid][1] != "bash":
                return {}, "unsupported_wrapper_receiver"
            if argv(p.pid) not in (["bash", wrapper], ["/bin/bash", wrapper]):
                return {}, "unsupported_wrapper_invocation"
            direct = children(p.pid)
            if not direct:
                return {}, "runtime_child_missing"
            if len(direct) != 1:
                return {}, "working_or_unexplained_descendants"
            wrappers.append(observed)
            launcher_pid = direct[0]
        parent = instance(launcher_pid)
        parent_argv = argv(launcher_pid)
        if rows[launcher_pid][1] != "node" or Path(parent["executable"]).name != "node" or len(parent_argv) < 2:
            return {}, "unsupported_runtime_launcher"
        launcher = script_digest(parent_argv[1], "/@openai/codex/bin/codex.js", CODEX_LAUNCHER_SHA256)
        direct = children(launcher_pid)
        if len(direct) != 1:
            return {}, "working_or_unexplained_descendants"
        receiver = direct[0]
        native = instance(receiver)
        if not native["executable"].endswith(CODEX_NATIVE_SUFFIX) or rows[receiver][1] != native["executable"]:
            return {}, "unsupported_runtime_executable"
        cdhash = ""
        if profile.get("cdhash"):
            from .claude import code_directory_hash
            cdhash = code_directory_hash(receiver)
            if cdhash != profile["cdhash"]:
                return {}, "unsupported_runtime_code_identity"
        # Parent/native must come from the same installed npm package.
        package = launcher["path"].removesuffix("/bin/codex.js")
        if native["executable"] != package + CODEX_NATIVE_SUFFIX.split("/@openai/codex", 1)[1]:
            return {}, "runtime_installation_mismatch"
        processes = wrappers + [parent, native]
        scripts = [launcher]
        roles = set()
        for child in children(receiver):
            observed = instance(child)
            args = argv(child)
            path = observed["executable"]
            if path == native["executable"] + "-code-mode-host" and args == [path] and not children(child):
                role = "code_mode_host"
                processes.append(observed)
            elif path == CUA_BIN + "node_repl" and args == [path] and not children(child):
                role = "direct_repl"
                processes.append(observed)
            elif path == CUA_BIN + "node" and len(args) == 2 and args[0] == path:
                role = "computer_bridge"
                script = script_digest(args[1], "/unified-computer-use/26.903.71938/scripts/launch.mjs", CUA_LAUNCHER_SHA256)
                nested = children(child)
                if len(nested) != 1:
                    return {}, "working_or_unexplained_descendants"
                repl = instance(nested[0])
                if repl["executable"] != CUA_BIN + "node_repl" or argv(nested[0]) != [repl["executable"]] or children(nested[0]):
                    return {}, "working_or_unexplained_descendants"
                processes.extend([observed, repl])
                scripts.append(script)
            else:
                return {}, "working_or_unexplained_descendants"
            if role in roles:
                return {}, "working_or_unexplained_descendants"
            roles.add(role)
        return {"pid": receiver, "birth": native["birth"], "executable": native["executable"],
                "pane_birth": p.process_started, "processes": processes, "scripts": scripts,
                "wrapper_sha256": wrapper_hash, "code_directory_hash": cdhash}, ""
    except (OSError, ValueError, KeyError, subprocess.SubprocessError):
        return {}, "process_inventory_or_script_unavailable"


def composer_reason(raw):
    """Only the composer/footer, for already-receipted managed completion."""
    try:
        from .. import session_hygiene as h
    except ImportError:
        import session_hygiene as h
    raw_lines = raw.splitlines()
    lines = [h.strip_ansi(line).strip() for line in raw_lines]
    prompts = [i for i, line in enumerate(lines) if line.startswith("›")]
    if not prompts:
        return "nonempty_or_unknown_prompt"
    prompt = prompts[-1]
    # Exact dim native placeholder, not user text that happens to spell it.
    placeholder = "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
    if lines[prompt] != "›" and raw_lines[prompt].strip() != placeholder:
        return "nonempty_or_unknown_prompt"
    # Anything besides the known status line beneath the composer may be a
    # multiline draft, queued input, approval dialog or background-task footer.
    footer = [line for line in lines[prompt + 1:] if line]
    if len(footer) != 1 or not re.fullmatch(r"gpt-[\w.-]+ (?:minimal|low|medium|high|xhigh|max) · .+", footer[0]):
        return "nonempty_or_unknown_prompt"
    return ""


def prompt_reason(h, profile, raw):
    text = h.strip_ansi(raw)
    if not profile.get("cdhash") and not re.search(r"OpenAI Codex\s+\(v" + re.escape(profile["version"]) + r"\)", text):
        return "unsupported_runtime_version_or_unknown"
    raw_lines = raw.splitlines()
    lines = [h.strip_ansi(line).strip() for line in raw_lines]
    prompts = [i for i, line in enumerate(lines) if line.startswith("›")]
    if not prompts or (len(prompts) < 2 and not profile.get("cdhash")):
        return "completed_prompt_unavailable"
    prompt = prompts[-1]
    if reason := composer_reason(raw):
        return reason
    footer = [line for line in lines[prompt + 1:] if line]
    start = prompts[-2] + 1 if len(prompts) >= 2 else 0
    turn_lines = lines[start:prompt]
    turn = "\n".join(turn_lines)
    if not any(line.startswith("• ") and not line.startswith("• You have ") for line in turn_lines):
        return "completed_prompt_unavailable"
    # A native final-turn divider closes prior tool output. Searching historic
    # diffs/transcripts for "ctrl+t to view transcript" misclassified old results
    # as running. Only the status below that divider is current UI state.
    dividers = [i for i, line in enumerate(turn_lines)
                if re.fullmatch(r"─{10,}|─ Worked for .+ ─+", line)]
    if len(prompts) < 2 and not dividers:
        return "completed_prompt_unavailable"
    current = "\n".join(turn_lines[dividers[-1] + 1:] if dividers else turn_lines) + "\n" + "\n".join(footer)
    if re.search(r"(?m)^(?:■ Conversation interrupted|✗ You cancel(?:ed|led) the request)\b", current):
        return "interrupted_turn"
    if re.search(r"(?:\b[1-9][0-9]* (?:background )?(?:shells?|tasks?|agents?|terminals?)\b|background terminal|/ps\b|queued message)", current, re.I):
        return "background_work_visible"
    if h.screen_has_active_marker(current) or h.screen_has_operator_prompt(current):
        return "active_turn_approval_or_question"
    return ""


ADAPTER_ID = "codex"


def enrollment(args, read_bytes):
    from .claude import legacy_wrapper
    if getattr(args, "wrapper", ""):
        return {"wrapper_path": args.wrapper,
                "wrapper_sha256": legacy_wrapper(args.wrapper, read_bytes, "codex")}
    return {}
