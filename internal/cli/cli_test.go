// CLI tests: exercise Run in-process, end to end, against real files in
// temp directories — the full snap → resolve → record → apply loop,
// every subcommand, flags, exit codes, and store discovery, without
// building a binary or shelling out to git.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI invokes Run and captures both streams.
func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// inTempRepo creates a temp dir with a fake .git and chdirs into it.
// Tests using it must not run in parallel.
func inTempRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir()) // macOS /var -> /private/var
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	})
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const conflicted = `package config

func Defaults() Options {
<<<<<<< HEAD
	return Options{Retries: 5}
=======
	return Options{Retries: 3, Timeout: 30}
>>>>>>> main
}
`

const resolved = `package config

func Defaults() Options {
	return Options{Retries: 5, Timeout: 30}
}
`

// recordFixture drives the real snap → resolve → record loop.
func recordFixture(t *testing.T) {
	t.Helper()
	write(t, "src/config.go", conflicted)
	if code, _, errOut := runCLI(t, "snap"); code != ExitOK {
		t.Fatalf("snap: %s", errOut)
	}
	write(t, "src/config.go", resolved)
	if code, _, errOut := runCLI(t, "record"); code != ExitOK {
		t.Fatalf("record: %s", errOut)
	}
}

func TestVersionCommand(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := runCLI(t, arg)
		if code != ExitOK || out != "rerekit 0.1.0\n" {
			t.Errorf("%s: code=%d out=%q", arg, code, out)
		}
	}
}

func TestHelpAndBareInvocation(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {}} {
		code, out, _ := runCLI(t, args...)
		if code != ExitOK || !strings.Contains(out, "Usage:") {
			t.Errorf("%v: code=%d", args, code)
		}
	}
}

func TestUnknownCommandExitsUsage(t *testing.T) {
	code, _, errOut := runCLI(t, "frobnicate")
	if code != ExitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestInitCreatesStore(t *testing.T) {
	inTempRepo(t)
	code, out, errOut := runCLI(t, "init")
	if code != ExitOK || !strings.Contains(out, "created .rerekit/") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(".rerekit", "resolutions")); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCLI(t, "init")
	if code != ExitOK || !strings.Contains(out, "already exists") {
		t.Fatalf("second init: code=%d out=%q", code, out)
	}
	code, _, errOut = runCLI(t, "init", "somewhere")
	if code != ExitUsage || !strings.Contains(errOut, "takes no arguments") {
		t.Fatalf("init with args: code=%d err=%q", code, errOut)
	}
}

func TestInitFromSubdirTargetsGitRoot(t *testing.T) {
	dir := inTempRepo(t)
	sub := filepath.Join(dir, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "init")
	if code != ExitOK || !strings.Contains(out, "in "+dir) {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rerekit")); err != nil {
		t.Fatal("store not created at git root")
	}
}

func TestSnapFindsConflictsAndAutoInits(t *testing.T) {
	inTempRepo(t)
	write(t, "src/config.go", conflicted)
	write(t, "clean.go", "package main\n")
	code, out, _ := runCLI(t, "snap")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, out)
	}
	for _, want := range []string{
		"initialized empty store in .rerekit/",
		"snapped src/config.go (1 conflict)",
		"rerekit snap: 1 file snapped, 1 conflict pending",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "clean.go") {
		t.Error("clean file must not be snapped")
	}
}

func TestSnapNoConflicts(t *testing.T) {
	inTempRepo(t)
	write(t, "a.go", "package a\n")
	code, out, _ := runCLI(t, "snap")
	if code != ExitOK || !strings.Contains(out, "no conflicted files found") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// Explicitly naming a clean file deserves feedback, not silence.
	code, _, errOut := runCLI(t, "snap", "a.go")
	if code != ExitOK || !strings.Contains(errOut, "no conflict markers found") {
		t.Fatalf("explicit clean path: code=%d err=%q", code, errOut)
	}
}

func TestSnapSkipsBinaryAndStoreFiles(t *testing.T) {
	inTempRepo(t)
	write(t, "bin.dat", "<<<<<<< a\n\x00\n=======\nb\n>>>>>>> c\n")
	runCLI(t, "init")
	write(t, ".rerekit/resolutions/planted.txt", conflicted)
	code, out, _ := runCLI(t, "snap")
	if code != ExitOK || !strings.Contains(out, "no conflicted files found") {
		t.Fatalf("binary or store file was snapped: %q", out)
	}
}

