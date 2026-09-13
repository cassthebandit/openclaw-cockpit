"""Antigravity CLI 1.2.2: tested macOS arm64 standalone executable."""
from __future__ import annotations
import hashlib
import json
import re
import subprocess
from pathlib import Path

ADAPTER_ID = "agy"
BINARY_SHA256 = "cabadc15a61944372bede1fdff186701c17467dd9d718e97dc79283055d3c101"
PROFILES = {
    "agy-1.2.2-direct": {
        "platform": "darwin", "adapter": ADAPTER_ID, "capture_ansi": True,
        "command": "agy", "version": "1.2.2", "keys": ("C-d", "C-d"), "redraws_while_idle": True,
        "proof": "native macOS arm64 AGY 1.2.2 fixture 2026-09-12: sampled-screen enrollment, retained double EOF, archive/dead-removal/no-repeat",
    },
}


def runtime(p, profile, record, *, process_path, legacy_wrapper):
    try:
        rows = {}
        output = subprocess.run(["ps", "-axo", "pid=,ppid=,comm="], capture_output=True, text=True, check=True, timeout=5).stdout
        for line in output.splitlines():
            fields = line.strip().split(maxsplit=2)
            if len(fields) != 3 or not fields[0].isdigit() or not fields[1].isdigit():
                return {}, "process_inventory_unavailable"
            rows[fields[0]] = fields[1:]
        if p.pid not in rows or rows[p.pid][1] != "agy":
            return {}, "unsupported_runtime_receiver"
        if any(parent == p.pid for parent, _ in rows.values()):
            return {}, "working_or_unexplained_descendants"
        executable = Path(process_path(p.pid))
        # AGY's updater renames the running binary. Content identity, not its
        # mutable installation name, binds the tested build to this profile.
        if not executable.is_absolute() or not re.fullmatch(r"agy(?:\.[0-9]+\.old)?", executable.name):
            return {}, "unsupported_runtime_executable"
        if not executable.is_file() or executable.stat().st_size > 256 * 1024 * 1024:
            return {}, "unsupported_runtime_executable"
        hasher = hashlib.sha256()
        with executable.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                hasher.update(chunk)
        digest = hasher.hexdigest()
        if digest != BINARY_SHA256:
            return {}, "unsupported_runtime_build"
        # Only the same HOME is supported: do not check one user's bindings
        # while sending keys to another configuration. Never retain ps env.
        env = subprocess.run(["ps", "eww", "-p", p.pid, "-o", "command="], capture_output=True, text=True, check=True, timeout=5).stdout
        homes = re.findall(r"(?:^|\s)HOME=([^\s]+)", env)
        if homes != [str(Path.home())]:
            return {}, "runtime_home_unproven"
        path = Path.home() / ".gemini/antigravity-cli/keybindings.json"
        data = path.read_bytes()
        bindings = json.loads(data)
        if bindings.get("cli.exit") != ["ctrl+d"] or any("ctrl+d" in keys for action, keys in bindings.items() if action != "cli.exit"):
            return {}, "unsupported_exit_keybindings"
        birth = subprocess.run(["ps", "-p", p.pid, "-o", "lstart="], capture_output=True, text=True, check=True, timeout=5).stdout.strip()
        if not birth:
            return {}, "runtime_birth_unavailable"
        return {"pid": p.pid, "birth": birth, "pane_birth": p.process_started,
                "executable": str(executable), "sha256": digest,
                "keybindings_sha256": hashlib.sha256(data).hexdigest()}, ""
    except (OSError, ValueError, TypeError, AttributeError, subprocess.SubprocessError):
        return {}, "process_inventory_or_keybindings_unavailable"


def prompt_reason(h, profile, raw):
    lines = [h.strip_ansi(line).strip() for line in raw.splitlines() if h.strip_ansi(line).strip()]
    if not any(re.search(r"Antigravity CLI " + re.escape(profile["version"]) + r"(?:\s|$)", line) for line in lines):
        return "unsupported_runtime_version_or_unknown"
    prompts = [i for i, line in enumerate(lines) if line.startswith(">")]
    if len(prompts) < 2:
        return "completed_prompt_unavailable"
    prompt = prompts[-1]
    if lines[prompt] != ">" or prompt < 1 or prompt + 2 != len(lines) - 1:
        return "nonempty_or_unknown_prompt"
    if not all(re.fullmatch("─{10,}", lines[i]) for i in (prompt - 1, prompt + 1)):
        return "nonempty_or_unknown_prompt"
    if not re.fullmatch(r"\? for shortcuts\s+.+ · (?:low|medium|high)", lines[-1]):
        return "nonempty_or_unknown_prompt"
    turn = "\n".join(lines[prompts[-2] + 1:prompt - 1])
    if not turn:
        return "completed_prompt_unavailable"
    if re.search(r"\b(?:interrupt(?:ed)?|cancel(?:ed|led)|esc to|approval|permission|trust|waiting|queued|background|running)\b", turn, re.I):
        return "active_interrupted_or_pending_turn"
    if h.screen_has_active_marker(turn) or h.screen_has_operator_prompt(turn):
        return "active_turn_approval_or_question"
    return ""
