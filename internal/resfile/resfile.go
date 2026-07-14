// Package resfile encodes and decodes rerekit resolution files — the
// committable, human-readable `.res` format.
//
// The format is deliberately boring: a magic line, `key: value` headers,
// then labelled sections whose payload lines are each prefixed with `|`.
// The prefix makes the file unambiguous no matter what the payload
// contains (including conflict markers and section headers themselves),
// while staying line-oriented so resolutions diff and review like any
// other text. Encoding is deterministic: the same resolution always
// marshals to the same bytes, so committed files never churn.
package resfile

import (
	"fmt"
	"strings"

	"github.com/JaydenCJ/rerekit/internal/conflict"
	"github.com/JaydenCJ/rerekit/internal/fingerprint"
)

// Magic is the first line of every resolution file and versions the
// format.
const Magic = "rerekit-resolution-v1"

// Extension is the file extension used in the store.
const Extension = ".res"

// Section header lines. Payload lines under each are `|`-prefixed.
const (
	secOurs       = "--- ours ---"
	secBase       = "--- base ---"
	secTheirs     = "--- theirs ---"
	secResolution = "--- resolution ---"
	secEnd        = "--- end ---"
)

// Resolution is one recorded conflict resolution. Sides and resolution
// lines are stored normalized (no trailing \r); the CLI re-applies the
// target file's line endings on replay.
type Resolution struct {
	Fingerprint string // full 64-hex sha256, see package fingerprint
	Path        string // repo-relative path where first recorded (informational)
	OursLabel   string // marker labels as seen at record time (informational)
	TheirsLabel string

	Ours       []string
	Base       []string
	Theirs     []string
	HasBase    bool
	Resolution []string
}

// New builds a Resolution from a parsed hunk and the lines that replaced
// it. All line slices are normalized and the fingerprint is computed.
func New(h *conflict.Hunk, path string, resolved []string) *Resolution {
	r := &Resolution{
		Fingerprint: fingerprint.Hunk(h),
		Path:        path,
		OursLabel:   h.OursLabel,
		TheirsLabel: h.TheirsLabel,
		Ours:        fingerprint.Normalize(h.Ours),
		Theirs:      fingerprint.Normalize(h.Theirs),
		HasBase:     h.HasBase,
		Resolution:  fingerprint.Normalize(resolved),
	}
	if h.HasBase {
		r.Base = fingerprint.Normalize(h.Base)
	}
	return r
}

// ID returns the short identifier used for file names and CLI lookups.
func (r *Resolution) ID() string {
	return fingerprint.ID(r.Fingerprint)
}

// Marshal renders the resolution file. Output always ends with a
// newline and contains no timestamps or machine-specific paths.
func Marshal(r *Resolution) []byte {
	var b strings.Builder
	b.WriteString(Magic + "\n")
	b.WriteString("fingerprint: " + r.Fingerprint + "\n")
	if r.Path != "" {
		b.WriteString("path: " + r.Path + "\n")
	}
	if r.OursLabel != "" {
		b.WriteString("ours-label: " + r.OursLabel + "\n")
	}
	if r.TheirsLabel != "" {
		b.WriteString("theirs-label: " + r.TheirsLabel + "\n")
	}
	b.WriteString("\n")
	writeSection(&b, secOurs, r.Ours)
	if r.HasBase {
		writeSection(&b, secBase, r.Base)
	}
	writeSection(&b, secTheirs, r.Theirs)
	writeSection(&b, secResolution, r.Resolution)
	b.WriteString(secEnd + "\n")
	return []byte(b.String())
}

func writeSection(b *strings.Builder, header string, lines []string) {
	b.WriteString(header + "\n")
	for _, l := range lines {
		b.WriteString("|" + l + "\n")
	}
}

// Unmarshal parses a resolution file and verifies its integrity: the
// stored fingerprint must match a recomputation over the conflict
// sides. Reviewers may freely edit the resolution section — that is the
// point of the format — but an edited conflict body would silently
// never match again, so it is rejected loudly instead.
func Unmarshal(data []byte) (*Resolution, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || lines[0] != Magic {
		return nil, fmt.Errorf("resfile: line 1: missing %q header", Magic)
	}
	r := &Resolution{}

	// Headers: `key: value` until the first blank line.
	i := 1
	for ; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			i++
			break
		}
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("resfile: line %d: malformed header %q", i+1, line)
		}
		switch key {
		case "fingerprint":
			r.Fingerprint = value
		case "path":
			r.Path = value
		case "ours-label":
			r.OursLabel = value
		case "theirs-label":
			r.TheirsLabel = value
		default:
			// Unknown headers are tolerated so v1 readers survive
			// additive format evolution.
		}
	}
	if !validFingerprint(r.Fingerprint) {
		return nil, fmt.Errorf("resfile: fingerprint header missing or not 64 hex characters")
	}

	// Sections, in fixed order; base is optional.
	var err error
	if r.Ours, i, err = readSection(lines, i, secOurs); err != nil {
		return nil, err
	}
	if i < len(lines) && lines[i] == secBase {
		r.HasBase = true
		if r.Base, i, err = readSection(lines, i, secBase); err != nil {
			return nil, err
		}
	}
	if r.Theirs, i, err = readSection(lines, i, secTheirs); err != nil {
		return nil, err
	}
	if r.Resolution, i, err = readSection(lines, i, secResolution); err != nil {
		return nil, err
	}
	if i >= len(lines) || lines[i] != secEnd {
		return nil, fmt.Errorf("resfile: line %d: expected %q", i+1, secEnd)
	}
	if i != len(lines)-1 {
		return nil, fmt.Errorf("resfile: line %d: content after %q", i+2, secEnd)
	}

	if got := fingerprint.Sides(r.Ours, r.Theirs); got != r.Fingerprint {
		return nil, fmt.Errorf("resfile: fingerprint mismatch: header says %s but conflict body hashes to %s (was the ours/theirs body edited?)",
			fingerprint.ID(r.Fingerprint), fingerprint.ID(got))
	}
	return r, nil
}

// readSection expects lines[i] to be the given header and consumes the
// `|`-prefixed payload that follows, returning the payload and the
// index of the next non-payload line.
func readSection(lines []string, i int, header string) ([]string, int, error) {
	if i >= len(lines) || lines[i] != header {
		return nil, 0, fmt.Errorf("resfile: line %d: expected %q", i+1, header)
	}
	i++
	var payload []string
	for ; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "|") {
			return payload, i, nil
		}
		payload = append(payload, lines[i][1:])
	}
	return nil, 0, fmt.Errorf("resfile: unexpected end of file inside %q", header)
}

func validFingerprint(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	for _, c := range fp {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
