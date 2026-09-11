# PR3 performance verification

Baseline: PR2 main `54d9c94`. Three sequential 1-second benchmark repetitions per
case, baseline then optimized on the same Apple M1 Max/macOS arm64 host. Values
below are medians, not end-to-end keyboard latency or a universal speedup claim.

- Unchanged lifecycle refresh, same text storage: 25.90 ms -> 0.011 ms;
  9,332,357 -> 3,288 allocated bytes per refresh.
- Fresh snapshot with equal text in distinct allocations: 25.80 ms -> 0.039 ms;
  9,328,243 -> 3,288 bytes. This includes content-equality comparisons.
- One changed pane: 26.42 ms -> 0.920 ms; 9,355,685 -> 318,657 bytes.
- All panes changed: 30.05 ms -> 26.86 ms; roughly 9.4 MB in both. The classifier
  still runs for changed content; no meaningful all-changed speedup is claimed.
- Dirty organized wall: 6.71 ms -> 5.55 ms (17% lower); 1,387,044 -> 1,043,186
  bytes (25% lower), 43,039 -> 33,236 allocations. The time gain is below the
  aspirational 20% target, but useful without a renderer rewrite.
- Clean heartbeat/cached View: 2.23 -> 2.21 microseconds; unchanged 56 bytes,
  two allocations and zero frame rebuilds.

## Design and correctness

Lifecycle refresh snapshots every semantic classifier input by value, sharing
reuse between snapshot and per-capture updates. Removal prunes entries. In-place
metadata mutation cannot evade invalidation. The classifier itself is unchanged,
including full ANSI/OSC parsing and approval/activity precedence.

Each preview owns one body cache and one border-composition cache. Headers,
time labels, focus/hover, viewport output and info lines are produced before
lookup. Exact strings, widths and colors decide reuse; zones and hitbox geometry
are recomputed outside the cache. Entries disappear with their preview. No
unbounded historical-frame cache, dependency, daemon, polling or janitor change.

Full Go tests/race/vet, launcher regression, and uncapped pinned lint passed.
Baseline vs optimized golden frames and zones were byte-identical. Added tests
compare warm/cold frames and hitboxes across narrow/wide sizes, focus, cursor,
hover, collapse, time, Unicode/ANSI output, scroll and lifecycle transitions.
Lifecycle tests exercise mutable metadata, capture/snapshot agreement, removal
and pane ID reuse. Body tests include CJK, combining characters, emoji and OSC.
The deployed private PR2 cleanup suite also passed (105 focused tests).

## Documentation and profiling

Used [Go diagnostics](https://go.dev/doc/diagnostics) for separate CPU/allocation
profiling. Exact installed Lipgloss v2.0.4 Width documentation requires terminal
cell width, including wide characters and ANSI awareness; no byte/rune-width
substitution was made. [Lipgloss source](https://github.com/charmbracelet/lipgloss)
and [Bubbles viewport documentation](https://pkg.go.dev/charm.land/bubbles/v2@v2.1.0/viewport)
remain the rendering/scrolling contracts. Existing SQLite and tmux polling work
is deliberately deferred. CPU samples still show substantial grapheme/width
work; that is not justification to weaken Unicode correctness.

Reproduce with `go test ./internal/ui -run '^$' -bench
'Benchmark(PR3|ViewOrganizedWall|ViewNoopFastTicks)' -benchmem -count=3`.
