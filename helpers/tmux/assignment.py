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
import stat
import subprocess
import sys
import time
import uuid

try:
    from . import holds, event_log
except ImportError:
    import holds
    import event_log

ACTIVITY = {"UserPromptSubmit", "PreToolUse", "PermissionRequest", "Interrupt", "StopFailure", "PostToolUseFailure"}
MAX_RESULT = 16 * 1024 * 1024
# Printable across tmux versions; exact field counts refuse delimiter collisions.
TMUX_FIELD_SEP = "|:oc:|"


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


def prepare(runtime: str, run_dir: str | Path, command: list[str], *, keep_open: bool = False, bootstrap: bool = False, event_config: dict | None = None) -> dict:
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
    if event_config is not None:
        manifest["event_config"] = event_config
    if runtime == "codex" and bootstrap:
        manifest["bootstrap_marker"] = "COCKPIT_READY:" + uuid.uuid4().hex
    _write(root / "launch.json", manifest)
    _write(root / "state.json", {"generation": 0, "session_id": None, "receipt": None, "pending": None})
    helper = str(Path(__file__).resolve())
    hook_command = shlex.join([sys.executable, "-B", helper, "hook"])
    events = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "Stop"]
    if runtime == "claude":
        events.extend(["StopFailure", "PostToolUse", "PostToolUseFailure"])
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
    finish_command = shlex.join([sys.executable, "-B", helper, "finish", "--run-dir", str(root)])
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
    if runtime == "claude":
        suffix += (
            "If shell access is unavailable, use Write as your LAST tool to create " + str(root / "completion.json") +
            " containing JSON with exactly these fields: run_id=" + json.dumps(run_id) +
            ', outcome="succeeded" (or "failed"), result=<your complete nonempty result text>. '
            "Do not use another tool after that Write. End your final response with exactly " +
            "COCKPIT_FILE_FINISHED:" + run_id + ". This route still requires no remaining background work.\n"
        )
    if manifest.get("bootstrap_marker"):
        result_command.append("Lifecycle initialization only, not the assignment. Do not use tools, read files, or change anything. "
                              "Reply with exactly " + manifest["bootstrap_marker"] + ". The actual assignment will be submitted separately.")
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


def is_ready(run_dir: str | Path) -> bool:
    """Native lifecycle readiness; Codex initialization must have ended its turn."""
    try:
        root = Path(run_dir)
        with _locked(root):
            launch = _read(root / "launch.json")
            state = _read(root / "state.json")
        return bool(state.get("session_id")) and (not launch.get("bootstrap_marker") or state.get("bootstrap_complete") is True)
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
    return receipt.get("file_marker") or "COCKPIT_ASSIGNMENT_FINISHED:" + receipt["run_id"] + ":" + receipt["nonce"]


def snapshot_valid(receipt: dict) -> bool:
    snapshot = Path(receipt["result_path"])
    try:
        return (not snapshot.is_symlink() and snapshot.is_file()
                and hashlib.sha256(snapshot.read_bytes()).hexdigest() == receipt["result_sha256"])
    except OSError:
        return False


