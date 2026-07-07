package ui

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

const (
	maxRuntimeTimelineEvents     = 250
	maxRuntimeTimelineDetailRows = 12
	runtimeTimelineDetailRowCap  = 20
)

type runtimeTimelineEventType string

const (
	runtimeTimelineAppeared          runtimeTimelineEventType = "appeared"
	runtimeTimelineResolved          runtimeTimelineEventType = "resolved"
	runtimeTimelineGrouped           runtimeTimelineEventType = "grouped"
	runtimeTimelineHiddenSummary     runtimeTimelineEventType = "hidden_summary"
	runtimeTimelineSourceUnavailable runtimeTimelineEventType = "source_unavailable"
)

type runtimeTimelineEvent struct {
	Type   runtimeTimelineEventType
	Key    string
	Label  string
	Reason string
	At     time.Time
}

type runtimeTimelineCard struct {
	Key               string
	Label             string
	State             string
	DisplayStatus     string
	DisplayGroup      string
	PresentationGroup string
	LifecycleState    string
	SourceTruth       string
	SourceKinds       string
	Suppressed        bool
	GroupedRecords    int
	SourceUnavailable bool
	Hidden            bool
}

type runtimeTimelineSnapshot struct {
	Cards             map[string]runtimeTimelineCard
	RawCards          int
	VisibleCards      int
	GroupedCards      int
	HiddenCards       int
	SourceUnavailable bool
}

func (m *Model) updateRuntimeTimeline(snapshot tmux.Snapshot) {
	current := buildRuntimeTimelineSnapshot(snapshot.Sessions)
	if !m.timelineSeeded {
		m.lastRuntimeTimeline = current
		m.timelineSeeded = true
		return
	}
	events := diffRuntimeTimeline(m.lastRuntimeTimeline, current, snapshot.Timestamp)
	m.appendRuntimeTimelineEvents(events)
	m.lastRuntimeTimeline = current
}

func (m *Model) appendRuntimeTimelineEvents(events []runtimeTimelineEvent) {
	if len(events) == 0 {
		return
	}
	m.runtimeTimeline = append(m.runtimeTimeline, events...)
	if overflow := len(m.runtimeTimeline) - maxRuntimeTimelineEvents; overflow > 0 {
		copy(m.runtimeTimeline, m.runtimeTimeline[overflow:])
		m.runtimeTimeline = m.runtimeTimeline[:maxRuntimeTimelineEvents]
	}
}

func buildRuntimeTimelineSnapshot(sessions []tmux.Session) runtimeTimelineSnapshot {
	snap := runtimeTimelineSnapshot{Cards: map[string]runtimeTimelineCard{}}
	bases := map[string][]runtimeTimelineCard{}
	for _, session := range sessions {
		pane, ok := runtimeTimelinePane(session)
		if !ok || pane.Cockpit == nil {
			continue
		}
		card := runtimeTimelineCardFromPane(session, pane)
		bases[card.Key] = append(bases[card.Key], card)
		snap.RawCards = max(snap.RawCards, cockpitIntField(pane.Cockpit.RawCardCount))
		snap.VisibleCards = max(snap.VisibleCards, cockpitIntField(pane.Cockpit.VisibleCardCount))
		snap.GroupedCards = max(snap.GroupedCards, cockpitIntField(pane.Cockpit.GroupedCardCount))
		snap.HiddenCards = max(snap.HiddenCards, cockpitIntField(pane.Cockpit.HiddenCardCount))
		if card.SourceUnavailable {
			snap.SourceUnavailable = true
		}
	}
	keys := make([]string, 0, len(bases))
	for key := range bases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cards := bases[key]
		sort.SliceStable(cards, func(i, j int) bool {
			return cards[i].semanticDiscriminator() < cards[j].semanticDiscriminator()
		})
		for i, card := range cards {
			finalKey := key
			if len(cards) > 1 {
				finalKey = fmt.Sprintf("%s#%s#%02d", key, shortHash(card.semanticDiscriminator()), i+1)
			}
			card.Key = finalKey
			snap.Cards[finalKey] = card
		}
	}
	return snap
}

func runtimeTimelinePane(session tmux.Session) (tmux.Pane, bool) {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if isOpenClawRuntimePane(pane) {
				return pane, true
			}
		}
	}
	return tmux.Pane{}, false
}

