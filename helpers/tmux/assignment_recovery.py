"""Explicit recovery of completed reports under one obsolete supervisor.

No signals, terminal input, state writes or cleanup decisions. The original
supervisor still owns/reaps/classifies its child. Hygiene owns exact enrollment,
quiet observation, archive, native EOF and only-then dead-pane cleanup.
"""
from __future__ import annotations

import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
from types import SimpleNamespace

try:
    from .runtime_adapters import claude, codex
except ImportError:
    from runtime_adapters import claude, codex

SUPERVISOR_SHA256 = "85863b4b3d380762578536778bd5e37bea998a2b28defa0d662dbc00dfc87dd3"
NATIVE_PROFILES = {
    "codex": {**codex.PROFILES["codex-0.153.4-npm"],
              "cdhash": "9227334847123dd04d3369b05b27769de1987426",
              "proof_pending": True},
    "claude": {**claude.PROFILES["claude-2.1.270-direct"], "command": "claude",
               "version": "2.1.269", "cdhash": "93fe1d93d6dd42cea8c12480ceba430aeec4aaf0",
               "proof_pending": True},
}


ADAPTER_ID = "assignment_recovery"
PROFILE_NAMES = {"codex": "assignment-codex-0.153.4", "claude": "assignment-claude-2.1.269"}
PROFILES = {PROFILE_NAMES[k]: {**v, "command": "bash", "wrapper": True,
            "adapter": ADAPTER_ID, "runtime": k, "proof_pending": True,
            "proof": "pinned old supervisor recovery; supervised native exit proof pending"}
            for k, v in NATIVE_PROFILES.items()}
PROFILES[PROFILE_NAMES["claude"]].update(proof_pending=False, proof=
    "native Claude269 original-supervisor proof 2026-09-13: archived reviewed report; "
    "doubleEOF; original child/supervisor/Bash gone; truthful incomplete/exit1")

PROFILES[PROFILE_NAMES["codex"]].update(proof_pending=False, proof=
    "native Codex153.4 disposable original-supervisor proof 2026-09-13: genuine retained receipt; "
    "EOF; all original processes gone; retained succeeded/exit0")


def _adoption():
    try:
        from . import adoption
    except ImportError:
        import adoption
    return adoption


def _bytes(path):
    return _adoption().result_bytes(path)


def _json(path):
    data, digest = _bytes(str(path))
    if len(data) > 65536:
        raise ValueError("recovery_metadata_oversized")
    value = json.loads(data)
    if not isinstance(value, dict):
        raise ValueError("recovery_metadata_not_object")
    return value, digest


def profile(record):
    """Outer tmux guard stays Bash; inner native profile is checked separately."""
    return {**PROFILES[PROFILE_NAMES[record["runtime"]]], "command": "bash", "wrapper": True}


