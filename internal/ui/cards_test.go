// File cards_test.go covers card header formatting logic.
package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

// TestFormatHeaderOmitsHost ensures pane titles that match the host are hidden.
func TestFormatHeaderOmitsHost(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "s", Windows: []tmux.Window{{Name: "win"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title:        "dev-host",
		LastActivity: time.Now().Add(-time.Minute),
	}

	got := formatHeader(80, session, window, pane, false, false, false, false, "", "[x]", "dev-host")
	if strings.Contains(got, "dev-host") {
		t.Fatalf("formatHeader should omit host when title matches, got %q", got)
	}
}

// TestFormatHeaderKeepsCustomTitle ensures non-host titles remain visible.
func TestFormatHeaderKeepsCustomTitle(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "s", Windows: []tmux.Window{{Name: "win"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title:        "npm run dev",
		LastActivity: time.Now().Add(-time.Minute),
	}

	got := formatHeader(80, session, window, pane, false, false, false, false, "", "[x]", "dev-host")
	if !strings.Contains(got, "npm run dev") {
		t.Fatalf("formatHeader should keep custom title, got %q", got)
	}
}

func TestFormatHeaderUsesCockpitMetadata(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "raw-session", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title: "zsh",
		Cockpit: &tmux.CockpitMeta{
			Agent:   "codex",
			Owner:   "workshop-4",
			Project: "tmuxwatch",
			State:   "waiting",
		},
	}

	got := formatHeader(100, session, window, pane, false, false, false, false, "", "[x]", "dev-host")
	for _, want := range []string{"CODEX", "waiting", "workshop-4", "tmuxwatch"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatHeader missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "raw-session") {
		t.Fatalf("formatHeader should prefer cockpit metadata over raw session name, got %q", got)
	}
}

func TestFormatHeaderMarksDisplayOnlyMetadata(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "raw-session", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title: "zsh",
		Cockpit: &tmux.CockpitMeta{
			ContractVersion: "display-only",
			ManagedBy:       "manual_adopt",
			Agent:           "claude",
			Owner:           "workshop-4",
			Project:         "tmuxwatch",
			State:           "running",
		},
	}

	got := formatHeader(120, session, window, pane, false, false, false, false, "", "[x]", "dev-host")
	for _, want := range []string{"ADOPTED", "CLAUDE", "workshop-4", "tmuxwatch"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatHeader missing %q in %q", want, got)
		}
	}
}

func TestFormatHeaderUsesServiceSessionName(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "smonitor", Windows: []tmux.Window{{Name: "smonitor"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title: "smonitor",
		Cockpit: &tmux.CockpitMeta{
			ContractVersion: "display-only",
			ManagedBy:       "manual_adopt",
			Kind:            "service",
			Agent:           "service",
			Owner:           "Cass",
			Project:         "cameras",
			State:           "running",
		},
	}

	got := formatHeader(120, session, window, pane, false, false, false, false, "running", "[x]", "dev-host")
	for _, want := range []string{"SERVICE", "smonitor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatHeader missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "ADOPTED") || strings.Contains(got, "Cass") || strings.Contains(got, "cameras") {
		t.Fatalf("service header should stay specific and compact, got %q", got)
	}
}

func TestCockpitStateDowngradesDeadManagedRunningPane(t *testing.T) {
	t.Parallel()

	pane := tmux.Pane{
		Dead: true,
		Cockpit: &tmux.CockpitMeta{
			ContractVersion: "1",
			ManagedBy:       "agent_wall",
			State:           "running",
		},
	}

	if got := cockpitState(pane, false); got != "stale" {
		t.Fatalf("cockpitState() = %q, want stale", got)
	}
}

func TestFormatHeaderUsesSessionAttentionState(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "worker", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{Title: "zsh"}

	got := formatHeader(100, session, window, pane, false, false, false, false, "failed", "[x]", "dev-host")
	if !strings.Contains(got, "failed") {
		t.Fatalf("formatHeader should include session attention state, got %q", got)
	}
}

func TestFormatHeaderDedupesServiceFallbackPartsAndStaleState(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "AI-Alerts", Windows: []tmux.Window{{Name: "AI-Alerts"}}}
	window := session.Windows[0]
	pane := tmux.Pane{Title: "AI-Alerts"}

	got := formatHeader(120, session, window, pane, false, false, true, false, "quiet", "[x]", "dev-host")
	if strings.Count(got, "AI-Alerts") != 1 {
		t.Fatalf("formatHeader should dedupe repeated service labels, got %q", got)
	}
	if strings.Contains(got, "stale") {
		t.Fatalf("quiet service header should not show stale, got %q", got)
	}
}

