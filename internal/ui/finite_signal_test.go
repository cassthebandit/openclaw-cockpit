package ui

import (
	"testing"
	"time"
)

func TestFastCaptureRetriesDeferredFiniteSignal(t *testing.T) {
	now := time.Unix(1752000000, 0)
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.clock = func() time.Time { return now }
	buildBudgetWall(t, m, t.TempDir(), 2)
	for i := 0; i < aggregateCaptureBudgetPerSecond-1; i++ {
		if !m.takeCaptureToken(now) {
			t.Fatal("fixture token setup")
		}
	}
	first := m.planFastCaptures()
	if len(first) != 1 {
		t.Fatalf("first captures %d want 1", len(first))
	}
	now = now.Add(time.Second)
	next := m.planFastCaptures()
	if len(next) != 1 {
		t.Fatalf("deferred finite output captures %d want 1", len(next))
	}
}