def _wrapper(path, root, run_id, runtime):
    """Match the one pre-repair supervised launcher, not arbitrary shell argv."""
    data, digest = _bytes(str(path))
    if len(data) > 65536:
        raise ValueError("recovery_wrapper_oversized")
    text = data.decode()
    match = re.search(r"^cat > (/[A-Za-z0-9_./-]+) <<'JSON'\n(.*?)\nJSON\nset \+e\n([^\n]+)\n", text, re.M | re.S)
    log = re.search(r"^: > (/[A-Za-z0-9_./-]+)$", text, re.M)
    if not match or not log:
        raise ValueError("recovery_wrapper_unknown")
    payload = json.loads(match[2]); argv = payload.get("argv")
    if (payload.get("command_kind") != runtime + "_tui" or not isinstance(argv, list)
            or len(argv) < 7 or not all(isinstance(a, str) for a in argv)
            or shlex.join(argv) != match[3] or argv[2:5] != ["supervise", "--run-dir", str(root)]
            or argv[5] != "--" or payload.get("prompt_file") != str(root / "prompt.md")):
        raise ValueError("recovery_wrapper_command_unknown")
    helper = Path(argv[1])
    if not helper.is_absolute() or not str(helper).endswith("/helpers/tmux/assignment.py"):
        raise ValueError("recovery_supervisor_unknown")
    if _bytes(str(helper))[1] != SUPERVISOR_SHA256:
        raise ValueError("recovery_supervisor_unknown")
    # Reuse the launcher's small pure permission/setup formatter; no rendering
    # function that writes files is called during observation.
    try:
        from .agent_wall import sensitive_output_setup
    except ImportError:
        from agent_wall import sensitive_output_setup
    pane_log, launch_log = log[1], match[1]
    prefix = f'''#!/usr/bin/env bash
set -e
{sensitive_output_setup([pane_log, launch_log, payload.get("debug_file", "")])}
: > {pane_log}
# A new CLI invocation must not inherit completion from a prior incarnation.
for oc_field in completed_at exit_code end_reason teardown_marked_at teardown_reason; do
  tmux set-option -pu -t "$TMUX_PANE" "@oc_$oc_field" >/dev/null 2>&1
done
tmux set-option -p -t "$TMUX_PANE" @oc_launch_id {shlex.quote(run_id)} >/dev/null 2>&1
tmux set-option -p -t "$TMUX_PANE" @oc_state running >/dev/null 2>&1
tmux set-option -p -t "$TMUX_PANE" @oc_updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null 2>&1
tmux pipe-pane -o -t "$TMUX_PANE" {shlex.quote("cat >> " + pane_log)} >/dev/null 2>&1
'''
    tail = f'''exit_code=$?
if [ "$exit_code" -eq 0 ]; then
  oc_state=done
  oc_end_reason=expected_exit
else
  oc_state=failed
  oc_end_reason=process_exit_nonzero
fi

echo "[agent-wall] {runtime} TUI exited with status $exit_code"
exit "$exit_code"
'''
    if text != prefix + match[0] + tail:
        raise ValueError("recovery_wrapper_unknown")
    return argv, digest


def _snapshot(pane, root, wrapper, result):
    root = Path(root)
    if (not root.is_absolute() or root.resolve() != root
            or root.parent != Path(pane.meta.get("run_root", "")) / "assignments"
            or not re.fullmatch(r"[0-9a-f]{32}", root.name)):
        raise ValueError("recovery_assignment_root_unknown")
    launch, launch_hash = _json(root / "launch.json")
    state, state_hash = _json(root / "state.json")
    process, process_hash = _json(root / "process.json")
    run_id, runtime = launch.get("run_id"), launch.get("runtime")
    if (runtime not in NATIVE_PROFILES or not isinstance(run_id, str) or not run_id
            or run_id != pane.meta.get("launch_id") or process.get("run_id") != run_id
            or launch.get("keep_open") is not False or type(state.get("generation")) is not int
            or state["generation"] < 1 or not isinstance(state.get("session_id"), str) or not state["session_id"]
            or state.get("pending") is not None or state.get("receipt") is not None
            or state.get("blocked_reason") is not None or state.get("activity") != "PreToolUse"):
        raise ValueError("recovery_assignment_not_quiescent")
    expected = process.get("pane_identity", [])[:5]
    if (len(expected) != 5 or expected[:3] != [pane.pane, pane.pid, pane.server_session_id]
            or expected[4] != run_id or not re.fullmatch(r"@[0-9]+", expected[3])):
        raise ValueError("recovery_original_pane_changed")
    identity_format = "|".join(("#{pane_id}", "#{pane_pid}", "#{session_id}", "#{window_id}", "#{@oc_launch_id}"))
    observed = subprocess.run(["tmux", "-u", "-S", pane.server_socket, "display-message", "-p", "-t", pane.pane, identity_format],
                              capture_output=True, text=True, check=True, timeout=5).stdout.strip().split("|")
    if observed != expected:
        raise ValueError("recovery_original_pane_changed")
    outcome_hash, outcome_kind = "", "missing_native_receipt"
    try:
        outcome, outcome_hash = _json(root / "outcome.json")
    except FileNotFoundError:
        if state.get("consumed_nonce"):
            raise ValueError("recovery_consumed_outcome_missing")
    else:
        if (outcome.get("run_id") != run_id or outcome.get("session_id") != state["session_id"]
                or outcome.get("generation") != state["generation"] or outcome.get("retained") is not True
                or not state.get("consumed_nonce") or outcome.get("nonce") != state["consumed_nonce"]
                or outcome.get("outcome") not in {"succeeded", "failed"}
                or outcome.get("shutdown_status") is not None
                or Path(outcome.get("result_path", "")).parent != root
                or _bytes(outcome["result_path"])[1] != outcome.get("result_sha256")):
            raise ValueError("recovery_retained_completion_changed")
        outcome_kind = "retained_native_completion"
    result_hash = _bytes(str(result))[1]
    if outcome_hash and result_hash != outcome["result_sha256"]:
        raise ValueError("recovery_owner_result_differs_from_receipt")
    argv, wrapper_hash = _wrapper(Path(wrapper), root, run_id, runtime)
    lock = (root / "state.lock").lstat()
    if not stat.S_ISREG(lock.st_mode):
        raise ValueError("recovery_lock_not_regular")
    return {"pane_identity": _adoption().identity(pane), "runtime": runtime, "run_id": run_id, "session_id": state["session_id"],
            "generation": state["generation"], "launch_sha256": launch_hash,
            "state_sha256": state_hash, "process_sha256": process_hash, "outcome_sha256": outcome_hash,
            "classification": outcome_kind, "result_sha256": result_hash, "wrapper_sha256": wrapper_hash,
            "lock_identity": [lock.st_dev, lock.st_ino]}, process, argv


