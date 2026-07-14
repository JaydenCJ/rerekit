// Parser tests: marker recognition, merge and diff3 styles, CRLF,
// malformed input, and the lossless Parse→Render round-trip that the
// apply path depends on.
package conflict

import (
	"strings"
	"testing"
)

const simple = `package config

<<<<<<< HEAD
	Retries: 5,
=======
	Retries: 3,
>>>>>>> main
done
`

func mustParse(t *testing.T, content string) *File {
	t.Helper()
	f, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestParseSimpleConflict(t *testing.T) {
	f := mustParse(t, simple)
	hs := f.Hunks()
	if len(hs) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hs))
	}
	h := hs[0]
	if got := strings.Join(h.Ours, "|"); got != "\tRetries: 5," {
		t.Errorf("ours = %q", got)
	}
	if got := strings.Join(h.Theirs, "|"); got != "\tRetries: 3," {
		t.Errorf("theirs = %q", got)
	}
	if h.OursLabel != "HEAD" || h.TheirsLabel != "main" {
		t.Errorf("labels = %q / %q", h.OursLabel, h.TheirsLabel)
	}
	if h.HasBase {
		t.Error("merge-style hunk must not report a base")
	}
	if h.Line != 3 {
		t.Errorf("hunk line = %d, want 3", h.Line)
	}
	// Segments must alternate context / hunk / context.
	if len(f.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(f.Segments))
	}
	if f.Segments[0].Hunk != nil || f.Segments[1].Hunk == nil || f.Segments[2].Hunk != nil {
		t.Error("expected context / hunk / context")
	}
}

func TestParseDiff3Base(t *testing.T) {
	f := mustParse(t, `<<<<<<< ours
a
||||||| 1abc234
old
=======
b
>>>>>>> theirs
`)
	h := f.Hunks()[0]
	if !h.HasBase || h.BaseLabel != "1abc234" {
		t.Fatalf("base not parsed: %+v", h)
	}
	if got := strings.Join(h.Base, "|"); got != "old" {
		t.Errorf("base = %q", got)
	}
}

func TestParseEmptySides(t *testing.T) {
	// A pure insertion conflict: ours empty, theirs has the new lines.
	f := mustParse(t, "<<<<<<< HEAD\n=======\nadded\n>>>>>>> main\n")
	h := f.Hunks()[0]
	if len(h.Ours) != 0 || len(h.Theirs) != 1 {
		t.Fatalf("ours=%d theirs=%d", len(h.Ours), len(h.Theirs))
	}
}

func TestParseMultipleHunks(t *testing.T) {
	f := mustParse(t, "a\n<<<<<<< x\n1\n=======\n2\n>>>>>>> y\nmid\n<<<<<<< x\n3\n=======\n4\n>>>>>>> y\nz\n")
	if len(f.Hunks()) != 2 {
		t.Fatalf("hunks = %d, want 2", len(f.Hunks()))
	}
	if f.Hunks()[1].Line != 8 {
		t.Errorf("second hunk line = %d, want 8", f.Hunks()[1].Line)
	}
}

func TestParseAdjacentHunksShareNoContext(t *testing.T) {
	f := mustParse(t, "<<<<<<< a\n1\n=======\n2\n>>>>>>> b\n<<<<<<< a\n3\n=======\n4\n>>>>>>> b\n")
	if len(f.Segments) != 2 || f.Segments[0].Hunk == nil || f.Segments[1].Hunk == nil {
		t.Fatalf("expected two adjacent hunk segments, got %d segments", len(f.Segments))
	}
}

