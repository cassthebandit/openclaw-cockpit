package ui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/steipete/tmuxwatch/internal/tmux"
)

func runtimeTimelineSession(id, label string, meta tmux.CockpitMeta) tmux.Session {
	meta.ManagedBy = "openclaw_runtime_snapshot"
	meta.Kind = "runtime"
	meta.Agent = "openclaw-runtime"
	if meta.State == "" {
		meta.State = "failed"
	}
	if meta.DisplayStatus == "" {
		meta.DisplayStatus = meta.State
	}
	return tmux.Session{
		ID:           "openclaw-runtime:" + id,
		Name:         label,
		LastActivity: time.Unix(100, 0),
		Windows: []tmux.Window{{
			ID:      "@openclaw-runtime:" + id,
			Name:    "openclaw-runtime",
			Session: "openclaw-runtime:" + id,
			Panes: []tmux.Pane{{
				ID:           "%openclaw-runtime:" + id,
				Session:      "openclaw-runtime:" + id,
				Title:        label,
				LastActivity: time.Unix(100, 0),
				Cockpit:      &meta,
			}},
		}},
	}
}

func TestRuntimeTimelineFooterTruncatesOnRuneBoundary(t *testing.T) {
	t.Parallel()

	m := &Model{runtimeTimeline: []runtimeTimelineEvent{
		{Type: runtimeTimelineAppeared, Label: "Alpha"},
		{Type: runtimeTimelineGrouped, Label: "Beta", Reason: "grouped x3"},
	}}

	got := m.formatRuntimeTimelineLine(27)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated footer should remain valid UTF-8, got %q", got)
	}
}

func runtimeSnapshot(at time.Time, sessions ...tmux.Session) tmux.Snapshot {
	return tmux.Snapshot{Timestamp: at, Sessions: sessions}
}

func TestRuntimeTimelineBootstrapNoop(t *testing.T) {
	t.Parallel()

	m := &Model{}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0), runtimeTimelineSession("a", "A", tmux.CockpitMeta{})))

	if got := len(m.runtimeTimeline); got != 0 {
		t.Fatalf("bootstrap should not emit events, got %d", got)
	}
}

func TestRuntimeTimelineTimestampOnlyChurnNoop(t *testing.T) {
	t.Parallel()

	first := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "1", VisibleCardCount: "1"})
	second := first
	second.LastActivity = time.Unix(500, 0)
	second.Windows[0].Panes[0].LastActivity = time.Unix(500, 0)

	m := &Model{}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0), first))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(2, 0), second))

	if got := len(m.runtimeTimeline); got != 0 {
		t.Fatalf("timestamp-only churn should not emit events, got %#v", m.runtimeTimeline)
	}
}

func TestRuntimeTimelineAppearedAndResolved(t *testing.T) {
	t.Parallel()

	m := &Model{}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0)))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(2, 0), runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "1"})))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(3, 0)))

	if len(m.runtimeTimeline) != 2 {
		t.Fatalf("expected appeared and resolved, got %#v", m.runtimeTimeline)
	}
	if m.runtimeTimeline[0].Type != runtimeTimelineAppeared || m.runtimeTimeline[1].Type != runtimeTimelineResolved {
		t.Fatalf("unexpected events: %#v", m.runtimeTimeline)
	}
}

func TestRuntimeTimelineDuplicateWeakIdentityDistinctAndStableAcrossReorder(t *testing.T) {
	t.Parallel()

	a := runtimeTimelineSession("same", "same", tmux.CockpitMeta{LifecycleState: "needs_attention", SourceTruth: "cron"})
	b := runtimeTimelineSession("same", "same", tmux.CockpitMeta{LifecycleState: "needs_attention", SourceTruth: "taskflow"})
	first := buildRuntimeTimelineSnapshot([]tmux.Session{a, b})
	second := buildRuntimeTimelineSnapshot([]tmux.Session{b, a})

	if len(first.Cards) != 2 || len(second.Cards) != 2 {
		t.Fatalf("duplicate cards should remain distinct: first=%#v second=%#v", first.Cards, second.Cards)
	}
	for key := range first.Cards {
		if _, ok := second.Cards[key]; !ok {
			t.Fatalf("duplicate key %q not stable across reorder: first=%#v second=%#v", key, first.Cards, second.Cards)
		}
	}
}

func TestRuntimeTimelineGroupedTransitionUsesLocalCountAndDoesNotRepeat(t *testing.T) {
	t.Parallel()

	plain := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2", GroupedCardCount: "0", GroupedRecordCount: "1"})
	grouped := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "1", GroupedCardCount: "1", GroupedRecordCount: "3", LogicalGroupKey: "runtime:a"})

	m := &Model{}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0), plain))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(2, 0), grouped))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(3, 0), grouped))

	if len(m.runtimeTimeline) != 1 || m.runtimeTimeline[0].Type != runtimeTimelineGrouped {
		t.Fatalf("expected one grouped event, got %#v", m.runtimeTimeline)
	}
}

