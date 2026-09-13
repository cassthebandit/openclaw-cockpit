"""Claude native process and prompt recognition; no terminal effects."""
from __future__ import annotations

import json
import re
import shlex
import subprocess
from pathlib import Path

PROFILES = {
    "claude-2.1.270-direct": {
        "platform": "darwin", "adapter": "claude", "capture_ansi": False,
        "command": "claude.exe", "process_command": "claude", "version": "2.1.270",
        "keys": ("C-d", "C-d"), "prompt": "❯", "proof": "parent native fixtures 2026-09-12: retained double EOF, typed-input veto, direct post-turn process",
    },
}
PROFILES["claude-2.1.270-legacy-bash"] = {
    **PROFILES["claude-2.1.270-direct"], "wrapper": True, "command": "bash",
    "proof": "parent native legacy Bash fixture 2026-09-12: sole Claude child, retained propagated exit and final footer",
}
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


def legacy_wrapper(path, read_bytes):
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
    if payload.get("command_kind") != "claude_tui" or not isinstance(argv, list) or not argv or not all(isinstance(a, str) for a in argv):
        raise ValueError("unsupported_wrapper_runtime")
    command = list(argv)
    if command[0] == "env":
        command.pop(0)
        while len(command) >= 2 and command[0] == "-u" and re.fullmatch(r"[A-Z_]+", command[1]):
            del command[:2]
        if command and command[0] == "TERM=xterm-256color":
            command.pop(0)
    if not command or command[0] != "claude" or shlex.join(argv) != match[3]:
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
    if text != prefix + match[0] + LEGACY_TAIL:
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
        try:
            executable = process_path(receiver)
        except OSError as error:
            return {}, "runtime_executable_unavailable:" + str(error)
        if not executable.endswith("/@anthropic-ai/claude-code/bin/claude.exe"):
            return {}, "unsupported_runtime_executable"
        birth = subprocess.run(["ps", "-p", receiver, "-o", "lstart="], capture_output=True, text=True, check=True, timeout=5).stdout.strip()
        if not birth:
            return {}, "runtime_birth_unavailable"
        return {"pid": receiver, "birth": birth, "executable": executable,
                "wrapper_sha256": wrapper_hash, "pane_birth": p.process_started}, ""
    except (OSError, ValueError, subprocess.SubprocessError):
        return {}, "process_inventory_unavailable"



def prompt_reason(h, profile, raw):
    text = h.strip_ansi(raw)
    if not re.search(r"Claude Code v" + re.escape(profile["version"]) + r"(?:\s|$)", text):
        return "unsupported_runtime_version_or_unknown"
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    prompts = [i for i, line in enumerate(lines) if line.startswith(profile["prompt"])]
    if not prompts or lines[prompts[-1]] != profile["prompt"]:
        return "nonempty_or_unknown_prompt"
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
