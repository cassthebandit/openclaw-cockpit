package tmux

import (
	"context"
	"strings"
	"testing"
)

// TestSplitTmuxRowRoundTripsEscapedFields pins the escape algebra: any value,
// including ones full of tildes, escape tokens, and the separator itself,
// survives escape → frame → split → decode exactly.
func TestSplitTmuxRowRoundTripsEscapedFields(t *testing.T) {
	t.Parallel()

	escape := func(value string) string {
		return strings.ReplaceAll(value, "~", tmuxFieldEscape)
	}
	values := []string{
		"plain",
		"",
		"~",
		"~~",
		"~e",
		"~e~e",
		"a~~b~e::OC_FIELD::c~",
		"tail~",
		"~head",
	}
	row := make([]string, 0, len(values))
	for _, value := range values {
		row = append(row, escape(value))
	}
	got := splitTmuxRow(strings.Join(row, tmuxFieldSep))
	if len(got) != len(values) {
		t.Fatalf("field count = %d, want %d (%q)", len(got), len(values), got)
	}
	for i, want := range values {
		if got[i] != want {
			t.Fatalf("field %d = %q, want %q", i, got[i], want)
		}
	}
}

// TestEscapedTmuxFormatShape pins the generated format string so a regression
// back to raw unescaped fields cannot slip through silently.
func TestEscapedTmuxFormatShape(t *testing.T) {
	t.Parallel()

	got := escapedTmuxFormat("session_id", "@oc_goal")
	want := "#{s|~|~e|:session_id}~~#{s|~|~e|:@oc_goal}"
	if got != want {
		t.Fatalf("escapedTmuxFormat = %q, want %q", got, want)
	}
}

// TestListPanesRowOverHardCapFailsVisibly proves AC7's failure half: a row
// above the documented 1 MiB cap surfaces an explicit cap error instead of a
// panic or a fabricated empty-success snapshot. A fake runner is required
// here: tmux's own command-length limits prevent installing a >1 MiB option
// on a real server, so the oversized row cannot be produced on a disposable
// server without mutating server internals.
func TestListPanesRowOverHardCapFailsVisibly(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", tmuxRowCapBytes+1024)
	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(huge + "\n"), nil
	}}
	panes, _, err := c.listPanes(context.Background())
	if err == nil {
		t.Fatalf("listPanes over cap must fail, got %d panes", len(panes))
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Fatalf("cap error must be self-describing, got %v", err)
	}
	if panes != nil {
		t.Fatalf("no pane list may be fabricated on cap failure, got %d", len(panes))
	}
}

// TestSnapshotOverCapReturnsErrorNotEmptySuccess proves the full Snapshot
// path surfaces the cap error instead of fabricating an empty healthy wall
// (fake runner for the same command-length reason).
func TestSnapshotOverCapReturnsErrorNotEmptySuccess(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("z", tmuxRowCapBytes+1024)
	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(huge + "\n"), nil
	}}
	snap, err := c.Snapshot(context.Background())
	if err == nil {
		t.Fatalf("Snapshot over cap must fail visibly, got %d sessions", len(snap.Sessions))
	}
	if len(snap.Sessions) != 0 {
		t.Fatalf("failed snapshot must not carry fabricated sessions, got %d", len(snap.Sessions))
	}
}

// TestListSessionsRowOverHardCapFailsVisibly covers the same cap on the
// session list (fake runner for the same reason as the pane variant).
func TestListSessionsRowOverHardCapFailsVisibly(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("y", tmuxRowCapBytes+1024)
	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(huge + "\n"), nil
	}}
	sessions, err := c.listSessions(context.Background())
	if err == nil {
		t.Fatalf("listSessions over cap must fail, got %d sessions", len(sessions))
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Fatalf("cap error must be self-describing, got %v", err)
	}
}
