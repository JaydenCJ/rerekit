// Package conflict parses text containing git-style conflict markers
// into an alternating sequence of context and conflict segments, and
// renders those segments back to bytes losslessly. Both merge style
// (ours/theirs) and diff3 style (ours/base/theirs) are understood, and
// CRLF marker lines round-trip unchanged.
package conflict

import (
	"fmt"
	"strings"
)

// Marker prefixes are the standard seven-character git conflict markers.
// Custom conflict-marker-size attributes are out of scope for 0.1.0.
const (
	markerOurs   = "<<<<<<<"
	markerBase   = "|||||||"
	markerSplit  = "======="
	markerTheirs = ">>>>>>>"
)

// Hunk is one conflict region, from its <<<<<<< line through its
// >>>>>>> line. Side lines are stored raw (any \r kept) so a parsed
// file renders back byte-identically; consumers that need normalized
// text strip \r themselves.
type Hunk struct {
	Ours   []string // lines between <<<<<<< and ||||||| or =======
	Base   []string // lines between ||||||| and ======= (diff3 only)
	Theirs []string // lines between ======= and >>>>>>>

	HasBase bool // true when a diff3 ||||||| section was present

	OursLabel   string // text after "<<<<<<< " (branch, ref, commit)
	BaseLabel   string // text after "||||||| "
	TheirsLabel string // text after ">>>>>>> "

	CRLF bool // the <<<<<<< marker line ended in \r\n
	Line int  // 1-based line number of the <<<<<<< marker
}

// Segment is either a run of plain context lines (Hunk == nil) or a
// single conflict hunk. A parsed File alternates between the two,
// except that two hunks may be adjacent when git emitted conflicts
// with no context line between them.
type Segment struct {
	Context []string
	Hunk    *Hunk
}

// File is a parsed conflicted file.
type File struct {
	Segments     []Segment
	FinalNewline bool // whether the original content ended with \n
}

// Hunks returns the conflict hunks in file order.
func (f *File) Hunks() []*Hunk {
	var hs []*Hunk
	for i := range f.Segments {
		if f.Segments[i].Hunk != nil {
			hs = append(hs, f.Segments[i].Hunk)
		}
	}
	return hs
}

// HasMarker is a cheap pre-check used when scanning a tree: it reports
// whether content contains a line that begins with the ours marker.
// Only Parse decides whether the markers form a well-formed conflict.
func HasMarker(content string) bool {
	if strings.HasPrefix(content, markerOurs) {
		return true
	}
	return strings.Contains(content, "\n"+markerOurs)
}

// marker classifies a line. It returns the marker prefix ("" when the
// line is plain context), the label after it, and whether the line
// carried a trailing \r. A marker line is exactly seven marker
// characters followed by end-of-line or a space.
func marker(line string) (kind, label string, crlf bool) {
	if strings.HasSuffix(line, "\r") {
		crlf = true
		line = line[:len(line)-1]
	}
	if len(line) < 7 {
		return "", "", false
	}
	prefix := line[:7]
	switch prefix {
	case markerOurs, markerBase, markerSplit, markerTheirs:
	default:
		return "", "", false
	}
	switch {
	case len(line) == 7:
		return prefix, "", crlf
	case line[7] == ' ':
		return prefix, line[8:], crlf
	}
	return "", "", false
}

// Parse splits content into context and conflict segments. It fails on
// malformed marker sequences (unterminated or nested conflicts, or a
// stray ||||||| section) because a tool that rewrites files must never
// guess at their structure.
func Parse(content string) (*File, error) {
	lines := strings.Split(content, "\n")
	f := &File{FinalNewline: true}
	if last := len(lines) - 1; lines[last] == "" {
		lines = lines[:last]
	} else {
		f.FinalNewline = false
	}

	var ctx []string
	flush := func() {
		if len(ctx) > 0 {
			f.Segments = append(f.Segments, Segment{Context: ctx})
			ctx = nil
		}
	}

	for i := 0; i < len(lines); {
		kind, label, crlf := marker(lines[i])
		if kind != markerOurs {
			// ======= and >>>>>>> outside a conflict are legitimate
			// content (setext headings, ASCII art); only <<<<<<< opens
			// a hunk.
			ctx = append(ctx, lines[i])
			i++
			continue
		}
		flush()
		h, next, err := parseHunk(lines, i, label, crlf)
		if err != nil {
			return nil, err
		}
		f.Segments = append(f.Segments, Segment{Hunk: h})
		i = next
	}
	flush()
	return f, nil
}

// parseHunk consumes one conflict starting at lines[start] (the <<<<<<<
// line) and returns the hunk plus the index of the first line after it.
func parseHunk(lines []string, start int, oursLabel string, crlf bool) (*Hunk, int, error) {
	h := &Hunk{OursLabel: oursLabel, CRLF: crlf, Line: start + 1}
	const (
		inOurs = iota
		inBase
		inTheirs
	)
	state := inOurs
	for i := start + 1; i < len(lines); i++ {
		kind, label, _ := marker(lines[i])
		switch {
		case kind == markerOurs:
			return nil, 0, fmt.Errorf("line %d: nested conflict marker inside conflict started at line %d", i+1, h.Line)
		case kind == markerBase:
			if state != inOurs {
				return nil, 0, fmt.Errorf("line %d: unexpected ||||||| in conflict started at line %d", i+1, h.Line)
			}
			h.HasBase = true
			h.BaseLabel = label
			state = inBase
		case kind == markerSplit && label == "" && state != inTheirs:
			// git always emits ======= bare; a labelled one is content.
			state = inTheirs
		case kind == markerTheirs:
			if state != inTheirs {
				return nil, 0, fmt.Errorf("line %d: >>>>>>> before ======= in conflict started at line %d", i+1, h.Line)
			}
			h.TheirsLabel = label
			return h, i + 1, nil
		default:
			switch state {
			case inOurs:
				h.Ours = append(h.Ours, lines[i])
			case inBase:
				h.Base = append(h.Base, lines[i])
			case inTheirs:
				h.Theirs = append(h.Theirs, lines[i])
			}
		}
	}
	return nil, 0, fmt.Errorf("line %d: unterminated conflict (no >>>>>>> before end of file)", h.Line)
}

// Render reconstructs the file content. Parse followed by Render is
// byte-identical for any input Parse accepts.
func (f *File) Render() string {
	var b strings.Builder
	for i := range f.Segments {
		s := &f.Segments[i]
		if s.Hunk == nil {
			for _, l := range s.Context {
				b.WriteString(l)
				b.WriteByte('\n')
			}
			continue
		}
		h := s.Hunk
		eol := "\n"
		if h.CRLF {
			eol = "\r\n"
		}
		writeMarker(&b, markerOurs, h.OursLabel, eol)
		for _, l := range h.Ours {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		if h.HasBase {
			writeMarker(&b, markerBase, h.BaseLabel, eol)
			for _, l := range h.Base {
				b.WriteString(l)
				b.WriteByte('\n')
			}
		}
		writeMarker(&b, markerSplit, "", eol)
		for _, l := range h.Theirs {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		writeMarker(&b, markerTheirs, h.TheirsLabel, eol)
	}
	out := b.String()
	if !f.FinalNewline {
		out = strings.TrimSuffix(out, "\n")
	}
	return out
}

func writeMarker(b *strings.Builder, prefix, label, eol string) {
	b.WriteString(prefix)
	if label != "" {
		b.WriteByte(' ')
		b.WriteString(label)
	}
	b.WriteString(eol)
}
