// File cards_test.go covers card header formatting logic.
package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
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

func TestFormatHeaderUsesCompactOpenClawRuntimeHeader(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "openclaw-runtime:flow-1", Windows: []tmux.Window{{Name: "taskflow"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title:        "backend packet",
		LastActivity: time.Now().Add(-2 * time.Minute),
		Cockpit: &tmux.CockpitMeta{
			ContractVersion: "runtime-card.v1",
			ManagedBy:       "openclaw_runtime_snapshot",
			Kind:            "runtime",
			Agent:           "taskflow",
			Owner:           "agent:main:discord:channel:1",
			Project:         "OpenClaw Runtime",
			Goal:            "backend packet",
			State:           "blocked",
			DisplayStatus:   "blocked",
			DisplayGroup:    "needs_attention",
		},
	}

	got := formatHeader(140, session, window, pane, false, false, false, false, "blocked", "[x]", "dev-host")
	for _, want := range []string{"TASKFLOW", "blocked", "backend packet", "last"} {
		if !strings.Contains(got, want) {
			t.Fatalf("runtime header missing %q in %q", want, got)
		}
	}
	for _, forbidden := range []string{"ADOPTED", "agent:main", "discord:channel", "OpenClaw Runtime"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("runtime header leaked %q in %q", forbidden, got)
		}
	}
	if strings.Count(got, "blocked") != 1 {
		t.Fatalf("runtime header should not duplicate state, got %q", got)
	}
}