func TestRuntimeTimelineGlobalGroupedCountAloneDoesNotGroup(t *testing.T) {
	t.Parallel()

	before := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2", GroupedCardCount: "0", GroupedRecordCount: "1"})
	after := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2", GroupedCardCount: "1", GroupedRecordCount: "1"})

	events := diffRuntimeTimeline(
		buildRuntimeTimelineSnapshot([]tmux.Session{before}),
		buildRuntimeTimelineSnapshot([]tmux.Session{after}),
		time.Unix(2, 0),
	)
	for _, event := range events {
		if event.Type == runtimeTimelineGrouped {
			t.Fatalf("global grouped count alone should not emit grouped, got %#v", events)
		}
	}
}

func TestRuntimeTimelineSourceOutageSuppressesResolvedSpamAndDoesNotRepeat(t *testing.T) {
	t.Parallel()

	a := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2"})
	b := runtimeTimelineSession("b", "B", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2"})
	sourceError := runtimeTimelineSession("source-error", "OpenClaw runtime snapshot", tmux.CockpitMeta{
		State:          "failed",
		LifecycleState: "source_unavailable",
		SourceTruth:    "source_error",
		RawCardCount:   "1",
	})

	m := &Model{}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0), a, b))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(2, 0), sourceError))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(3, 0), sourceError))

	if len(m.runtimeTimeline) != 1 {
		t.Fatalf("source outage should emit one event, got %#v", m.runtimeTimeline)
	}
	if m.runtimeTimeline[0].Type != runtimeTimelineSourceUnavailable {
		t.Fatalf("expected source unavailable, got %#v", m.runtimeTimeline)
	}
}

func TestRuntimeTimelineFilterToggleDoesNotCreateEvents(t *testing.T) {
	t.Parallel()

	session := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "1", VisibleCardCount: "1"})
	m := &Model{viewFilter: "decision"}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0), session))
	m.viewFilter = "all"
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(2, 0), session))

	if got := len(m.runtimeTimeline); got != 0 {
		t.Fatalf("filter-only toggle should not emit timeline events, got %#v", m.runtimeTimeline)
	}
}

func TestRuntimeTimelineHiddenSummaryDoesNotClaimCardFate(t *testing.T) {
	t.Parallel()

	a := runtimeTimelineSession("a", "A", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2", HiddenCardCount: "0"})
	b := runtimeTimelineSession("b", "B", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "2", HiddenCardCount: "0"})
	remaining := runtimeTimelineSession("b", "B", tmux.CockpitMeta{RawCardCount: "2", VisibleCardCount: "1", HiddenCardCount: "1"})

	m := &Model{}
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(1, 0), a, b))
	m.updateRuntimeTimeline(runtimeSnapshot(time.Unix(2, 0), remaining))

	if len(m.runtimeTimeline) != 1 || m.runtimeTimeline[0].Type != runtimeTimelineHiddenSummary {
		t.Fatalf("expected hidden summary only, got %#v", m.runtimeTimeline)
	}
}

func TestRuntimeTimelineCapDropsOldest(t *testing.T) {
	t.Parallel()

	m := &Model{}
	events := make([]runtimeTimelineEvent, maxRuntimeTimelineEvents+5)
	for i := range events {
		events[i] = runtimeTimelineEvent{Type: runtimeTimelineAppeared, Key: string(rune('a' + i%26)), Label: "event"}
	}
	m.appendRuntimeTimelineEvents(events)

	if got := len(m.runtimeTimeline); got != maxRuntimeTimelineEvents {
		t.Fatalf("timeline cap length = %d, want %d", got, maxRuntimeTimelineEvents)
	}
}

func TestRuntimeTimelineFooterDoesNotLeakEvidencePath(t *testing.T) {
	t.Parallel()

	m := &Model{runtimeTimeline: []runtimeTimelineEvent{{
		Type:   runtimeTimelineAppeared,
		Key:    "secret-evidence-id",
		Label:  "Runtime Card",
		Reason: "/Users/cass/private/evidence",
	}}}

	got := m.formatRuntimeTimelineLine(200)
	if !strings.Contains(got, "appeared Runtime Card") {
		t.Fatalf("timeline footer missing compact event, got %q", got)
	}
	for _, forbidden := range []string{"secret-evidence-id", "/Users/cass/private/evidence"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("timeline footer leaked %q in %q", forbidden, got)
		}
	}
}

