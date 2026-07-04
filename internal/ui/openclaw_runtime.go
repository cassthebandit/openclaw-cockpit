package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

const (
	defaultOpenClawRuntimeScript  = "/Users/cass/.openclaw/workspace/tools/openclaw_runtime/cockpit_snapshot.py"
	defaultOpenClawRuntimeTimeout = 20 * time.Second
	openClawRuntimeCardContract   = "runtime-card.v1"
)

type openClawRuntimeSnapshot struct {
	CardContract string                `json:"cardContract"`
	Cards        []openClawRuntimeCard `json:"cards"`
}

type openClawRuntimeCard struct {
	ID                string   `json:"id"`
	DedupeKey         string   `json:"dedupeKey"`
	CardContract      string   `json:"cardContract"`
	Kind              string   `json:"kind"`
	Label             string   `json:"label"`
	DisplayTitle      string   `json:"displayTitle"`
	DisplayStatus     string   `json:"displayStatus"`
	DisplayGroup      string   `json:"displayGroup"`
	Reason            string   `json:"reason"`
	NextAction        string   `json:"nextAction"`
	Runtime           string   `json:"runtime"`
	StateClass        string   `json:"stateClass"`
	Status            string   `json:"status"`
	Severity          string   `json:"severity"`
	LifecycleState    string   `json:"lifecycleState"`
	SourceTruth       string   `json:"sourceTruth"`
	SourceProvenance  string   `json:"sourceProvenance"`
	Actionability     string   `json:"actionability"`
	TeardownPolicy    string   `json:"teardownPolicy"`
	PolicyScope       string   `json:"policyScope"`
	AggregationPolicy string   `json:"aggregationPolicy"`
	Summary           string   `json:"summary"`
	DeliveryStatus    string   `json:"deliveryStatus"`
	RunID             string   `json:"runId"`
	ChildSessionKey   string   `json:"childSessionKey"`
	OwnerKey          string   `json:"ownerKey"`
	ParentFlowID      string   `json:"parentFlowId"`
	LastEventAgeMs    *int64   `json:"lastEventAgeMs"`
	CreatedAgeMs      *int64   `json:"createdAgeMs"`
	RequesterSession  string   `json:"requesterSessionKey"`
	EvidenceIDs       []string `json:"evidenceIds"`
	SourceKinds       []string `json:"sourceKinds"`
	SourceCount       int      `json:"sourceCount"`
	SourceSummaries   []string `json:"sourceSummaries"`
}

// AppendOpenClawRuntimeSessions adds optional OpenClaw runtime cards to a tmux
// snapshot. It is used by both the live UI and --dump mode.
func AppendOpenClawRuntimeSessions(snapshot tmux.Snapshot, source RuntimeSource) tmux.Snapshot {
	snapshot.Sessions = append(snapshot.Sessions, openClawRuntimeSessions(source, snapshot.Timestamp)...)
	return snapshot
}

func openClawRuntimeSessions(source RuntimeSource, now time.Time) []tmux.Session {
	if !source.Enabled {
		return nil
	}
	cards, err := loadOpenClawRuntimeCards(source)
	if err != nil {
		cards = []openClawRuntimeCard{{
			ID:                "source-error",
			CardContract:      openClawRuntimeCardContract,
			Kind:              "source",
			Label:             "OpenClaw runtime snapshot",
			DisplayTitle:      "OpenClaw runtime snapshot",
			DisplayStatus:     "failed",
			DisplayGroup:      "needs_attention",
			Reason:            "runtime_failed",
			NextAction:        "inspect manually",
			Runtime:           "openclaw-runtime",
			StateClass:        "attention",
			Status:            "failed",
			Severity:          "error",
			LifecycleState:    "source_unavailable",
			SourceTruth:       "source_error",
			SourceProvenance:  "openclaw runtime snapshot adapter",
			Actionability:     "operator_action",
			TeardownPolicy:    "retry_next_poll",
			PolicyScope:       "source_identity",
			AggregationPolicy: "source_error",
			Summary:           err.Error(),
		}}
	}
	sessions := make([]tmux.Session, 0, len(cards))
	for i, card := range cards {
		sessions = append(sessions, openClawRuntimeSession(card, i, now))
	}
	return sessions
}

func loadOpenClawRuntimeCards(source RuntimeSource) ([]openClawRuntimeCard, error) {
	script := strings.TrimSpace(source.Script)
	if script == "" {
		script = defaultOpenClawRuntimeScript
	}
	limit := source.Limit
	if limit <= 0 {
		limit = 20
	}
	timeout := source.Timeout
	if timeout <= 0 {
		timeout = defaultOpenClawRuntimeTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", script, "--limit", strconv.Itoa(limit), "--format", "json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("runtime snapshot timed out after %s", timeout)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("runtime snapshot failed: %s", detail)
	}
	var snapshot openClawRuntimeSnapshot
	if err := json.Unmarshal(out, &snapshot); err != nil {
		return nil, fmt.Errorf("runtime snapshot returned invalid JSON: %w", err)
	}
	if err := validateOpenClawRuntimeSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot.Cards, nil
}

