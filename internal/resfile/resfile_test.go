// Resolution-file format tests: deterministic marshalling, exact
// round-trips (including payloads that look like markers or section
// headers), strict parse errors, and the fingerprint integrity check.
package resfile

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/rerekit/internal/conflict"
)

func sample() *Resolution {
	h := &conflict.Hunk{
		Ours:        []string{"\tRetries: 5,"},
		Theirs:      []string{"\tRetries: 3,", "\tTimeout: 30,"},
		OursLabel:   "HEAD",
		TheirsLabel: "main",
	}
	return New(h, "src/config.go", []string{"\tRetries: 5,", "\tTimeout: 30,"})
}

func TestMarshalShapeAndDeterminism(t *testing.T) {
	if string(Marshal(sample())) != string(Marshal(sample())) {
		t.Fatal("identical resolutions must marshal identically")
	}
	// Committed files must not churn: nothing time- or host-dependent.
	out := string(Marshal(sample()))
	for _, banned := range []string{"date:", "time:", "recorded", "host:", "user:"} {
		if strings.Contains(out, banned) {
			t.Errorf("marshalled file contains %q:\n%s", banned, out)
		}
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if lines[0] != Magic {
		t.Errorf("first line = %q", lines[0])
	}
	for _, want := range []string{
		"path: src/config.go",
		"ours-label: HEAD",
		"theirs-label: main",
		secOurs, secTheirs, secResolution,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if lines[len(lines)-1] != secEnd {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
	if !strings.Contains(out, "|\tRetries: 5,\n") {
		t.Fatalf("payload not pipe-prefixed:\n%s", out)
	}
}

func TestRoundTrip(t *testing.T) {
	r := sample()
	back, err := Unmarshal(Marshal(r))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Fingerprint != r.Fingerprint || back.Path != r.Path ||
		back.OursLabel != r.OursLabel || back.TheirsLabel != r.TheirsLabel {
		t.Errorf("headers changed: %+v", back)
	}
	if strings.Join(back.Resolution, "\n") != strings.Join(r.Resolution, "\n") {
		t.Errorf("resolution changed: %v", back.Resolution)
	}
}

func TestRoundTripHostileContent(t *testing.T) {
	// Payload lines that mimic the format itself must survive: conflict
	// markers, section headers, pipes, empty lines.
	h := &conflict.Hunk{
		Ours:   []string{"<<<<<<< HEAD", "--- resolution ---", ""},
		Theirs: []string{"|already piped", "--- end ---"},
	}
	r := New(h, "weird.txt", []string{">>>>>>> main", "", "rerekit-resolution-v1"})
	back, err := Unmarshal(Marshal(r))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if strings.Join(back.Ours, "\x00") != strings.Join(r.Ours, "\x00") ||
		strings.Join(back.Theirs, "\x00") != strings.Join(r.Theirs, "\x00") ||
		strings.Join(back.Resolution, "\x00") != strings.Join(r.Resolution, "\x00") {
		t.Fatalf("hostile payload corrupted: %+v", back)
	}
}

func TestRoundTripDiff3Base(t *testing.T) {
	h := &conflict.Hunk{
		Ours: []string{"a"}, Theirs: []string{"b"},
		HasBase: true, Base: []string{"old1", "old2"},
	}
	r := New(h, "f.txt", []string{"ab"})
	back, err := Unmarshal(Marshal(r))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !back.HasBase || strings.Join(back.Base, ",") != "old1,old2" {
		t.Fatalf("base lost: %+v", back)
	}
}

func TestRoundTripEmptyResolution(t *testing.T) {
	// Deleting both sides is a legitimate resolution.
	h := &conflict.Hunk{Ours: []string{"a"}, Theirs: []string{"b"}}
	r := New(h, "f.txt", nil)
	back, err := Unmarshal(Marshal(r))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(back.Resolution) != 0 {
		t.Fatalf("resolution = %v, want empty", back.Resolution)
	}
}

func TestNewNormalizesCRLF(t *testing.T) {
	h := &conflict.Hunk{Ours: []string{"a\r"}, Theirs: []string{"b\r"}, CRLF: true}
	r := New(h, "f.txt", []string{"c\r"})
	if r.Ours[0] != "a" || r.Theirs[0] != "b" || r.Resolution[0] != "c" {
		t.Fatalf("CRLF not normalized: %+v", r)
	}
}

func TestIDIsShortFingerprint(t *testing.T) {
	r := sample()
	if r.ID() != r.Fingerprint[:12] {
		t.Fatalf("ID = %q", r.ID())
	}
}

func TestUnmarshalRejectsEditedConflictBody(t *testing.T) {
	// Editing ours/theirs breaks the identity: the file would silently
	// never match again, so loading must fail loudly.
	data := string(Marshal(sample()))
	tampered := strings.Replace(data, "|\tRetries: 5,\n--- theirs", "|\tRetries: 6,\n--- theirs", 1)
	if tampered == data {
		t.Fatal("test setup: replacement did not apply")
	}
	_, err := Unmarshal([]byte(tampered))
	if err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("err = %v, want fingerprint mismatch", err)
	}
}

func TestUnmarshalAllowsEditedResolutionBody(t *testing.T) {
	// The resolution section is the reviewable, editable part.
	data := string(Marshal(sample()))
	edited := strings.Replace(data, "|\tTimeout: 30,\n--- end", "|\tTimeout: 60,\n--- end", 1)
	if edited == data {
		t.Fatal("test setup: replacement did not apply")
	}
	back, err := Unmarshal([]byte(edited))
	if err != nil {
		t.Fatalf("editing the resolution must stay loadable: %v", err)
	}
	if back.Resolution[1] != "\tTimeout: 60," {
		t.Fatalf("edit lost: %v", back.Resolution)
	}
}

func TestUnmarshalErrors(t *testing.T) {
	valid := string(Marshal(sample()))
	cases := map[string]string{
		"empty":               "",
		"wrong magic":         strings.Replace(valid, Magic, "rerekit-resolution-v9", 1),
		"missing fingerprint": strings.Replace(valid, "fingerprint: ", "fp: ", 1),
		"short fingerprint":   strings.Replace(valid, "fingerprint: ", "fingerprint: abc\nx-old: ", 1),
		"bad payload prefix":  strings.Replace(valid, "|\tRetries: 3,", "\tRetries: 3,", 1),
		"missing end":         strings.TrimSuffix(valid, secEnd+"\n"),
		"trailing garbage":    valid + "extra\n",
		"missing section":     strings.Replace(valid, secTheirs+"\n", "", 1),
	}
	for name, data := range cases {
		if _, err := Unmarshal([]byte(data)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestUnmarshalToleratesUnknownHeaders(t *testing.T) {
	// Additive format evolution: a v1 reader skips headers it does not
	// know instead of failing.
	data := strings.Replace(string(Marshal(sample())),
		"path: ", "x-future: something\npath: ", 1)
	if _, err := Unmarshal([]byte(data)); err != nil {
		t.Fatalf("unknown header must be tolerated: %v", err)
	}
}
