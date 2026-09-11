package ui

import (
	"strings"
	"testing"
)

// Repeated snapshots with unchanged, ANSI-heavy pane content.
func BenchmarkPR3UnchangedLifecycleRefresh(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	for si := range m.sessions {
		for wi := range m.sessions[si].Windows {
			for pi := range m.sessions[si].Windows[wi].Panes {
				p := &m.sessions[si].Windows[wi].Panes[pi]
				p.PreviewText = benchPaneBody(600, "\x1b[38;5;108m* working...\x1b[0m esc to interrupt\n")
			}
		}
	}
	m.refreshLifecycleVerdicts()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.refreshLifecycleVerdicts()
	}
}

func BenchmarkPR3ChangedLifecycleRefresh(b *testing.B) {
	for _, all := range []bool{false, true} {
		name := "one-pane"
		if all {
			name = "all-panes"
		}
		b.Run(name, func(b *testing.B) {
			m := benchWallModel(b, 30, 8)
			contents := []string{benchPaneBody(600, "Thinking… esc to interrupt"), benchPaneBody(600, "Goal achieved\n› ")}
			for si := range m.sessions {
				m.sessions[si].Windows[0].Panes[0].PreviewText = contents[0]
			}
			m.refreshLifecycleVerdicts()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for si := range m.sessions {
					if si == 0 || all {
						m.sessions[si].Windows[0].Panes[0].PreviewText = contents[i%2]
					}
				}
				m.refreshLifecycleVerdicts()
			}
		})
	}
}

// Fresh snapshots generally carry equal text in new string allocations. This
// separates content-equality cost from the cheap same-string cache hit above.
func BenchmarkPR3FreshSnapshotLifecycleRefresh(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	text := benchPaneBody(600, "Thinking… esc to interrupt")
	copies := make([][2]string, len(m.sessions))
	for si := range m.sessions {
		copies[si] = [2]string{strings.Clone(text), strings.Clone(text)}
		m.sessions[si].Windows[0].Panes[0].PreviewText = copies[si][0]
	}
	m.refreshLifecycleVerdicts()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for si := range m.sessions {
			m.sessions[si].Windows[0].Panes[0].PreviewText = copies[si][i%2]
		}
		m.refreshLifecycleVerdicts()
	}
}
