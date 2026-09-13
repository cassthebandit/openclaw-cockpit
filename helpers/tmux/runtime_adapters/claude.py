"""Claude native process and prompt recognition; no terminal effects."""
from __future__ import annotations

import ctypes
import json
import re
import shlex
import subprocess
from pathlib import Path

PROFILES = {
    "claude-2.1.270-direct": {
        "platform": "darwin", "adapter": "claude", "capture_ansi": True,
        "command": "claude.exe", "process_command": "claude", "version": "2.1.270",
        "keys": ("C-d", "C-d"), "prompt": "❯", "proof": "parent native fixtures 2026-09-12: retained double EOF, typed-input veto, direct post-turn process",
    },
}
PROFILES["claude-2.1.270-legacy-bash"] = {
    **PROFILES["claude-2.1.270-direct"], "wrapper": True, "command": "bash",
    "proof": "parent native legacy Bash fixture 2026-09-12: sole Claude child, retained propagated exit and final footer",
}
# Kernel-observed code-directory digests of the original signed builds. Unlike
# argv or lsof's /claude label, this remains image identity after auto-update
# unlinks the executable. Old native EOF proof is still required before use.
for _version, _cdhash in (("2.1.268", "ff06ad0bbd1aec671505378be972aa4113669376"),):
    PROFILES[f"claude-{_version}-legacy-bash"] = {
        **PROFILES["claude-2.1.270-legacy-bash"], "version": _version,
        "cdhash": _cdhash,
        "proof": "native Claude268 original wrapper proof 2026-09-13: archived completed prompt, doubleEOF, wrapper exit0, both original processes gone",
    }


def code_directory_hash(pid):
    """macOS CS_OPS_CDHASH observes the running image, not the replacement file."""
    library = ctypes.CDLL(None, use_errno=True)
    function = library.csops
    function.argtypes = [ctypes.c_int, ctypes.c_uint, ctypes.c_void_p, ctypes.c_size_t]
    function.restype = ctypes.c_int
    buffer = ctypes.create_string_buffer(20)
    if function(int(pid), 5, buffer, len(buffer)) != 0:
        raise OSError(ctypes.get_errno(), "running_code_identity_unavailable")
    return buffer.raw.hex()


# Literal suffix of the pre-supervisor public launcher, not arbitrary Bash.
LEGACY_TAIL = '''exit_code=$?
if [ "$exit_code" -eq 0 ]; then
  oc_state=done
  oc_end_reason=expected_exit
else
  oc_state=failed
  oc_end_reason=process_exit_nonzero
fi
tmux pipe-pane -t "$TMUX_PANE" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_state "$oc_state" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_exit_code "$exit_code" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_end_reason "$oc_end_reason" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
echo "[agent-wall] claude TUI exited with status $exit_code"
exit "$exit_code"
'''