func TestMarkerLookalikesStayContent(t *testing.T) {
	// A Markdown setext heading uses ======= as content; without an
	// opening <<<<<<< it must stay plain context.
	f := mustParse(t, "Title\n=======\nbody\n")
	if len(f.Hunks()) != 0 {
		t.Fatal("======= outside a conflict must be context")
	}
	if got := f.Render(); got != "Title\n=======\nbody\n" {
		t.Errorf("render = %q", got)
	}
	// Only exactly seven marker chars followed by EOL or space count.
	f = mustParse(t, "<<<<<<<< not a marker\n")
	if len(f.Hunks()) != 0 {
		t.Fatal("8-char <<<<<<<< must not open a conflict")
	}
	// git emits ======= bare; "======= x" inside ours is payload.
	f = mustParse(t, "<<<<<<< a\n======= x\n=======\nb\n>>>>>>> c\n")
	if got := strings.Join(f.Hunks()[0].Ours, "|"); got != "======= x" {
		t.Errorf("ours = %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"unterminated":     "<<<<<<< a\nx\n=======\ny\n",
		"nested start":     "<<<<<<< a\n<<<<<<< b\n=======\n>>>>>>> c\n",
		"theirs first":     "<<<<<<< a\n>>>>>>> b\n",
		"double base":      "<<<<<<< a\n||||||| b\n||||||| c\n=======\n>>>>>>> d\n",
		"base after split": "<<<<<<< a\n=======\n||||||| b\n=======x\n>>>>>>> d\n",
	}
	for name, content := range cases {
		if _, err := Parse(content); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

func TestParseErrorMentionsLineNumber(t *testing.T) {
	_, err := Parse("ok\n<<<<<<< a\nx\n")
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want mention of line 2", err)
	}
}

func TestRenderRoundTripsByteIdentically(t *testing.T) {
	// Apply rewrites files through Parse→Render, so any accepted input
	// must survive unchanged: LF, CRLF, diff3, empty sides, and a
	// missing final newline.
	for _, content := range []string{
		simple,
		"no conflicts at all\n",
		"",
		"<<<<<<< a\n=======\n>>>>>>> b\n",
		"lead\n<<<<<<< a\nx\n||||||| o\nb\n=======\ny\n>>>>>>> c\ntail\n",
		"a\n<<<<<<< x\n1\n=======\n2\n>>>>>>> y", // no final newline
		"ctx\r\n<<<<<<< HEAD\r\nours\r\n=======\r\ntheirs\r\n>>>>>>> main\r\ntail\r\n",
	} {
		f := mustParse(t, content)
		if got := f.Render(); got != content {
			t.Errorf("round trip changed %q -> %q", content, got)
		}
	}
	if f := mustParse(t, "a\n<<<<<<< x\n1\n=======\n2\n>>>>>>> y"); f.FinalNewline {
		t.Error("FinalNewline should be false")
	}
	crlf := mustParse(t, "<<<<<<< HEAD\r\no\r\n=======\r\nt\r\n>>>>>>> main\r\n")
	if !crlf.Hunks()[0].CRLF {
		t.Error("CRLF flag not set from marker line")
	}
}

func TestHasMarker(t *testing.T) {
	if !HasMarker(simple) {
		t.Error("simple must report a marker")
	}
	if !HasMarker("<<<<<<< at start\n") {
		t.Error("marker on first line must be found")
	}
	if HasMarker("x <<<<<<< mid-line\n") {
		t.Error("mid-line <<<<<<< is not a marker line")
	}
	if HasMarker("plain\ntext\n") {
		t.Error("plain text must not report a marker")
	}
}

func TestMarkerClassification(t *testing.T) {
	kind, label, crlf := marker(">>>>>>> feature/x\r")
	if kind != markerTheirs || label != "feature/x" || !crlf {
		t.Errorf("got %q %q %v", kind, label, crlf)
	}
	if k, _, _ := marker("======="); k != markerSplit {
		t.Error("bare ======= must classify as split")
	}
	if k, _, _ := marker("========"); k != "" {
		t.Error("8 chars must not classify")
	}
	if k, _, _ := marker("<<<<<<"); k != "" {
		t.Error("6 chars must not classify")
	}
}