func TestFormatHeaderShowsCockpitLaunchTiming(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "worker", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{Title: "zsh", Cockpit: &tmux.CockpitMeta{
		Agent:     "codex",
		StartedAt: time.Now().Add(-12 * time.Minute).UTC().Format(time.RFC3339),
	}}

	got := formatHeader(120, session, window, pane, false, false, false, false, "running", "[x]", "dev-host")
	if !strings.Contains(got, "launched") {
		t.Fatalf("formatHeader should show launch timing, got %q", got)
	}
}

func TestFormatHeaderDoesNotDuplicateDoneStateWhenDoneTimingExists(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "worker", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{Title: "zsh", Cockpit: &tmux.CockpitMeta{
		Agent:       "codex",
		State:       "done",
		CompletedAt: time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339),
	}}

	got := formatHeader(120, session, window, pane, false, false, false, false, "done", "[x]", "dev-host")
	if strings.Count(got, "done") != 1 {
		t.Fatalf("formatHeader should not duplicate done state, got %q", got)
	}
}

func TestCompactFinishedBodyLimitsTranscriptNoise(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8"}, "\n")
	got := compactFinishedBody(80, body, "done", 6)

	if strings.Contains(got, "\n7\n") || strings.HasSuffix(got, "\n8") {
		t.Fatalf("compactFinishedBody should trim finished transcript, got %q", got)
	}
	if !strings.Contains(got, "more lines") {
		t.Fatalf("compactFinishedBody should mention hidden transcript lines, got %q", got)
	}
	if active := compactFinishedBody(80, body, "running", 6); active != body {
		t.Fatalf("running body should stay unmodified")
	}
}

func TestCompactOverviewBodyLeavesDetailUncapped(t *testing.T) {
	t.Parallel()

	lines := make([]string, 0, maxOverviewBodyLines+4)
	for i := 0; i < maxOverviewBodyLines+4; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i+1))
	}
	body := strings.Join(lines, "\n")

	const budget = 12
	got := compactOverviewBody(80, body, true, budget)
	if count := strings.Count(got, "\n") + 1; count != budget {
		t.Fatalf("overview body line count = %d, want %d; body %q", count, budget, got)
	}
	if !strings.Contains(got, "open detail") {
		t.Fatalf("overview body should point to detail view, got %q", got)
	}
	if detail := compactOverviewBody(80, body, false, budget); detail != body {
		t.Fatalf("detail body should stay unmodified")
	}
}

func TestCompactOverviewBodyIgnoresTrailingViewportPadding(t *testing.T) {
	t.Parallel()

	body := "goal: quiet service\npolicy: manual\nservice running\n\n\n\n"
	const budget = 8
	got := compactOverviewBody(80, body, true, budget)
	if strings.Contains(got, "more lines") {
		t.Fatalf("blank viewport padding should not be treated as hidden content, got %q", got)
	}
	if count := strings.Count(got, "\n") + 1; count != budget {
		t.Fatalf("overview body should be padded to fixed height, got %d lines in %q", count, got)
	}
}

func TestCockpitCleanupLineBoundsEvidencePath(t *testing.T) {
	t.Parallel()

	pane := tmux.Pane{Cockpit: &tmux.CockpitMeta{
		CleanupPolicy: "kill_on_done",
		TTL:           "30m",
		HoldReason:    "review",
		EndReason:     "process_exit_nonzero",
		ProgressPath:  "progress.jsonl",
		EvidencePath:  "/Users/cass/.openclaw/workspace/memory/runs/secretish/result.txt",
	}}

	got := cockpitCleanupLine(pane)
	for _, want := range []string{"policy: kill_on_done", "ttl: 30m", "hold: review", "end: process_exit_nonzero", "progress: progress.jsonl", "evidence: result.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cleanup line missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "/Users/cass") {
		t.Fatalf("cleanup line should not expose absolute path, got %q", got)
	}
}

func TestCockpitAttentionLineSurfacesHiddenPaneState(t *testing.T) {
	t.Parallel()

	session := tmux.Session{
		Name: "multi",
		Windows: []tmux.Window{{
			Name: "main",
			Panes: []tmux.Pane{
				{ID: "%1", Active: true, Cockpit: &tmux.CockpitMeta{State: "running"}},
				{ID: "%2", Cockpit: &tmux.CockpitMeta{State: "failed"}},
			},
		}},
	}
	activePane := session.Windows[0].Panes[0]
	state := sessionAttentionState(nil, session)

	got := cockpitAttentionLine(80, nil, session, activePane, state)
	if !strings.Contains(got, "attention: failed in another pane") {
		t.Fatalf("attention line should surface hidden failure, got %q", got)
	}
}