func TestFormatHeaderShowsGroupedRuntimeBadge(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "openclaw-runtime:grouped", Windows: []tmux.Window{{Name: "cron"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title:        "qmd-sidecar-live-shadow-collector-step06b",
		LastActivity: time.Now().Add(-2 * time.Minute),
		Cockpit: &tmux.CockpitMeta{
			ContractVersion:    "runtime-card.v1",
			ManagedBy:          "openclaw_runtime_snapshot",
			Kind:               "runtime",
			Agent:              "cron",
			Goal:               "qmd-sidecar-live-shadow-collector-step06b",
			State:              "failed",
			DisplayStatus:      "failed",
			DisplayGroup:       "needs_attention",
			GroupedRecordCount: "4",
		},
	}

	got := formatHeader(140, session, window, pane, false, false, false, false, "failed", "[x]", "dev-host")
	if !strings.Contains(got, "x4") {
		t.Fatalf("runtime header missing grouped badge: %q", got)
	}
}

func TestCockpitEvidenceLineCompactsGroupedEvidence(t *testing.T) {
	t.Parallel()

	meta := &tmux.CockpitMeta{
		EvidencePath:       "id-4,id-3,id-2,id-1",
		GroupedRecordCount: "4",
	}

	got := cockpitEvidenceLine(meta)
	if got != "evidence: 4 ids, open detail for full list" {
		t.Fatalf("cockpitEvidenceLine() = %q", got)
	}
}

func TestCockpitEvidenceLineCountsIDsNotGroupedRecords(t *testing.T) {
	t.Parallel()

	meta := &tmux.CockpitMeta{
		EvidencePath:       "task-4,task-3,task-2,task-1",
		GroupedRecordCount: "2",
	}

	got := cockpitEvidenceLine(meta)
	if got != "evidence: 4 ids, open detail for full list" {
		t.Fatalf("cockpitEvidenceLine() = %q", got)
	}
}

func TestCardTopRowsStripWideGlyphs(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "raw-session", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title: "zsh",
		Cockpit: &tmux.CockpitMeta{
			ContractVersion: "runtime-card.v1",
			ManagedBy:       "openclaw_runtime_snapshot",
			Kind:            "runtime",
			Agent:           "subagent",
			Goal:            "🌧️ backend\tpacket",
			State:           "blocked",
			DisplayStatus:   "blocked",
		},
	}

	header := formatHeader(100, session, window, pane, false, false, false, false, "blocked", "[x]", "dev-host")
	goal := cockpitSubtleLine(100, "goal: "+pane.Cockpit.Goal)
	for _, row := range []string{header, goal} {
		if strings.ContainsAny(row, "🌧️\t") {
			t.Fatalf("card top row should be border-safe, got %q", row)
		}
		if !strings.Contains(row, "backend packet") {
			t.Fatalf("card top row lost normalized text: %q", row)
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

func TestFormatHeaderTruncatesLongLabelsToSingleLine(t *testing.T) {
	t.Parallel()

	session := tmux.Session{Name: "worker", Windows: []tmux.Window{{Name: "main"}}}
	window := session.Windows[0]
	pane := tmux.Pane{
		Title:        "OOXML, hashes, secrets, or credentials. Final response: one concise sentence after a very long task title",
		LastActivity: time.Now().Add(-20 * time.Hour),
		Cockpit: &tmux.CockpitMeta{
			Kind:    "agent",
			Agent:   "cli",
			Project: "OOXML, hashes, secrets, or credentials. Final response: one concise sentence after a very long task title",
			State:   "failed",
			Goal:    "OOXML, hashes, secrets, or credentials. Final response: one concise sentence after a very long task title",
		},
	}

	got := formatHeader(88, session, window, pane, false, false, false, false, "failed", "[^] [-] [x]", "dev-host")
	if strings.Contains(got, "\n") {
		t.Fatalf("header should stay one line, got %q", got)
	}
	if width := lipgloss.Width(got); width > 88 {
		t.Fatalf("header width = %d, want <= 88; got %q", width, got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("header should show truncation marker, got %q", got)
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

func TestCollapsedGroupRendersDividerOnly(t *testing.T) {
	m := accordionModel(t)
	m.toggleGroupCollapsed(groupSubsystemFailures.name)

	view := stripANSI(m.renderSessionPreviews(0))

	// Active Agents is expanded: caret ▾ + its card is laid out.
	if !strings.Contains(view, groupCaretExpanded+" "+groupActiveAgents.name) {
		t.Fatalf("expected expanded Active Agents divider in view:\n%s", view)
	}
	// Sub-System Failures is collapsed: caret ▸ + name + count + summary; no cards.
	if !strings.Contains(view, groupCaretCollapsed+" "+groupSubsystemFailures.name) {
		t.Fatalf("expected collapsed Sub-System Failures divider (caret ▸) in view:\n%s", view)
	}

	foundAgentCard, foundRuntimeCard := false, false
	for _, card := range m.cardLayout {
		if card.sessionID == "$live-agent" {
			foundAgentCard = true
		}
		if strings.HasPrefix(card.sessionID, "$runtime-route") {
			foundRuntimeCard = true
		}
	}
	if !foundAgentCard {
		t.Fatal("expanded Active Agents card should be laid out")
	}
	if foundRuntimeCard {
		t.Fatal("collapsed Runtime cards must not be laid out (divider only)")
	}
}

func TestPrimaryGroupsRenderWhenEmpty(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 180
	m.height = 40
	m.cardInnerWidth = 40
	m.cardInnerHeight = 6

	view := stripANSI(m.renderSessionPreviews(0))
	for _, group := range primaryCockpitGroups() {
		caret := groupCaretExpanded
		if m.isGroupCollapsed(group.name) {
			caret = groupCaretCollapsed
		}
		want := caret + " " + group.name + "  0"
		if !strings.Contains(view, want) {
			t.Fatalf("empty primary group %q missing from view:\n%s", want, view)
		}
	}
}

func TestActiveAgentCardsUseGroupAccentBorder(t *testing.T) {
	m := accordionModel(t)
	view := m.renderSessionPreviews(0)
	divider := m.renderGroupDivider(groupActiveAgents, 1, false, "")
	activeANSI := fmt.Sprintf("\x1b[38;5;%sm", groupColorActive)

	if strings.Count(view, activeANSI) <= strings.Count(divider, activeANSI) {
		t.Fatalf("active card border should use Active Agents accent %q; view=%q", groupColorActive, view)
	}
}

func TestAgentLifecycleCardsPreserveCLIForegroundColors(t *testing.T) {
	m := accordionModel(t)
	preview := m.previews["$live-agent"]
	preview.viewport.SetContent("\x1b[38;5;10mgreen status\x1b[0m\n\x1b[48;5;1mred background\x1b[0m")

	view := m.renderSessionPreviews(0)
	if !strings.Contains(view, "\x1b[38;5;10m") {
		t.Fatalf("agent lifecycle card should preserve CLI foreground color, view=%q", view)
	}
	if strings.Contains(view, "\x1b[48;5;1m") {
		t.Fatalf("agent lifecycle card should strip CLI background color, view=%q", view)
	}
}

func TestAgentLifecycleCardsNormalizeDarkCLIForegroundColors(t *testing.T) {
	m := accordionModel(t)
	preview := m.previews["$live-agent"]
	preview.viewport.SetContent("\x1b[30mblack runtime tag\x1b[0m\n\x1b[38;5;0mdark indexed tag\x1b[0m")

	view := m.renderSessionPreviews(0)
	if strings.Contains(view, "\x1b[30m") || strings.Contains(view, "\x1b[38;5;0m") {
		t.Fatalf("agent lifecycle card should not preserve unreadable dark foregrounds, view=%q", view)
	}
	if !strings.Contains(view, "\x1b[38;5;246m") {
		t.Fatalf("dark agent foregrounds should normalize to readable card text, view=%q", view)
	}
}

func TestNonAgentCardsStripCLIColors(t *testing.T) {
	body := "\x1b[38;5;10mgreen status\x1b[0m\n\x1b[48;5;1mred background\x1b[0m"
	view := renderCardBodyBlock(80, body, false)

	if strings.Contains(view, "\x1b[38;5;10m") || strings.Contains(view, "\x1b[48;5;1m") {
		t.Fatalf("non-agent body rendering should strip pane ANSI, view=%q", view)
	}
}

func TestAgentCLIColorPassthroughGroups(t *testing.T) {
	t.Parallel()

	for _, group := range []cockpitGroup{groupActiveAgents, groupInactiveAgents, groupFailedAgents} {
		if !agentCLIColorPassthroughGroup(group.name) {
			t.Fatalf("%s should preserve agent CLI foreground colors", group.name)
		}
	}
	for _, group := range []cockpitGroup{groupOperationalFailures, groupSubsystemFailures, groupServices} {
		if agentCLIColorPassthroughGroup(group.name) {
			t.Fatalf("%s should not preserve raw pane colors", group.name)
		}
	}
}

func TestGroupDividerShowsCaretCountSummary(t *testing.T) {
	m := accordionModel(t)
	// route_health runtime cards resolve to a "review" attention state.
	summary := m.groupCollapsedSummary(groupSubsystemFailures, m.filteredSessions())
	if !strings.Contains(summary, "review") {
		t.Fatalf("collapsed Sub-System summary should mention member states, got %q", summary)
	}
	divider := stripANSI(m.renderGroupDivider(groupSubsystemFailures, 3, true, summary))
	for _, want := range []string{groupCaretCollapsed, groupSubsystemFailures.name, "3", "review"} {
		if !strings.Contains(divider, want) {
			t.Fatalf("collapsed divider missing %q in %q", want, divider)
		}
	}
	expanded := stripANSI(m.renderGroupDivider(groupActiveAgents, 2, false, ""))
	if !strings.Contains(expanded, groupCaretExpanded) {
		t.Fatalf("expanded divider should show ▾ caret, got %q", expanded)
	}
}

func TestRenderSessionPreviewsKeepsAutoFollowAnchoredAfterViewportShrink(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.width = 100
	m.height = 30
	m.cardCols = 1
	m.cardInnerWidth = 80
	m.cardInnerHeight = 4
	session := tmux.Session{
		ID:   "$live",
		Name: "live",
		Windows: []tmux.Window{{
			Active: true,
			Panes:  []tmux.Pane{{ID: "%live", Active: true, LastActivity: time.Now()}},
		}},
	}
	m.sessions = []tmux.Session{session}

	vp := viewportFor(innerDimension{width: 80, height: 12})
	vp.SetContent(numberedLines(40))
	vp.GotoBottom()
	m.previews[session.ID] = &sessionPreview{
		viewport:   &vp,
		paneID:     "%live",
		autoFollow: true,
	}

	m.renderSessionPreviews(0)

	if !m.previews[session.ID].viewport.AtBottom() {
		t.Fatalf("auto-follow viewport should stay bottom-anchored after render-time height shrink")
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