def _runtime(pane, process, argv, runtime, wrapper, process_path):
    native_runtimes = _adoption().native_runtimes
    output = subprocess.run(["ps", "-axo", "pid=,ppid=,comm="], capture_output=True, text=True, check=True, timeout=5).stdout
    rows = {}
    for line in output.splitlines():
        fields = line.strip().split(maxsplit=2)
        if len(fields) != 3 or not fields[0].isdigit() or not fields[1].isdigit():
            raise ValueError("recovery_process_inventory_unknown")
        rows[fields[0]] = fields[1:]
    supervisor, child = str(process.get("supervisor_pid", "")), str(process.get("child_pid", ""))
    def children(pid):
        return {p for p, (parent, _) in rows.items() if parent == pid}
    def args(pid):
        return shlex.split(subprocess.run(["ps", "-p", pid, "-o", "args="], capture_output=True, text=True, check=True, timeout=5).stdout.strip())
    def birth(pid):
        value = subprocess.run(["ps", "-p", pid, "-o", "lstart="], capture_output=True, text=True, check=True, timeout=5).stdout.strip()
        if not value: raise ValueError("recovery_process_birth_unknown")
        return value
    if (pane.command != "bash" or process_path(pane.pid) != "/bin/bash"
            or args(pane.pid) not in (["bash", str(wrapper)], ["/bin/bash", str(wrapper)])
            or children(pane.pid) != {supervisor} or children(supervisor) != {child}
            or supervisor not in rows or child not in rows):
        raise ValueError("recovery_owner_tree_changed")
    supervisor_args = args(supervisor)
    executable = process_path(supervisor)
    # ps flattens runtime operands (including literal TOML quotes). Compare the
    # unambiguous supervisor prefix; bind all flattened argv in the baseline.
    requested = Path(argv[0]).resolve(strict=True)
    allowed_python = {str(requested)}
    # CPython's macOS framework launcher execs this bundled interpreter.
    if (re.fullmatch(r"python3\.[0-9]+", requested.name) and requested.parent.name == "bin"
            and requested.parent.parent.parent.name == "Versions"
            and requested.parent.parent.parent.parent.name == "Python.framework"):
        allowed_python.add(str(requested.parent.parent / "Resources/Python.app/Contents/MacOS/Python"))
    if (not supervisor_args or supervisor_args[1:6] != argv[1:6]
            or str(Path(supervisor_args[0]).resolve()) != executable or executable not in allowed_python):
        raise ValueError("recovery_supervisor_invocation_changed")
    native = SimpleNamespace(pid=child, process_started=birth(child), command=rows[child][1])
    instance, reason = native_runtimes.runtime(native, NATIVE_PROFILES[runtime], {}, process_path=process_path,
                                              legacy_wrapper=_adoption().legacy_wrapper)
    if reason: raise ValueError(reason)
    return {"pane_birth": birth(pane.pid), "supervisor_pid": supervisor, "supervisor_birth": birth(supervisor),
            "supervisor_executable": executable, "supervisor_argv_sha256": hashlib.sha256(json.dumps(supervisor_args).encode()).hexdigest(), "native": instance}


