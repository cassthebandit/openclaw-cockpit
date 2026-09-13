#!/usr/bin/env python3
"""Foreground helper service loops. A service manager owns starting/stopping them."""
from __future__ import annotations
import argparse
import json
from pathlib import Path
import subprocess
import sys
import time
try:
    from . import lifecycle
except ImportError:
    import lifecycle


def cycle(service: str, config: dict, config_path: str | None) -> int:
    root = Path(__file__).resolve().parent
    if service == "hygiene":
        commands = [[sys.executable, "-B", str(root / "session_hygiene.py"), "apply", "--policy", policy, "--json"] for policy in ("smoke", "kill-safe")]
        log = Path(config["log_dir"]) / "janitor.log"
    else:
        commands = [[sys.executable, "-B", str(root / "tmux_inspector.py"), "annotate", "--json", "--stale-seconds", str(config["inspector_stale_seconds"])]]
        for session in config["inspector_protected_sessions"]:
            commands[0].extend(["--protect-session", session])
        log = Path(config["inspector_log_dir"]) / "inspector.log"
    # Validate/config and log access must succeed before any mutating child.
    log.parent.mkdir(parents=True, exist_ok=True)
    if log.exists() and log.stat().st_size >= config["log_max_bytes"]:
        log.replace(log.with_suffix(".log.1"))
    status = 0
    with log.open("a", encoding="utf-8") as stream:
        for command in commands:
            label = command[command.index("--policy") + 1] if "--policy" in command else service
            stream.write(f"=== {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())} {label} ===\n")
            stream.flush()
            if service == "hygiene" and config_path:
                command.extend(["--lifecycle-config", config_path])
            code = subprocess.run(command, stdout=stream, stderr=stream).returncode
            status = max(status, code if code >= 0 else 128 - code)
    return status


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("service", choices=("hygiene", "inspector"))
    parser.add_argument("--config")
    parser.add_argument("--once", action="store_true", help="One configured cycle, then exit.")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    try:
        config, _ = lifecycle.load(args.config)
    except ValueError as error:
        parser.error(str(error))
    interval = config["cleanup_interval_seconds" if args.service == "hygiene" else "inspector_interval_seconds"]
    if args.dry_run:
        print(json.dumps({"service": args.service, "interval": interval, "config": config}, indent=2))
        return 0
    while True:
        try:
            status = cycle(args.service, config, args.config)
        except OSError as error:
            print(f"{args.service}: {error}", file=sys.stderr)
            return 1
        if args.once:
            return status
        time.sleep(interval)

if __name__ == "__main__":
    raise SystemExit(main())
