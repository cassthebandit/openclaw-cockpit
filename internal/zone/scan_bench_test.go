package zone

import (
	"fmt"
	"strings"
	"testing"
)

// benchFrame composes a synthetic cockpit-like frame: rows of bordered cards
// with ANSI styling, each card wrapped in a zone mark plus three control
// marks, matching the marker density of a busy organized wall.
func benchFrame(m *Manager, cards, cardLines, cardWidth int) string {
	var rows []string
	for c := 0; c < cards; c++ {
		controls := m.Mark(fmt.Sprintf("max:%d", c), "[^]") + " " +
			m.Mark(fmt.Sprintf("collapse:%d", c), "[-]") + " " +
			m.Mark(fmt.Sprintf("close:%d", c), "[x]")
		var card strings.Builder
		fmt.Fprintf(&card, "\x1b[38;5;250msession-%02d · running\x1b[0m %s\n", c, controls)
		for l := 0; l < cardLines; l++ {
			fmt.Fprintf(&card, "\x1b[38;5;246m%s\x1b[0m\n",
				strings.Repeat("x", cardWidth))
		}
		rows = append(rows, m.Mark(fmt.Sprintf("card:%d", c), strings.TrimRight(card.String(), "\n")))
	}
	return strings.Join(rows, "\n")
}

// BenchmarkScanWall measures Manager.Scan on a frame comparable to a large
// organized cockpit wall (40 cards x 4 zones, ANSI-heavy bodies).
func BenchmarkScanWall(b *testing.B) {
	m := New()
	frame := benchFrame(m, 40, 14, 200)
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Scan(frame)
	}
}

// BenchmarkScanSparse measures Scan on the same byte volume with only a few
// zones, isolating the per-marker cost from the linear text scan.
func BenchmarkScanSparse(b *testing.B) {
	m := New()
	frame := benchFrame(m, 4, 140, 200)
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Scan(frame)
	}
}
