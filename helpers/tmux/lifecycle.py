#!/usr/bin/env python3
"""Validated helper policy. Loading configuration never mutates a terminal."""
from __future__ import annotations
import argparse
import json
import math
import os
from pathlib import Path

DEFAULTS = {
    "adopt_existing_exited": False, "existing_session_mode": "selected-only",
    "cleanup_whitelist": [], "cleanup_blacklist": [],
    "live_retirement": "off", "live_retirement_quiet_seconds": 1800,
    "inspector_protected_sessions": [],
    "closeout_default": "close", "completed_retention_seconds": 60,
    "failed_retention_seconds": 180, "teardown_grace_seconds": 60,
    "active_idle_mark_seconds": 180, "adopted_grace_seconds": 3600,
    "temporary_hold_hours": 24, "cleanup_interval_seconds": 60,
    "inspector_interval_seconds": 5, "inspector_stale_seconds": 1800,
    "max_kills": 10, "log_max_bytes": 5242880,
    "state_dir": "~/.local/state/openclaw-cockpit", "archive_dir": "",
    "status_file": "", "log_dir": "", "inspector_log_dir": "",
}
ENVIRONMENT = {
    "OPENCLAW_COCKPIT_STATE_DIR": "state_dir",
    "CASS_TMUX_HYGIENE_INTERVAL": "cleanup_interval_seconds",
    "CASS_TMUX_JANITOR_LOG_MAX_BYTES": "log_max_bytes",
    "CASS_TMUX_HYGIENE_STATUS_FILE": "status_file",
    "CASS_TMUX_INSPECTOR_INTERVAL": "inspector_interval_seconds",
    "CASS_TMUX_HYGIENE_LOG_DIR": "log_dir",
    "CASS_TMUX_INSPECTOR_LOG_DIR": "inspector_log_dir",
}
PATHS = {"state_dir", "archive_dir", "status_file", "log_dir", "inspector_log_dir"}
POSITIVE = {"cleanup_interval_seconds", "inspector_interval_seconds", "temporary_hold_hours", "max_kills", "log_max_bytes", "live_retirement_quiet_seconds"}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def validate(values: dict) -> None:
    unknown = values.keys() - DEFAULTS.keys()
    if unknown:
        raise ValueError("unknown lifecycle setting: " + ", ".join(sorted(unknown)))
    for key, value in values.items():
        if key == "adopt_existing_exited":
            if type(value) is not bool:
                raise ValueError(f"{key}: expected boolean")
        elif key in {"existing_session_mode", "live_retirement"}:
            choices = ("selected-only", "all-except-protected") if key == "existing_session_mode" else ("off", "verified", "observed")
            if value not in choices:
                raise ValueError(f"{key}: expected one of {choices}")
        elif key in {"cleanup_whitelist", "cleanup_blacklist"}:
            if not isinstance(value, list) or len(value) > 256:
                raise ValueError(f"{key}: expected at most 256 typed selectors")
            for selector in value:
                if not isinstance(selector, dict) or len(selector) != 1 or next(iter(selector)) not in {"exact", "glob"}:
                    raise ValueError(f"{key}: selector requires exactly one exact or glob key")
                kind, pattern = next(iter(selector.items()))
                if not isinstance(pattern, str) or not 1 <= len(pattern) <= 256 or any(ord(c) < 32 or ord(c) == 127 for c in pattern):
                    raise ValueError(f"{key}: selector must be 1..256 characters without controls")
                if kind == "glob" and any(c in pattern for c in "?[]"):
                    raise ValueError(f"{key}: glob supports only *")
        elif key == "inspector_protected_sessions":
            if not isinstance(value, list) or any(not isinstance(item, str) or not item or any(ord(c) < 32 for c in item) for item in value):
                raise ValueError("inspector_protected_sessions: expected nonempty session-name strings")
        elif key in PATHS:
            if not isinstance(value, str) or (key == "state_dir" and not value):
                raise ValueError(f"{key}: expected a nonempty path string")
            if any(ord(character) < 32 or ord(character) == 127 for character in value):
                raise ValueError(f"{key}: path must not contain control characters")
            try:
                absolute = not value or Path(value).expanduser().is_absolute()
            except RuntimeError as error:
                raise ValueError(f"{key}: home directory cannot be resolved") from error
            if not absolute:
                raise ValueError(f"{key}: path must be absolute or start with ~/")
        elif key == "closeout_default":
            if value not in ("close", "keep_open"):
                raise ValueError("closeout_default: expected close or keep_open")
        elif type(value) not in (int, float) or not math.isfinite(value) or value < 0 or (key in POSITIVE and value == 0):
            raise ValueError(f"{key}: expected a {'positive' if key in POSITIVE else 'nonnegative'} finite number")
        elif key != "temporary_hold_hours" and (not isinstance(value, int) or value > 2147483647):
            raise ValueError(f"{key}: expected an integer <= 2147483647")
        elif key == "temporary_hold_hours" and value > 8760:
            raise ValueError("temporary_hold_hours: maximum is 8760")


def load(path: str | None = None, *, overrides: dict | None = None, environ=None) -> tuple[dict, dict]:
    env = os.environ if environ is None else environ
    explicit = path or env.get("OPENCLAW_COCKPIT_LIFECYCLE_CONFIG")
    source = Path(explicit or "~/.config/openclaw-cockpit/lifecycle.json").expanduser()
    values = dict(DEFAULTS)
    sources = dict.fromkeys(values, "built-in")
    if source.exists() or explicit:
        try:
            content = json.loads(source.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
        except (OSError, ValueError) as error:
            raise ValueError(f"lifecycle config {source}: {error}") from error
        if not isinstance(content, dict):
            raise ValueError(f"lifecycle config {source}: expected JSON object")
        validate(content)
        values.update(content)
        sources.update(dict.fromkeys(content, str(source)))
    for variable, key in ENVIRONMENT.items():
        if variable in env:
            raw = env[variable]
            try:
                value = raw if key in PATHS else int(raw)
            except ValueError as error:
                raise ValueError(f"{variable}: expected integer") from error
            validate({key: value})
            values[key] = value
            sources[key] = "environment:" + variable
    supplied = {key: value for key, value in (overrides or {}).items() if value is not None}
    validate(supplied)
    values.update(supplied)
    sources.update(dict.fromkeys(supplied, "explicit option"))
    validate(values)
    values["state_dir"] = str(Path(values["state_dir"]).expanduser())
    for key, suffix in (("archive_dir", "cleanup-ledger"), ("status_file", "hygiene/status.json"), ("log_dir", "logs"), ("inspector_log_dir", "logs")):
        values[key] = str(Path(values[key]).expanduser()) if values[key] else str(Path(values["state_dir"]) / suffix)
    return values, sources


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config")
    parser.add_argument("--dump-config", action="store_true")
    parser.add_argument("--validate-config", action="store_true")
    args = parser.parse_args(argv)
    try:
        values, sources = load(args.config)
    except ValueError as error:
        parser.error(str(error))
    print(json.dumps({"config": values, "sources": sources} if args.dump_config else {"valid": True}, indent=2))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
