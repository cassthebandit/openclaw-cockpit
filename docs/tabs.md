# Tab Bar Integration

The tab strip is rendered locally with Lip Gloss in `internal/ui/state.go`.
The Bubble Tea Model owns tab selection and detail state. `internal/zone`
provides mouse hit regions; there is no BubbleApp/tabtitles dependency.

- Overview always shows the wall; the session tab appears while detail is open.
- `shift+left/right` selects tabs. `d`, `ctrl+m`, or the card maximize control
  opens detail. Escape from the session tab returns to the wall.
- Headers offer maximize/restore, collapse/expand, and hide controls. Hiding is
  local presentation only; no control closes a tmux session.
- `z` collapses a focused card, `Z` expands cards, and `c`/`C` toggle/expand
  accordion groups when input is not focused in a pane.

Tests in `state_test.go`, `handlers_test.go`, and `layout_test.go` cover tab,
mouse and keyboard ownership. Rendering owns viewport sizes and hit geometry;
keyboard navigation uses the same per-group column and width policies.
