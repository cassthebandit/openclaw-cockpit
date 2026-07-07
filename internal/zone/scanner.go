package zone

import (
	"unicode/utf8"

	"github.com/muesli/ansi"
)

const eof = 1

type scanner struct {
	manager   *Manager
	enabled   bool
	iteration int

	input string
	pos   int

	newlines int
	tracked  map[string]*ZoneInfo
	found    map[string]*ZoneInfo
}

func newScanner(m *Manager, input string, iteration int) *scanner {
	return &scanner{
		manager:   m,
		enabled:   m.Enabled(),
		iteration: iteration,
		input:     input,
		tracked:   make(map[string]*ZoneInfo),
		found:     make(map[string]*ZoneInfo),
	}
}

func (s *scanner) run() {
	var out []byte
	out = make([]byte, 0, len(s.input))
	lineStart := 0

	for s.pos < len(s.input) {
		if rid, end, ok := zoneMarkerAt(s.input, s.pos); ok {
			s.trackMarker(rid, ansi.PrintableRuneWidth(string(out[lineStart:])))
			s.pos = end
			continue
		}

		r, width := utf8.DecodeRuneInString(s.input[s.pos:])
		if r == utf8.RuneError && width == 0 {
			break
		}
		out = append(out, s.input[s.pos:s.pos+width]...)
		s.pos += width
		if r == '\n' {
			s.newlines++
			lineStart = len(out)
		}
	}

	s.input = string(out)
}

func (s *scanner) trackMarker(rid string, x int) {
	if !s.enabled {
		return
	}

	if item, ok := s.tracked[rid]; ok {
		item.EndX = x - 1
		item.EndY = s.newlines
		s.found[rid] = item
		delete(s.tracked, rid)
		return
	}

	s.tracked[rid] = &ZoneInfo{
		Id:        s.manager.getReverse(rid),
		iteration: s.iteration,
		StartX:    x,
		StartY:    s.newlines,
	}
}

func zoneMarkerAt(input string, pos int) (string, int, bool) {
	if pos+4 > len(input) || input[pos] != identStart || input[pos+1] != identBracket {
		return "", pos, false
	}

	end := pos + 2
	if end >= len(input) || !isNumberByte(input[end]) {
		return "", pos, false
	}
	for end < len(input) && isNumberByte(input[end]) {
		end++
	}
	if end >= len(input) || input[end] != identEnd {
		return "", pos, false
	}
	end++
	return input[pos:end], end, true
}

func isNumberByte(b byte) bool {
	return b >= '0' && b <= '9'
}
