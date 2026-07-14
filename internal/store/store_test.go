// Store tests: root discovery, layout creation, resolution CRUD with
// change detection, pending snapshot round-trips, and the loud failure
// on corrupt store files.
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/rerekit/internal/conflict"
	"github.com/JaydenCJ/rerekit/internal/resfile"
)

func newResolution(t *testing.T, ours, theirs, res string) *resfile.Resolution {
	t.Helper()
	h := &conflict.Hunk{Ours: []string{ours}, Theirs: []string{theirs}}
	return resfile.New(h, "src/a.go", []string{res})
}

func initStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir()}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDiscoverPrefersRerekitDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Discover(sub)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != root {
		t.Fatalf("root = %s, want %s", s.Root, root)
	}
}

func TestDiscoverFallsBackToGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Discover(sub)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != root || s.Initialized() {
		t.Fatalf("root = %s initialized = %v", s.Root, s.Initialized())
	}
	// Linked worktrees have a .git *file*, not a directory.
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = Discover(wt)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != wt {
		t.Fatalf("worktree root = %s", s.Root)
	}
}

func TestDiscoverInnerRerekitWinsOverOuterGit(t *testing.T) {
	// A nested project with its own store must not leak into a parent
	// repository.
	outer := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "vendor", "proj")
	if err := os.MkdirAll(filepath.Join(inner, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Discover(inner)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != inner {
		t.Fatalf("root = %s, want %s", s.Root, inner)
	}
}

func TestDiscoverErrsOutsideAnyRepo(t *testing.T) {
	if _, err := Discover(t.TempDir()); err == nil {
		t.Fatal("expected ErrNoStore")
	}
}

func TestInitCreatesLayoutAndGitignore(t *testing.T) {
	s := initStore(t)
	for _, d := range []string{s.resolutionsDir(), s.pendingDir()} {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			t.Errorf("%s: %v", d, err)
		}
	}
	gi, err := os.ReadFile(filepath.Join(s.Dir(), ".gitignore"))
	if err != nil || !strings.Contains(string(gi), "pending/") {
		t.Fatalf("gitignore = %q, %v", gi, err)
	}
	// Idempotent, and never overwrites a user-edited .gitignore.
	custom := []byte("pending/\nmine\n")
	if err := os.WriteFile(filepath.Join(s.Dir(), ".gitignore"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	gi, _ = os.ReadFile(filepath.Join(s.Dir(), ".gitignore"))
	if string(gi) != string(custom) {
		t.Fatal("re-init clobbered .gitignore")
	}
}

func TestSaveResolutionStatuses(t *testing.T) {
	s := initStore(t)
	r := newResolution(t, "a", "b", "ab")
	if st, err := s.SaveResolution(r); err != nil || st != "new" {
		t.Fatalf("first save: %q, %v", st, err)
	}
	if st, err := s.SaveResolution(r); err != nil || st != "unchanged" {
		t.Fatalf("identical save: %q, %v", st, err)
	}
	r2 := newResolution(t, "a", "b", "different")
	if st, err := s.SaveResolution(r2); err != nil || st != "updated" {
		t.Fatalf("changed save: %q, %v", st, err)
	}
}

func TestLoadAndDeleteResolution(t *testing.T) {
	s := initStore(t)
	r := newResolution(t, "x", "y", "xy")
	if _, err := s.SaveResolution(r); err != nil {
		t.Fatal(err)
	}
	back, err := s.LoadResolution(r.ID())
	if err != nil || back.Fingerprint != r.Fingerprint {
		t.Fatalf("load: %v", err)
	}
	if err := s.DeleteResolution(r.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadResolution(r.ID()); err == nil || !strings.Contains(err.Error(), "no resolution") {
		t.Fatalf("err = %v", err)
	}
	if err := s.DeleteResolution(r.ID()); err == nil {
		t.Fatal("double delete must error")
	}
}

func TestLoadRejectsMalformedID(t *testing.T) {
	s := initStore(t)
	for _, id := range []string{"", "short", "ZZZZZZZZZZZZ", "../../escape", strings.Repeat("a", 13)} {
		if _, err := s.LoadResolution(id); err == nil || !strings.Contains(err.Error(), "invalid resolution ID") {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

func TestListResolutionsSortedAndFailsOnCorruption(t *testing.T) {
	s := initStore(t)
	for i, res := range []string{"r1", "r2", "r3"} {
		r := newResolution(t, "a"+res, "b", res)
		if _, err := s.SaveResolution(r); err != nil {
			t.Fatal(err, i)
		}
	}
	list, err := s.ListResolutions()
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %d, %v", len(list), err)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID() >= list[i].ID() {
			t.Fatal("list not sorted by ID")
		}
	}
	// A corrupt file must fail the listing with its path, not vanish.
	bad := filepath.Join(s.resolutionsDir(), strings.Repeat("0", 12)+resfile.Extension)
	if err := os.WriteFile(bad, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListResolutions(); err == nil || !strings.Contains(err.Error(), strings.Repeat("0", 12)) {
		t.Fatalf("err = %v, want mention of the corrupt file", err)
	}
}

func TestListResolutionsEmptyOrMissingDir(t *testing.T) {
	s := &Store{Root: t.TempDir()} // never initialized
	list, err := s.ListResolutions()
	if err != nil || len(list) != 0 {
		t.Fatalf("uninitialized store must list empty: %d, %v", len(list), err)
	}
}

func TestPendingRoundTrip(t *testing.T) {
	s := initStore(t)
	content := "line1\n<<<<<<< a\nx\n=======\ny\n>>>>>>> b\nline2\n"
	if err := s.SavePending("dir/file.go", content); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListPending()
	if err != nil || len(list) != 1 {
		t.Fatalf("pending: %d, %v", len(list), err)
	}
	if list[0].Path != "dir/file.go" || list[0].Content != content {
		t.Fatalf("round trip changed snapshot: %+v", list[0])
	}
	// A missing final newline must survive the round trip too.
	noNL := "a\n<<<<<<< x\n1\n=======\n2\n>>>>>>> y"
	if err := s.SavePending("no-nl", noNL); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	if list[1].Content != noNL {
		t.Fatalf("content = %q, want %q", list[1].Content, noNL)
	}
}

func TestPendingReplaceAndDelete(t *testing.T) {
	s := initStore(t)
	if err := s.SavePending("f", "v1\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePending("f", "v2\n"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListPending()
	if len(list) != 1 || list[0].Content != "v2\n" {
		t.Fatalf("replace failed: %+v", list)
	}
	if err := s.DeletePending("f"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePending("f"); err != nil {
		t.Fatal("deleting a missing snapshot must be a no-op")
	}
	list, _ = s.ListPending()
	if len(list) != 0 {
		t.Fatal("snapshot not deleted")
	}
}

func TestPendingSortedByPath(t *testing.T) {
	s := initStore(t)
	for _, p := range []string{"z.go", "a.go", "m/n.go"} {
		if err := s.SavePending(p, "x\n"); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	got := []string{list[0].Path, list[1].Path, list[2].Path}
	if got[0] != "a.go" || got[1] != "m/n.go" || got[2] != "z.go" {
		t.Fatalf("order = %v", got)
	}
}

func TestRelAbsRoundTripAndEscapeRejection(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if _, err := s.Rel(filepath.Join(s.Root, "ok.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rel(filepath.Dir(s.Root)); err == nil {
		t.Fatal("parent of root must be rejected")
	}
	if _, err := s.Rel(filepath.Join(filepath.Dir(s.Root), "elsewhere")); err == nil {
		t.Fatal("sibling of root must be rejected")
	}
	abs := filepath.Join(s.Root, "a", "b.go")
	rel, err := s.Rel(abs)
	if err != nil || rel != "a/b.go" {
		t.Fatalf("rel = %q, %v", rel, err)
	}
	if s.Abs(rel) != abs {
		t.Fatalf("abs = %q, want %q", s.Abs(rel), abs)
	}
}
