// Package fingerprint turns a conflict hunk into a stable identity.
//
// The fingerprint covers the two conflicting sides and nothing else:
// labels are ignored (they change with every rebase), the diff3 base is
// ignored (so merge and diff3 checkouts of the same conflict match),
// line endings are normalized (a CRLF checkout matches an LF one), and
// the two sides are ordered canonically (ours and theirs swap places
// when a rebase flips direction, but it is still the same conflict).
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/JaydenCJ/rerekit/internal/conflict"
)

// domain separates rerekit fingerprints from any other sha256 use and
// versions the normalization rules.
const domain = "rerekit-fp-v1"

// IDLen is the number of hex characters used for short IDs (file names,
// CLI arguments). The full 64-char digest is stored in every file.
const IDLen = 12

// Hunk fingerprints a parsed conflict hunk.
func Hunk(h *conflict.Hunk) string {
	return Sides(h.Ours, h.Theirs)
}

// Sides fingerprints a conflict given its two sides as raw lines.
func Sides(ours, theirs []string) string {
	a, b := normalize(ours), normalize(theirs)
	if b < a {
		a, b = b, a
	}
	sum := sha256.Sum256([]byte(domain + "\x00" + a + "\x00" + b))
	return hex.EncodeToString(sum[:])
}

// ID shortens a full fingerprint to the display/lookup form.
func ID(fp string) string {
	if len(fp) < IDLen {
		return fp
	}
	return fp[:IDLen]
}

// Normalize strips trailing \r from every line, the same normalization
// the fingerprint applies before hashing. Recorded resolutions store
// sides in this form.
func Normalize(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

func normalize(lines []string) string {
	return strings.Join(Normalize(lines), "\n")
}
