"""Existing-session policy and observations; hygiene owns all effects and storage.

Profiles are deliberately version-bound. Empty proof means unavailable, never
an operator-configurable bypass of the native receiver/gesture test requirement.
"""
from __future__ import annotations

import hashlib
import json
import os
import re
import stat
from fnmatch import fnmatchcase
from pathlib import Path

try:
    from . import native_runtimes, holds
except ImportError:
    import native_runtimes, holds

PROFILES = native_runtimes.PROFILES
process_path = native_runtimes.process_path
MAX_RESULT_BYTES = 16 * 1024 * 1024
MAX_RECORDS = 256
RISK = ("Observed only: background/scheduled telemetry unavailable; owner attests no intended "
        "detached work. Sampling cannot see every event or atomically lock the runtime. "
        "A raced exit gesture can edit a draft or lose in-memory work.")


def matches(name, selectors):
    return any(name == s["exact"] if "exact" in s else fnmatchcase(name, s["glob"]) for s in selectors)


def selected(name, config):
    return config["existing_session_mode"] == "all-except-protected" or matches(name, config["cleanup_whitelist"])


def identity(p):
    # process birth is separately bound for live release; dead processes disappear
    # from ps, but must still match their retained attempt for final archival.
    return hashlib.sha256(json.dumps((p.server_socket, p.server_pid, p.server_started, p.server_session_id,
                                     p.session, p.created, p.pane, p.pid)).encode()).hexdigest()


def record_for(state, p):
    record = state.get("adoptions", {}).get(identity(p), {})
    return dict(record) if isinstance(record, dict) else {}


def protection(panes, config, *, adoption=False, record=None):
    for p in panes:
        if matches(p.session, config["cleanup_blacklist"]):
            return "blacklist_protected"
        if p.command == "openclaw-cockpit" or p.session in config["inspector_protected_sessions"] or p.meta.get("kind") in {"service", "viewer", "runtime"}:
            return "service_protected"
        if p.self_ancestor != "0":
            return "self_identity_unknown_or_protected"
        if adoption:
            if p.meta.get("launch_id"):
                recovery = native_runtimes.assignment_recovery
                if not record or not recovery.bound(p, record):
                    return "managed_launch_owns_closeout"
                if p.dead:
                    if not record.get("attempt"):
                        return "managed_recovery_exit_not_requested"
                elif reason := recovery.observe(p, record["assignment_recovery"])[1]:
                    return reason
            if p.meta.get("managed_by", "") not in {"", "agent_wall", "tmux_inspector"}:
                return "ambiguous_owner_protected"
            if holds.review_active(p.meta):
                return "hold_reason_active"
            if p.meta.get("keep_open") == "1":
                return "explicit_keep_open"
            policy = p.meta.get("cleanup_policy", "")
            if policy in {"manual", "hide"} and not (policy == "manual" and p.meta.get("managed_by") == "tmux_inspector"):
                return "manual_policy_protected"
    return ""


def topology(panes):
    if len(panes) != 1:
        return "unsupported_topology"
    p = panes[0]
    if not (p.server_socket and p.server_pid.isdigit() and p.server_started.isdigit()
            and re.fullmatch(r"\$\d+", p.server_session_id) and re.fullmatch(r"%\d+", p.pane)
            and p.created.isdigit() and p.pid.isdigit()):
        return "server_identity_unknown"
    if (p.session_windows, p.window_panes, p.window_linked, p.session_grouped) != ("1", "1", "0", "0"):
        return "unsupported_topology"
    if p.session_attached != "0" or p.pane_in_mode != "0":
        return "attached_copy_mode_or_unknown"
    return ""


