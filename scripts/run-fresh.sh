#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT"

if [[ -z "${TMUX:-}" ]]; then
  if [[ "${OPENCLAW_COCKPIT_FORCE_TMUX:-${TMUXWATCH_FORCE_TMUX:-0}}" == "1" ]]; then
    echo "openclaw-cockpit must run inside tmux. Launch via tmux new-session -s watch './scripts/run-fresh.sh'" >&2
    exit 1
  fi
  echo "[openclaw-cockpit] TMUX not detected; running directly. Set OPENCLAW_COCKPIT_FORCE_TMUX=1 to require tmux." >&2
fi

echo "Cleaning build cache..."
go clean -cache

echo "Rebuilding and running openclaw-cockpit..."
exec go run ./cmd/openclaw-cockpit "$@"
