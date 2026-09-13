package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestJobOrderIgnoresUpdatesAndCompactsRemoval(t *testing.T) {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	now := time.Unix(1800000000, 0)
	for i, id := range []string{"$old", "$middle", "$new"} {
		s := captureTestSession(id, "%"+id, tmux.CockpitMeta{Kind: "agent", State: "running"})
		s.CreatedAt = now.Add(time.Duration(i) * time.Minute)
		m.sessions = append(m.sessions, s)
	}
	ids := func() []string {
		var out []string
		for _, s := range m.filteredSessions() {
			out = append(out, s.ID)
		}
		return out
	}
	want := []string{"$old", "$middle", "$new"}
	for step := 0; step < 6; step++ {
		i := step % 3
		m.sessions[i] = withActivity(m.sessions[i], now.Add(time.Duration(step+10)*time.Hour))
		m.sessions[i].Name = intString(step)
		if got := ids(); !slices.Equal(got, want) {
			t.Fatalf("activity/label changed order: %v", got)
		}
	}
	slices.Reverse(m.sessions)
	if got := ids(); !slices.Equal(got, want) {
		t.Fatalf("snapshot order leaked: %v", got)
	}
	m.sessions = slices.DeleteFunc(m.sessions, func(s tmux.Session) bool { return s.ID == "$middle" })
	if got := ids(); !slices.Equal(got, []string{"$old", "$new"}) {
		t.Fatal(got)
	}
	visual := m.stackedSessions(m.filteredSessions())
	if visual[0].ID != "$new" || visual[1].ID != "$old" {
		t.Fatal("oldest must paint at right/bottom")
	}
}

func TestSyntheticBirthDoesNotFollowActivity(t *testing.T) {
	now := time.Unix(1800000000, 0)
	age := int64(10000)
	activity := int64(1000)
	card := openClawRuntimeCard{ID: "fixed", CreatedAgeMs: &age, LastEventAgeMs: &activity}
	first := openClawRuntimeSession(card, 0, now)
	if !first.CreatedAt.Equal(now.Add(-10 * time.Second)) {
		t.Fatal(first.CreatedAt)
	}
	card.CreatedAgeMs = nil
	missing := openClawRuntimeSession(card, 0, now)
	if !missing.CreatedAt.IsZero() {
		t.Fatal("missing birth fabricated from activity")
	}
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.rememberSessionBirths([]tmux.Session{missing})
	birth := m.sessionBirth[missing.ID]
	m.rememberSessionBirths([]tmux.Session{openClawRuntimeSession(card, 0, now.Add(time.Hour))})
	if !m.sessionBirth[missing.ID].Equal(birth) {
		t.Fatal("first-seen order moved")
	}
}

func TestStackPartialRows(t *testing.T) {
	for _, tc := range []struct{ count, cols, want int }{{1, 3, 2}, {4, 3, 2}, {5, 3, 1}, {6, 3, 0}, {2, 1, 0}} {
		if got := leadingStackSlots(tc.count, tc.cols); got != tc.want {
			t.Fatalf("%+v: %d", tc, got)
		}
	}
}

func TestUnknownBirthBatchTiesByIdentity(t *testing.T) {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	sessions := []tmux.Session{{ID: "$b"}, {ID: "$a"}}
	sortSessionsForCockpit(m, sessions)
	if sessions[0].ID != "$a" || !m.sessionBirth["$a"].Equal(m.sessionBirth["$b"]) {
		t.Fatal("initial source order leaked into missing-birth tie")
	}
}