func TestRuntimeTimelineDetailLinesShowRecentSafeEvents(t *testing.T) {
	t.Parallel()

	m := &Model{runtimeTimeline: []runtimeTimelineEvent{
		{Type: runtimeTimelineAppeared, Label: "Alpha", Reason: "appeared", At: time.Date(2026, 7, 6, 1, 2, 1, 0, time.UTC)},
		{Type: runtimeTimelineResolved, Label: "Beta", Reason: "resolved", At: time.Date(2026, 7, 6, 1, 2, 2, 0, time.UTC)},
		{Type: runtimeTimelineGrouped, Label: "Gamma", Reason: "grouped x3", At: time.Date(2026, 7, 6, 1, 2, 3, 0, time.UTC)},
		{Type: runtimeTimelineHiddenSummary, Label: "runtime snapshot", Reason: "hidden 1 -> 2", At: time.Date(2026, 7, 6, 1, 2, 4, 0, time.UTC)},
	}}

	lines := m.runtimeTimelineDetailLines(100, 2)
	if got, want := len(lines), 3; got != want {
		t.Fatalf("detail line count = %d, want %d: %#v", got, want, lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"timeline:",
		"01:02:03 grouped Gamma - grouped x3",
		"01:02:04 hidden runtime snapshot - hidden 1 -> 2",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("detail lines missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "Alpha") || strings.Contains(joined, "Beta") {
		t.Fatalf("detail lines should only show the most recent capped events, got %q", joined)
	}
}

func TestRuntimeTimelineDetailReasonPolicyMatchesFooter(t *testing.T) {
	t.Parallel()

	m := &Model{runtimeTimeline: []runtimeTimelineEvent{
		{Type: runtimeTimelineAppeared, Label: "Alpha", Reason: "safe appeared reason", At: time.Date(2026, 7, 6, 1, 2, 1, 0, time.UTC)},
		{Type: runtimeTimelineResolved, Label: "Beta", Reason: "safe resolved reason", At: time.Date(2026, 7, 6, 1, 2, 2, 0, time.UTC)},
		{Type: runtimeTimelineGrouped, Label: "Gamma", Reason: "grouped x3", At: time.Date(2026, 7, 6, 1, 2, 3, 0, time.UTC)},
		{Type: runtimeTimelineHiddenSummary, Label: "runtime snapshot", Reason: "hidden 1 -> 2", At: time.Date(2026, 7, 6, 1, 2, 4, 0, time.UTC)},
		{Type: runtimeTimelineSourceUnavailable, Label: "runtime source", Reason: "safe source reason", At: time.Date(2026, 7, 6, 1, 2, 5, 0, time.UTC)},
	}}

	got := strings.Join(m.runtimeTimelineDetailLines(140, 10), "\n")
	for _, want := range []string{
		"01:02:03 grouped Gamma - grouped x3",
		"01:02:04 hidden runtime snapshot - hidden 1 -> 2",
		"01:02:05 source unavailable runtime source",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail lines missing %q in %q", want, got)
		}
	}
	for _, forbidden := range []string{
		"safe appeared reason",
		"safe resolved reason",
		"safe source reason",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("detail lines should omit %q, got %q", forbidden, got)
		}
	}
}

func TestRuntimeTimelineDetailOnlyForSelectedRuntimeCard(t *testing.T) {
	t.Parallel()

	session := runtimeTimelineSession("a", "Runtime Card", tmux.CockpitMeta{})
	pane := session.Windows[0].Panes[0]
	m := &Model{
		viewMode:      viewModeOverview,
		detailSession: session.ID,
		runtimeTimeline: []runtimeTimelineEvent{{
			Type:   runtimeTimelineAppeared,
			Label:  "Runtime Card",
			Reason: "appeared",
			At:     time.Date(2026, 7, 6, 1, 2, 3, 0, time.UTC),
		}},
	}

	if got := strings.Join(cockpitCardInfoLines(100, m, session, pane, ""), "\n"); strings.Contains(got, "timeline:") {
		t.Fatalf("overview info lines should not render timeline detail, got %q", got)
	}

	m.viewMode = viewModeDetail
	if got := strings.Join(cockpitCardInfoLines(100, m, session, pane, ""), "\n"); !strings.Contains(got, "timeline:") {
		t.Fatalf("selected runtime detail should render timeline detail, got %q", got)
	}

	m.detailSession = "openclaw-runtime:other"
	if got := strings.Join(cockpitCardInfoLines(100, m, session, pane, ""), "\n"); strings.Contains(got, "timeline:") {
		t.Fatalf("unselected runtime card should not render timeline detail, got %q", got)
	}

	plain := tmux.Session{
		ID: "plain",
		Windows: []tmux.Window{{
			Panes: []tmux.Pane{{ID: "%plain"}},
		}},
	}
	m.detailSession = plain.ID
	if got := strings.Join(cockpitCardInfoLines(100, m, plain, plain.Windows[0].Panes[0], ""), "\n"); strings.Contains(got, "timeline:") {
		t.Fatalf("non-runtime detail should not render timeline detail, got %q", got)
	}
}

