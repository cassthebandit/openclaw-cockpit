package ui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