func runtimeTimelineCardFromPane(session tmux.Session, pane tmux.Pane) runtimeTimelineCard {
	meta := pane.Cockpit
	key := strings.TrimPrefix(session.ID, "openclaw-runtime:")
	grouped := cockpitIntField(meta.GroupedRecordCount)
	lifecycle := strings.ToLower(strings.TrimSpace(meta.LifecycleState))
	sourceTruth := strings.ToLower(strings.TrimSpace(meta.SourceTruth))
	state := strings.ToLower(strings.TrimSpace(meta.State))
	sourceUnavailable := lifecycle == "source_unavailable" ||
		strings.Contains(sourceTruth, "source_error") ||
		strings.Contains(sourceTruth, "source_unavailable") ||
		strings.Contains(key, "source-error")
	if sourceUnavailable {
		key = "source-error"
	}
	suppressed := strings.EqualFold(strings.TrimSpace(meta.Suppressed), "true")
	hidden := suppressed || state == "hidden" || state == "suppressed"
	return runtimeTimelineCard{
		Key:               firstNonEmpty(key, session.ID),
		Label:             cardSafeLine(firstNonEmpty(session.Name, meta.Goal, session.ID)),
		State:             state,
		DisplayStatus:     strings.ToLower(strings.TrimSpace(meta.DisplayStatus)),
		DisplayGroup:      strings.ToLower(strings.TrimSpace(meta.DisplayGroup)),
		PresentationGroup: strings.ToLower(strings.TrimSpace(meta.PresentationGroup)),
		LifecycleState:    lifecycle,
		SourceTruth:       sourceTruth,
		SourceKinds:       strings.ToLower(strings.TrimSpace(meta.SourceKinds)),
		Suppressed:        suppressed,
		GroupedRecords:    grouped,
		SourceUnavailable: sourceUnavailable,
		Hidden:            hidden,
	}
}

func (c runtimeTimelineCard) semanticDiscriminator() string {
	return strings.Join([]string{
		c.State,
		c.DisplayStatus,
		c.DisplayGroup,
		c.PresentationGroup,
		c.LifecycleState,
		c.SourceTruth,
		c.SourceKinds,
		c.Label,
	}, "|")
}

func diffRuntimeTimeline(prev, curr runtimeTimelineSnapshot, at time.Time) []runtimeTimelineEvent {
	var events []runtimeTimelineEvent
	sourceUnavailableStarted := curr.SourceUnavailable && !prev.SourceUnavailable
	if sourceUnavailableStarted {
		events = append(events, runtimeTimelineEvent{
			Type:   runtimeTimelineSourceUnavailable,
			Key:    "source-error",
			Label:  "runtime source",
			Reason: "source unavailable",
			At:     at,
		})
	}
	if curr.HiddenCards > prev.HiddenCards {
		events = append(events, runtimeTimelineEvent{
			Type:   runtimeTimelineHiddenSummary,
			Key:    "hidden-summary",
			Label:  "runtime snapshot",
			Reason: fmt.Sprintf("hidden %d -> %d", prev.HiddenCards, curr.HiddenCards),
			At:     at,
		})
	}

	keys := sortedRuntimeTimelineKeys(curr.Cards)
	for _, key := range keys {
		card := curr.Cards[key]
		prior, existed := prev.Cards[key]
		if !existed {
			if card.Hidden || sourceUnavailableStarted && card.SourceUnavailable {
				continue
			}
			events = append(events, runtimeTimelineEvent{
				Type:   runtimeTimelineAppeared,
				Key:    key,
				Label:  card.Label,
				Reason: "appeared",
				At:     at,
			})
			continue
		}
		if prior.GroupedRecords <= 1 && card.GroupedRecords > 1 || card.GroupedRecords > prior.GroupedRecords {
			events = append(events, runtimeTimelineEvent{
				Type:   runtimeTimelineGrouped,
				Key:    key,
				Label:  card.Label,
				Reason: fmt.Sprintf("grouped x%d", card.GroupedRecords),
				At:     at,
			})
		}
	}

	groupedAbsorption := curr.GroupedCards > prev.GroupedCards
	for _, key := range sortedRuntimeTimelineKeys(prev.Cards) {
		if _, ok := curr.Cards[key]; ok {
			continue
		}
		if sourceUnavailableStarted || groupedAbsorption || curr.HiddenCards > prev.HiddenCards {
			continue
		}
		if curr.RawCards > 0 && prev.RawCards > 0 && curr.RawCards >= prev.RawCards {
			continue
		}
		card := prev.Cards[key]
		events = append(events, runtimeTimelineEvent{
			Type:   runtimeTimelineResolved,
			Key:    key,
			Label:  card.Label,
			Reason: "resolved",
			At:     at,
		})
	}
	return events
}

