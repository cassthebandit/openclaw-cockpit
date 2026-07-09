// Package tmux provides a thin wrapper around the tmux binary so the UI can
// reason about sessions, windows, and panes without parsing shell output.
package tmux

import (
	"fmt"
	"strings"
	"time"
)

// Session represents a tmux session with its windows populated.
type Session struct {
	ID        string
	Name      string
	Attached  bool
	CreatedAt time.Time
	// LastActivity records the most recent activity timestamp reported by tmux.
	LastActivity time.Time
	Windows      []Window
}

// Window represents a tmux window and its panes.
type Window struct {
	ID       string
	Name     string
	Active   bool
	Session  string
	Index    int
	LastPane time.Time
	Panes    []Pane
}

// Pane represents a tmux pane.
type Pane struct {
	ID            string
	Title         string
	Active        bool
	Window        string
	Session       string
	CurrentCmd    string
	CurrentPath   string
	TTY           string
	LastActivity  time.Time
	CreatedAt     time.Time
	Width, Height int
	Dead          bool
	DeadStatus    int
	Cockpit       *CockpitMeta `json:",omitempty"`
	PreviewText   string       `json:",omitempty"`
}

// Snapshot contains the state of the tmux server.
type Snapshot struct {
	Sessions          []Session
	Timestamp         time.Time
	PaneParseWarnings int
}

// CockpitMeta holds optional OpenClaw/Cass item metadata carried by tmux pane
// user options. Plain tmux panes leave this nil and keep the old UI behaviour.
type CockpitMeta struct {
	ContractVersion      string `json:",omitempty"`
	ManagedBy            string `json:",omitempty"`
	Kind                 string `json:",omitempty"`
	Agent                string `json:",omitempty"`
	Owner                string `json:",omitempty"`
	Project              string `json:",omitempty"`
	Goal                 string `json:",omitempty"`
	State                string `json:",omitempty"`
	DisplayStatus        string `json:",omitempty"`
	DisplayGroup         string `json:",omitempty"`
	PresentationGroup    string `json:",omitempty"`
	PresentationLabel    string `json:",omitempty"`
	Reason               string `json:",omitempty"`
	NextAction           string `json:",omitempty"`
	WhyVisible           string `json:",omitempty"`
	SuggestionKind       string `json:",omitempty"`
	SuggestedAction      string `json:",omitempty"`
	SuggestedCommand     string `json:",omitempty"`
	SuggestionConfidence string `json:",omitempty"`
	Skeleton             string `json:",omitempty"`
	SkeletonReason       string `json:",omitempty"`
	Suppressed           string `json:",omitempty"`
	LifecycleState       string `json:",omitempty"`
	SourceTruth          string `json:",omitempty"`
	SourceProvenance     string `json:",omitempty"`
	Actionability        string `json:",omitempty"`
	TeardownPolicy       string `json:",omitempty"`
	PolicyScope          string `json:",omitempty"`
	AggregationPolicy    string `json:",omitempty"`
	SourceKinds          string `json:",omitempty"`
	SourceCount          string `json:",omitempty"`
	LogicalGroupKey      string `json:",omitempty"`
	GroupedRecordCount   string `json:",omitempty"`
	RawCardCount         string `json:",omitempty"`
	VisibleCardCount     string `json:",omitempty"`
	GroupedCardCount     string `json:",omitempty"`
	HiddenCardCount      string `json:",omitempty"`
	RunRoot              string `json:",omitempty"`
	ThreadID             string `json:",omitempty"`
	SessionID            string `json:",omitempty"`
	StartedAt            string `json:",omitempty"`
	UpdatedAt            string `json:",omitempty"`
	CompletedAt          string `json:",omitempty"`
	ExitCode             string `json:",omitempty"`
	TTL                  string `json:",omitempty"`
	CleanupPolicy        string `json:",omitempty"`
	EvidencePath         string `json:",omitempty"`
	HoldReason           string `json:",omitempty"`
	WhyHeadless          string `json:",omitempty"`
	PaneLog              string `json:",omitempty"`
	ProgressPath         string `json:",omitempty"`
	EndReason            string `json:",omitempty"`
	RouteFailure         string `json:",omitempty"`
	TeardownMarkedAt     string `json:",omitempty"`
	TeardownReason       string `json:",omitempty"`
	JanitorState         string `json:",omitempty"`
	LastMeaningfulAt     string `json:",omitempty"`
}