def accept_event(root: Path, payload: dict) -> str | None:
    """Validate an event and publish a close request. Caller must hold state.lock."""
    launch = _read(root / "launch.json")
    state = _read(root / "state.json")
    event = payload.get("hook_event_name")
    if payload.get("agent_id"):
        return None
    session_id = payload.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        return None
    if state["session_id"] and state["session_id"] != session_id:
        if event == "SessionStart":
            state.update(blocked_reason="runtime session replaced; start a new managed assignment", pending=None, receipt=None)
            _write(root / "state.json", state)
        return None
    if event == "SessionStart" and launch["runtime"] == "claude" and payload.get("source") == "compact":
        # Automatic compaction preserves the same runtime session and work.
        # It cannot bind a new launch. A manual /compact already invalidated the
        # receipt through UserPromptSubmit; replacement sessions were rejected above.
        return None
    if not state["session_id"]:
        if event not in {"SessionStart", "UserPromptSubmit"}:
            return None
        state["session_id"] = session_id
    if event in ACTIVITY or event == "SessionStart":
        state.update(generation=state["generation"] + 1, receipt=None, pending=None,
                     activity=event, activity_at=time.time(), blocked_reason=None, completion_write=None)
        if (event == "PreToolUse" and launch["runtime"] == "claude" and payload.get("tool_name") == "Write"
                and payload.get("tool_use_id") and isinstance(payload.get("tool_input"), dict)
                and payload["tool_input"].get("file_path") == str(root / "completion.json")):
            state["completion_write"] = {"tool_use_id": payload["tool_use_id"], "generation": state["generation"]}
        _write(root / "state.json", state)
        return None
    if event == "PostToolUse":
        armed = state.get("completion_write")
        if (armed and armed["generation"] == state["generation"] and launch["runtime"] == "claude"
                and payload.get("tool_name") == "Write" and payload.get("tool_use_id") == armed["tool_use_id"]
                and isinstance(payload.get("tool_input"), dict)
                and payload["tool_input"].get("file_path") == str(root / "completion.json")):
            state["completion_write"] = None
            try:
                request_path = root / "completion.json"
                if request_path.is_symlink() or not stat.S_ISREG(request_path.stat().st_mode) or request_path.stat().st_size > MAX_RESULT:
                    raise ValueError("completion request is not a bounded regular file")
                request = _read(request_path)
                if (set(request) != {"run_id", "outcome", "result"} or request["run_id"] != launch["run_id"]
                        or request["outcome"] not in {"succeeded", "failed"}
                        or not isinstance(request["result"], str) or not request["result"].strip()):
                    raise ValueError("invalid completion request")
                # The successful Write must describe these exact bytes, not an old file.
                if json.loads(payload["tool_input"].get("content", "")) != request:
                    raise ValueError("completion Write content does not match saved file")
                nonce = uuid.uuid4().hex
                snapshot = root / ("result-" + nonce + ".bin")
                content = request["result"].encode("utf-8")
                fd = os.open(snapshot, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, "wb") as stream:
                    stream.write(content)
                    stream.flush()
                    os.fsync(stream.fileno())
                state["receipt"] = {"run_id": launch["run_id"], "generation": state["generation"],
                    "session_id": session_id, "nonce": nonce, "outcome": request["outcome"],
                    "result_path": str(snapshot), "result_sha256": hashlib.sha256(content).hexdigest(),
                    "file_marker": "COCKPIT_FILE_FINISHED:" + launch["run_id"]}
            except (OSError, ValueError, TypeError) as error:
                state.update(receipt=None, blocked_reason="completion file rejected: " + str(error))
            _write(root / "state.json", state)
        return None
    receipt = state.get("receipt")
    if (event == "Stop" and not receipt and launch.get("bootstrap_marker")
            and str(payload.get("last_assistant_message", "")).strip() == launch["bootstrap_marker"]
            and payload.get("turn_id") and not payload.get("agent_id")):
        state["bootstrap_complete"] = True
        _write(root / "state.json", state)
        return None
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
    if not snapshot_valid(receipt):
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
        self.session = subprocess.check_output(["tmux", "-u", "display-message", "-p", "-t", self.pane, "#{session_name}"], text=True).strip()
        if self.initial[4] != run_id:
            raise ValueError("pane launch identity is missing or changed")

    def inspect(self) -> list[str]:
        if not self.pane.startswith("%") or not self.pane[1:].isdigit():
            raise ValueError("supervisor requires an exact TMUX_PANE")
        template = TMUX_FIELD_SEP.join(["#{pane_id}", "#{pane_pid}", "#{session_id}", "#{window_id}", "#{@oc_launch_id}", "#{@oc_hold_reason}", "#{pane_dead}", "#{@oc_hold_until}", "#{@oc_keep_open}"])
        output = subprocess.check_output(["tmux", "-u", "display-message", "-p", "-t", self.pane, template], text=True, encoding="utf-8").strip()
        fields = output.split(TMUX_FIELD_SEP)
        if len(fields) != 9 or fields[0] != self.pane or fields[6] != "0":
            raise ValueError("pane is missing, replaced or dead")
        return fields

    def current_session(self) -> str:
        self.validate()
        self.session = subprocess.check_output(["tmux", "-u", "display-message", "-p", "-t", self.pane, "#{session_name}"], text=True).strip()
        return self.session

    def validate(self) -> bool:
        current = self.inspect()
        if current[:5] != self.initial[:5]:
            raise ValueError("pane ownership changed")
        return holds.active({"hold_reason": current[5], "hold_until": current[7], "keep_open": current[8]})

    def delayed_close_reason(self, runtime: str) -> str:
        """A retained Stop no longer locks the runtime. Inspect its current composer."""
        self.validate()
        fields = ["#{session_attached}", "#{pane_in_mode}", "#{session_windows}", "#{window_panes}",
                  "#{window_linked}", "#{session_grouped}", "#{pane_synchronized}", "#{pane_input_off}"]
        topology = subprocess.check_output(["tmux", "-u", "display-message", "-p", "-t", self.pane,
                                           TMUX_FIELD_SEP.join(fields)], text=True).strip().split(TMUX_FIELD_SEP)
        if topology != ["0", "0", "1", "1", "0", "0", "0", "0"]:
            return "retained session attached, input disabled, or topology changed"
        try:
            from .runtime_adapters import claude, codex
        except ImportError:
            from runtime_adapters import claude, codex
        adapter = {"claude": claude, "codex": codex}.get(runtime)
        if adapter is None or not hasattr(adapter, "composer_reason"):
            return "retained runtime composer inspection unavailable"
        raw = subprocess.check_output(["tmux", "-u", "capture-pane", "-p", "-e", "-t", self.pane, "-S", "-240"], text=True)
        return adapter.composer_reason(raw)

    def stamp(self, state: str, reason: str, completed: bool = False, exit_code: int | None = None) -> None:
        # Each write uses an exact pane target, with a fresh ownership predicate
        # in the same tmux command queue as the mutation.
        fields = {"state": state, "end_reason": reason,
                  "updated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
        if exit_code is not None:
            fields["exit_code"] = str(exit_code)
        if completed:
            fields["completed_at"] = fields["updated_at"]
        self.validate()
        for key, value in fields.items():
            condition = "#{&&:#{==:#{@oc_launch_id}," + self.run_id + "},#{==:#{pane_pid}," + self.initial[1] + "}}"
            command = shlex.join(["set-option", "-p", "-t", self.pane, "@oc_" + key, value])
            failure = "display-message -p COCKPIT_IDENTITY_CHANGED"
            output = subprocess.check_output(["tmux", "-u", "if-shell", "-F", "-t", self.pane, condition, command, failure], text=True, encoding="utf-8")
            if "COCKPIT_IDENTITY_CHANGED" in output:
                raise ValueError("pane ownership changed during metadata write")
        if state == "running":
            for key in ("completed_at", "exit_code"):
                command = shlex.join(["set-option", "-pu", "-t", self.pane, "@oc_" + key])
                output = subprocess.check_output(["tmux", "-u", "if-shell", "-F", "-t", self.pane, condition, command, failure], text=True, encoding="utf-8")
                if "COCKPIT_IDENTITY_CHANGED" in output:
                    raise ValueError("pane ownership changed while clearing completion")


def supervise(root: Path, command: list[str], *, guard=None, retained_poll_seconds: float = 1.0) -> int:
    launch = _read(root / "launch.json")
    guard = guard or TmuxGuard(launch["run_id"])
    if retained_poll_seconds <= 0:
        raise ValueError("retained polling interval must be positive")

    def log(event, result="", reason=""):
        session = guard.current_session() if hasattr(guard, "current_session") else getattr(guard, "session", getattr(guard, "pane", ""))
        event_log.append(event, session=session, identity=launch["run_id"], source="automatic",
                         result=result, reason=reason, config=launch.get("event_config"))

    # Establish required logging before launching a runtime that could complete.
    # A failed log setup is a visible launch error, not an inert live supervisor.
    try:
        log("start", "starting")
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        _write(root / "closeout-error.json", {"run_id": launch["run_id"], "reason": "start log failed: " + str(error)})
        guard.stamp("failed", "start_log_failed", completed=True, exit_code=1)
        return 1
    environment = dict(os.environ, OPENCLAW_COCKPIT_ASSIGNMENT_DIR=str(root))
    try:
        child = subprocess.Popen(command, env=environment)
    except OSError as error:
        _write(root / "outcome.json", {"run_id": launch["run_id"], "outcome": "failed", "end_reason": "runtime_launch_failed", "reason": str(error)})
        guard.stamp("failed", "runtime_launch_failed", completed=True, exit_code=1)
        return 1
    seen_generation = -1
    completed = None
    seen_blocked = None
    shutdown_deadline = None

    def retain(reason):
        if completed.get("retention_reason") != reason or completed.get("shutdown_status") != "retained":
            completed.update(retained=True, shutdown_status="retained", retention_reason=reason)
            _write(root / "outcome.json", completed)
            log("retention", "retained", reason)
            guard.stamp("done" if completed["outcome"] == "succeeded" else "failed", "assignment_retained:" + reason, completed=True)

    def delayed_reason():
        return guard.delayed_close_reason(launch["runtime"]) if hasattr(guard, "delayed_close_reason") else ""

    def attempt_exit(*, delayed):
        """Persist first, then perform complete fresh checks without more log I/O."""
        nonlocal shutdown_deadline
        if delayed:
            # Avoid repeated attempt logs for an unchanged temporary obstruction.
            if not snapshot_valid(completed):
                raise ValueError("saved result changed while completion was retained")
            if reason := delayed_reason():
                retain(reason)
                return
        log("exit_attempt", "checking", "hold_released_or_expired" if delayed else "assignment_finished")
        completed["shutdown_status"] = "checking"
        _write(root / "outcome.json", completed)
        # Hook generation/session changes are excluded by state.lock. Tmux input,
        # holds and POSIX signals are separate systems: these are fresh samples,
        # not an atomic transaction across those systems.
        reason = delayed_reason() if delayed else ""
        if not snapshot_valid(completed):
            raise ValueError("saved result changed at process-exit boundary")
        held = guard.validate()
        if held or reason:
            retain("hold_active" if held else reason)
            return
        # No logging, persistence, or child reap between the last checks and signal.
        child.send_signal(signal.SIGTERM)
        shutdown_deadline = time.monotonic() + 10
        completed.update(retained=False, shutdown_status="exit_requested", retention_reason="")
        _write(root / "outcome.json", completed)
        log("exit", "requested", "hold_released_or_expired" if delayed else "assignment_finished")

    try:
        _write(root / "process.json", {"run_id": launch["run_id"], "supervisor_pid": os.getpid(), "child_pid": child.pid, "pane_identity": getattr(guard, "initial", [])})
        while child.poll() is None:
            with _locked(root):
                state = _read(root / "state.json")
                if state["generation"] != seen_generation:
                    activity = state.get("activity")
                    active_state = "failed" if activity == "StopFailure" else "waiting" if activity == "PermissionRequest" else "running"
                    guard.stamp(active_state, "assignment_" + str(activity or "active"))
                    seen_generation = state["generation"]
                    completed = None
                    seen_blocked = None
                blocked = state.get("blocked_reason")
                if blocked and blocked != seen_blocked:
                    guard.stamp("blocked", blocked)
                seen_blocked = blocked
                if completed and (blocked or completed["session_id"] != state.get("session_id")):
                    completed = None
                pending = state.get("pending")
                accepted_now = False
                if pending and time.monotonic() < pending["deadline"]:
                    if not snapshot_valid(pending):
                        raise ValueError("saved result disappeared or changed before closeout")
                    held = guard.validate()
                    # Do not publish a completed outcome until its log is durable.
                    log("completion", pending["outcome"], "held" if held else "close")
                    completed = {**pending, "retained": held, "shutdown_status": "retained" if held else "ready"}
                    _write(root / "outcome.json", completed)
                    guard.stamp("done" if pending["outcome"] == "succeeded" else "failed",
                                "assignment_retained" if held else "assignment_finished", completed=True)
                    state.update(pending=None, consumed_nonce=pending["nonce"], receipt=None)
                    _write(root / "state.json", state)
                    accepted_now = True
                    if not held:
                        attempt_exit(delayed=False)
                if completed and completed["retained"] and not accepted_now:
                    if guard.validate():
                        # A renewed hold can expire independently of the previous
                        # one while a transient attachment/composer refusal persists.
                        completed.pop("hold_end_logged", None)
                    else:
                        if not completed.get("hold_end_logged"):
                            log("hold_end", "released_or_expired")
                            completed["hold_end_logged"] = True
                        attempt_exit(delayed=True)
            if shutdown_deadline is not None and time.monotonic() >= shutdown_deadline:
                log("exit", "unconfirmed", "owned worker did not exit after managed SIGTERM")
                guard.stamp("blocked", "owned worker did not exit after managed SIGTERM")
                if completed:
                    completed["shutdown_status"] = "waiting_for_exit"
                    _write(root / "outcome.json", completed)
                shutdown_deadline = None  # No repeated signal or SIGKILL escalation.
            time.sleep(retained_poll_seconds if completed and completed["retained"] else 0.05)
        code = child.returncode
        if completed:
            if not snapshot_valid(completed):
                raise ValueError("saved result changed before recording runtime exit")
            end_reason = "retained_assignment_exited" if completed["retained"] else "managed_assignment_exit"
            completed.update(process_exit_code=code, end_reason=end_reason, shutdown_status="exited")
            _write(root / "outcome.json", completed)
            log("exit", "exited", end_reason)
            result_code = 0 if completed["outcome"] == "succeeded" and (not completed["retained"] or code == 0) else 1
            guard.stamp("done" if result_code == 0 else "failed", end_reason, completed=True, exit_code=result_code)
            return result_code
        _write(root / "outcome.json", {"run_id": launch["run_id"], "outcome": "incomplete", "process_exit_code": code,
                                      "end_reason": "process_exited_without_assignment_completion"})
        guard.stamp("failed", "process_exited_without_assignment_completion", completed=True, exit_code=1)
        return 1
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"[assignment] closeout blocked: {error}", file=sys.stderr, flush=True)
        try:
            _write(root / "closeout-error.json", {"run_id": launch["run_id"], "reason": str(error)})
            if completed:
                completed["shutdown_status"] = "closeout_error"
                _write(root / "outcome.json", completed)
        except OSError:
            pass
        try:
            guard.stamp("blocked", str(error)[:300])
        except (OSError, ValueError, subprocess.SubprocessError):
            pass
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
