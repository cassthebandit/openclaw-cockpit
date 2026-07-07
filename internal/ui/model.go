// File model.go defines the core Bubble Tea model structure and shared
// constants that govern tmuxwatch behaviour.
package ui

import (
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

const (
	defaultPollInterval   = time.Second
	minPreviewHeight      = 6
	maxOverviewBodyLines  = 10
	maxCapturesPerTick    = 6
	cardPadding           = 1
	cardColumnGap         = 1
	closeLabel            = "[x]"
	maximizeLabel         = "[^]"
	restoreLabel          = "[v]"
	collapseLabel         = "[-]"
	expandLabel           = "[+]"
	groupCaretExpanded    = "▾"
	groupCaretCollapsed   = "▸"
	scrollStep            = 3
	pulseDuration         = 1500 * time.Millisecond
	quitChordWindow       = 600 * time.Millisecond
	staleThreshold        = time.Hour
	minCaptureLines       = 80
	maxCaptureLines       = 600
	captureSlackLines     = 40
	borderColorBase       = "240"
	borderColorFocus      = "252"
	borderColorPulse      = "250"
	borderColorCursor     = "252"
	borderColorHover      = "245"
	borderColorExitFail   = "167"
	borderColorExitOK     = "108"
	borderColorWaiting    = "179"
	borderColorBlocked    = "173"
	borderColorStale      = "244"
	groupColorActive      = "108"
	groupColorInactive    = "179"
	groupColorFailed      = "167"
	groupColorOperational = "173"
	groupColorSubsystem   = "67"
	groupColorServices    = "245"
	headerColorBase       = "250"
	headerColorFocus      = "252"
	headerColorPulse      = "250"
	headerColorCursor     = "252"
	headerColorExitFail   = "167"
	headerColorExitOK     = "108"
	headerColorWaiting    = "179"
	headerColorBlocked    = "173"
	headerColorStale      = "244"
)

type viewMode int

const (
	viewModeOverview viewMode = iota
	viewModeDetail
)

type (
	snapshotMsg    struct{ snapshot tmux.Snapshot }
	statusMsg      string
	paneContentMsg struct {
		sessionID string
		paneID    string
		text      string
		err       error
	}
	paneVarsMsg struct {
		sessionID string
		paneID    string
		vars      map[string]string
		err       error
	}
	errMsg        struct{ err error }
	tickMsg       struct{}
	searchBlurMsg struct{}
)

type sessionPreview struct {
	viewport    *viewport.Model
	paneID      string
	lastContent string
	lastChanged time.Time
	vars        map[string]string
	autoFollow  bool
}

type cardBounds struct {
	sessionID      string
	zoneID         string
	closeZoneID    string
	maximizeZoneID string
	collapseZoneID string
}

// groupZone records the clickable hit-test region for an accordion group
// divider so a mouse click on the divider can toggle that group's collapse.
type groupZone struct {
	name   string
	zoneID string
}

type commandItem struct {
	label   string
	enabled bool
	run     func(*Model) tea.Cmd
}

// Model owns the Bubble Tea state machine and cached tmux snapshot data.
type Model struct {
	client        *tmux.Client
	pollInterval  time.Duration
	captureBudget int
	zonePrefix    string

	width  int
	height int

	sessions []tmux.Session

	captureOffset int

	previews        map[string]*sessionPreview
	hidden          map[string]struct{}
	stale           map[string]struct{}
	collapsed       map[string]struct{}
	collapsedGroups map[string]struct{}
	seededGroups    map[string]struct{}
	groupZones      []groupZone

	paletteOpen     bool
	paletteIndex    int
	paletteCommands []commandItem

	searchInput  textinput.Model
	searching    bool
	searchQuery  string
	commandInput textinput.Model
	commanding   bool
	viewFilter   string
	toast        *toastState

	focusedSession string
	cardLayout     []cardBounds
	cursorSession  string
	hoveredSession string
	hoveredControl string

	viewMode        viewMode
	detailSession   string
	activeTab       int
	cardCols        int
	preferredCols   int
	cardInnerWidth  int
	cardInnerHeight int
	previewOffset   int

	// Whole-wall (page) scroll state. pageOffset is the top line of the composed
	// grid currently shown; the rest are recomputed each render (windowGrid) and
	// read by input handlers on the following frame.
	pageOffset        int
	pageScrollEngaged bool
	pageMaxOffset     int
	pageContentHeight int
	cardTopLine       map[string]int
	cardLineHeight    map[string]int
	tabSessionIDs     []string
	footer            *viewport.Model
	footerHeight      int

	debugMsgs   []tea.Msg
	traceMouse  bool
	hostname    string
	monitorOnly bool
	organized   bool
	runtime     RuntimeSource

	timelineSeeded      bool
	lastRuntimeTimeline runtimeTimelineSnapshot
	runtimeTimeline     []runtimeTimelineEvent

	artifactOutcomes  map[string]string
	lifecycleVerdicts map[string]paneLifecycleVerdict

	lastUpdated time.Time
	err         error
	inflight    bool

	cachedStatus      string
	paneParseWarnings int
	lastCtrlC         time.Time
	lastEsc           time.Time
}

// RuntimeSource configures an optional OpenClaw runtime snapshot source that is
// rendered as read-only display panes alongside live tmux sessions.
type RuntimeSource struct {
	Enabled bool
	Script  string
	Limit   int
	Timeout time.Duration
}

// SetPreferredColumns caps the overview grid at a caller-selected column
// count while still allowing layout to shrink when the terminal is too narrow.
func (m *Model) SetPreferredColumns(cols int) {
	if cols < 0 {
		cols = 0
	}
	m.preferredCols = cols
}

// SetOrganized enables cockpit grouping and sorting in the overview wall.
func (m *Model) SetOrganized(enabled bool) {
	m.organized = enabled
}

// SetOpenClawRuntimeSource enables read-only OpenClaw runtime cards.
func (m *Model) SetOpenClawRuntimeSource(script string, limit int, timeout time.Duration) {
	if limit <= 0 {
		limit = 20
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	m.runtime = RuntimeSource{
		Enabled: true,
		Script:  strings.TrimSpace(script),
		Limit:   limit,
		Timeout: timeout,
	}
}

// sessionLabel strips leading sigils from tmux session identifiers for
// friendlier display.
func sessionLabel(id string) string {
	if len(id) > 1 && id[0] == '$' {
		return id[1:]
	}
	return id
}

// NewModel builds a Model with defaults and the provided tmux client.
func NewModel(client *tmux.Client, poll time.Duration, captureBudget int, debugMsgs []tea.Msg, traceMouse bool, monitorOnly bool) *Model {
	if poll <= 0 {
		poll = defaultPollInterval
	}
	if captureBudget <= 0 {
		captureBudget = maxCapturesPerTick
	}
	ti := textinput.New()
	ti.Placeholder = "filter sessions, windows, panes"
	ti.CharLimit = 256
	ti.Prompt = "/ "
	ci := textinput.New()
	ci.Placeholder = "pulse, all, decision, route, handoff, services"
	ci.CharLimit = 64
	ci.Prompt = ": "
	return &Model{
		client:            client,
		pollInterval:      poll,
		captureBudget:     captureBudget,
		zonePrefix:        zone.NewPrefix(),
		previews:          make(map[string]*sessionPreview),
		hidden:            make(map[string]struct{}),
		stale:             make(map[string]struct{}),
		collapsed:         make(map[string]struct{}),
		collapsedGroups:   make(map[string]struct{}),
		seededGroups:      make(map[string]struct{}),
		artifactOutcomes:  make(map[string]string),
		lifecycleVerdicts: make(map[string]paneLifecycleVerdict),
		cardTopLine:       make(map[string]int),
		cardLineHeight:    make(map[string]int),
		searchInput:       ti,
		commandInput:      ci,
		cardLayout:        make([]cardBounds, 0),
		cardCols:          1,
		cardInnerWidth:    20,
		cardInnerHeight:   minPreviewHeight,
		inflight:          true,
		previewOffset:     topPaddingLines,
		debugMsgs:         append([]tea.Msg(nil), debugMsgs...),
		traceMouse:        traceMouse,
		monitorOnly:       monitorOnly,
		toast:             &toastState{},
		viewMode:          viewModeOverview,
		tabSessionIDs:     make([]string, 0),
		footer:            footerViewport(),
		footerHeight:      3,
		hostname:          lookupHostname(),
	}
}

func lookupHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(h))
}

func footerViewport() *viewport.Model {
	v := viewport.New(viewport.WithHeight(3))
	v.MouseWheelEnabled = false
	v.MouseWheelDelta = scrollStep
	return &v
}

// Init starts the initial tmux snapshot fetch and ticking loop.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		fetchSnapshotCmd(m.client, m.runtime),
		scheduleTick(m.pollInterval),
	}
	for _, msg := range m.debugMsgs {
		cmds = append(cmds, emitMsg(msg))
	}
	m.debugMsgs = nil
	return tea.Batch(cmds...)
}
