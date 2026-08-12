# Local hot reload

Hot reload is an optional contributor convenience. It is not required to install or run OpenClaw Cockpit.

## One-shot development run

Use the repository helper when you want a clean rebuild from source:

```sh
./gorunfresh --organize --preserve-colors
```

The helper clears the Go build cache, then runs `go run ./cmd/openclaw-cockpit`. It works best inside tmux. Set `OPENCLAW_COCKPIT_FORCE_TMUX=1` if your local workflow should refuse to run outside tmux.

## Watch mode

The repository includes a Poltergeist target for rebuilding and restarting the development binary after successful Go changes. This path requires compatible `poltergeist` and `polter` executables with `polter --watch` support.

```sh
./gorunfresh --watch --organize --preserve-colors
```

That command delegates to `scripts/run-watch.sh`. `scripts/run-hot.sh` is the more flexible form when you need to provide `POLTERGEIST_BIN`, `POLTER_BIN`, or a custom daemon log path.

The target configuration lives in [`../poltergeist.config.json`](../poltergeist.config.json). Failed rebuilds leave the last successful binary running; a later successful build replaces it.

## Recommended tmux loop

```sh
tmux new-session -s cockpit-dev \
  './gorunfresh --watch --organize --preserve-colors --exclude-session cockpit-dev'
```

Edit a Go file, wait for a successful rebuild, and confirm the dashboard restarts in the same tmux session. Stop the watch process with `ctrl+c` when development is finished.

If watch mode reports that `--watch` is unavailable, update the local Poltergeist installation or use the one-shot helper. Cockpit itself does not depend on Poltergeist.