func sortedRuntimeTimelineKeys(cards map[string]runtimeTimelineCard) []string {
	keys := make([]string, 0, len(cards))
	for key := range cards {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (m *Model) formatRuntimeTimelineLine(width int) string {
	if len(m.runtimeTimeline) == 0 {
		return ""
	}
	start := len(m.runtimeTimeline) - 3
	if start < 0 {
		start = 0
	}
	parts := make([]string, 0, len(m.runtimeTimeline)-start)
	for _, event := range m.runtimeTimeline[start:] {
		parts = append(parts, event.shortLabel())
	}
	line := "timeline: " + strings.Join(parts, " · ")
	if width > 0 && len(line) > width {
		return cardSafeLine(truncateRunes(line, max(0, width-1)))
	}
	return cardSafeLine(line)
}

func (m *Model) runtimeTimelineDetailLines(width int, limit int) []string {
	if m == nil || len(m.runtimeTimeline) == 0 || limit <= 0 {
		return nil
	}
	if limit > runtimeTimelineDetailRowCap {
		limit = runtimeTimelineDetailRowCap
	}
	start := len(m.runtimeTimeline) - limit
	if start < 0 {
		start = 0
	}
	lines := []string{cockpitSubtleLine(width, "timeline:")}
	for _, event := range m.runtimeTimeline[start:] {
		if line := event.detailLine(width); line != "" {
			lines = append(lines, cockpitSubtleLine(width, line))
		}
	}
	if len(lines) == 1 {
		return nil
	}
	return lines
}

func (e runtimeTimelineEvent) detailLine(width int) string {
	label := runtimeTimelineSafeLabel(e.Label)
	displayType := runtimeTimelineDisplayType(e.Type)
	parts := []string{runtimeTimelineEventClock(e.At), displayType}
	if label != "" {
		parts = append(parts, label)
	}
	line := strings.Join(parts, " ")
	if reason := e.detailReason(displayType, label); reason != "" {
		line += " - " + reason
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return cardSafeLine(line)
}

func (e runtimeTimelineEvent) detailReason(displayType string, label string) string {
	switch e.Type {
	case runtimeTimelineGrouped, runtimeTimelineHiddenSummary:
	default:
		return ""
	}
	reason := runtimeTimelineSafeReason(e.Reason)
	if reason == "" ||
		strings.EqualFold(reason, displayType) ||
		strings.EqualFold(reason, label) {
		return ""
	}
	return reason
}

func runtimeTimelineEventClock(at time.Time) string {
	if at.IsZero() {
		return "--:--:--"
	}
	return at.Format("15:04:05")
}

func runtimeTimelineDisplayType(eventType runtimeTimelineEventType) string {
	switch eventType {
	case runtimeTimelineHiddenSummary:
		return "hidden"
	case runtimeTimelineSourceUnavailable:
		return "source unavailable"
	case runtimeTimelineAppeared, runtimeTimelineResolved, runtimeTimelineGrouped:
		return string(eventType)
	default:
		return "event"
	}
}

func runtimeTimelineSafeLabel(value string) string {
	return runtimeTimelineSafeSummary(value, "runtime item")
}

func runtimeTimelineSafeReason(value string) string {
	return runtimeTimelineSafeSummary(value, "")
}

func runtimeTimelineSafeSummary(value string, fallback string) string {
	value = cardSafeLine(value)
	if value == "" {
		return fallback
	}
	if runtimeTimelineSummaryUnsafe(value) {
		return fallback
	}
	return value
}

func runtimeTimelineSummaryUnsafe(value string) bool {
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, `/\`) ||
		strings.HasPrefix(value, "~") ||
		strings.Contains(lower, ".openclaw") ||
		strings.Contains(lower, "/users/") ||
		strings.Contains(lower, "/private/") ||
		strings.Contains(lower, "workspace/memory/runs") ||
		strings.HasPrefix(strings.TrimSpace(value), "{") ||
		strings.HasPrefix(strings.TrimSpace(value), "[") {
		return true
	}
	return runtimeTimelineLooksLikeEvidenceIDs(value)
}

func runtimeTimelineLooksLikeEvidenceIDs(value string) bool {
	parts := strings.Split(value, ",")
	if len(parts) > 1 {
		for _, part := range parts {
			if !runtimeTimelineLooksLikeIDToken(strings.TrimSpace(part)) {
				return false
			}
		}
		return true
	}
	return runtimeTimelineLooksLikeOpaqueID(value)
}

func runtimeTimelineLooksLikeIDToken(value string) bool {
	if len(value) < 3 || strings.ContainsAny(value, " \t") {
		return false
	}
	hasSeparator := false
	hasDigit := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
		case unicode.IsDigit(r):
			hasDigit = true
		case r == '-' || r == '_' || r == ':' || r == '.':
			hasSeparator = true
		default:
			return false
		}
	}
	lower := strings.ToLower(value)
	return hasSeparator && (hasDigit || strings.Contains(lower, "id"))
}

func runtimeTimelineLooksLikeOpaqueID(value string) bool {
	if len(value) < 12 || strings.ContainsAny(value, " \t") {
		return false
	}
	if !runtimeTimelineLooksLikeIDToken(value) {
		return false
	}
	lower := strings.ToLower(value)
	return strings.Contains(lower, "evidence") || strings.Contains(lower, "secret")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func (e runtimeTimelineEvent) shortLabel() string {
	label := cardSafeLine(firstNonEmpty(e.Label, e.Key, "runtime"))
	switch e.Type {
	case runtimeTimelineGrouped:
		return fmt.Sprintf("grouped %s %s", label, cardSafeLine(e.Reason))
	case runtimeTimelineSourceUnavailable:
		return "source unavailable"
	case runtimeTimelineHiddenSummary:
		return cardSafeLine(e.Reason)
	default:
		return fmt.Sprintf("%s %s", e.Type, label)
	}
}

func shortHash(value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("%08x", h.Sum32())
}
