package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenClawRuntimeDefaultScriptUsesCurrentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(home, ".openclaw", "workspace", "tools", "openclaw_runtime", "cockpit_snapshot.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatalf("create default script directory: %v", err)
	}
	if err := os.WriteFile(script, []byte(`import json
print(json.dumps({"cardContract": "runtime-card.v1", "summary": {}, "cards": []}))
`), 0o644); err != nil {
		t.Fatalf("write default runtime script: %v", err)
	}

	cards, err := loadOpenClawRuntimeCards(RuntimeSource{Enabled: true, Limit: 5, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("load default runtime script: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("cards = %d, want 0", len(cards))
	}
}

func TestOpenClawRuntimeSessionMapsAttentionCard(t *testing.T) {
	t.Parallel()

	ageMs := int64(90_000)
	now := time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC)
	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:                   "flow:123",
		DedupeKey:            "flow:123",
		CardContract:         "runtime-card.v1",
		Kind:                 "flow",
		Label:                "backend packet",
		DisplayTitle:         "backend packet",
		DisplayStatus:        "blocked",
		DisplayGroup:         "needs_attention",
		PresentationGroup:    "delivery_handoff",
		PresentationLabel:    "Delivery / Handoff",
		Reason:               "done_only_no_final",
		NextAction:           "inspect final delivery",
		WhyVisible:           "Work may have finished, but no clean final-delivery marker is present.",
		SuggestionKind:       "inspect_handoff",
		SuggestedAction:      "Inspect parent/child handoff before marking this resolved.",
		SuggestedCommand:     "openclaw tasks flow list --json",
		SuggestionConfidence: "medium",
		Runtime:              "taskflow",
		StateClass:           "attention",
		Status:               "blocked",
		LifecycleState:       "needs_attention",
		SourceTruth:          "reported_by_openclaw",
		SourceProvenance:     "openclaw tasks/list + tasks/flow/list + tasks/audit",
		Actionability:        "operator_action",
		TeardownPolicy:       "drop_when_source_absent",
		PolicyScope:          "source_identity",
		AggregationPolicy:    "primary_identity",
		Summary:              "needs a final deliverable",
		OwnerKey:             "agent:main",
		ParentFlowID:         "flow-123",
		LastEventAgeMs:       &ageMs,
		EvidenceIDs:          []string{"flow-123", "task-1"},
		SourceKinds:          []string{"flow", "task"},
		SourceCount:          2,
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
	if pane.Cockpit.ContractVersion != "runtime-card.v1" {
		t.Fatalf("contract version = %q, want runtime-card.v1", pane.Cockpit.ContractVersion)
	}
	if pane.Cockpit.LifecycleState != "needs_attention" || pane.Cockpit.SourceTruth != "reported_by_openclaw" {
		t.Fatalf("runtime provenance missing from metadata: %#v", pane.Cockpit)
	}
	if pane.Cockpit.Owner != "" || pane.Cockpit.Project != "" {
		t.Fatalf("runtime metadata should keep owner/project out of headers: %#v", pane.Cockpit)
	}
	if pane.Cockpit.DisplayGroup != "needs_attention" {
		t.Fatalf("display group = %q, want needs_attention", pane.Cockpit.DisplayGroup)
	}
	if pane.Cockpit.PresentationGroup != "delivery_handoff" || pane.Cockpit.PresentationLabel != "Delivery / Handoff" {
		t.Fatalf("presentation metadata missing from cockpit meta: %#v", pane.Cockpit)
	}
	if !strings.Contains(pane.PreviewText, "needs a final deliverable") {
		t.Fatalf("preview missing summary: %q", pane.PreviewText)
	}
	for _, want := range []string{
		"lifecycle: needs_attention",
		"cause: done_only_no_final",
		"next: inspect final delivery",
		"presentation: Delivery / Handoff",
		"why visible: Work may have finished",
		"suggestion: Inspect parent/child handoff before marking this resolved. · kind inspect_handoff · confidence medium · read-only hint: openclaw tasks flow list --json",
		"action: operator_action",
		"source: reported_by_openclaw via openclaw tasks/list + tasks/flow/list + tasks/audit",
		"teardown: drop_when_source_absent",
		"evidence: flow-123,task-1",
		"sources: 2 flow,task",
		"owner: agent:main",
	} {
		if !strings.Contains(pane.PreviewText, want) {
			t.Fatalf("preview missing %q: %q", want, pane.PreviewText)
		}
	}
	if got := now.Sub(session.LastActivity); got != 90*time.Second {
		t.Fatalf("LastActivity age = %s", got)
	}
}

