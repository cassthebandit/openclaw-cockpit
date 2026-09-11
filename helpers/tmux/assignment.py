#!/usr/bin/env python3
"""Launch-owned interactive assignment closeout (stdlib/POSIX only).

An explicit saved-result receipt plus a matching synchronous main Stop hook is
required. A generic Stop, process exit, quiet terminal or elapsed lease is never
assignment success. This is lifecycle evidence, not independent result review.
Hook configuration is launch-local; hook trust remains the runtime's decision.
"""
from __future__ import annotations

import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import time
import uuid

ACTIVITY = {"UserPromptSubmit", "PreToolUse", "PermissionRequest", "Interrupt", "StopFailure"}
MAX_RESULT = 16 * 1024 * 1024


def _write(path: Path, value: dict) -> None:
    temporary = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, sort_keys=True)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _read(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


@contextlib.contextmanager
def _locked(root: Path):
    with (root / "state.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield


def prepare(runtime: str, run_dir: str | Path, command: list[str], *, keep_open: bool = False) -> dict:
    """Create a unique launch directory and return command/prompt_suffix/run_id.

    command must be runtime argv (including optional env prefix), without an
    initial prompt operand. Caller appends the returned suffix to its prompt.
    Never reuse a run directory, even when reusing a tmux session name.
    """
    if runtime not in {"claude", "codex"}:
        raise ValueError("assignment closeout supports only claude and codex")
    root = Path(run_dir).expanduser().absolute()
    root.mkdir(parents=True, mode=0o700, exist_ok=False)
    run_id = str(uuid.uuid4())
    manifest = {"run_id": run_id, "runtime": runtime, "keep_open": keep_open}
    _write(root / "launch.json", manifest)
    _write(root / "state.json", {"generation": 0, "session_id": None, "receipt": None, "pending": None})
    helper = str(Path(__file__).resolve())
    hook_command = shlex.join([sys.executable, helper, "hook"])
    events = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "Stop"]
    if runtime == "claude":
        events.append("StopFailure")
    else:
        events.append("Interrupt")
    hooks = {event: [{"hooks": [{"type": "command", "command": hook_command, "timeout": 3 if event == "Interrupt" else 30}]}] for event in events}
    result_command = list(command)
    if runtime == "claude":
        settings = root / "hooks.json"
        _write(settings, {"hooks": hooks})
        result_command.extend(["--settings", str(settings)])
    else:
        # Inline TOML config layer avoids editing user/project hooks or auth.
        for event in events:
            value = '[{hooks=[{type="command",command=' + json.dumps(hook_command) + ',timeout=' + str(3 if event == 'Interrupt' else 30) + '}]}]'
            result_command.extend(["-c", f"hooks.{event}={value}"])
    finish_command = shlex.join([sys.executable, helper, "finish", "--run-dir", str(root)])
    suffix = (
        "\nManaged assignment completion: Intermediate responses, approvals, and paused work are not completion. "
        "Only when this entire assignment is finished, save its complete result/diagnostics to a nonempty file; "
        "ensure no background tasks or scheduled wakeups remain. Then run " + finish_command +
        " --result <absolute-result-file> --outcome succeeded (or failed for a finished unsuccessful assignment). "
        "The helper saves an immutable result snapshot and returns a unique marker. Do not call other tools "
        "afterward. Include that marker verbatim as the last line of your final response. "
        "If more work is needed, continue normally and call finish again only at the actual end. "
        "Do not exit or kill your own terminal. This receipt is assignment outcome, not independent acceptance.\n"
    )
    return {**manifest, "run_dir": str(root), "command": result_command, "prompt_suffix": suffix}


def is_bound(run_dir: str | Path) -> bool:
    """True only after a real lifecycle hook bound this unique launch session."""
    try:
        root = Path(run_dir)
        with _locked(root):
            session_id = _read(root / "state.json").get("session_id")
        return isinstance(session_id, str) and bool(session_id)
    except (OSError, ValueError):
        return False


def finish(root: Path, result: Path, outcome: str) -> str:
    if outcome not in {"succeeded", "failed"}:
        raise ValueError("invalid assignment outcome")
    if not result.is_file() or result.is_symlink():
        raise ValueError("result must be a regular non-symlink file")
    with result.open("rb") as stream:
        content = stream.read(MAX_RESULT + 1)
    if not content or len(content) > MAX_RESULT:
        raise ValueError("result must contain 1 byte to 16 MiB")
    with _locked(root):
        launch = _read(root / "launch.json")
        state = _read(root / "state.json")
        if not state["session_id"]:
            raise ValueError("runtime hook/session binding missing; completion unsupported until hooks are active")
        nonce = uuid.uuid4().hex
        snapshot = root / ("result-" + nonce + ".bin")
        fd = os.open(snapshot, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        receipt = {"run_id": launch["run_id"], "generation": state["generation"], "session_id": state["session_id"],
                   "nonce": nonce, "outcome": outcome, "result_path": str(snapshot),
                   "result_sha256": hashlib.sha256(content).hexdigest()}
        state.update(receipt=receipt, pending=None)
        _write(root / "state.json", state)
    return marker(receipt)


def marker(receipt: dict) -> str:
    return "COCKPIT_ASSIGNMENT_FINISHED:" + receipt["run_id"] + ":" + receipt["nonce"]


def accept_event(root: Path, payload: dict) -> str | None:
    """Validate an event and publish a close request. Caller must hold state.lock."""
    launch = _read(root / "launch.json")
    state = _read(root / "state.json")
    event = payload.get("hook_event_name")
    session_id = payload.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        return None
    if state["session_id"] and state["session_id"] != session_id:
        return None
    if not state["session_id"]:
        if event not in {"SessionStart", "UserPromptSubmit"}:
            return None
        state["session_id"] = session_id
    if event in ACTIVITY or event == "SessionStart":
        state.update(generation=state["generation"] + 1, receipt=None, pending=None,
                     activity=event, activity_at=time.time(), blocked_reason=None)
        _write(root / "state.json", state)
        return None
    receipt = state.get("receipt")
    if event != "Stop" or not receipt or payload.get("agent_id"):
        return None
    if receipt["generation"] != state["generation"] or receipt["session_id"] != session_id:
        return None
    final = payload.get("last_assistant_message")
    if not isinstance(final, str) or not final.strip().endswith(marker(receipt)):
        return None
    reason = None
    if launch["runtime"] == "codex" and not payload.get("turn_id"):
        reason = "Codex Stop has no turn_id"
    if launch["runtime"] == "claude" and (payload.get("background_tasks") != [] or payload.get("session_crons") != []):
        reason = "Claude background task/scheduled wakeup state is missing or nonempty"
    snapshot = Path(receipt["result_path"])
    if snapshot.is_symlink() or not snapshot.is_file() or hashlib.sha256(snapshot.read_bytes()).hexdigest() != receipt["result_sha256"]:
        reason = "saved result snapshot is missing or changed"
    if reason:
        state.update(blocked_reason=reason, pending=None)
        _write(root / "state.json", state)
        return None
    if state.get("consumed_nonce") == receipt["nonce"]:
        return None
    request = {**receipt, "hook_pid": os.getpid(), "turn_id": payload.get("turn_id"),
               "final_message": final, "requested_at": time.time(), "deadline": time.monotonic() + 20}
    _write(root / ("stop-" + receipt["nonce"] + ".json"), request)
    state.update(pending=request, blocked_reason=None)
    _write(root / "state.json", state)
    return receipt["nonce"]


def hook(root: Path, payload: dict) -> dict:
    with _locked(root):
        nonce = accept_event(root, payload)
    if nonce is None:
        return {}
    # Deliberately synchronous: the runtime cannot continue this Stop while the
    # owner decides. Timeout revokes the request, never authorizes an exit.
    while True:
        with _locked(root):
            state = _read(root / "state.json")
            if state.get("consumed_nonce") == nonce:
                return {"continue": False, "stopReason": "Managed assignment result saved"}
            pending = state.get("pending")
            if not pending or pending["nonce"] != nonce:
                return {}
            if time.monotonic() >= pending["deadline"]:
                state.update(pending=None, blocked_reason="assignment owner did not acknowledge Stop before deadline")
                _write(root / "state.json", state)
                return {}
        time.sleep(0.05)


class TmuxGuard:
    """Bind metadata writes to the same pane/process incarnation and launch UUID."""
    def __init__(self, run_id: str):
        self.pane = os.environ.get("TMUX_PANE", "")
        self.run_id = run_id
        self.initial = self.inspect()
        if self.initial[4] != run_id:
            raise ValueError("pane launch identity is missing or changed")

    def inspect(self) -> list[str]:
        if not self.pane.startswith("%") or not self.pane[1:].isdigit():
            raise ValueError("supervisor requires an exact TMUX_PANE")
        template = "#{pane_id}|#{pane_pid}|#{session_id}|#{window_id}|#{@oc_launch_id}|#{@oc_hold_reason}|#{pane_dead}"
        output = subprocess.check_output(["tmux", "display-message", "-p", "-t", self.pane, template], text=True).strip()
        fields = output.split("|")
        if len(fields) != 7 or fields[0] != self.pane or fields[6] != "0":
            raise ValueError("pane is missing, replaced or dead")
        return fields

    def validate(self) -> bool:
        current = self.inspect()
        if current[:5] != self.initial[:5]:
            raise ValueError("pane ownership changed")
        return bool(current[5])

    def stamp(self, state: str, reason: str, completed: bool = False) -> None:
        # Each write uses an exact pane target, with a fresh ownership predicate
        # in the same tmux command queue as the mutation.
        fields = {"state": state, "end_reason": reason,
                  "updated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
        if completed:
            fields["completed_at"] = fields["updated_at"]
        self.validate()
        for key, value in fields.items():
            condition = "#{&&:#{==:#{@oc_launch_id}," + self.run_id + "},#{==:#{pane_pid}," + self.initial[1] + "}}"
            command = shlex.join(["set-option", "-p", "-t", self.pane, "@oc_" + key, value])
            failure = "display-message -p COCKPIT_IDENTITY_CHANGED"
            output = subprocess.check_output(["tmux", "if-shell", "-F", "-t", self.pane, condition, command, failure], text=True)
            if "COCKPIT_IDENTITY_CHANGED" in output:
                raise ValueError("pane ownership changed during metadata write")
        if state == "running":
            for key in ("completed_at", "exit_code"):
                command = shlex.join(["set-option", "-pu", "-t", self.pane, "@oc_" + key])
                output = subprocess.check_output(["tmux", "if-shell", "-F", "-t", self.pane, condition, command, failure], text=True)
                if "COCKPIT_IDENTITY_CHANGED" in output:
                    raise ValueError("pane ownership changed while clearing completion")


def supervise(root: Path, command: list[str], *, guard=None) -> int:
    launch = _read(root / "launch.json")
    guard = guard or TmuxGuard(launch["run_id"])
    # Inherit the real terminal descriptors. No headless runtime, PTY proxy,
    # detached monitor, broad process-group kill, or janitor responsibility.
    environment = dict(os.environ, OPENCLAW_COCKPIT_ASSIGNMENT_DIR=str(root))
    child = subprocess.Popen(command, env=environment)
    seen_generation = -1
    completed = None
    try:
        _write(root / "process.json", {"run_id": launch["run_id"], "supervisor_pid": os.getpid(), "child_pid": child.pid})
        while child.poll() is None:
            with _locked(root):
                state = _read(root / "state.json")
                if state["generation"] != seen_generation:
                    activity = state.get("activity")
                    active_state = "failed" if activity == "StopFailure" else "waiting" if activity == "PermissionRequest" else "running"
                    guard.stamp(active_state, "assignment_" + str(activity or "active"))
                    seen_generation = state["generation"]
                    completed = None
                if state.get("blocked_reason"):
                    guard.stamp("blocked", state["blocked_reason"])
                pending = state.get("pending")
                if pending and time.monotonic() < pending["deadline"]:
                    # No child poll/reap between this ownership check and signal:
                    # an unreaped child PID cannot be recycled on POSIX.
                    held = guard.validate()
                    retained = launch["keep_open"] or held
                    completed = {**pending, "retained": retained}
                    # Write outcome and diagnostics before any process exit.
                    _write(root / "outcome.json", completed)
                    guard.stamp("done" if pending["outcome"] == "succeeded" else "failed",
                                "assignment_retained" if retained else "assignment_finished", completed=True)
                    state.update(pending=None, consumed_nonce=pending["nonce"], receipt=None)
                    _write(root / "state.json", state)
                    if not retained:
                        # Repeat hold/identity check immediately before action.
                        if guard.validate():
                            completed["retained"] = True
                            _write(root / "outcome.json", completed)
                            guard.stamp("done" if pending["outcome"] == "succeeded" else "failed", "assignment_retained", completed=True)
                        else:
                            child.send_signal(signal.SIGTERM)
                            try:
                                code = child.wait(timeout=10)
                            except subprocess.TimeoutExpired:
                                # Never escalate to SIGKILL on a timer.
                                guard.stamp("blocked", "owned worker did not exit after managed SIGTERM")
                                completed = None
                            else:
                                completed["process_exit_code"] = code
                                completed["end_reason"] = "managed_assignment_exit"
                                _write(root / "outcome.json", completed)
                                return 0 if pending["outcome"] == "succeeded" else 1
            time.sleep(0.05)
        code = child.returncode
        # A manual/native exit is not proof that an assignment finished.
        if completed:
            completed.update(process_exit_code=code, end_reason="retained_assignment_exited")
            _write(root / "outcome.json", completed)
            return 0 if completed["outcome"] == "succeeded" and code == 0 else 1
        _write(root / "outcome.json", {"run_id": launch["run_id"], "outcome": "incomplete", "process_exit_code": code,
                                      "end_reason": "process_exited_without_assignment_completion"})
        return 1
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        # Keep the child and terminal alive after an evidence/ownership failure;
        # never return to a wrapper that might label/retire the live session.
        print(f"[assignment] closeout blocked: {error}", file=sys.stderr, flush=True)
        child.wait()
        return 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    for action in ("hook", "finish", "supervise"):
        item = sub.add_parser(action)
        item.add_argument("--run-dir", type=Path, required=action != "hook")
        if action == "finish":
            item.add_argument("--result", type=Path, required=True)
            item.add_argument("--outcome", choices=("succeeded", "failed"), required=True)
        if action == "supervise":
            item.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.action == "hook" and args.run_dir is None:
        inherited_root = os.environ.get("OPENCLAW_COCKPIT_ASSIGNMENT_DIR")
        if not inherited_root:
            print("{}")
            return 0
        args.run_dir = Path(inherited_root)
    try:
        if args.action == "finish":
            print(finish(args.run_dir, args.result, args.outcome))
        elif args.action == "hook":
            print(json.dumps(hook(args.run_dir, json.load(sys.stdin))))
        else:
            command = args.command[1:] if args.command[:1] == ["--"] else args.command
            if not command:
                parser.error("supervise requires runtime argv after --")
            return supervise(args.run_dir, command)
    except (OSError, ValueError) as error:
        print(f"assignment: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
