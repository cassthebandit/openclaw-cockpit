package ui

import (
	"strings"
	"testing"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
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

func TestOverviewStatusDoesNotDumpFocusedPaneVars(t *testing.T) {
	t.Parallel()

	m := &Model{
		width:          120,
		viewMode:       viewModeOverview,
		focusedSession: "$failed",
		previews: map[string]*sessionPreview{
			"$failed": {
				vars: map[string]string{
					"@oc_agent":     "gemini-batch",
					"@oc_exit_code": "1",
					"@oc_evidence":  "lanes/gemini.md",
				},
			},
		},
	}

	got := m.buildStatusLine(120)
	if strings.Contains(got, "vars:") || strings.Contains(got, "@oc_agent") {
		t.Fatalf("overview status leaked pane variables: %q", got)
	}
	if !strings.Contains(got, "focused: failed") {
		t.Fatalf("overview status should keep a compact focus hint, got %q", got)
	}
}

func TestDetailStatusShowsDetailPaneVars(t *testing.T) {
	t.Parallel()

	m := &Model{
		width:         120,
		viewMode:      viewModeDetail,
		detailSession: "$failed",
		previews: map[string]*sessionPreview{
			"$failed": {
				vars: map[string]string{
					"@oc_agent":     "gemini-batch",
					"@oc_exit_code": "1",
				},
			},
		},
	}

	got := m.buildStatusLine(120)
	if !strings.Contains(got, "vars:") || !strings.Contains(got, "@oc_agent=gemini-batch") {
		t.Fatalf("detail status should include pane variables, got %q", got)
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

func TestFormatCockpitSummaryTreatsHeldMarkedAsHeld(t *testing.T) {
	t.Parallel()

	session := tmux.Session{
		ID:   "$held-marked-lane",
		Name: "held-marked-lane",
		Windows: []tmux.Window{{
			ID:   "@held-marked-lane",
			Name: "main",
			Panes: []tmux.Pane{{
				ID:      "%held-marked-lane",
				Session: "held-marked-lane",
				Cockpit: &tmux.CockpitMeta{
					Kind:         "agent",
					Agent:        "fable",
					State:        "marked-for-teardown",
					HoldReason:   "parent review",
					JanitorState: "marked_for_teardown",
				},
			}},
		}},
	}
	m := &Model{
		sessions: []tmux.Session{session},
		janitorStatus: janitorStatusView{
			State: "ok",
			Sessions: map[string]janitorSessionStatus{
				"held-marked-lane": {JanitorState: "protected", LastRefusal: "hold_reason_active"},
			},
		},
	}

	got := m.formatCockpitSummary(200)
	if !strings.Contains(got, "held 1") {
		t.Fatalf("cockpit summary should count held+marked conflict as held, got %q", got)
	}
	if strings.Contains(got, "marked-for-teardown") {
		t.Fatalf("cockpit summary should not report protected held mark as teardown-eligible, got %q", got)
	}
}

func TestFormatCockpitSummaryCountsSkeletonsSuppressedAndGrouped(t *testing.T) {
	t.Parallel()

	session := tmux.Session{
		ID:   "runtime-skeleton",
		Name: "runtime-skeleton",
		Windows: []tmux.Window{{
			ID:   "runtime-skeleton-win",
			Name: "main",
			Panes: []tmux.Pane{{
				ID:      "runtime-skeleton-pane",
				Session: "runtime-skeleton",
				Cockpit: &tmux.CockpitMeta{
					ManagedBy:          "openclaw_runtime_snapshot",
					Kind:               "runtime",
					Agent:              "openclaw-runtime",
					State:              "review",
					Skeleton:           "true",
					Suppressed:         "true",
					SourceCount:        "3",
					GroupedRecordCount: "3",
				},
			}},
		}},
	}

	m := &Model{sessions: []tmux.Session{session}}

	got := m.formatCockpitSummary(200)
	for _, want := range []string{"cockpit items: 1", "review 1", "skeletons 1", "suppressed 1", "grouped 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cockpit summary missing %q in %q", want, got)
		}
	}
}

func TestFormatCockpitSummaryUsesSnapshotRawForGroupedOnlyCards(t *testing.T) {
	t.Parallel()

	session := tmux.Session{
		ID:   "runtime-grouped",
		Name: "runtime-grouped",
		Windows: []tmux.Window{{
			ID:   "runtime-grouped-win",
			Name: "main",
			Panes: []tmux.Pane{{
				ID:      "runtime-grouped-pane",
				Session: "runtime-grouped",
				Cockpit: &tmux.CockpitMeta{
					ManagedBy:          "openclaw_runtime_snapshot",
					Kind:               "runtime",
					Agent:              "openclaw-runtime",
					State:              "failed",
					GroupedRecordCount: "4",
					RawCardCount:       "26",
					VisibleCardCount:   "19",
					GroupedCardCount:   "3",
					HiddenCardCount:    "7",
				},
			}},
		}},
	}

	m := &Model{sessions: []tmux.Session{session}}

	got := m.formatCockpitSummary(200)
	for _, want := range []string{"grouped 1", "raw 26/shown 19/pulse groups 3/hidden 7"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cockpit summary missing %q in %q", want, got)
		}
	}
}

func TestRenderTitleBarExcludesQuietServicesFromStaleCount(t *testing.T) {
	t.Parallel()

	service := sessionForGroup("smonitor", "go2rtc", "/Users/cass/.openclaw/workspace/config/camera-rtsp", "")
	m := &Model{
		sessions: []tmux.Session{service},
		stale: map[string]struct{}{
			service.ID: {},
		},
	}

	got := renderTitleBar(m, 120)
	if !strings.Contains(got, "1 items") || !strings.Contains(got, "services 1") {
		t.Fatalf("title should summarize quiet live service as service item, got %q", got)
	}
	if strings.Contains(got, "stale") {
		t.Fatalf("title should not show stale for quiet live service, got %q", got)
	}
}

func TestRenderTitleBarShowsRuntimeGroupSummary(t *testing.T) {
	t.Parallel()

	runtime := func(name, state, presentation string) tmux.Session {
		session := tmux.Session{
			ID:   name,
			Name: name,
			Windows: []tmux.Window{{
				ID:   name + "-win",
				Name: "main",
				Panes: []tmux.Pane{{
					ID:      name + "-pane",
					Session: name,
					Cockpit: &tmux.CockpitMeta{
						ManagedBy:         "openclaw_runtime_snapshot",
						Kind:              "runtime",
						Agent:             "openclaw-runtime",
						State:             state,
						PresentationGroup: presentation,
					},
				}},
			}},
		}
		return session
	}
	m := &Model{
		sessions: []tmux.Session{
			runtime("decision", "failed", "needs_decision"),
			runtime("route", "failed", "route_health"),
			runtime("handoff", "blocked", "delivery_handoff"),
		},
	}

	got := renderTitleBar(m, 180)
	for _, want := range []string{"3 items", "attention 3", "decision 1", "route 1", "handoff 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("title summary missing %q in %q", want, got)
		}
	}
}

func TestRenderTitleBarCountsFailedAgentProblems(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("failed-agent", "failed")
	m := &Model{sessions: []tmux.Session{session}}

	got := renderTitleBar(m, 180)
	for _, want := range []string{"1 items", "attention 1", "problems 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("title summary missing %q in %q", want, got)
		}
	}
}
