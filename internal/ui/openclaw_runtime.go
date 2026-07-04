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
)

type openClawRuntimeSnapshot struct {
	Cards []openClawRuntimeCard `json:"cards"`
}

type openClawRuntimeCard struct {
	ID               string   `json:"id"`
	DedupeKey        string   `json:"dedupeKey"`
	Kind             string   `json:"kind"`
	Label            string   `json:"label"`
	DisplayTitle     string   `json:"displayTitle"`
	DisplayStatus    string   `json:"displayStatus"`
	DisplayGroup     string   `json:"displayGroup"`
	Reason           string   `json:"reason"`
	NextAction       string   `json:"nextAction"`
	Runtime          string   `json:"runtime"`
	StateClass       string   `json:"stateClass"`
	Status           string   `json:"status"`
	Severity         string   `json:"severity"`
	Summary          string   `json:"summary"`
	DeliveryStatus   string   `json:"deliveryStatus"`
	RunID            string   `json:"runId"`
	ChildSessionKey  string   `json:"childSessionKey"`
	OwnerKey         string   `json:"ownerKey"`
	ParentFlowID     string   `json:"parentFlowId"`
	LastEventAgeMs   *int64   `json:"lastEventAgeMs"`
	CreatedAgeMs     *int64   `json:"createdAgeMs"`
	RequesterSession string   `json:"requesterSessionKey"`
	EvidenceIDs      []string `json:"evidenceIds"`
	SourceKinds      []string `json:"sourceKinds"`
	SourceCount      int      `json:"sourceCount"`
	SourceSummaries  []string `json:"sourceSummaries"`
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
			ID:            "source-error",
			Kind:          "source",
			Label:         "OpenClaw runtime snapshot",
			DisplayTitle:  "OpenClaw runtime snapshot",
			DisplayStatus: "failed",
			DisplayGroup:  "needs_attention",
			Reason:        "runtime_failed",
			NextAction:    "inspect manually",
			Runtime:       "openclaw-runtime",
			StateClass:    "attention",
			Status:        "failed",
			Severity:      "error",
			Summary:       err.Error(),
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
	return snapshot.Cards, nil
}

func openClawRuntimeSession(card openClawRuntimeCard, index int, now time.Time) tmux.Session {
	id := sanitizedRuntimeID(firstNonEmpty(card.DedupeKey, card.ID))
	runtime := valueOr(card.Runtime, "openclaw-runtime")
	label := valueOr(card.DisplayTitle, valueOr(card.Label, runtime))
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
			ContractVersion: "display-only",
			ManagedBy:       "openclaw_runtime_snapshot",
			Kind:            "runtime",
			Agent:           runtime,
			Owner:           "",
			Project:         "",
			Goal:            label,
			State:           state,
			DisplayStatus:   valueOr(card.DisplayStatus, state),
			DisplayGroup:    valueOr(card.DisplayGroup, "unknown"),
			Reason:          card.Reason,
			NextAction:      card.NextAction,
			SourceKinds:     strings.Join(card.SourceKinds, ","),
			SourceCount:     runtimeSourceCount(card),
			SessionID:       firstNonEmpty(card.ChildSessionKey, card.RequesterSession, card.ParentFlowID, card.RunID, card.DedupeKey, card.ID),
			UpdatedAt:       activity.UTC().Format(time.RFC3339),
			EvidencePath:    runtimeCardEvidence(card),
			HoldReason:      runtimeCardHoldReason(card),
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

func runtimeCardPreview(card openClawRuntimeCard) string {
	evidence := runtimeCardEvidence(card)
	sources := strings.Join(card.SourceKinds, ",")
	lines := []string{
		valueOr(card.DisplayTitle, valueOr(card.Label, valueOr(card.Runtime, "OpenClaw runtime item"))),
		"",
		"runtime: " + valueOr(card.Runtime, "unknown"),
		"status: " + valueOr(card.DisplayStatus, valueOr(card.Status, "unknown")),
		"cause: " + valueOr(card.Reason, valueOr(card.Summary, "unknown")),
		"next: " + valueOr(card.NextAction, "inspect manually"),
	}
	if evidence != "" {
		lines = append(lines, "evidence: "+evidence)
	}
	if sources != "" || card.SourceCount > 0 {
		lines = append(lines, "sources: "+runtimeSourceCount(card)+" "+sources)
	}
	if card.Severity != "" {
		lines = append(lines, "severity: "+card.Severity)
	}
	if card.DeliveryStatus != "" {
		lines = append(lines, "delivery: "+card.DeliveryStatus)
	}
	if card.Summary != "" {
		lines = append(lines, "", card.Summary)
	}
	for _, summary := range card.SourceSummaries {
		summary = strings.TrimSpace(summary)
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
			lines = append(lines, field.name+": "+field.value)
		}
	}
	return strings.Join(lines, "\n")
}

func runtimeCardEvidence(card openClawRuntimeCard) string {
	if len(card.EvidenceIDs) > 0 {
		return strings.Join(card.EvidenceIDs, ",")
	}
	return firstNonEmpty(card.RunID, card.ChildSessionKey, card.ParentFlowID, card.DedupeKey, card.ID)
}

func runtimeCardHoldReason(card openClawRuntimeCard) string {
	if card.Summary != "" && runtimeCardState(card) != "running" {
		return card.Summary
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
