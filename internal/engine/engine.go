// Package engine implements the two halves of rerekit's core loop:
//
//   - Extract: given the preimage of a conflicted file (snapshotted by
//     `rerekit snap`) and the file after a human resolved it, recover
//     exactly which lines replaced each conflict hunk.
//   - Apply: given a freshly conflicted file and a lookup over recorded
//     resolutions, splice the recorded lines back in place of every
//     hunk whose fingerprint matches.
//
// Extraction anchors on the context segments between hunks: they must
// reappear, in order, in the resolved file. That property holds for how
// conflicts are actually resolved (the human edits between the markers)
// and lets the boundary between hunk and context stay exact; if the
// user also rewrote context, extraction refuses rather than guessing.
package engine

import (
	"fmt"

	"github.com/JaydenCJ/rerekit/internal/conflict"
	"github.com/JaydenCJ/rerekit/internal/fingerprint"
	"github.com/JaydenCJ/rerekit/internal/resfile"
)

// Extracted pairs a preimage hunk with the lines that resolved it
// (normalized: no trailing \r).
type Extracted struct {
	Hunk       *conflict.Hunk
	Resolution []string
}

// Extract recovers per-hunk resolutions. pre is the parsed preimage;
// post is the resolved file content.
func Extract(pre *conflict.File, post string) ([]Extracted, error) {
	postLines := splitLines(post)
	segs := pre.Segments
	var out []Extracted
	cursor := 0

	for i := 0; i < len(segs); i++ {
		seg := segs[i]
		if seg.Hunk == nil {
			// A leading context segment (any later context is consumed
			// as a hunk boundary below): must match as an exact prefix.
			if i != 0 {
				return nil, fmt.Errorf("internal: unexpected free-standing context segment %d", i)
			}
			if !matchAt(postLines, seg.Context, 0) {
				return nil, fmt.Errorf("leading context no longer matches (lines above the first conflict were edited)")
			}
			cursor = len(seg.Context)
			continue
		}

		h := seg.Hunk
		last := i == len(segs)-1
		if !last && segs[i+1].Hunk != nil {
			// Two conflicts with zero context lines between them: the
			// resolved text cannot be split unambiguously into two
			// resolutions, so refuse rather than record garbage.
			return nil, fmt.Errorf("conflicts at lines %d and %d are adjacent with no context between them; resolve boundaries cannot be recovered", h.Line, segs[i+1].Hunk.Line)
		}

		var end, next int
		switch {
		case last:
			// The file ends with this hunk: everything left is its
			// resolution.
			end, next = len(postLines), len(postLines)
		case i+1 == len(segs)-1:
			// The following context is the file's tail: anchor it as an
			// exact suffix so trailing edits are detected.
			boundary := segs[i+1].Context
			end = len(postLines) - len(boundary)
			if end < cursor || !matchAt(postLines, boundary, end) {
				return nil, fmt.Errorf("context after conflict at line %d no longer ends the file; cannot locate the resolution boundary", h.Line)
			}
			next = len(postLines)
			i++ // boundary consumed
		default:
			// Interior boundary: earliest occurrence at or after the
			// cursor. Deterministic, and correct whenever the human
			// left context lines alone.
			boundary := segs[i+1].Context
			pos := indexOf(postLines, boundary, cursor)
			if pos < 0 {
				return nil, fmt.Errorf("context after conflict at line %d was edited; cannot locate the resolution boundary", h.Line)
			}
			end, next = pos, pos+len(boundary)
			i++ // boundary consumed
		}

		out = append(out, Extracted{
			Hunk:       h,
			Resolution: fingerprint.Normalize(postLines[cursor:end]),
		})
		cursor = next
	}

	if cursor != len(postLines) {
		return nil, fmt.Errorf("resolved file has %d unexpected trailing lines after the final context", len(postLines)-cursor)
	}
	return out, nil
}

// Stats reports what Apply did to one file.
type Stats struct {
	Applied   int // hunks replaced by a recorded resolution
	Remaining int // hunks with no matching resolution, left untouched
}

// Apply replaces every hunk whose fingerprint resolves via lookup with
// its recorded resolution, re-applying CRLF endings when the hunk's
// markers used them. The file is mutated in place; call Render to get
// the new content.
func Apply(f *conflict.File, lookup func(fp string) *resfile.Resolution) Stats {
	var st Stats
	for i := range f.Segments {
		h := f.Segments[i].Hunk
		if h == nil {
			continue
		}
		r := lookup(fingerprint.Hunk(h))
		if r == nil {
			st.Remaining++
			continue
		}
		lines := r.Resolution
		if h.CRLF {
			withCR := make([]string, len(lines))
			for j, l := range lines {
				withCR[j] = l + "\r"
			}
			lines = withCR
		}
		f.Segments[i] = conflict.Segment{Context: lines}
		st.Applied++
	}
	return st
}

// splitLines splits content into lines without a trailing empty
// element for the final newline.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

// matchAt reports whether want occurs in lines exactly at pos.
func matchAt(lines, want []string, pos int) bool {
	if pos < 0 || pos+len(want) > len(lines) {
		return false
	}
	for i, w := range want {
		if lines[pos+i] != w {
			return false
		}
	}
	return true
}

// indexOf returns the first position >= from where want occurs, or -1.
func indexOf(lines, want []string, from int) int {
	for pos := from; pos+len(want) <= len(lines); pos++ {
		if matchAt(lines, want, pos) {
			return pos
		}
	}
	return -1
}
