// File view_test.go exercises view-specific helpers.
package ui

import (
	"strings"
	"testing"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

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