// HasData reports whether any cockpit metadata field is populated.
func (m CockpitMeta) HasData() bool {
	return strings.TrimSpace(m.ContractVersion) != "" ||
		strings.TrimSpace(m.ManagedBy) != "" ||
		strings.TrimSpace(m.Kind) != "" ||
		strings.TrimSpace(m.Agent) != "" ||
		strings.TrimSpace(m.Owner) != "" ||
		strings.TrimSpace(m.Project) != "" ||
		strings.TrimSpace(m.Goal) != "" ||
		strings.TrimSpace(m.State) != "" ||
		strings.TrimSpace(m.DisplayStatus) != "" ||
		strings.TrimSpace(m.DisplayGroup) != "" ||
		strings.TrimSpace(m.PresentationGroup) != "" ||
		strings.TrimSpace(m.PresentationLabel) != "" ||
		strings.TrimSpace(m.Reason) != "" ||
		strings.TrimSpace(m.NextAction) != "" ||
		strings.TrimSpace(m.WhyVisible) != "" ||
		strings.TrimSpace(m.SuggestionKind) != "" ||
		strings.TrimSpace(m.SuggestedAction) != "" ||
		strings.TrimSpace(m.SuggestedCommand) != "" ||
		strings.TrimSpace(m.SuggestionConfidence) != "" ||
		strings.TrimSpace(m.Skeleton) != "" ||
		strings.TrimSpace(m.SkeletonReason) != "" ||
		strings.TrimSpace(m.Suppressed) != "" ||
		strings.TrimSpace(m.LifecycleState) != "" ||
		strings.TrimSpace(m.SourceTruth) != "" ||
		strings.TrimSpace(m.SourceProvenance) != "" ||
		strings.TrimSpace(m.Actionability) != "" ||
		strings.TrimSpace(m.TeardownPolicy) != "" ||
		strings.TrimSpace(m.PolicyScope) != "" ||
		strings.TrimSpace(m.AggregationPolicy) != "" ||
		strings.TrimSpace(m.SourceKinds) != "" ||
		strings.TrimSpace(m.SourceCount) != "" ||
		strings.TrimSpace(m.LogicalGroupKey) != "" ||
		strings.TrimSpace(m.GroupedRecordCount) != "" ||
		strings.TrimSpace(m.RawCardCount) != "" ||
		strings.TrimSpace(m.VisibleCardCount) != "" ||
		strings.TrimSpace(m.GroupedCardCount) != "" ||
		strings.TrimSpace(m.HiddenCardCount) != "" ||
		strings.TrimSpace(m.RunRoot) != "" ||
		strings.TrimSpace(m.ThreadID) != "" ||
		strings.TrimSpace(m.SessionID) != "" ||
		strings.TrimSpace(m.StartedAt) != "" ||
		strings.TrimSpace(m.UpdatedAt) != "" ||
		strings.TrimSpace(m.CompletedAt) != "" ||
		strings.TrimSpace(m.ExitCode) != "" ||
		strings.TrimSpace(m.TTL) != "" ||
		strings.TrimSpace(m.CleanupPolicy) != "" ||
		strings.TrimSpace(m.EvidencePath) != "" ||
		strings.TrimSpace(m.HoldReason) != "" ||
		strings.TrimSpace(m.WhyHeadless) != "" ||
		strings.TrimSpace(m.PaneLog) != "" ||
		strings.TrimSpace(m.ProgressPath) != "" ||
		strings.TrimSpace(m.EndReason) != "" ||
		strings.TrimSpace(m.RouteFailure) != "" ||
		strings.TrimSpace(m.TeardownMarkedAt) != "" ||
		strings.TrimSpace(m.TeardownReason) != "" ||
		strings.TrimSpace(m.JanitorState) != "" ||
		strings.TrimSpace(m.LastMeaningfulAt) != ""
}

// DisplayOnly reports metadata that was manually adopted for visibility but
// does not carry a managed lifecycle contract.
func (m CockpitMeta) DisplayOnly() bool {
	return strings.EqualFold(strings.TrimSpace(m.ContractVersion), "display-only") ||
		strings.EqualFold(strings.TrimSpace(m.ManagedBy), "manual_adopt")
}

// TitleOrCmd returns the most descriptive label for a pane for display
// purposes, preferring the title and falling back to the running command.
func (p Pane) TitleOrCmd() string {
	title := strings.TrimSpace(p.Title)
	if title != "" {
		return title
	}
	cmd := strings.TrimSpace(p.CurrentCmd)
	if cmd != "" {
		return cmd
	}
	return "pane"
}

// StatusString reports whether the pane is still running or the exit code if
// it has terminated.
func (p Pane) StatusString() string {
	if !p.Dead {
		return "running"
	}
	if p.DeadStatus == 0 {
		return "exit 0"
	}
	return fmt.Sprintf("exit %d", p.DeadStatus)
}
