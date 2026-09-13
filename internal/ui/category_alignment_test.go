package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the real renderer for every registered category, including sparse
// rows, width transitions and collapse. Classification is pinned so this tests
// placement independently of lifecycle routing and current process state.
func TestEveryCategoryLeftJustified(t *testing.T) {
	for _, group := range allCockpitGroups() {
		for _, count := range []int{1, 2, 4, 5, 9} {
			t.Run(fmt.Sprintf("%s/%d", group.name, count), func(t *testing.T) {
				m := NewModel(nil, time.Second, 4, nil, false, true)
				m.SetOrganized(true)
				m.groupCollapseOverride = make(map[string]bool)
				m.height = 200
				m.preferredCols = 4
				for i := 0; i < count; i++ {
					s := captureTestSession(fmt.Sprintf("$%02d", i), fmt.Sprintf("%%%d", i), tmux.CockpitMeta{Kind: "agent", State: "running"})
					s.CreatedAt = time.Unix(1800000000+int64(i), 0)
					m.sessions = append(m.sessions, s)
					seedPreviewForSizingTest(m, s, 80, 10)
				}
				for _, width := range []int{361, 140, 60, 361} {
					m.width = width
					m.updatePreviewDimensions(count)
					for _, s := range m.sessions {
						m.classifyEntry(s.ID).group = group
						m.classifyEntry(s.ID).groupKnown = true
					}
					m.seedGroupCollapse(allCockpitGroups())
					delete(m.collapsedGroups, group.name)
					rendered := ansi.Strip(m.renderSessionCards(m.filteredSessions()))
					lines := strings.Split(rendered, "\n")
					cells := m.cursorCells()
					cols, _ := m.cardLayoutForGroup(group, count)
					if len(m.cardLayout) != count || len(cells) != count {
						t.Fatalf("missing cards at width%d", width)
					}
					for i, bounds := range m.cardLayout {
						col := i % cols
						if col == 0 {
							if bounds.screenX0 != 0 || !strings.HasPrefix(lines[bounds.gridTop], "╭") {
								t.Fatalf("%d columns: row starts at x%d: %q", cols, bounds.screenX0, lines[bounds.gridTop])
							}
						}
						if cells[i].id != bounds.sessionID || cells[i].row != i/cols || cells[i].center != (bounds.screenX0+bounds.screenX1+1)/2 {
							t.Fatalf("navigation differs from rendered card: %+v / %+v", cells[i], bounds)
						}
						m.previewOffset = 0
						m.pageContentHeight = len(lines)
						if !m.cardGeometryContains(bounds, cells[i].center, bounds.gridTop+1) {
							t.Fatal("mouse geometry misses card")
						}
					}
					m.toggleGroupCollapsed(group.name)
					m.renderSessionCards(m.filteredSessions())
					if len(m.cardLayout) != 0 || len(m.cursorCells()) != 0 {
						t.Fatal("collapsed cards still interactive")
					}
				}
			})
		}
	}
}
