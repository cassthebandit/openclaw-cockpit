package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

const (
	defaultOpenClawRuntimeScript  = "/Users/cass/.openclaw/workspace/tools/openclaw_runtime/cockpit_snapshot.py"
	defaultOpenClawRuntimeTimeout = 20 * time.Second
	openClawRuntimeCardContract   = "runtime-card.v1"
	// runtimeOutputCapBytes bounds how much snapshot-script stdout is read
	// before JSON decoding; a producer past the cap is killed and reported
	// instead of being slurped into memory.
	runtimeOutputCapBytes = 4 << 20 // 4 MiB
	// runtimeStderrCapBytes bounds captured stderr used for error details.
	runtimeStderrCapBytes = 64 << 10 // 64 KiB
)

// cappedBuffer keeps at most limit bytes and silently discards the rest, so
// a runaway producer cannot grow the error-detail buffer without bound.
type cappedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := c.limit - c.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			c.buf.Write(p[:remaining])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

type openClawRuntimeSnapshot struct {
	CardContract string                 `json:"cardContract"`
	Summary      openClawRuntimeSummary `json:"summary"`
	Cards        []openClawRuntimeCard  `json:"cards"`
}

type openClawRuntimeSummary struct {
	RawRuntimeCardCount     int `json:"rawRuntimeCardCount"`
	VisibleRuntimeCardCount int `json:"visibleRuntimeCardCount"`
	GroupedRuntimeCardCount int `json:"groupedRuntimeCardCount"`
	HiddenRuntimeCardCount  int `json:"hiddenRuntimeCardCount"`
}