def result_bytes(path):
    p = Path(path)
    if not p.is_absolute():
        raise ValueError("release_result_requires_absolute_path")
    fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        before = os.fstat(fd)
        if not stat.S_ISREG(before.st_mode) or not 0 < before.st_size <= MAX_RESULT_BYTES:
            raise ValueError("release_result_requires_bounded_nonempty_regular_file")
        with os.fdopen(fd, "rb", closefd=False) as stream:
            data = stream.read(MAX_RESULT_BYTES + 1)
        after = os.fstat(fd)
        if (before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_size, after.st_mtime_ns, after.st_ctime_ns) or len(data) != before.st_size:
            raise ValueError("release_result_changed")
        return data, hashlib.sha256(data).hexdigest()
    finally:
        os.close(fd)


def legacy_wrapper(path, runtime_name="claude"):
    return native_runtimes.legacy_wrapper(path, result_bytes, runtime_name)


def runtime(p, profile, record):
    return native_runtimes.runtime(p, profile, record, process_path=process_path,
                                   legacy_wrapper=legacy_wrapper)


def observe(h, p, record):
    profile = PROFILES.get(record.get("profile"))
    if profile is None:
        return {}, "unsupported_runtime_profile"
    if profile.get("redraws_while_idle") and record.get("screen_sampling") is not True:
        return {}, "screen_sampling_attestation_required"
    if not profile["proof"] or profile.get("proof_pending"):
        return {}, "runtime_profile_unproven:" + record["profile"]
    if any(any(c in str(v) for c in "#,{}\n\r") for v in h.dead_retirement_expectations(p).values()):
        return {}, "unsafe_guard_literal"
    if p.remain_on_exit != "on":
        return {}, "exit_retention_unavailable"
    if (p.pane_input_off, p.pane_pipe, p.pane_synchronized) != ("0", "0", "0"):
        return {}, "input_pipe_synchronization_or_unknown"
    if not p.window_activity.isdigit():
        return {}, "activity_observation_unavailable"
    instance, reason = runtime(p, profile, record)
    if reason:
        return {}, reason
    capture_args = ["capture-pane", "-p"]
    if profile["capture_ansi"]:
        capture_args.append("-e")
    cp = h.run_tmux(*capture_args, "-t", p.pane, "-S", "-240", check=False)
    if cp.returncode:
        return {}, "capture_observation_failed"
    # Raw text hash includes drafts, footers and activity; never normalize them away.
    reason = native_runtimes.prompt_reason(h, profile, cp.stdout)
    if reason:
        return {}, reason
    try:
        _, digest = result_bytes(record["result_path"])
    except (OSError, ValueError, KeyError) as error:
        return {}, "result_observation_failed:" + str(error)
    if digest != record.get("result_sha256"):
        return {}, "release_result_changed"
    return {"runtime": instance, "capture_sha256": hashlib.sha256(cp.stdout.encode()).hexdigest(),
            "result_sha256": digest, "window_activity": "screen-sampling-owner-attested" if record.get("screen_sampling") is True else p.window_activity,
            "capture": "success", "process_inventory": "success", "background_telemetry": "unavailable",
            "risk": RISK}, ""