func TestOpenClawRuntimeSessionCarriesSkeletonMetadata(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:                "skeleton-card",
		CardContract:      "runtime-card.v1",
		DisplayTitle:      "old expected control",
		DisplayStatus:     "blocked",
		DisplayGroup:      "needs_attention",
		PresentationGroup: "skeletons",
		PresentationLabel: "Skeletons",
		Runtime:           "cron",
		Reason:            "blocked_flow_stale",
		Skeleton:          true,
		SkeletonReason:    "expected-control suppression from policy",
		Suppressed:        true,
	}, 0, time.Now())

	pane := session.Windows[0].Panes[0]
	if pane.Cockpit.Skeleton != "true" || pane.Cockpit.Suppressed != "true" {
		t.Fatalf("skeleton metadata missing from cockpit meta: %#v", pane.Cockpit)
	}
	if !strings.Contains(pane.PreviewText, "skeleton: true · suppressed · expected-control suppression from policy") {
		t.Fatalf("preview missing skeleton line: %q", pane.PreviewText)
	}
}

func TestOpenClawRuntimeSessionCarriesPulseGroupDetail(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:                     "pulse:needs_decision:cron:runtime_failed",
		DedupeKey:              "pulse:needs_decision:cron:runtime_failed",
		CardContract:           "runtime-card.v1",
		DisplayTitle:           "qmd-sidecar-live-shadow-collector-step06b",
		DisplayStatus:          "failed",
		DisplayGroup:           "needs_attention",
		PresentationGroup:      "needs_decision",
		PresentationLabel:      "Needs Decision",
		Runtime:                "cron",
		Reason:                 "runtime_failed",
		NextAction:             "inspect runtime failure",
		LifecycleState:         "needs_attention",
		SourceTruth:            "reported_by_openclaw",
		SourceProvenance:       "openclaw tasks/list",
		Actionability:          "operator_action",
		TeardownPolicy:         "drop_when_source_absent",
		PolicyScope:            "local_overlay",
		AggregationPolicy:      "primary_identity+pulse_group",
		SourceCount:            4,
		LogicalGroupKey:        "pulse:needs_decision:cron:runtime_failed:inspect-runtime-failure:qmd",
		GroupedRecordCount:     4,
		RawCardCount:           4,
		VisibleCardCount:       19,
		EvidenceIDs:            []string{"task-4", "task-3", "task-2", "task-1"},
		SourceKinds:            []string{"task"},
		GroupedEvidenceIDs:     []string{"task-4", "task-3", "task-2", "task-1"},
		GroupedSourceKinds:     []string{"task"},
		GroupedSourceSummaries: []string{"runtime failed copy 4", "runtime failed copy 3"},
		summary: openClawRuntimeSummary{
			RawRuntimeCardCount:     26,
			VisibleRuntimeCardCount: 19,
			GroupedRuntimeCardCount: 3,
			HiddenRuntimeCardCount:  7,
		},
	}, 0, time.Now())

	pane := session.Windows[0].Panes[0]
	if pane.Cockpit.GroupedRecordCount != "4" || pane.Cockpit.RawCardCount != "26" || pane.Cockpit.VisibleCardCount != "19" {
		t.Fatalf("grouped summary metadata missing: %#v", pane.Cockpit)
	}
	for _, want := range []string{
		"group: 4 runtime cards",
		"snapshot: raw 26 · shown 19 · grouped 3 · hidden 7",
		"evidence: task-4,task-3,task-2,task-1",
		"sources: 4 task",
		"policy: local_overlay · primary_identity+pulse_group",
		"- runtime failed copy 4",
	} {
		if !strings.Contains(pane.PreviewText, want) {
			t.Fatalf("grouped preview missing %q: %q", want, pane.PreviewText)
		}
	}
}