func validateOpenClawRuntimeSnapshot(snapshot openClawRuntimeSnapshot) error {
	if strings.TrimSpace(snapshot.CardContract) != openClawRuntimeCardContract {
		return fmt.Errorf("runtime snapshot contract = %q, want %s", snapshot.CardContract, openClawRuntimeCardContract)
	}
	for i, card := range snapshot.Cards {
		if strings.TrimSpace(card.CardContract) != openClawRuntimeCardContract {
			return fmt.Errorf("runtime card %d contract = %q, want %s", i, card.CardContract, openClawRuntimeCardContract)
		}
	}
	return nil
}

func openClawRuntimeSession(card openClawRuntimeCard, index int, now time.Time) tmux.Session {
	id := sanitizedRuntimeID(firstNonEmpty(card.DedupeKey, card.ID))
	runtime := cardSafeLine(valueOr(card.Runtime, "openclaw-runtime"))
	label := cardSafeLine(valueOr(card.DisplayTitle, valueOr(card.Label, runtime)))
	state := runtimeCardState(card)
	activity := runtimeCardActivity(card, now)
	sessionID := "openclaw-runtime:" + id
	paneID := "%openclaw-runtime:" + id

	pane := tmux.Pane{
		ID:           paneID,
		Title:        label,
		Active:       true,
		Window:       "@openclaw-runtime:" + id,
		Session:      sessionID,
		CurrentCmd:   "openclaw-runtime",
		CurrentPath:  "OpenClaw Runtime",
		LastActivity: activity,
		CreatedAt:    activity,
		Width:        100,
		Height:       24,
		Cockpit: &tmux.CockpitMeta{
			ContractVersion:   runtimeCardContract(card),
			ManagedBy:         "openclaw_runtime_snapshot",
			Kind:              "runtime",
			Agent:             runtime,
			Owner:             "",
			Project:           "",
			Goal:              label,
			State:             state,
			DisplayStatus:     cardSafeLine(valueOr(card.DisplayStatus, state)),
			DisplayGroup:      cardSafeLine(valueOr(card.DisplayGroup, "unknown")),
			Reason:            cardSafeLine(card.Reason),
			NextAction:        cardSafeLine(card.NextAction),
			LifecycleState:    cardSafeLine(runtimeCardLifecycle(card)),
			SourceTruth:       cardSafeLine(card.SourceTruth),
			SourceProvenance:  cardSafeLine(card.SourceProvenance),
			Actionability:     cardSafeLine(card.Actionability),
			TeardownPolicy:    cardSafeLine(card.TeardownPolicy),
			PolicyScope:       cardSafeLine(card.PolicyScope),
			AggregationPolicy: cardSafeLine(card.AggregationPolicy),
			SourceKinds:       cardSafeLine(strings.Join(card.SourceKinds, ",")),
			SourceCount:       runtimeSourceCount(card),
			SessionID:         firstNonEmpty(card.ChildSessionKey, card.RequesterSession, card.ParentFlowID, card.RunID, card.DedupeKey, card.ID),
			UpdatedAt:         activity.UTC().Format(time.RFC3339),
			EvidencePath:      runtimeCardEvidence(card),
			HoldReason:        runtimeCardHoldReason(card),
		},
		PreviewText: runtimeCardPreview(card),
	}

	return tmux.Session{
		ID:           sessionID,
		Name:         label,
		CreatedAt:    activity,
		LastActivity: activity,
		Windows: []tmux.Window{{
			ID:       "@openclaw-runtime:" + id,
			Name:     runtime,
			Active:   true,
			Session:  sessionID,
			Index:    index,
			LastPane: activity,
			Panes:    []tmux.Pane{pane},
		}},
	}
}

func runtimeCardActivity(card openClawRuntimeCard, now time.Time) time.Time {
	if card.LastEventAgeMs != nil && *card.LastEventAgeMs >= 0 {
		return now.Add(-time.Duration(*card.LastEventAgeMs) * time.Millisecond)
	}
	if card.CreatedAgeMs != nil && *card.CreatedAgeMs >= 0 {
		return now.Add(-time.Duration(*card.CreatedAgeMs) * time.Millisecond)
	}
	return now
}

func runtimeCardState(card openClawRuntimeCard) string {
	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(card.DisplayStatus, card.Status)))
	switch status {
	case "failed", "timed_out", "timeout", "lost", "error":
		return "failed"
	case "blocked":
		return "blocked"
	case "running", "active":
		return "running"
	case "done", "completed", "success":
		return "done"
	}
	switch strings.ToLower(strings.TrimSpace(card.StateClass)) {
	case "active":
		return "running"
	case "attention":
		return "review"
	case "done":
		return "done"
	default:
		return "review"
	}
}

func runtimeCardContract(card openClawRuntimeCard) string {
	return cardSafeLine(valueOr(card.CardContract, "runtime-card.unknown"))
}