func TestRecordFullLoop(t *testing.T) {
	inTempRepo(t)
	write(t, "src/config.go", conflicted)
	runCLI(t, "snap")
	write(t, "src/config.go", resolved)
	code, out, _ := runCLI(t, "record")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "src/config.go (new)") ||
		!strings.Contains(out, "rerekit record: 1 resolution recorded from 1 file") {
		t.Fatalf("out=%q", out)
	}
	// The snapshot is consumed and a .res file exists.
	entries, err := os.ReadDir(".rerekit/pending")
	if err != nil || len(entries) != 0 {
		t.Fatalf("pending not consumed: %v, %v", entries, err)
	}
	res, err := os.ReadDir(".rerekit/resolutions")
	if err != nil || len(res) != 1 || !strings.HasSuffix(res[0].Name(), ".res") {
		t.Fatalf("resolutions dir: %v, %v", res, err)
	}
}

func TestRecordNothingPendingAndPathRejection(t *testing.T) {
	inTempRepo(t)
	runCLI(t, "init")
	code, out, _ := runCLI(t, "record")
	if code != ExitOK || !strings.Contains(out, "nothing pending") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, _, errOut := runCLI(t, "record", "some/path")
	if code != ExitUsage || !strings.Contains(errOut, "takes no path arguments") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestRecordSkipsStillConflictedFile(t *testing.T) {
	inTempRepo(t)
	write(t, "src/config.go", conflicted)
	runCLI(t, "snap")
	// Not resolved yet: record must keep the snapshot for later.
	code, out, _ := runCLI(t, "record")
	if code != ExitOK || !strings.Contains(out, "skipped src/config.go: still conflicted") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	entries, _ := os.ReadDir(".rerekit/pending")
	if len(entries) != 1 {
		t.Fatal("snapshot must be kept")
	}
}

func TestRecordRefusesEditedContext(t *testing.T) {
	inTempRepo(t)
	write(t, "src/config.go", conflicted)
	runCLI(t, "snap")
	// Resolve the conflict but also rewrite the surrounding context.
	write(t, "src/config.go", "package config\n\nfunc Renamed() Options {\n\treturn Options{}\n}\n")
	code, _, errOut := runCLI(t, "record")
	if code != ExitOK || !strings.Contains(errOut, "pending kept") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	entries, _ := os.ReadDir(".rerekit/pending")
	if len(entries) != 1 {
		t.Fatal("failed extraction must keep the snapshot")
	}
}

func TestRecordKeepPending(t *testing.T) {
	inTempRepo(t)
	write(t, "src/config.go", conflicted)
	runCLI(t, "snap")
	write(t, "src/config.go", resolved)
	code, _, _ := runCLI(t, "record", "--keep-pending")
	if code != ExitOK {
		t.Fatal("record --keep-pending failed")
	}
	entries, _ := os.ReadDir(".rerekit/pending")
	if len(entries) != 1 {
		t.Fatal("--keep-pending must not consume the snapshot")
	}
}

func TestApplyReplaysRecordedResolution(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	// The same conflict reappears on the next rebase — sides swapped,
	// labels different.
	swapped := strings.Replace(strings.Replace(strings.Replace(conflicted,
		"\treturn Options{Retries: 5}", "X", 1),
		"\treturn Options{Retries: 3, Timeout: 30}", "\treturn Options{Retries: 5}", 1),
		"X", "\treturn Options{Retries: 3, Timeout: 30}", 1)
	write(t, "src/config.go", swapped)
	code, out, _ := runCLI(t, "apply")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "resolved src/config.go: 1 of 1 conflicts") ||
		!strings.Contains(out, "rerekit apply: 1 conflict resolved, 0 remaining") {
		t.Fatalf("out=%q", out)
	}
	if got := read(t, "src/config.go"); got != resolved {
		t.Fatalf("file = %q, want the recorded resolution", got)
	}
}

func TestApplyDryRunLeavesFileUntouched(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	write(t, "src/config.go", conflicted)
	code, out, _ := runCLI(t, "apply", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "would resolve src/config.go: 1 of 1 conflicts") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if read(t, "src/config.go") != conflicted {
		t.Fatal("--dry-run must not write")
	}
}