func TestOpenClawRuntimeSessionMarksLocalOverlayMergePolicy(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:                "logical-flow:taskflow:owner:goal",
		CardContract:      "runtime-card.v1",
		DisplayTitle:      "merged flow",
		DisplayStatus:     "blocked",
		DisplayGroup:      "needs_attention",
		Runtime:           "taskflow",
		Reason:            "done_only_no_final",
		PolicyScope:       "local_overlay",
		AggregationPolicy: "logical_flow_day_merge",
	}, 0, time.Now())

	pane := session.Windows[0].Panes[0]
	if !strings.Contains(pane.PreviewText, "policy: local_overlay · logical_flow_day_merge") {
		t.Fatalf("preview should expose local overlay merge policy: %q", pane.PreviewText)
	}
}

func TestOpenClawRuntimeSessionMapsFailedDelivery(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:             "child-session",
		CardContract:   "runtime-card.v1",
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

func TestOpenClawRuntimeSessionPrefersDisplayStatus(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:            "flow:blocked",
		CardContract:  "runtime-card.v1",
		DisplayTitle:  "blocked runtime",
		DisplayStatus: "blocked",
		DisplayGroup:  "needs_attention",
		Runtime:       "taskflow",
		Status:        "succeeded",
		StateClass:    "done",
	}, 0, time.Now())

	pane := session.Windows[0].Panes[0]
	if pane.Cockpit.State != "blocked" {
		t.Fatalf("state = %q, want blocked", pane.Cockpit.State)
	}
	if pane.Cockpit.DisplayStatus != "blocked" {
		t.Fatalf("display status = %q, want blocked", pane.Cockpit.DisplayStatus)
	}
}

func TestOpenClawRuntimeSessionStripsWideGlyphsFromDisplayText(t *testing.T) {
	t.Parallel()

	session := openClawRuntimeSession(openClawRuntimeCard{
		ID:            "emoji-runtime",
		CardContract:  "runtime-card.v1",
		DisplayTitle:  "🌧️ backend packet",
		DisplayStatus: "blocked",
		DisplayGroup:  "needs_attention",
		Runtime:       "taskflow",
		Reason:        "done\tonly",
		NextAction:    "inspect\tfinal",
		Summary:       "💨 done only no final",
		SourceSummaries: []string{
			"🥊 backend handoff pending",
		},
	}, 0, time.Now())

	pane := session.Windows[0].Panes[0]
	for _, value := range []string{session.Name, pane.Title, pane.Cockpit.Goal, pane.Cockpit.HoldReason, pane.PreviewText} {
		if strings.ContainsAny(value, "🌧️💨🥊\t") {
			t.Fatalf("display text should be border-safe, got %q", value)
		}
	}
	for _, want := range []string{"backend packet", "cause: done only", "next: inspect final", "done only no final", "- backend handoff pending"} {
		if !strings.Contains(pane.PreviewText, want) {
			t.Fatalf("preview missing %q: %q", want, pane.PreviewText)
		}
	}
}

func TestValidateOpenClawRuntimeSnapshotRejectsMissingContract(t *testing.T) {
	t.Parallel()

	err := validateOpenClawRuntimeSnapshot(openClawRuntimeSnapshot{
		Cards: []openClawRuntimeCard{{
			ID:           "card-1",
			CardContract: "runtime-card.v1",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime snapshot contract") {
		t.Fatalf("expected snapshot contract error, got %v", err)
	}
}

func TestValidateOpenClawRuntimeSnapshotRejectsMissingCardContract(t *testing.T) {
	t.Parallel()

	err := validateOpenClawRuntimeSnapshot(openClawRuntimeSnapshot{
		CardContract: "runtime-card.v1",
		Cards: []openClawRuntimeCard{{
			ID: "card-1",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime card 0 contract") {
		t.Fatalf("expected card contract error, got %v", err)
	}
}
