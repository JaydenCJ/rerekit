// Package cli implements the rerekit command-line interface. Run is
// invoked by main and by the in-process integration tests; it never
// calls os.Exit and writes only to the streams it is given.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JaydenCJ/rerekit/internal/conflict"
	"github.com/JaydenCJ/rerekit/internal/store"
	"github.com/JaydenCJ/rerekit/internal/version"
)

// Exit codes, documented in the README.
const (
	ExitOK        = 0 // success
	ExitConflicts = 1 // apply left conflicts unresolved (scripting signal)
	ExitUsage     = 2 // bad command line
	ExitErr       = 3 // runtime error (I/O, malformed store, bad input)
)

const usageText = `rerekit — committable, human-readable git conflict resolutions.

Usage:
  rerekit <command> [flags] [paths...]

Commands:
  init                    create the .rerekit/ store at the project root
  snap [paths]            snapshot conflicted files (run while conflicts exist)
  record [--keep-pending] extract resolutions from snapshots after you resolve
  apply [--dry-run] [--snap] [paths]
                          replay recorded resolutions onto fresh conflicts
  status [--format text|json] [paths]
                          conflicted files, replayable hunks, store counts
  list [--format text|json]
                          recorded resolutions
  show <id>               print one resolution file
  forget <id...> | --all  remove recorded resolutions
  version                 print the version

Paths default to the whole tree under the store root. Exit codes:
0 ok, 1 apply left conflicts unresolved, 2 usage error, 3 runtime error.
`

// Run executes one CLI invocation and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return ExitOK
	}
	cmd, rest := args[0], args[1:]
	c := &ctx{stdout: stdout, stderr: stderr}
	switch cmd {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usageText)
		return ExitOK
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "rerekit %s\n", version.Version)
		return ExitOK
	case "init":
		return c.cmdInit(rest)
	case "snap":
		return c.cmdSnap(rest)
	case "record":
		return c.cmdRecord(rest)
	case "apply":
		return c.cmdApply(rest)
	case "status":
		return c.cmdStatus(rest)
	case "list":
		return c.cmdList(rest)
	case "show":
		return c.cmdShow(rest)
	case "forget":
		return c.cmdForget(rest)
	default:
		fmt.Fprintf(stderr, "rerekit: unknown command %q (run \"rerekit help\")\n", cmd)
		return ExitUsage
	}
}

// ctx carries the output streams through one invocation.
type ctx struct {
	stdout io.Writer
	stderr io.Writer
}

func (c *ctx) errf(format string, a ...any) int {
	fmt.Fprintf(c.stderr, "rerekit: "+format+"\n", a...)
	return ExitErr
}

func (c *ctx) warnf(format string, a ...any) {
	fmt.Fprintf(c.stderr, "warning: "+format+"\n", a...)
}

// openStore discovers the store from the working directory.
func (c *ctx) openStore() (*store.Store, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return store.Discover(cwd)
}

// count renders "1 conflict" / "3 conflicts" for regular nouns.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// target is one conflicted file selected for snap/apply/status.
type target struct {
	Rel     string // canonical repo-relative path
	Content string // exact current content
	File    *conflict.File
}

// conflictedTargets resolves path arguments (or scans the whole tree
// when none are given) to parsed conflicted files, sorted by path.
// Explicitly named files fail hard on problems; scanned files are
// skipped with a warning so one odd file cannot block a whole replay.
func (c *ctx) conflictedTargets(s *store.Store, args []string) ([]target, error) {
	type candidate struct {
		abs      string
		explicit bool
	}
	var cands []candidate
	if len(args) == 0 {
		for _, abs := range scanTree(s.Root, c.warnf) {
			cands = append(cands, candidate{abs: abs})
		}
	}
	for _, arg := range args {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		if fi.IsDir() {
			for _, p := range scanTree(abs, c.warnf) {
				cands = append(cands, candidate{abs: p})
			}
			continue
		}
		cands = append(cands, candidate{abs: abs, explicit: true})
	}

	seen := map[string]bool{}
	var out []target
	for _, cand := range cands {
		rel, err := s.Rel(cand.abs)
		if err != nil {
			return nil, err
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		data, err := os.ReadFile(cand.abs)
		if err != nil {
			if cand.explicit {
				return nil, err
			}
			c.warnf("%s: %v (skipped)", rel, err)
			continue
		}
		content := string(data)
		if !conflict.HasMarker(content) {
			if cand.explicit {
				c.warnf("%s: no conflict markers found (skipped)", rel)
			}
			continue
		}
		f, err := conflict.Parse(content)
		if err != nil {
			if cand.explicit {
				return nil, fmt.Errorf("%s: %w", rel, err)
			}
			c.warnf("%s: %v (skipped)", rel, err)
			continue
		}
		if len(f.Hunks()) == 0 {
			continue
		}
		out = append(out, target{Rel: rel, Content: content, File: f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// scanTree walks root and returns files that contain a conflict marker
// line, skipping VCS/store directories, symlinks, and binary files.
func scanTree(root string, warn func(string, ...any)) []string {
	var hits []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			warn("%s: %v (skipped)", path, err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", store.DirName:
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			warn("%s: %v (skipped)", path, err)
			return nil
		}
		if isBinary(data) || !conflict.HasMarker(string(data)) {
			return nil
		}
		hits = append(hits, path)
		return nil
	})
	sort.Strings(hits)
	return hits
}

// isBinary applies git's heuristic: a NUL byte in the first 8000 bytes.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return strings.IndexByte(string(data[:n]), 0) >= 0
}