func TestApplyUnmatchedExitsOne(t *testing.T) {
	inTempRepo(t)
	runCLI(t, "init")
	// Nothing conflicted at all is a clean exit 0 first.
	code0, out0, _ := runCLI(t, "apply")
	if code0 != ExitOK || !strings.Contains(out0, "no conflicted files found") {
		t.Fatalf("empty tree: code=%d out=%q", code0, out0)
	}
	write(t, "other.go", "<<<<<<< a\nnever seen\n=======\nbefore\n>>>>>>> b\n")
	code, out, _ := runCLI(t, "apply")
	if code != ExitConflicts {
		t.Fatalf("code=%d, want %d", code, ExitConflicts)
	}
	if !strings.Contains(out, "unmatched other.go: 1 conflict (no recorded resolution)") ||
		!strings.Contains(out, "rerekit apply: 0 conflicts resolved, 1 remaining") {
		t.Fatalf("out=%q", out)
	}
	if !strings.Contains(read(t, "other.go"), "<<<<<<<") {
		t.Fatal("unmatched conflict must stay in place")
	}
}

func TestApplyPartialWritesAndSnaps(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	// One known conflict plus one unknown in the same file, and a second
	// file where nothing matches at all.
	mixed := conflicted + "\n<<<<<<< HEAD\nnew stuff\n=======\nother stuff\n>>>>>>> main\ntail\n"
	write(t, "src/config.go", mixed)
	write(t, "other.go", "<<<<<<< a\nnever seen\n=======\nbefore\n>>>>>>> b\n")
	code, out, _ := runCLI(t, "apply", "--snap")
	if code != ExitConflicts {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "resolved src/config.go: 1 of 2 conflicts (1 remaining)") {
		t.Fatalf("out=%q", out)
	}
	got := read(t, "src/config.go")
	if !strings.Contains(got, "Retries: 5, Timeout: 30") || !strings.Contains(got, "new stuff") {
		t.Fatalf("partial apply wrote wrong content: %q", got)
	}
	// --snap captured every leftover for a later record — including the
	// fully-unmatched file, which apply itself never rewrites.
	entries, _ := os.ReadDir(".rerekit/pending")
	if len(entries) != 2 {
		t.Fatalf("--snap must snapshot all remaining conflicts, got %d snapshots", len(entries))
	}
}

func TestApplyRestrictsToGivenPath(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	write(t, "src/config.go", conflicted)
	write(t, "elsewhere/config.go", conflicted)
	code, out, _ := runCLI(t, "apply", "src")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if strings.Contains(out, "elsewhere") {
		t.Fatal("apply must not touch files outside the given path")
	}
	if !strings.Contains(read(t, "elsewhere/config.go"), "<<<<<<<") {
		t.Fatal("file outside the given path was rewritten")
	}
}

func TestStatusTextAndCounts(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	write(t, "src/config.go", conflicted)
	write(t, "new.go", "<<<<<<< a\nq\n=======\nw\n>>>>>>> b\n")
	code, out, _ := runCLI(t, "status")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, out)
	}
	for _, want := range []string{
		"new.go: 1 conflict, 0 replayable",
		"src/config.go: 1 conflict, 1 replayable",
		"rerekit status: 2 conflicted files, 2 conflicts (1 replayable), 0 pending snapshots, 1 recorded resolution",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestStatusJSON(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	write(t, "src/config.go", conflicted)
	code, out, _ := runCLI(t, "status", "--format", "json")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, out)
	}
	var doc struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		Resolutions   int    `json:"resolutions"`
		Files         []struct {
			Path       string `json:"path"`
			Conflicts  int    `json:"conflicts"`
			Replayable int    `json:"replayable"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.Tool != "rerekit" || doc.SchemaVersion != 1 || doc.Resolutions != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	if len(doc.Files) != 1 || doc.Files[0].Replayable != 1 {
		t.Fatalf("files = %+v", doc.Files)
	}
}

func TestListTextAndJSON(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	code, out, _ := runCLI(t, "list")
	if code != ExitOK || !strings.Contains(out, "src/config.go") || !strings.Contains(out, "ID") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = runCLI(t, "list", "--format", "json")
	if code != ExitOK {
		t.Fatal("list --format json failed")
	}
	var doc struct {
		Resolutions []struct {
			ID              string `json:"id"`
			Fingerprint     string `json:"fingerprint"`
			Path            string `json:"path"`
			ResolutionLines int    `json:"resolution_lines"`
		} `json:"resolutions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Resolutions) != 1 || len(doc.Resolutions[0].Fingerprint) != 64 ||
		doc.Resolutions[0].ID != doc.Resolutions[0].Fingerprint[:12] {
		t.Fatalf("doc = %+v", doc)
	}
}

