// Engine tests: extraction recovers exactly what replaced each hunk
// (including edge positions, empty resolutions, and refusals when
// context was edited), and apply splices resolutions back with the
// target file's own line endings.
package engine

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/rerekit/internal/conflict"
	"github.com/JaydenCJ/rerekit/internal/fingerprint"
	"github.com/JaydenCJ/rerekit/internal/resfile"
)

func parse(t *testing.T, content string) *conflict.File {
	t.Helper()
	f, err := conflict.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func extract(t *testing.T, pre, post string) []Extracted {
	t.Helper()
	out, err := Extract(parse(t, pre), post)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

const onePre = "before\n<<<<<<< a\nours line\n=======\ntheirs line\n>>>>>>> b\nafter\n"

func TestExtractSingleHunk(t *testing.T) {
	got := extract(t, onePre, "before\nmerged line\nafter\n")
	if len(got) != 1 {
		t.Fatalf("extracted %d, want 1", len(got))
	}
	if joined(got[0].Resolution) != "merged line" {
		t.Errorf("resolution = %q", joined(got[0].Resolution))
	}
}

func TestExtractMultiLineResolution(t *testing.T) {
	got := extract(t, onePre, "before\nkeep ours\nand theirs\nplus new\nafter\n")
	if joined(got[0].Resolution) != "keep ours\nand theirs\nplus new" {
		t.Errorf("resolution = %q", joined(got[0].Resolution))
	}
}

func TestExtractEmptyResolution(t *testing.T) {
	// The human deleted both sides entirely.
	got := extract(t, onePre, "before\nafter\n")
	if len(got[0].Resolution) != 0 {
		t.Errorf("resolution = %v, want empty", got[0].Resolution)
	}
}

func TestExtractHunkAtEdgePositions(t *testing.T) {
	// A hunk opening the file, closing the file, or spanning the whole
	// file exercises the prefix/suffix anchoring special cases.
	cases := []struct{ name, pre, post, want string }{
		{"file start", "<<<<<<< a\nx\n=======\ny\n>>>>>>> b\ntail\n", "picked\ntail\n", "picked"},
		{"file end", "head\n<<<<<<< a\nx\n=======\ny\n>>>>>>> b\n", "head\nlast one\n", "last one"},
		{"whole file", "<<<<<<< a\nx\n=======\ny\n>>>>>>> b\n", "everything\nnew\n", "everything\nnew"},
	}
	for _, c := range cases {
		got := extract(t, c.pre, c.post)
		if joined(got[0].Resolution) != c.want {
			t.Errorf("%s: resolution = %q, want %q", c.name, joined(got[0].Resolution), c.want)
		}
	}
}

func TestExtractTwoHunks(t *testing.T) {
	pre := "h\n<<<<<<< a\n1\n=======\n2\n>>>>>>> b\nmid\n<<<<<<< a\n3\n=======\n4\n>>>>>>> b\nt\n"
	got := extract(t, pre, "h\nfirst\nmid\nsecond\nt\n")
	if len(got) != 2 {
		t.Fatalf("extracted %d, want 2", len(got))
	}
	if joined(got[0].Resolution) != "first" || joined(got[1].Resolution) != "second" {
		t.Errorf("resolutions = %q, %q", joined(got[0].Resolution), joined(got[1].Resolution))
	}
}

func TestExtractResolutionContainingContextLikeLines(t *testing.T) {
	// The final context is anchored as a suffix, so a resolution that
	// happens to repeat the trailing context is still split correctly.
	pre := "head\n<<<<<<< a\nx\n=======\ny\n>>>>>>> b\ntail\n"
	got := extract(t, pre, "head\ntail\nreally\ntail\n")
	if joined(got[0].Resolution) != "tail\nreally" {
		t.Errorf("resolution = %q", joined(got[0].Resolution))
	}
}

func TestExtractNormalizesCRLFResolution(t *testing.T) {
	pre := "before\r\n<<<<<<< a\r\nx\r\n=======\r\ny\r\n>>>>>>> b\r\nafter\r\n"
	got := extract(t, pre, "before\r\npicked\r\nafter\r\n")
	if joined(got[0].Resolution) != "picked" {
		t.Errorf("resolution = %q (CRLF must be stripped)", joined(got[0].Resolution))
	}
}

func TestExtractRefusesEditedContext(t *testing.T) {
	// Context is the anchor; when the human also rewrote it, recording
	// would capture the wrong region, so extraction must refuse.
	if _, err := Extract(parse(t, onePre), "REWRITTEN\nmerged\nafter\n"); err == nil ||
		!strings.Contains(err.Error(), "leading context") {
		t.Fatalf("edited leading context: err = %v", err)
	}
	if _, err := Extract(parse(t, onePre), "before\nmerged\nREWRITTEN\n"); err == nil ||
		!strings.Contains(err.Error(), "cannot locate") {
		t.Fatalf("edited trailing context: err = %v", err)
	}
	// Lines added after the final context are an edit outside any
	// conflict.
	pre := "h\n<<<<<<< a\n1\n=======\n2\n>>>>>>> b\nmid\n<<<<<<< a\n3\n=======\n4\n>>>>>>> b\nt\n"
	if _, err := Extract(parse(t, pre), "h\nfirst\nmid\nsecond\nt\nEXTRA\n"); err == nil {
		t.Fatal("expected refusal on trailing additions")
	}
}

func TestExtractRefusesAdjacentHunks(t *testing.T) {
	pre := "<<<<<<< a\n1\n=======\n2\n>>>>>>> b\n<<<<<<< a\n3\n=======\n4\n>>>>>>> b\n"
	_, err := Extract(parse(t, pre), "anything\n")
	if err == nil || !strings.Contains(err.Error(), "adjacent") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractInteriorBoundaryUsesEarliestMatch(t *testing.T) {
	// Deterministic tie-break: when the interior context recurs, the
	// earliest occurrence at/after the cursor wins.
	pre := "h\n<<<<<<< a\n1\n=======\n2\n>>>>>>> b\nsep\ntail\n<<<<<<< a\n3\n=======\n4\n>>>>>>> b\nend\n"
	got := extract(t, pre, "h\nr1\nsep\ntail\nr2\nend\n")
	if joined(got[0].Resolution) != "r1" || joined(got[1].Resolution) != "r2" {
		t.Errorf("resolutions = %q, %q", joined(got[0].Resolution), joined(got[1].Resolution))
	}
}

// ---- apply ----

// lookupFor builds a lookup over the given resolutions.
func lookupFor(rs ...*resfile.Resolution) func(string) *resfile.Resolution {
	m := map[string]*resfile.Resolution{}
	for _, r := range rs {
		m[r.Fingerprint] = r
	}
	return func(fp string) *resfile.Resolution { return m[fp] }
}

// recordOne extracts the single resolution from pre/post and returns it
// as a resfile.Resolution — the real record path in miniature.
func recordOne(t *testing.T, pre, post string) *resfile.Resolution {
	t.Helper()
	got := extract(t, pre, post)
	if len(got) != 1 {
		t.Fatalf("expected one hunk, got %d", len(got))
	}
	return resfile.New(got[0].Hunk, "test.txt", got[0].Resolution)
}

func TestApplyReplacesMatchingHunk(t *testing.T) {
	r := recordOne(t, onePre, "before\nmerged line\nafter\n")
	f := parse(t, onePre)
	st := Apply(f, lookupFor(r))
	if st.Applied != 1 || st.Remaining != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if got := f.Render(); got != "before\nmerged line\nafter\n" {
		t.Errorf("render = %q", got)
	}
}

func TestApplyMatchesEquivalentConflictVariants(t *testing.T) {
	// Same conflict, opposite rebase direction (sides swapped, labels
	// changed) or diff3 checkout (base section added): the normalized
	// fingerprint must still match.
	r := recordOne(t, onePre, "before\nmerged line\nafter\n")
	swapped := "before\n<<<<<<< feature/z\ntheirs line\n=======\nours line\n>>>>>>> 4f2a91c\nafter\n"
	f := parse(t, swapped)
	st := Apply(f, lookupFor(r))
	if st.Applied != 1 {
		t.Fatalf("swapped: stats = %+v", st)
	}
	if got := f.Render(); got != "before\nmerged line\nafter\n" {
		t.Errorf("swapped: render = %q", got)
	}
	diff3 := "before\n<<<<<<< a\nours line\n||||||| 1abc234\nold\n=======\ntheirs line\n>>>>>>> b\nafter\n"
	if st := Apply(parse(t, diff3), lookupFor(r)); st.Applied != 1 {
		t.Fatalf("diff3: stats = %+v", st)
	}
}

func TestApplyLeavesUnknownHunksUntouched(t *testing.T) {
	f := parse(t, onePre)
	st := Apply(f, lookupFor())
	if st.Applied != 0 || st.Remaining != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if got := f.Render(); got != onePre {
		t.Errorf("unmatched file changed: %q", got)
	}
}

func TestApplyPartialFile(t *testing.T) {
	pre := "h\n<<<<<<< a\n1\n=======\n2\n>>>>>>> b\nmid\n<<<<<<< a\nX\n=======\nY\n>>>>>>> b\nt\n"
	known := recordOne(t, "h\n<<<<<<< a\n1\n=======\n2\n>>>>>>> b\nmid\n", "h\nfirst\nmid\n")
	f := parse(t, pre)
	st := Apply(f, lookupFor(known))
	if st.Applied != 1 || st.Remaining != 1 {
		t.Fatalf("stats = %+v", st)
	}
	want := "h\nfirst\nmid\n<<<<<<< a\nX\n=======\nY\n>>>>>>> b\nt\n"
	if got := f.Render(); got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

func TestApplyEmptyResolutionDeletesHunk(t *testing.T) {
	r := recordOne(t, onePre, "before\nafter\n")
	f := parse(t, onePre)
	if st := Apply(f, lookupFor(r)); st.Applied != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if got := f.Render(); got != "before\nafter\n" {
		t.Errorf("render = %q", got)
	}
}

func TestApplyRestoresCRLFOnCRLFTarget(t *testing.T) {
	// Recorded from an LF checkout, replayed onto a CRLF one: the
	// spliced lines must follow the target file's endings.
	r := recordOne(t, onePre, "before\nmerged line\nafter\n")
	crlf := "before\r\n<<<<<<< a\r\nours line\r\n=======\r\ntheirs line\r\n>>>>>>> b\r\nafter\r\n"
	f := parse(t, crlf)
	if st := Apply(f, lookupFor(r)); st.Applied != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if got := f.Render(); got != "before\r\nmerged line\r\nafter\r\n" {
		t.Errorf("render = %q", got)
	}
}

func TestRecordThenReplayIsByteIdentical(t *testing.T) {
	// The headline promise: resolve once, and the replayed file equals
	// the file the human produced.
	post := "before\nkeep ours\nand theirs\nafter\n"
	r := recordOne(t, onePre, post)
	f := parse(t, onePre)
	Apply(f, lookupFor(r))
	if got := f.Render(); got != post {
		t.Fatalf("replay differs from the human resolution:\n%q\n%q", got, post)
	}
}

func TestExtractFingerprintMatchesApplyFingerprint(t *testing.T) {
	// Record and apply must agree on identity or nothing ever replays.
	got := extract(t, onePre, "before\nz\nafter\n")
	f := parse(t, onePre)
	if fingerprint.Hunk(got[0].Hunk) != fingerprint.Hunk(f.Hunks()[0]) {
		t.Fatal("fingerprint disagreement between extract and apply paths")
	}
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0}, {"a", 1}, {"a\n", 1}, {"a\nb", 2}, {"a\nb\n", 2}, {"\n", 1},
	}
	for _, c := range cases {
		if got := len(splitLines(c.in)); got != c.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", c.in, got, c.want)
		}
	}
}