type openClawRuntimeCard struct {
	ID                     string   `json:"id"`
	DedupeKey              string   `json:"dedupeKey"`
	CardContract           string   `json:"cardContract"`
	Kind                   string   `json:"kind"`
	Label                  string   `json:"label"`
	DisplayTitle           string   `json:"displayTitle"`
	DisplayStatus          string   `json:"displayStatus"`
	DisplayGroup           string   `json:"displayGroup"`
	PresentationGroup      string   `json:"presentationGroup"`
	PresentationLabel      string   `json:"presentationLabel"`
	Reason                 string   `json:"reason"`
	NextAction             string   `json:"nextAction"`
	WhyVisible             string   `json:"whyVisible"`
	SuggestionKind         string   `json:"suggestionKind"`
	SuggestedAction        string   `json:"suggestedAction"`
	SuggestedCommand       string   `json:"suggestedCommand"`
	SuggestionConfidence   string   `json:"suggestionConfidence"`
	Skeleton               bool     `json:"skeleton"`
	SkeletonReason         string   `json:"skeletonReason"`
	Suppressed             bool     `json:"suppressed"`
	Runtime                string   `json:"runtime"`
	StateClass             string   `json:"stateClass"`
	Status                 string   `json:"status"`
	Severity               string   `json:"severity"`
	LifecycleState         string   `json:"lifecycleState"`
	SourceTruth            string   `json:"sourceTruth"`
	SourceProvenance       string   `json:"sourceProvenance"`
	Actionability          string   `json:"actionability"`
	TeardownPolicy         string   `json:"teardownPolicy"`
	PolicyScope            string   `json:"policyScope"`
	AggregationPolicy      string   `json:"aggregationPolicy"`
	Summary                string   `json:"summary"`
	DeliveryStatus         string   `json:"deliveryStatus"`
	RunID                  string   `json:"runId"`
	ChildSessionKey        string   `json:"childSessionKey"`
	OwnerKey               string   `json:"ownerKey"`
	ParentFlowID           string   `json:"parentFlowId"`
	LastEventAgeMs         *int64   `json:"lastEventAgeMs"`
	CreatedAgeMs           *int64   `json:"createdAgeMs"`
	RequesterSession       string   `json:"requesterSessionKey"`
	EvidenceIDs            []string `json:"evidenceIds"`
	SourceKinds            []string `json:"sourceKinds"`
	SourceCount            int      `json:"sourceCount"`
	SourceSummaries        []string `json:"sourceSummaries"`
	LogicalGroupKey        string   `json:"logicalGroupKey"`
	GroupedRecordCount     int      `json:"groupedRecordCount"`
	RawCardCount           int      `json:"rawCardCount"`
	VisibleCardCount       int      `json:"visibleCardCount"`
	GroupedEvidenceIDs     []string `json:"groupedEvidenceIds"`
	GroupedSourceKinds     []string `json:"groupedSourceKinds"`
	GroupedSourceSummaries []string `json:"groupedSourceSummaries"`
	summary                openClawRuntimeSummary
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
	stderr := &cappedBuffer{limit: runtimeStderrCapBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("runtime snapshot failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runtime snapshot failed: %w", err)
	}
	// Bounded read BEFORE any allocation-heavy decode: stop at cap+1 and kill
	// an over-cap producer instead of buffering whatever it emits.
	out, readErr := io.ReadAll(io.LimitReader(stdout, runtimeOutputCapBytes+1))
	overCap := len(out) > runtimeOutputCapBytes
	if overCap {
		cancel()
	}
	waitErr := cmd.Wait()
	if overCap {
		return nil, fmt.Errorf("runtime snapshot output exceeds the %d byte cap", runtimeOutputCapBytes)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("runtime snapshot timed out after %s", timeout)
	}
	if readErr != nil {
		return nil, fmt.Errorf("runtime snapshot failed: %w", readErr)
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = waitErr.Error()
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
	// The requested card limit is enforced locally even when the producer
	// returns more cards than asked for.
	if len(snapshot.Cards) > limit {
		snapshot.Cards = snapshot.Cards[:limit]
	}
	for i := range snapshot.Cards {
		snapshot.Cards[i].summary = snapshot.Summary
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
			ContractVersion:      runtimeCardContract(card),
			ManagedBy:            "openclaw_runtime_snapshot",
			Kind:                 "runtime",
			Agent:                runtime,
			Owner:                "",
			Project:              "",
			Goal:                 label,
			State:                state,
			DisplayStatus:        cardSafeLine(valueOr(card.DisplayStatus, state)),
			DisplayGroup:         cardSafeLine(valueOr(card.DisplayGroup, "unknown")),
			PresentationGroup:    cardSafeLine(card.PresentationGroup),
			PresentationLabel:    cardSafeLine(card.PresentationLabel),
			Reason:               cardSafeLine(card.Reason),
			NextAction:           cardSafeLine(card.NextAction),
			WhyVisible:           cardSafeLine(card.WhyVisible),
			SuggestionKind:       cardSafeLine(card.SuggestionKind),
			SuggestedAction:      cardSafeLine(card.SuggestedAction),
			SuggestedCommand:     cardSafeLine(card.SuggestedCommand),
			SuggestionConfidence: cardSafeLine(card.SuggestionConfidence),
			Skeleton:             boolString(card.Skeleton),
			SkeletonReason:       cardSafeLine(card.SkeletonReason),
			Suppressed:           boolString(card.Suppressed),
			LifecycleState:       cardSafeLine(runtimeCardLifecycle(card)),
			SourceTruth:          cardSafeLine(card.SourceTruth),
			SourceProvenance:     cardSafeLine(card.SourceProvenance),
			Actionability:        cardSafeLine(card.Actionability),
			TeardownPolicy:       cardSafeLine(card.TeardownPolicy),
			PolicyScope:          cardSafeLine(card.PolicyScope),
			AggregationPolicy:    cardSafeLine(card.AggregationPolicy),
			SourceKinds:          cardSafeLine(strings.Join(card.SourceKinds, ",")),
			SourceCount:          runtimeSourceCount(card),
			LogicalGroupKey:      cardSafeLine(card.LogicalGroupKey),
			GroupedRecordCount:   intString(card.GroupedRecordCount),
			RawCardCount:         intString(firstPositive(card.summary.RawRuntimeCardCount, card.RawCardCount)),
			VisibleCardCount:     intString(firstPositive(card.summary.VisibleRuntimeCardCount, card.VisibleCardCount)),
			GroupedCardCount:     intString(card.summary.GroupedRuntimeCardCount),
			HiddenCardCount:      intString(card.summary.HiddenRuntimeCardCount),
			SessionID:            firstNonEmpty(card.ChildSessionKey, card.RequesterSession, card.ParentFlowID, card.RunID, card.DedupeKey, card.ID),
			UpdatedAt:            activity.UTC().Format(time.RFC3339),
			EvidencePath:         runtimeCardEvidence(card),
			HoldReason:           runtimeCardHoldReason(card),
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
	if group := cardSafeLine(firstNonEmpty(card.PresentationLabel, card.PresentationGroup)); group != "" {
		lines = append(lines, "presentation: "+group)
	}
	if groupLine := runtimeCardGroupLine(card); groupLine != "" {
		lines = append(lines, "group: "+groupLine)
	}
	if summaryLine := runtimeCardSummaryLine(card); summaryLine != "" {
		lines = append(lines, "snapshot: "+summaryLine)
	}
	if why := cardSafeLine(card.WhyVisible); why != "" {
		lines = append(lines, "why visible: "+why)
	}
	if suggestion := runtimeCardSuggestionLine(card); suggestion != "" {
		lines = append(lines, "suggestion: "+suggestion)
	}
	if card.Skeleton || card.Suppressed || strings.TrimSpace(card.SkeletonReason) != "" {
		lines = append(lines, "skeleton: "+runtimeCardSkeletonLine(card))
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
	if groupedEvidence := cardSafeLine(strings.Join(card.GroupedEvidenceIDs, ",")); groupedEvidence != "" {
		lines = append(lines, "evidence: "+groupedEvidence)
	} else if evidence != "" {
		lines = append(lines, "evidence: "+cardSafeLine(evidence))
	}
	if groupedSources := cardSafeLine(strings.Join(card.GroupedSourceKinds, ",")); groupedSources != "" {
		lines = append(lines, "sources: "+runtimeSourceCount(card)+" "+groupedSources)
	} else if sources != "" || card.SourceCount > 0 {
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
	summaries := card.SourceSummaries
	if len(card.GroupedSourceSummaries) > 0 {
		summaries = card.GroupedSourceSummaries
	}
	for _, summary := range summaries {
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

func runtimeCardGroupLine(card openClawRuntimeCard) string {
	count := card.GroupedRecordCount
	if count <= 1 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d runtime cards", count)}
	if key := cardSafeLine(card.LogicalGroupKey); key != "" {
		parts = append(parts, "key "+key)
	}
	return strings.Join(parts, " · ")
}

func runtimeCardSummaryLine(card openClawRuntimeCard) string {
	raw := firstPositive(card.summary.RawRuntimeCardCount, card.RawCardCount)
	visible := firstPositive(card.summary.VisibleRuntimeCardCount, card.VisibleCardCount)
	grouped := card.summary.GroupedRuntimeCardCount
	hidden := card.summary.HiddenRuntimeCardCount
	if raw <= 0 && visible <= 0 && grouped <= 0 && hidden <= 0 {
		return ""
	}
	parts := []string{}
	if raw > 0 {
		parts = append(parts, fmt.Sprintf("raw %d", raw))
	}
	if visible > 0 {
		parts = append(parts, fmt.Sprintf("shown %d", visible))
	}
	if grouped > 0 {
		parts = append(parts, fmt.Sprintf("grouped %d", grouped))
	}
	if hidden > 0 {
		parts = append(parts, fmt.Sprintf("hidden %d", hidden))
	}
	return strings.Join(parts, " · ")
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

func runtimeCardSuggestionLine(card openClawRuntimeCard) string {
	parts := []string{}
	if action := cardSafeLine(card.SuggestedAction); action != "" {
		parts = append(parts, action)
	}
	if kind := cardSafeLine(card.SuggestionKind); kind != "" && kind != "none" {
		parts = append(parts, "kind "+kind)
	}
	if confidence := cardSafeLine(card.SuggestionConfidence); confidence != "" {
		parts = append(parts, "confidence "+confidence)
	}
	if command := cardSafeLine(card.SuggestedCommand); command != "" {
		parts = append(parts, "read-only hint: "+command)
	}
	return strings.Join(parts, " · ")
}

func runtimeCardSkeletonLine(card openClawRuntimeCard) string {
	parts := []string{}
	if card.Skeleton {
		parts = append(parts, "true")
	} else {
		parts = append(parts, "false")
	}
	if card.Suppressed {
		parts = append(parts, "suppressed")
	}
	if reason := cardSafeLine(card.SkeletonReason); reason != "" {
		parts = append(parts, reason)
	}
	return strings.Join(parts, " · ")
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

func intString(value int) string {
	if value > 0 {
		return strconv.Itoa(value)
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
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

func boolString(value bool) string {
	if value {
		return "true"
	}
	return ""
}