def legacy_wrapper(path, read_bytes, runtime_name="claude"):
    data, digest = read_bytes(path)
    if len(data) > 65536:
        raise ValueError("unsupported_wrapper_size")
    text = data.decode("utf-8")
    match = re.search(r"^cat > (/[A-Za-z0-9_./-]+) <<'JSON'\n(.*?)\nJSON\n([^\n]+)\n", text, re.M | re.S)
    log = re.search(r"^: > (/[A-Za-z0-9_./-]+)$", text, re.M)
    if not match or not log:
        raise ValueError("unsupported_wrapper_template")
    payload = json.loads(match[2])
    argv = payload.get("argv")
    if payload.get("command_kind") != runtime_name + "_tui" or not isinstance(argv, list) or not argv or not all(isinstance(a, str) for a in argv):
        raise ValueError("unsupported_wrapper_runtime")
    command = list(argv)
    if command[0] == "env":
        command.pop(0)
        while len(command) >= 2 and command[0] == "-u" and re.fullmatch(r"[A-Z_]+", command[1]):
            del command[:2]
        if runtime_name == "codex" and command and re.fullmatch(r"CODEX_HOME=/[A-Za-z0-9_./-]+", command[0]):
            command.pop(0)
        if command and command[0] == "TERM=xterm-256color":
            command.pop(0)
    if not command or command[0] != runtime_name or shlex.join(argv) != match[3]:
        raise ValueError("unsupported_wrapper_command")
    pane_log, launch_log = log[1], match[1]
    prefix = f'''#!/usr/bin/env bash
set +e
mkdir -p "$(dirname {pane_log})" "$(dirname {launch_log})"
: > {pane_log}
# A new CLI invocation must not inherit completion from a prior incarnation.
for oc_field in completed_at exit_code end_reason teardown_marked_at teardown_reason; do
  tmux set-option -pu -t "$TMUX_PANE" "@oc_$oc_field" >/dev/null 2>&1 || true
done
tmux set-option -p -t "$TMUX_PANE" @oc_state running >/dev/null 2>&1 || true
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1 || true
tmux pipe-pane -o -t "$TMUX_PANE" "cat >> {pane_log}" >/dev/null 2>&1 || true
'''
    reset = '''# A new CLI invocation must not inherit completion from a prior incarnation.
for oc_field in completed_at exit_code end_reason teardown_marked_at teardown_reason; do
  tmux set-option -pu -t "$TMUX_PANE" "@oc_$oc_field" >/dev/null 2>&1 || true
done
'''
    tail = LEGACY_TAIL.replace("claude TUI", runtime_name + " TUI")
    if text not in (prefix + match[0] + tail, prefix.replace(reset, "") + match[0] + tail):
        raise ValueError("unsupported_wrapper_template")
    return digest


def runtime(p, profile, record, *, process_path, legacy_wrapper):
    """Direct native executable only; no generic shell/node helper allowance."""
    try:
        cp = subprocess.run(["ps", "-axo", "pid=,ppid=,comm="], capture_output=True,
                            text=True, check=True, timeout=5)
        rows = {}
        for line in cp.stdout.splitlines():
            fields = line.strip().split(maxsplit=2)
            if len(fields) != 3 or not fields[0].isdigit() or not fields[1].isdigit():
                return {}, "process_inventory_unavailable"
            rows[fields[0]] = (fields[1], fields[2])
        receiver = p.pid
        wrapper_hash = ""
        if profile.get("wrapper"):
            wrapper = record.get("wrapper_path", "")
            wrapper_hash = legacy_wrapper(wrapper)
            if record.get("wrapper_sha256") != wrapper_hash:
                return {}, "wrapper_changed"
            parent = rows.get(p.pid)
            if not parent or Path(parent[1]).name != "bash" or process_path(p.pid) != "/bin/bash":
                return {}, "unsupported_wrapper_receiver"
            args = subprocess.run(["ps", "-p", p.pid, "-o", "args="], capture_output=True, text=True, check=True, timeout=5)
            if shlex.split(args.stdout.strip()) not in (["bash", wrapper], ["/bin/bash", wrapper]):
                return {}, "unsupported_wrapper_invocation"
            children = [pid for pid, (parent, _) in rows.items() if parent == p.pid]
            if not children:
                return {}, "runtime_child_missing"
            if len(children) != 1:
                return {}, "working_or_unexplained_descendants"
            receiver = children[0]
        current = rows.get(receiver)
        if not current or current[1] != profile["process_command"]:
            return {}, "unsupported_runtime_version_or_receiver"
        if any(parent == receiver for parent, _ in rows.values()):
            return {}, "working_or_unexplained_descendants"
        cdhash = ""
        if profile.get("cdhash"):
            # Always check pinned image identity, even when a pathname exists.
            cdhash = code_directory_hash(receiver)
            if cdhash != profile["cdhash"]:
                return {}, "unsupported_runtime_code_identity"
        try:
            executable = process_path(receiver)
        except OSError as error:
            if not cdhash:
                return {}, "runtime_executable_unavailable:" + str(error)
            executable = ""  # Unavailable is not a fabricated filesystem path.
        if not cdhash and not executable.endswith("/@anthropic-ai/claude-code/bin/claude.exe"):
            return {}, "unsupported_runtime_executable"
        birth = subprocess.run(["ps", "-p", receiver, "-o", "lstart="], capture_output=True, text=True, check=True, timeout=5).stdout.strip()
        if not birth:
            return {}, "runtime_birth_unavailable"
        return {"pid": receiver, "birth": birth, "executable": executable,
                "wrapper_sha256": wrapper_hash, "pane_birth": p.process_started,
                "code_directory_hash": cdhash}, ""
    except (OSError, ValueError, subprocess.SubprocessError):
        return {}, "process_inventory_unavailable"



