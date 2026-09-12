"""Concrete runtime command preparation; no lifecycle or cleanup ownership."""
from __future__ import annotations
import argparse
TUI_TERM = "xterm-256color"
CLAUDE_UNSET_ENV = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"]

CLAUDE_SUBSCRIPTION_UNSET_ENV = CLAUDE_UNSET_ENV + [
    "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
    "ANTHROPIC_PROFILE", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR",
    "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR",
]


def build_claude_tui_command(args: argparse.Namespace) -> list[str]:
    owned = {"--model", "--effort", "--permission-mode", "--name", "--debug-file"}
    for extra in args.claude_arg:
        if extra.split("=", 1)[0] in owned:
            raise SystemExit("Claude extra argument conflicts with a launcher-owned option; use its top-level field")
    command = ["env"]
    unset = CLAUDE_SUBSCRIPTION_UNSET_ENV if getattr(args, "auth_route", "subscription") == "subscription" else []
    for var in unset:
        command.extend(["-u", var])
    command.append(f"TERM={TUI_TERM}")
    command.extend(
        [
            "claude",
            "--model",
            args.model,
            "--effort",
            args.effort,
            "--permission-mode",
            args.permission_mode,
            "--name",
            args.claude_name or args.name,
        ]
    )
    if args.debug_file:
        command.extend(["--debug-file", args.debug_file])
    for extra in args.claude_arg:
        command.append(extra)
    return command

def build_codex_tui_command(args: argparse.Namespace) -> list[str]:
    command = [
        "env",
        "CODEX_HOME=" + args.codex_home,
        f"TERM={TUI_TERM}",
        "codex",
        "--model",
        args.model,
        "--ask-for-approval",
        args.ask_for_approval,
        "--sandbox",
        args.sandbox,
    ]
    if getattr(args, "effort", None):
        command.extend(["-c", f'model_reasoning_effort="{args.effort}"'])
    if args.cd:
        command.extend(["--cd", args.cd])
    if args.no_alt_screen:
        command.append("--no-alt-screen")
    for add_dir in args.add_dir:
        command.extend(["--add-dir", add_dir])
    for extra in args.codex_arg:
        command.append(extra)
    return command

def build_agy_tui_command(args: argparse.Namespace) -> list[str]:
    command = ["agy"]
    if args.model:
        command.extend(["--model", args.model])
    if args.agy_project:
        command.extend(["--project", args.agy_project])
    if getattr(args, "new_project", False):
        command.append("--new-project")
    if args.sandbox:
        command.append("--sandbox")
    if args.log_file:
        command.extend(["--log-file", args.log_file])
    for add_dir in args.add_dir:
        command.extend(["--add-dir", add_dir])
    for extra in args.agy_arg:
        command.append(extra)
    return command
