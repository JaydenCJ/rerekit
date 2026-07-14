// Package store manages the on-disk layout of a rerekit store:
//
//	.rerekit/
//	├── .gitignore        # ignores pending/ — snapshots are per-checkout
//	├── resolutions/      # committed *.res files, one per conflict
//	└── pending/          # preimage snapshots awaiting `rerekit record`
//
// The store root is discovered by walking up from the working directory
// to the first ancestor containing `.rerekit`, falling back to the first
// containing `.git` (a directory, or the file a linked worktree uses).
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JaydenCJ/rerekit/internal/fingerprint"
	"github.com/JaydenCJ/rerekit/internal/resfile"
)

// DirName is the store directory created at the repository root.
const DirName = ".rerekit"

// ErrNoStore is returned by Discover when neither a .rerekit directory
// nor a .git entry exists in any ancestor.
var ErrNoStore = errors.New("no .rerekit store or git repository found (run \"rerekit init\" at your project root)")

// Store is rooted at the directory that contains (or will contain)
// .rerekit. All paths handed to pending APIs are relative to Root with
// forward slashes.
type Store struct {
	Root string
}

// Discover walks up from start. It prefers an existing .rerekit
// directory; otherwise it settles on the first ancestor that has a .git
// entry (where Init can create the store).
func Discover(start string) (*Store, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	gitRoot := ""
	for {
		if isDir(filepath.Join(dir, DirName)) {
			return &Store{Root: dir}, nil
		}
		if gitRoot == "" {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				gitRoot = dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if gitRoot != "" {
		return &Store{Root: gitRoot}, nil
	}
	return nil, ErrNoStore
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// Dir returns the .rerekit directory path.
func (s *Store) Dir() string { return filepath.Join(s.Root, DirName) }

func (s *Store) resolutionsDir() string { return filepath.Join(s.Dir(), "resolutions") }
func (s *Store) pendingDir() string     { return filepath.Join(s.Dir(), "pending") }

// Initialized reports whether the .rerekit directory exists.
func (s *Store) Initialized() bool { return isDir(s.Dir()) }

// Init creates the store layout. It is idempotent and never overwrites
// an existing .gitignore.
func (s *Store) Init() error {
	for _, d := range []string{s.resolutionsDir(), s.pendingDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	gi := filepath.Join(s.Dir(), ".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, os.ErrNotExist) {
		content := "# pending conflict snapshots are per-checkout state; only resolutions/ is shared\npending/\n"
		return os.WriteFile(gi, []byte(content), 0o644)
	}
	return nil
}

// Rel converts an absolute path inside the store to the canonical
// slash-separated repo-relative form used in files and output.
func (s *Store) Rel(abs string) (string, error) {
	rel, err := filepath.Rel(s.Root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the store root %s", abs, s.Root)
	}
	return filepath.ToSlash(rel), nil
}

// Abs converts a canonical repo-relative path back to a filesystem path.
func (s *Store) Abs(rel string) string {
	return filepath.Join(s.Root, filepath.FromSlash(rel))
}

// ---- resolutions ----

// SaveResolution writes r to resolutions/<id>.res. The returned status
// is "new", "updated" (an existing file changed), or "unchanged".
func (s *Store) SaveResolution(r *resfile.Resolution) (status string, err error) {
	if err := os.MkdirAll(s.resolutionsDir(), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(s.resolutionsDir(), r.ID()+resfile.Extension)
	data := resfile.Marshal(r)
	old, readErr := os.ReadFile(path)
	switch {
	case readErr == nil && string(old) == string(data):
		return "unchanged", nil
	case readErr == nil:
		status = "updated"
	default:
		status = "new"
	}
	return status, os.WriteFile(path, data, 0o644)
}

// LoadResolution reads one resolution by short ID.
func (s *Store) LoadResolution(id string) (*resfile.Resolution, error) {
	path, err := s.resolutionPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no resolution %s in %s", id, s.resolutionsDir())
	}
	if err != nil {
		return nil, err
	}
	r, err := resfile.Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// ResolutionSource returns the raw bytes of a resolution file, for
// `rerekit show`.
func (s *Store) ResolutionSource(id string) ([]byte, error) {
	path, err := s.resolutionPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no resolution %s in %s", id, s.resolutionsDir())
	}
	return data, err
}

// DeleteResolution removes one resolution by short ID.
func (s *Store) DeleteResolution(id string) error {
	path, err := s.resolutionPath(id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no resolution %s in %s", id, s.resolutionsDir())
	}
	return err
}

// ListResolutions loads every resolution, sorted by ID. A corrupt file
// fails the whole listing with its path — the store is meant to be
// reviewed and committed, so damage must surface, not be skipped.
func (s *Store) ListResolutions() ([]*resfile.Resolution, error) {
	entries, err := os.ReadDir(s.resolutionsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*resfile.Resolution
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), resfile.Extension) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), resfile.Extension)
		r, err := s.LoadResolution(id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (s *Store) resolutionPath(id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("invalid resolution ID %q (expected %d hex characters, as printed by \"rerekit list\")", id, fingerprint.IDLen)
	}
	return filepath.Join(s.resolutionsDir(), id+resfile.Extension), nil
}

func validID(id string) bool {
	if len(id) != fingerprint.IDLen {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ---- pending snapshots ----

const pendingMagic = "rerekit-pending-v1"

// Pending is a preimage snapshot of one conflicted file, taken by
// `rerekit snap` and consumed by `rerekit record`.
type Pending struct {
	Path    string // canonical repo-relative path
	Content string // exact file content at snapshot time
}

// SavePending snapshots content for relPath, replacing any previous
// snapshot for the same path.
func (s *Store) SavePending(relPath, content string) error {
	if err := os.MkdirAll(s.pendingDir(), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(pendingMagic + "\n")
	b.WriteString("path: " + relPath + "\n")
	lines := strings.Split(content, "\n")
	if last := len(lines) - 1; lines[last] == "" {
		lines = lines[:last]
	} else {
		b.WriteString("final-newline: false\n")
	}
	b.WriteString("\n--- content ---\n")
	for _, l := range lines {
		b.WriteString("|" + l + "\n")
	}
	b.WriteString("--- end ---\n")
	return os.WriteFile(s.pendingFile(relPath), []byte(b.String()), 0o644)
}

// ListPending returns all snapshots sorted by path.
func (s *Store) ListPending() ([]Pending, error) {
	entries, err := os.ReadDir(s.pendingDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Pending
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pre") {
			continue
		}
		p, err := s.loadPending(filepath.Join(s.pendingDir(), e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// DeletePending removes the snapshot for relPath; missing is not an
// error (record may be re-run).
func (s *Store) DeletePending(relPath string) error {
	err := os.Remove(s.pendingFile(relPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) pendingFile(relPath string) string {
	sum := sha256.Sum256([]byte(relPath))
	return filepath.Join(s.pendingDir(), hex.EncodeToString(sum[:8])+".pre")
}

func (s *Store) loadPending(path string) (Pending, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Pending{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || lines[0] != pendingMagic {
		return Pending{}, fmt.Errorf("%s: not a rerekit pending snapshot", path)
	}
	p := Pending{}
	finalNewline := true
	i := 1
	for ; i < len(lines) && lines[i] != ""; i++ {
		key, value, ok := strings.Cut(lines[i], ": ")
		if !ok {
			return Pending{}, fmt.Errorf("%s: line %d: malformed header", path, i+1)
		}
		switch key {
		case "path":
			p.Path = value
		case "final-newline":
			finalNewline = value != "false"
		}
	}
	i++ // blank line
	if i >= len(lines) || lines[i] != "--- content ---" {
		return Pending{}, fmt.Errorf("%s: missing content section", path)
	}
	i++
	var payload []string
	for ; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
		payload = append(payload, lines[i][1:])
	}
	if i >= len(lines) || lines[i] != "--- end ---" {
		return Pending{}, fmt.Errorf("%s: missing end marker", path)
	}
	if p.Path == "" {
		return Pending{}, fmt.Errorf("%s: missing path header", path)
	}
	p.Content = strings.Join(payload, "\n")
	if finalNewline && len(payload) > 0 {
		p.Content += "\n"
	}
	return p, nil
}