def enroll(pane, assignment_dir, wrapper, result, *, owner_reviewed_result=False, allow_missing_receipt=False,
           process_path=None):
    process_path = process_path or _adoption().process_path
    if owner_reviewed_result is not True:
        raise ValueError("recovery_requires_reviewed_result_authority")
    snapshot, process, argv = _snapshot(pane, assignment_dir, wrapper, result)
    if snapshot["classification"] == "missing_native_receipt" and allow_missing_receipt is not True:
        raise ValueError("recovery_requires_explicit_missing_receipt_acceptance")
    observed = _runtime(pane, process, argv, snapshot["runtime"], wrapper, process_path)
    return {"assignment_dir": str(assignment_dir), "wrapper_path": str(wrapper), "result_path": str(result),
            "runtime": snapshot["runtime"], "snapshot": snapshot, "baseline_runtime": observed,
            "owner_reviewed_result": True, "allow_missing_receipt": allow_missing_receipt}


def observe(pane, record, *, process_path=None):
    """Read-only evidence. Parent must also enforce profile proof and normal guards."""
    try:
        fresh = enroll(pane, record["assignment_dir"], record["wrapper_path"], record["result_path"],
                       owner_reviewed_result=record.get("owner_reviewed_result"),
                       allow_missing_receipt=record.get("allow_missing_receipt"), process_path=process_path)
        if fresh != record:
            return {}, "recovery_assignment_or_process_changed"
        return {"assignment": fresh["snapshot"], "runtime": fresh["baseline_runtime"]}, ""
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        return {}, "recovery_refused:" + str(error)


@contextlib.contextmanager
def final_guard(record):
    """Hold original generation lock across caller's final observe + native EOF.

    Caller must re-read live pane identity and enforce all ordinary policy,
    archive/logging and native-proof requirements inside this context. Never
    wait for child/supervisor exit while holding this lock.
    """
    path = Path(record["assignment_dir"]) / "state.lock"
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        current = os.fstat(fd)
        if not stat.S_ISREG(current.st_mode) or [current.st_dev, current.st_ino] != record["snapshot"]["lock_identity"]:
            raise ValueError("recovery_lock_changed")
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield
    finally:
        os.close(fd)


def enrollment(args, read_bytes):
    pane = getattr(args, "_enrollment_pane", None)
    if pane is None:
        raise ValueError("recovery_requires_fresh_enrollment_pane")
    assignment_dir, wrapper, result = (getattr(args, key, "") for key in ("assignment_dir", "wrapper", "result"))
    if not assignment_dir or not wrapper or not result:
        raise ValueError("recovery_requires_assignment_dir_wrapper_and_result")
    record = enroll(pane, assignment_dir, wrapper, result,
                    owner_reviewed_result=getattr(args, "owner_reviewed_result", False),
                    allow_missing_receipt=getattr(args, "allow_missing_receipt", False))
    if getattr(args, "profile", "") != PROFILE_NAMES[record["runtime"]]:
        raise ValueError("recovery_profile_runtime_mismatch")
    return {"assignment_recovery": record}


def runtime(pane, profile, record, *, process_path, legacy_wrapper):
    recovery = record.get("assignment_recovery", {})
    if recovery.get("runtime") != profile["runtime"]:
        return {}, "recovery_profile_runtime_mismatch"
    return observe(pane, recovery, process_path=process_path)


def prompt_reason(h, profile, raw):
    return _adoption().native_runtimes.prompt_reason(h, NATIVE_PROFILES[profile["runtime"]], raw)


def bound(pane, record):
    """Only immutable identity survives the original owner's post-exit writes."""
    recovery = record.get("assignment_recovery", {})
    snapshot = recovery.get("snapshot", {})
    return bool(recovery.get("runtime") in PROFILE_NAMES
                and record.get("profile") == PROFILE_NAMES[recovery["runtime"]]
                and snapshot.get("pane_identity") == _adoption().identity(pane)
                and snapshot.get("run_id") == pane.meta.get("launch_id"))