func TestListEmptyStore(t *testing.T) {
	inTempRepo(t)
	runCLI(t, "init")
	code, out, _ := runCLI(t, "list")
	if code != ExitOK || !strings.Contains(out, "no resolutions recorded") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = runCLI(t, "list", "--format", "json")
	if code != ExitOK || !strings.Contains(out, "\"resolutions\": []") {
		t.Fatalf("empty JSON list must be an array: %q", out)
	}
	code, _, errOut := runCLI(t, "list", "--format", "yaml")
	if code != ExitUsage || !strings.Contains(errOut, "unknown --format") {
		t.Fatalf("bad format: code=%d err=%q", code, errOut)
	}
	code, _, errOut = runCLI(t, "list", "some/path")
	if code != ExitUsage || !strings.Contains(errOut, "takes no path arguments") {
		t.Fatalf("list with paths: code=%d err=%q", code, errOut)
	}
}

func TestShowPrintsResolutionFile(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	_, listOut, _ := runCLI(t, "list", "--format", "json")
	var doc struct {
		Resolutions []struct {
			ID string `json:"id"`
		} `json:"resolutions"`
	}
	if err := json.Unmarshal([]byte(listOut), &doc); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "show", doc.Resolutions[0].ID)
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{"rerekit-resolution-v1", "--- ours ---", "--- resolution ---", "|\treturn Options{Retries: 5, Timeout: 30}"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	code, _, errOut := runCLI(t, "show", "abcdefabcdef")
	if code != ExitErr || !strings.Contains(errOut, "no resolution") {
		t.Fatalf("unknown id: code=%d err=%q", code, errOut)
	}
}

func TestForgetByIDAndAll(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	_, listOut, _ := runCLI(t, "list", "--format", "json")
	var doc struct {
		Resolutions []struct {
			ID string `json:"id"`
		} `json:"resolutions"`
	}
	if err := json.Unmarshal([]byte(listOut), &doc); err != nil {
		t.Fatal(err)
	}
	id := doc.Resolutions[0].ID
	code, out, _ := runCLI(t, "forget", id)
	if code != ExitOK || !strings.Contains(out, "forgot "+id) || !strings.Contains(out, "1 resolution removed") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// Rebuild one and clear with --all.
	recordFixture(t)
	code, out, _ = runCLI(t, "forget", "--all")
	if code != ExitOK || !strings.Contains(out, "1 resolution removed") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	_, out, _ = runCLI(t, "list")
	if !strings.Contains(out, "no resolutions recorded") {
		t.Fatal("forget --all left resolutions behind")
	}
	// Usage errors: no IDs, or --all combined with IDs.
	for _, args := range [][]string{{"forget"}, {"forget", "--all", "someid"}} {
		code, _, errOut := runCLI(t, args...)
		if code != ExitUsage || !strings.Contains(errOut, "resolution IDs, or --all") {
			t.Errorf("%v: code=%d err=%q", args, code, errOut)
		}
	}
}

func TestCommandsOutsideAnyRepoErr(t *testing.T) {
	// A bare temp dir: no .git, no .rerekit anywhere above (temp dirs
	// live outside the source tree).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	code, _, errOut := runCLI(t, "snap")
	if code != ExitErr || !strings.Contains(errOut, "rerekit init") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestSnapFromSubdirectoryUsesRootRelativePaths(t *testing.T) {
	dir := inTempRepo(t)
	write(t, "src/config.go", conflicted)
	sub := filepath.Join(dir, "src")
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "snap")
	if code != ExitOK || !strings.Contains(out, "snapped src/config.go") {
		t.Fatalf("paths must be store-root-relative: code=%d out=%q", code, out)
	}
}

func TestRecordedResolutionSurvivesCorruptStoreDetection(t *testing.T) {
	inTempRepo(t)
	recordFixture(t)
	// Corrupt the .res file: apply must fail loudly, not silently skip.
	entries, _ := os.ReadDir(".rerekit/resolutions")
	path := filepath.Join(".rerekit/resolutions", entries[0].Name())
	write(t, path, "rerekit-resolution-v1\ngarbage\n")
	write(t, "src/config.go", conflicted)
	code, _, errOut := runCLI(t, "apply")
	if code != ExitErr || !strings.Contains(errOut, "rerekit:") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}