func runtimeCardLifecycle(card openClawRuntimeCard) string {
	if lifecycle := cardSafeLine(card.LifecycleState); lifecycle != "" {
		return lifecycle
	}
	switch strings.ToLower(strings.TrimSpace(card.DisplayGroup)) {
	case "needs_attention":
		return "needs_attention"
	case "active":
		return "active"
	case "completed":
		return "resolved"
	case "unknown":
		return "unknown"
	}
	switch runtimeCardState(card) {
	case "running":
		return "active"
	case "done":
		return "resolved"
	case "failed", "blocked", "review":
		return "needs_attention"
	default:
		return "unknown"
	}
}

func runtimeCardPreview(card openClawRuntimeCard) string {
	evidence := runtimeCardEvidence(card)
	sources := cardSafeLine(strings.Join(card.SourceKinds, ","))
	lines := []string{
		cardSafeLine(valueOr(card.DisplayTitle, valueOr(card.Label, valueOr(card.Runtime, "OpenClaw runtime item")))),
		"",
		"runtime: " + cardSafeLine(valueOr(card.Runtime, "unknown")),
		"status: " + cardSafeLine(valueOr(card.DisplayStatus, valueOr(card.Status, "unknown"))),
		"lifecycle: " + runtimeCardLifecycle(card),
		"cause: " + cardSafeLine(valueOr(card.Reason, valueOr(card.Summary, "unknown"))),
		"next: " + cardSafeLine(valueOr(card.NextAction, "inspect manually")),
	}
	if action := cardSafeLine(card.Actionability); action != "" {
		lines = append(lines, "action: "+action)
	}
	if source := runtimeCardSourceLine(card); source != "" {
		lines = append(lines, "source: "+source)
	}
	if policy := runtimeCardPolicyLine(card); policy != "" {
		lines = append(lines, "policy: "+policy)
	}
	if teardown := cardSafeLine(card.TeardownPolicy); teardown != "" {
		lines = append(lines, "teardown: "+teardown)
	}
	if evidence != "" {
		lines = append(lines, "evidence: "+cardSafeLine(evidence))
	}
	if sources != "" || card.SourceCount > 0 {
		lines = append(lines, "sources: "+runtimeSourceCount(card)+" "+sources)
	}
	if card.Severity != "" {
		lines = append(lines, "severity: "+cardSafeLine(card.Severity))
	}
	if card.DeliveryStatus != "" {
		lines = append(lines, "delivery: "+cardSafeLine(card.DeliveryStatus))
	}
	if card.Summary != "" {
		lines = append(lines, "", cardSafeLine(card.Summary))
	}
	for _, summary := range card.SourceSummaries {
		summary = cardSafeLine(summary)
		if summary != "" && summary != card.Summary {
			lines = append(lines, "- "+summary)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"run", card.RunID},
		{"child", card.ChildSessionKey},
		{"flow", card.ParentFlowID},
		{"owner", card.OwnerKey},
		{"dedupe", firstNonEmpty(card.DedupeKey, card.ID)},
	} {
		if strings.TrimSpace(field.value) != "" {
			lines = append(lines, field.name+": "+cardSafeLine(field.value))
		}
	}
	return strings.Join(lines, "\n")
}

func runtimeCardSourceLine(card openClawRuntimeCard) string {
	truth := cardSafeLine(card.SourceTruth)
	provenance := cardSafeLine(card.SourceProvenance)
	switch {
	case truth != "" && provenance != "":
		return truth + " via " + provenance
	case truth != "":
		return truth
	default:
		return provenance
	}
}

func runtimeCardPolicyLine(card openClawRuntimeCard) string {
	scope := cardSafeLine(card.PolicyScope)
	aggregation := cardSafeLine(card.AggregationPolicy)
	if scope == "" && aggregation == "" {
		return ""
	}
	if scope == "source_identity" && (aggregation == "" || aggregation == "primary_identity") {
		return ""
	}
	if scope != "" && aggregation != "" {
		return scope + " · " + aggregation
	}
	if scope != "" {
		return scope
	}
	return aggregation
}

func runtimeCardEvidence(card openClawRuntimeCard) string {
	if len(card.EvidenceIDs) > 0 {
		return cardSafeLine(strings.Join(card.EvidenceIDs, ","))
	}
	return cardSafeLine(firstNonEmpty(card.RunID, card.ChildSessionKey, card.ParentFlowID, card.DedupeKey, card.ID))
}

func runtimeCardHoldReason(card openClawRuntimeCard) string {
	if card.Summary != "" && runtimeCardState(card) != "running" {
		return cardSafeLine(card.Summary)
	}
	return ""
}

func runtimeSourceCount(card openClawRuntimeCard) string {
	if card.SourceCount > 0 {
		return strconv.Itoa(card.SourceCount)
	}
	if len(card.SourceKinds) > 0 {
		return strconv.Itoa(len(card.SourceKinds))
	}
	return ""
}

func sanitizedRuntimeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "-", ":", "-", "/", "-", "\\", "-", "\t", "-")
	return replacer.Replace(id)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func valueOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}