func TestRuntimeTimelineDetailEmptyTimelineSilent(t *testing.T) {
	t.Parallel()

	session := runtimeTimelineSession("a", "Runtime Card", tmux.CockpitMeta{})
	pane := session.Windows[0].Panes[0]
	m := &Model{viewMode: viewModeDetail, detailSession: session.ID}

	got := strings.Join(cockpitCardInfoLines(100, m, session, pane, ""), "\n")
	if strings.Contains(got, "timeline:") {
		t.Fatalf("empty timeline should render no detail block, got %q", got)
	}
}

func TestRuntimeTimelineDetailDoesNotLeakKeysOrUnsafeReasonsAcrossEventTypes(t *testing.T) {
	t.Parallel()

	m := &Model{runtimeTimeline: []runtimeTimelineEvent{
		{
			Type:   runtimeTimelineAppeared,
			Key:    "/Users/cass/private/key-appeared",
			Label:  "Appeared Card",
			Reason: "/Users/cass/private/reason-appeared",
			At:     time.Date(2026, 7, 6, 1, 2, 1, 0, time.UTC),
		},
		{
			Type:   runtimeTimelineResolved,
			Key:    "workspace/memory/runs/key-resolved",
			Label:  "Resolved Card",
			Reason: "secret-evidence-id-42",
			At:     time.Date(2026, 7, 6, 1, 2, 2, 0, time.UTC),
		},
		{
			Type:   runtimeTimelineGrouped,
			Key:    "secret-evidence-id-43",
			Label:  "Grouped Card",
			Reason: "raw-id-1,raw-id-2",
			At:     time.Date(2026, 7, 6, 1, 2, 3, 0, time.UTC),
		},
		{
			Type:   runtimeTimelineHiddenSummary,
			Key:    "/private/tmp/key-hidden",
			Label:  "/Users/cass/.openclaw/workspace/memory/runs/private-run/RESULT.md",
			Reason: "/Users/cass/.openclaw/workspace/memory/runs/private-run/RESULT.md",
			At:     time.Date(2026, 7, 6, 1, 2, 4, 0, time.UTC),
		},
		{
			Type:   runtimeTimelineSourceUnavailable,
			Key:    "id-4,id-3",
			Label:  "runtime source",
			Reason: "id-4,id-3",
			At:     time.Date(2026, 7, 6, 1, 2, 5, 0, time.UTC),
		},
	}}

	got := strings.Join(m.runtimeTimelineDetailLines(120, 10), "\n")
	for _, want := range []string{"timeline:", "Appeared Card", "Resolved Card", "Grouped Card", "runtime item", "runtime source"} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail lines missing safe text %q in %q", want, got)
		}
	}
	for _, forbidden := range []string{
		"key-appeared",
		"reason-appeared",
		"key-resolved",
		"secret-evidence-id-42",
		"secret-evidence-id-43",
		"raw-id-1",
		"raw-id-2",
		"key-hidden",
		"/Users/cass",
		"/private/tmp",
		".openclaw",
		"workspace/memory/runs",
		"private-run",
		"RESULT.md",
		"id-4",
		"id-3",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("timeline detail leaked %q in %q", forbidden, got)
		}
	}
}

func TestRuntimeTimelineDetailTruncatesLongRows(t *testing.T) {
	t.Parallel()

	m := &Model{runtimeTimeline: []runtimeTimelineEvent{{
		Type:   runtimeTimelineGrouped,
		Label:  strings.Repeat("longlabel", 12),
		Reason: "grouped " + strings.Repeat("reason", 12),
		At:     time.Date(2026, 7, 6, 1, 2, 3, 0, time.UTC),
	}}}

	lines := m.runtimeTimelineDetailLines(42, 1)
	if got, want := len(lines), 2; got != want {
		t.Fatalf("detail line count = %d, want %d: %#v", got, want, lines)
	}
	row := lines[1]
	if !utf8.ValidString(row) {
		t.Fatalf("truncated detail row should remain valid UTF-8, got %q", row)
	}
	if width := lipgloss.Width(row); width > 42 {
		t.Fatalf("truncated detail row width = %d, want <= 42: %q", width, row)
	}
	if !strings.Contains(row, "...") {
		t.Fatalf("long detail row should include ellipsis, got %q", row)
	}
}
