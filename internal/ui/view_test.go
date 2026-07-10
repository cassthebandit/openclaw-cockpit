// File view_test.go exercises view-specific helpers.
package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func numberedLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%02d", i)
	}
	return strings.Join(lines, "\n")
}

func TestClampFooterKeepsTailLines(t *testing.T) {
	t.Parallel()

	status := strings.Join([]string{
		"helper hints",
		"cockpit summary",
		"runtime timeline",
		"stale warning",
		"janitor: stale 45m old",
	}, "\n")
	got := clampFooter(status, maxFooterHeight)
	if countLines(got) != maxFooterHeight {
		t.Fatalf("clamped footer lines = %d, want %d", countLines(got), maxFooterHeight)
	}
	if !strings.Contains(got, "janitor: stale") {
		t.Fatalf("footer clamp must keep janitor health (tail), got %q", got)
	}
	if strings.Contains(got, "helper hints") {
		t.Fatalf("footer clamp should drop the helper line first, got %q", got)
	}
}

func TestClampFooterLeavesShortStatusAlone(t *testing.T) {
	t.Parallel()

	status := "helper hints\njanitor: ok"
	if got := clampFooter(status, maxFooterHeight); got != status {
		t.Fatalf("short footer changed: %q", got)
	}
	if got := clampFooter(status, 0); got != "" {
		t.Fatalf("zero-limit footer = %q, want empty", got)
	}
}

func TestWindowGridFitsPinsOffsetZero(t *testing.T) {
	t.Parallel()

	m := &Model{width: 40, pageOffset: 5}
	content := numberedLines(6)
	out := m.windowGrid(content, 10)
	if m.pageScrollEngaged {
		t.Fatal("page scroll should not engage when content fits")
	}
	if m.pageOffset != 0 {
		t.Fatalf("offset should pin to 0 when content fits, got %d", m.pageOffset)
	}
	if out != content {
		t.Fatalf("fitting content should pass through unchanged, got %q", out)
	}
}

func TestWindowGridEngagesOnOverflow(t *testing.T) {
	t.Parallel()

	m := &Model{width: 40}
	out := m.windowGrid(numberedLines(30), 10)
	if !m.pageScrollEngaged {
		t.Fatal("page scroll should engage when the current layout overflows")
	}
	if got := countLines(out); got != 10 {
		t.Fatalf("windowed height = %d, want exactly 10", got)
	}
	if m.pageMaxOffset != 22 { // 30 total - (10 - 2 indicator lines)
		t.Fatalf("pageMaxOffset = %d, want 22", m.pageMaxOffset)
	}
	// At the top, only a bottom "more" indicator shows.
	if !strings.Contains(out, "▼") || !strings.Contains(out, "more") {
		t.Fatalf("expected a bottom more-indicator at the top of the wall: %q", out)
	}
}

func TestWindowGridClampsOffsetToBottom(t *testing.T) {
	t.Parallel()

	m := &Model{width: 40, pageOffset: 999}
	out := m.windowGrid(numberedLines(30), 10)
	if m.pageOffset != 22 {
		t.Fatalf("offset should clamp to max 22, got %d", m.pageOffset)
	}
	if !strings.Contains(out, "▲ 22 more") {
		t.Fatalf("expected a top more-indicator at the bottom of the wall: %q", out)
	}
}

func TestWindowGridReengagesWhenContentGrows(t *testing.T) {
	t.Parallel()

	m := &Model{width: 40}
	// Collapsed layout fits: no scroll.
	m.windowGrid(numberedLines(5), 10)
	if m.pageScrollEngaged {
		t.Fatal("small content should not engage page scroll")
	}
	// Expanding a group overflows the CURRENT layout: scroll engages.
	m.windowGrid(numberedLines(40), 10)
	if !m.pageScrollEngaged {
		t.Fatal("grown content should engage page scroll on the current layout")
	}
	m.pageOffset = 20
	// Shrinking back to a fitting layout disengages and pins the offset to 0.
	m.windowGrid(numberedLines(4), 10)
	if m.pageScrollEngaged || m.pageOffset != 0 {
		t.Fatalf("shrunk content should disengage and pin offset 0, got engaged=%v offset=%d", m.pageScrollEngaged, m.pageOffset)
	}
}

func TestClampHeight(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		content string
		limit   int
		want    string
	}{
		"zero limit":      {content: "line", limit: 0, want: ""},
		"empty content":   {content: "", limit: 3, want: ""},
		"under the cap":   {content: "a\nb", limit: 3, want: "a\nb"},
		"trim trailing":   {content: "a\nb\nc", limit: 2, want: "a\nb"},
		"trim final":      {content: "a\nb\n", limit: 2, want: "a\nb"},
		"no newline tail": {content: "a\nb", limit: 1, want: "a"},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := clampHeight(tc.content, tc.limit)
			if got != tc.want {
				t.Fatalf("clampHeight(%q, %d) = %q, want %q", tc.content, tc.limit, got, tc.want)
			}
		})
	}
}

func TestEmptyStateView(t *testing.T) {
	t.Parallel()

	view := emptyStateView(60, 10)
	if !strings.Contains(view, "No tmux sessions detected.") {
		t.Fatalf("empty state missing headline: %q", view)
	}
	if !strings.Contains(view, "tmux new -s demo") {
		t.Fatalf("empty state missing helper: %q", view)
	}
	if got := countLines(view); got != 10 {
		t.Fatalf("expected 10 lines for centered placement, got %d", got)
	}
}

func TestPlaceGridContent(t *testing.T) {
	t.Parallel()

	view := placeGridContent("a\nb", 20, 4)
	if countLines(view) != 4 {
		t.Fatalf("expected 4 lines, got %d", countLines(view))
	}
	if !strings.Contains(view, "a") {
		t.Fatalf("placed view missing content: %q", view)
	}

	if got := placeGridContent("irrelevant", 10, 0); got != "" {
		t.Fatalf("expected empty string for zero height, got %q", got)
	}
}

func TestViewBundlesPulseIntoTitleBar(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, 0, 0, nil, false, true)
	m.width = 100
	m.height = 12
	m.sessions = []tmux.Session{{
		ID:   "$dev",
		Name: "dev",
		Windows: []tmux.Window{{
			ID:   "@1",
			Name: "main",
			Panes: []tmux.Pane{{
				ID:      "%1",
				Session: "$dev",
			}},
		}},
	}}
	vp := viewportFor(innerDimension{width: 40, height: 3})
	m.previews["$dev"] = &sessionPreview{viewport: &vp, paneID: "%1"}

	got := m.View().Content
	if !strings.Contains(got, "OpenClaw Cockpit · Pulse") {
		t.Fatalf("title should include pulse view label, got %q", got)
	}
	if strings.Contains(got, "\n Overview\n") {
		t.Fatalf("overview should not render as its own header row: %q", got)
	}
}

func TestViewUsesCellMotionMouseMode(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, 0, 0, nil, false, true)
	m.width = 20
	m.height = 5

	got := m.View()
	if got.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want %v", got.MouseMode, tea.MouseModeCellMotion)
	}
}