def plan(h, panes, args, state, config, now):
    p = panes[0]
    record = record_for(state, p)
    enabled = config["adopt_existing_exited"] or config["live_retirement"] != "off" or bool(record)
    if not enabled:
        return None
    # Existing-session opt-in must not seize ordinary managed batch/smoke jobs.
    # Explicitly selected legacy sessions (or an existing enrollment) still use
    # adoption; unselected complete contracts keep their established owner.
    if not record and not selected(p.session, config) and all(h.full_contract(pane) for pane in panes):
        return None
    # Launch-owned jobs must continue through the original managed classifier.
    if any(pane.meta.get("launch_id") for pane in panes):
        if not record or not all(native_runtimes.assignment_recovery.bound(pane, record) for pane in panes):
            return None
    item = h.result(session=p.session, action="skip", reason="not_selected", panes=panes,
                    policy_source="adopted_dead" if p.dead else "adopted_observed")
    item.update(adoption_identity=identity(p), adoption_record=record,
                janitor_state="retaining_exited" if p.dead else "retained_until_exit")
    def retain(reason, *, invalidate=True):
        if invalidate and record.get("release_id") and not record.get("attempt"):
            record["invalidated"] = reason
            record.pop("quiet_since", None)
        item["reason"] = reason
        if any(word in reason for word in ("unavailable", "unknown", "failed")):
            item["janitor_state"] = "observations_unavailable"
        elif "protected" in reason or reason in {"hold_reason_active", "explicit_keep_open"}:
            item["janitor_state"] = "protected"
        elif reason == "owner_release_required" or reason.startswith("release_invalidated"):
            item["janitor_state"] = "release_pending_observation"
        return item
    reason = protection(panes, config, adoption=True, record=record) or topology(panes)
    if reason:
        return retain(reason)
    if not selected(p.session, config):
        return retain("not_selected")
    if not record and len(state.get("adoptions", {})) >= MAX_RECORDS:
        item.pop("adoption_record")
        item.pop("adoption_identity")
        return retain("adoption_record_capacity_reached")
    if p.dead:
        if not config["adopt_existing_exited"] and not (record.get("attempt") and config["live_retirement"] == "observed"):
            return retain("dead_adoption_disabled")
        if not p.dead_status.isdigit():
            return retain("dead_exit_metadata_unavailable")
        if reason := h.dead_retirement_refusal(panes):
            return retain(reason)
        first = record.setdefault("dead_observed_at", h.isoformat(now))
        started = h.parse_iso(first)
        if started is None or started > now:
            return retain("dead_observation_time_invalid")
        retention = config["failed_retention_seconds"] if p.dead_status != "0" else config["completed_retention_seconds"]
        deadline = started + h.timedelta_seconds(retention + config["teardown_grace_seconds"])
        item.update(reason="adopted_dead_retention", kill_not_before=h.isoformat(deadline), state="exited_unknown_outcome")
        if record.get("attempt"):
            item["pre_exit_archive"] = record["attempt"].get("archive_path", "")
        if now >= deadline:
            item.update(action="kill", reason="adopted_dead_ready", tmux_target=p.server_session_id, janitor_state="cleanup_pending")
        return item
    if record.get("attempt"):
        reason = "shutdown_unconfirmed"
        if p.command in {"sh", "bash", "zsh", "fish"}:
            profile = PROFILES.get(record.get("profile"), {})
            if not profile.get("wrapper"):
                reason = "retained_exited_to_shell"
            elif runtime(p, profile, record)[1] == "runtime_child_missing":
                reason = "retained_exited_to_shell"
        item["janitor_state"] = reason
        return retain(reason, invalidate=False)
    if config["live_retirement"] != "observed":
        return retain("verified_unavailable_for_adopted" if config["live_retirement"] == "verified" else "live_retirement_off")
    if not record.get("release_id"):
        return retain("owner_release_required", invalidate=False)
    if record.get("invalidated"):
        return retain("release_invalidated:" + record["invalidated"], invalidate=False)
    evidence, reason = observe(h, p, record)
    if reason:
        return retain(reason)
    if record.get("baseline") != evidence:
        return retain("new_activity_invalidated_release")
    previous = h.parse_iso(record.get("last_observed", ""))
    tolerance = max(5, config["cleanup_interval_seconds"] * 3)
    if previous is None or not 0 <= (now - previous).total_seconds() <= tolerance:
        record["quiet_since"] = h.isoformat(now)
    record["last_observed"] = h.isoformat(now)
    since = h.parse_iso(record.setdefault("quiet_since", h.isoformat(now)))
    if since is None or since > now:
        return retain("quiet_observation_time_invalid")
    deadline = since + h.timedelta_seconds(config["live_retirement_quiet_seconds"] + config["teardown_grace_seconds"])
    item.update(reason="observing", janitor_state="observing", observations=evidence, kill_not_before=h.isoformat(deadline))
    if now >= deadline:
        item.update(action="request_exit", reason="observed_retirement_ready", janitor_state="retirement_ready")
    return item
