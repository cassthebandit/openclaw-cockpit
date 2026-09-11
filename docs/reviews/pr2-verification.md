# PR2 correctness verification

Scope: recognizable session headers with source-correct launch timing; independent
runtime-card refresh; bounded subprocess pipes; evidence symlink containment;
strict one-document JSON configuration; dashboard-only launcher recovery.

Verified locally on macOS arm64:
- `go test ./... -count=1` and `go test -race ./... -count=1`
- `go vet ./...`, golangci-lint (zero issues), gofumpt
- Darwin, Linux, Windows cross-builds on amd64 and arm64 (compilation only)
- Promoted audit regressions, including five terminal sizes
- Unix child-process timeout/leak regression
- Real tmux launcher test on a disposable socket: absent/dead dashboard,
  repeat launch, unrelated worker preserved

Launcher tests do not claim a production Dock deployment or graphical click
verification. Those remain deployment checks. The existing workspace launcher,
if present, must support `--dashboard-only`; deploy its companion change first.

On Unix the runtime snapshot subprocess has its own process group, which is
terminated on timeout and cleaned up after return. On Windows the native direct
process cancellation and bounded pipe wait apply; descendant-tree termination
is not claimed. Evidence paths are resolved and checked before reads; this is
not an atomic filesystem sandbox against concurrent symlink replacement.

No cleanup authority was added to Cockpit. Production services were not changed.
