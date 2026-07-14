// Fingerprint tests: every invariance the replay path relies on —
// label-blind, base-blind, order-symmetric, CRLF-normalized — plus the
// distinctness of genuinely different conflicts.
package fingerprint

import (
	"testing"

	"github.com/JaydenCJ/rerekit/internal/conflict"
)

func hunk(ours, theirs []string) *conflict.Hunk {
	return &conflict.Hunk{Ours: ours, Theirs: theirs}
}

func TestStableFullLengthLowercaseHex(t *testing.T) {
	h := hunk([]string{"a"}, []string{"b"})
	if Hunk(h) != Hunk(h) {
		t.Fatal("fingerprint must be deterministic")
	}
	fp := Hunk(h)
	if len(fp) != 64 {
		t.Fatalf("len = %d, want 64", len(fp))
	}
	for _, c := range fp {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("non-hex character %q in %s", c, fp)
		}
	}
}

func TestSymmetricUnderSideSwap(t *testing.T) {
	// After a rebase flips direction, ours and theirs trade places but
	// it is the same conflict; the recorded resolution must still match.
	a := Hunk(hunk([]string{"retries = 5"}, []string{"retries = 3", "timeout = 30"}))
	b := Hunk(hunk([]string{"retries = 3", "timeout = 30"}, []string{"retries = 5"}))
	if a != b {
		t.Fatal("swapping sides must not change the fingerprint")
	}
}

func TestIgnoresLabels(t *testing.T) {
	a := &conflict.Hunk{Ours: []string{"x"}, Theirs: []string{"y"}, OursLabel: "HEAD", TheirsLabel: "4f2a91c"}
	b := &conflict.Hunk{Ours: []string{"x"}, Theirs: []string{"y"}, OursLabel: "feature/z", TheirsLabel: "main"}
	if Hunk(a) != Hunk(b) {
		t.Fatal("labels change every rebase and must not affect the fingerprint")
	}
}

func TestIgnoresDiff3Base(t *testing.T) {
	// The same conflict checked out under merge.conflictStyle=diff3
	// carries a base section; both styles must match one resolution.
	plain := &conflict.Hunk{Ours: []string{"x"}, Theirs: []string{"y"}}
	diff3 := &conflict.Hunk{Ours: []string{"x"}, Theirs: []string{"y"}, HasBase: true, Base: []string{"old"}}
	if Hunk(plain) != Hunk(diff3) {
		t.Fatal("diff3 base must not affect the fingerprint")
	}
}

func TestNormalizesCRLF(t *testing.T) {
	lf := hunk([]string{"x"}, []string{"y"})
	crlf := hunk([]string{"x\r"}, []string{"y\r"})
	if Hunk(lf) != Hunk(crlf) {
		t.Fatal("a CRLF checkout must match an LF one")
	}
}

func TestDistinguishesContent(t *testing.T) {
	base := Hunk(hunk([]string{"x"}, []string{"y"}))
	for name, h := range map[string]*conflict.Hunk{
		"different ours":   hunk([]string{"x2"}, []string{"y"}),
		"different theirs": hunk([]string{"x"}, []string{"y2"}),
		"extra line":       hunk([]string{"x", ""}, []string{"y"}),
		"both empty":       hunk(nil, nil),
	} {
		if Hunk(h) == base {
			t.Errorf("%s: collided with base fingerprint", name)
		}
	}
}

func TestLineBoundariesMatter(t *testing.T) {
	// ["ab"] and ["a","b"] must not hash identically: the separator is
	// part of the identity, not just the concatenated bytes.
	if Sides([]string{"ab"}, []string{"z"}) == Sides([]string{"a", "b"}, []string{"z"}) {
		t.Fatal("line structure must be part of the fingerprint")
	}
}

func TestIDAndNormalize(t *testing.T) {
	fp := Sides([]string{"a"}, []string{"b"})
	if got := ID(fp); len(got) != IDLen || fp[:IDLen] != got {
		t.Fatalf("ID = %q", got)
	}
	if ID("short") != "short" {
		t.Fatal("ID of a short string must be identity")
	}
	// Normalize (used by resfile and engine) must copy, not mutate.
	in := []string{"a\r", "b"}
	out := Normalize(in)
	if in[0] != "a\r" {
		t.Fatal("Normalize must copy, not mutate")
	}
	if out[0] != "a" || out[1] != "b" {
		t.Fatalf("out = %v", out)
	}
}
