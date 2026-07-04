package ui

import (
	"strings"
	"testing"
	"time"
)

func TestOpenClawRuntimeSessionMapsAttentionCard(t *testing.T) {
	t.Parallel()

	ageMs := int64(90_000)
	now := time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC)
	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:             "flow:123",
		Kind:           "flow",
		Label:          "backend packet",
		Runtime:        "taskflow",
		StateClass:     "attention",
		Status:         "blocked",
		Summary:        "needs a final deliverable",
		OwnerKey:       "agent:main",
		ParentFlowID:   "flow-123",
		LastEventAgeMs: &ageMs,
	}, 2, now)

	if session.ID != "openclaw-runtime:flow-123" {
		t.Fatalf("session.ID = %q", session.ID)
	}
	pane := session.Windows[0].Panes[0]
	if pane.Cockpit == nil {
		t.Fatalf("expected cockpit metadata")
	}
	if pane.Cockpit.State != "blocked" {
		t.Fatalf("state = %q, want blocked", pane.Cockpit.State)
	}
	if pane.Cockpit.ManagedBy != "openclaw_runtime_snapshot" {
		t.Fatalf("managed by = %q", pane.Cockpit.ManagedBy)
	}
	if !strings.Contains(pane.PreviewText, "needs a final deliverable") {
		t.Fatalf("preview missing summary: %q", pane.PreviewText)
	}
	if got := now.Sub(session.LastActivity); got != 90*time.Second {
		t.Fatalf("LastActivity age = %s", got)
	}
}

func TestOpenClawRuntimeSessionMapsFailedDelivery(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:             "child-session",
		Label:          "worker",
		Runtime:        "subagent",
		Status:         "lost",
		DeliveryStatus: "delivery_failed",
	}, 0, time.Now())

	pane := session.Windows[0].Panes[0]
	if pane.Cockpit.State != "failed" {
		t.Fatalf("state = %q, want failed", pane.Cockpit.State)
	}
	if !strings.Contains(pane.PreviewText, "delivery: delivery_failed") {
		t.Fatalf("preview missing delivery status: %q", pane.PreviewText)
	}
}
