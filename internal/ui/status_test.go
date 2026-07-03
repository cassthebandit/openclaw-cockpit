package ui

import (
	"strings"
	"testing"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

func TestBuildStatusLineShowsPaneParseWarnings(t *testing.T) {
	t.Parallel()

	m := &Model{
		width:             100,
		paneParseWarnings: 2,
	}

	got := m.buildStatusLine(100)
	if !strings.Contains(got, "2 pane row(s) hidden") {
		t.Fatalf("status line should show pane parse warning, got %q", got)
	}
}

func TestFormatStaleLineRoutesCleanupToHygieneTools(t *testing.T) {
	t.Parallel()

	got := formatStaleLine([]string{"old-worker"}, 120)
	if !strings.Contains(got, "session_hygiene.py or safe_kill.py") {
		t.Fatalf("stale line should route cleanup to hygiene tools, got %q", got)
	}
	if strings.Contains(got, "focus + X to clean") {
		t.Fatalf("stale line should not advertise disabled focus cleanup, got %q", got)
	}
}

func TestFormatCockpitSummaryIncludesAttentionAndSemanticStates(t *testing.T) {
	t.Parallel()

	session := func(name, state string) tmux.Session {
		return tmux.Session{
			ID:   name,
			Name: name,
			Windows: []tmux.Window{{
				ID:   name + "-win",
				Name: "main",
				Panes: []tmux.Pane{{
					ID:      name + "-pane",
					Session: name,
					Cockpit: &tmux.CockpitMeta{
						Kind:  "agent",
						Agent: "codex",
						State: state,
					},
				}},
			}},
		}
	}

	m := &Model{
		sessions: []tmux.Session{
			session("needs-review", "review"),
			session("route-failed", "route-fail"),
			session("clean-null", "null-safe"),
		},
	}

	got := m.formatCockpitSummary(200)
	for _, want := range []string{"cockpit items: 3", "review 1", "route-fail 1", "null-safe 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cockpit summary missing %q in %q", want, got)
		}
	}
}