def composer_reason(raw):
    """Require empty input or the native dim-only suggestion, never typed text."""
    # Import lazily to avoid the hygiene -> adoption -> adapter import cycle.
    try:
        from .. import session_hygiene as h
    except ImportError:
        import session_hygiene as h
    raw_lines = [line for line in raw.splitlines() if h.strip_ansi(line).strip()]
    lines = [h.strip_ansi(line).strip() for line in raw_lines]
    prompts = [i for i, line in enumerate(lines) if line.startswith("❯")]
    if not prompts:
        return "nonempty_or_unknown_prompt"
    prompt = prompts[-1]
    if prompt == 0 or prompt + 1 >= len(lines) or not all(lines[i].startswith("───") for i in (prompt - 1, prompt + 1)):
        return "nonempty_or_unknown_prompt"
    # A suggestion is fully dim, with no embedded controls or reset/normal text.
    suggestion = r"(?:\x1b\[39m)?❯[ \u00a0]\x1b\[2m[^\x00-\x1f\x7f]+\x1b\[0m"
    if lines[prompt] != "❯" and not re.fullmatch(suggestion, raw_lines[prompt].strip()):
        return "nonempty_or_unknown_prompt"
    footer = lines[prompt + 2:]
    if any(re.search(r"\b[1-9][0-9]* (?:shells?|tasks?|agents?)\b", line) for line in footer):
        return "background_work_visible"
    if footer and (len(footer) != 1 or not re.fullmatch(r"(?:⏸ (?:manual|plan) mode on · )?\? for shortcuts(?: · ← for agents)?", footer[0])):
        return "nonempty_or_unknown_prompt"
    return ""


def prompt_reason(h, profile, raw):
    text = h.strip_ansi(raw)
    if not profile.get("cdhash") and not re.search(r"Claude Code v" + re.escape(profile["version"]) + r"(?:\s|$)", text):
        return "unsupported_runtime_version_or_unknown"
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    prompts = [i for i, line in enumerate(lines) if line.startswith(profile["prompt"])]
    if reason := composer_reason(raw):
        return reason
    prompt = prompts[-1]
    # A blank first line of a multiline draft is not an empty composer.
    if prompt == 0 or prompt + 1 >= len(lines) or not all(lines[i].startswith("───") for i in (prompt - 1, prompt + 1)):
        return "nonempty_or_unknown_prompt"
    completed = [i for i, line in enumerate(lines[:prompt])
                 if re.match(r"^[✻✽✶✳✢] ", line) and h.screen_has_completion_marker(line)]
    if not completed or (len(prompts) > 1 and completed[-1] < prompts[-2]):
        return "completed_prompt_unavailable"
    # Current status/composer/footer, not old assistant prose or prior turns.
    current = "\n".join(lines[completed[-1]:])
    if re.search(r"\b[1-9][0-9]* (?:shells?|tasks?|agents?)(?:\b| still running)", current, re.I):
        return "background_work_visible"
    if h.screen_has_active_marker(current) or h.screen_has_operator_prompt(current):
        return "active_turn_approval_or_question"
    return ""



ADAPTER_ID = "claude"

def enrollment(args, read_bytes):
    if getattr(args, "wrapper", ""):
        return {"wrapper_path": args.wrapper, "wrapper_sha256": legacy_wrapper(args.wrapper, read_bytes)}
    return {}
