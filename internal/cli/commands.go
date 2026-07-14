// Subcommand implementations. Each cmd* method parses its own flags,
// performs the work through the internal packages, and returns an exit
// code; all user-visible strings live here.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/JaydenCJ/rerekit/internal/conflict"
	"github.com/JaydenCJ/rerekit/internal/engine"
	"github.com/JaydenCJ/rerekit/internal/fingerprint"
	"github.com/JaydenCJ/rerekit/internal/resfile"
	"github.com/JaydenCJ/rerekit/internal/store"
)

// newFlagSet builds a flag set that reports errors on our stderr and
// never os.Exits.
func (c *ctx) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	return fs
}

// ---- init ----

func (c *ctx) cmdInit(args []string) int {
	fs := c.newFlagSet("init")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(c.stderr, "rerekit init: takes no arguments (it initializes at the discovered project root)")
		return ExitUsage
	}
	cwd, err := os.Getwd()
	if err != nil {
		return c.errf("%v", err)
	}
	s, err := store.Discover(cwd)
	if errors.Is(err, store.ErrNoStore) {
		// No repository above us: initialize right here.
		s = &store.Store{Root: cwd}
	} else if err != nil {
		return c.errf("%v", err)
	}
	existed := s.Initialized()
	if err := s.Init(); err != nil {
		return c.errf("%v", err)
	}
	where := ""
	if s.Root != cwd {
		where = fmt.Sprintf(" in %s", s.Root)
	}
	if existed {
		fmt.Fprintf(c.stdout, "rerekit init: %s/ already exists%s\n", store.DirName, where)
	} else {
		fmt.Fprintf(c.stdout, "rerekit init: created %s/%s\n", store.DirName, where)
	}
	return ExitOK
}

// ---- snap ----

func (c *ctx) cmdSnap(args []string) int {
	fs := c.newFlagSet("snap")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	if !s.Initialized() {
		if err := s.Init(); err != nil {
			return c.errf("%v", err)
		}
		fmt.Fprintf(c.stdout, "initialized empty store in %s/\n", store.DirName)
	}
	targets, err := c.conflictedTargets(s, fs.Args())
	if err != nil {
		return c.errf("%v", err)
	}
	if len(targets) == 0 {
		fmt.Fprintln(c.stdout, "rerekit snap: no conflicted files found")
		return ExitOK
	}
	total := 0
	for _, t := range targets {
		if err := s.SavePending(t.Rel, t.Content); err != nil {
			return c.errf("%v", err)
		}
		n := len(t.File.Hunks())
		total += n
		fmt.Fprintf(c.stdout, "snapped %s (%s)\n", t.Rel, count(n, "conflict"))
	}
	fmt.Fprintf(c.stdout, "rerekit snap: %s snapped, %s pending\n",
		count(len(targets), "file"), count(total, "conflict"))
	return ExitOK
}

// ---- record ----

func (c *ctx) cmdRecord(args []string) int {
	fs := c.newFlagSet("record")
	keep := fs.Bool("keep-pending", false, "keep snapshots after recording")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(c.stderr, "rerekit record: takes no path arguments (it consumes snapshots taken by \"rerekit snap\")")
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	pendings, err := s.ListPending()
	if err != nil {
		return c.errf("%v", err)
	}
	if len(pendings) == 0 {
		fmt.Fprintln(c.stdout, "rerekit record: nothing pending (run \"rerekit snap\" while conflicts are unresolved)")
		return ExitOK
	}
	recorded, files := 0, 0
	for _, p := range pendings {
		current, err := os.ReadFile(s.Abs(p.Path))
		if err != nil {
			c.warnf("%s: %v (pending kept)", p.Path, err)
			continue
		}
		if conflict.HasMarker(string(current)) {
			if f, err := conflict.Parse(string(current)); err != nil || len(f.Hunks()) > 0 {
				fmt.Fprintf(c.stdout, "skipped %s: still conflicted\n", p.Path)
				continue
			}
		}
		pre, err := conflict.Parse(p.Content)
		if err != nil {
			c.warnf("%s: corrupt snapshot: %v (pending kept)", p.Path, err)
			continue
		}
		extracted, err := engine.Extract(pre, string(current))
		if err != nil {
			c.warnf("%s: %v (pending kept)", p.Path, err)
			continue
		}
		for _, e := range extracted {
			r := resfile.New(e.Hunk, p.Path, e.Resolution)
			status, err := s.SaveResolution(r)
			if err != nil {
				return c.errf("%v", err)
			}
			fmt.Fprintf(c.stdout, "recorded %s %s (%s)\n", r.ID(), p.Path, status)
			recorded++
		}
		files++
		if !*keep {
			if err := s.DeletePending(p.Path); err != nil {
				return c.errf("%v", err)
			}
		}
	}
	fmt.Fprintf(c.stdout, "rerekit record: %s recorded from %s\n",
		count(recorded, "resolution"), count(files, "file"))
	return ExitOK
}

// ---- apply ----

func (c *ctx) cmdApply(args []string) int {
	fs := c.newFlagSet("apply")
	dryRun := fs.Bool("dry-run", false, "report what would be resolved without writing")
	snap := fs.Bool("snap", false, "snapshot files that still have unresolved conflicts")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	resolutions, err := s.ListResolutions()
	if err != nil {
		return c.errf("%v", err)
	}
	byFP := make(map[string]*resfile.Resolution, len(resolutions))
	for _, r := range resolutions {
		byFP[r.Fingerprint] = r
	}
	lookup := func(fp string) *resfile.Resolution { return byFP[fp] }

	targets, err := c.conflictedTargets(s, fs.Args())
	if err != nil {
		return c.errf("%v", err)
	}
	if len(targets) == 0 {
		fmt.Fprintln(c.stdout, "rerekit apply: no conflicted files found")
		return ExitOK
	}

	verb := "resolved"
	if *dryRun {
		verb = "would resolve"
	}
	applied, remaining := 0, 0
	for _, t := range targets {
		st := engine.Apply(t.File, lookup)
		applied += st.Applied
		remaining += st.Remaining
		total := st.Applied + st.Remaining
		switch {
		case st.Applied == 0:
			fmt.Fprintf(c.stdout, "unmatched %s: %s (no recorded resolution)\n",
				t.Rel, count(total, "conflict"))
		case st.Remaining > 0:
			fmt.Fprintf(c.stdout, "%s %s: %d of %d conflicts (%d remaining)\n",
				verb, t.Rel, st.Applied, total, st.Remaining)
		default:
			fmt.Fprintf(c.stdout, "%s %s: %d of %d conflicts\n", verb, t.Rel, st.Applied, total)
		}
		if *dryRun {
			continue
		}
		content := t.Content
		if st.Applied > 0 {
			content = t.File.Render()
			if err := os.WriteFile(s.Abs(t.Rel), []byte(content), 0o644); err != nil {
				return c.errf("%v", err)
			}
		}
		if *snap && st.Remaining > 0 {
			if err := s.SavePending(t.Rel, content); err != nil {
				return c.errf("%v", err)
			}
			fmt.Fprintf(c.stdout, "snapped %s (%s)\n", t.Rel, count(st.Remaining, "conflict"))
		}
	}
	fmt.Fprintf(c.stdout, "rerekit apply: %s resolved, %d remaining\n",
		count(applied, "conflict"), remaining)
	if remaining > 0 {
		return ExitConflicts
	}
	return ExitOK
}

// ---- status ----

func (c *ctx) cmdStatus(args []string) int {
	fs := c.newFlagSet("status")
	format := fs.String("format", "text", "output format: text or json")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(c.stderr, "rerekit status: unknown --format %q (want text or json)\n", *format)
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	resolutions, err := s.ListResolutions()
	if err != nil {
		return c.errf("%v", err)
	}
	known := make(map[string]bool, len(resolutions))
	for _, r := range resolutions {
		known[r.Fingerprint] = true
	}
	pendings, err := s.ListPending()
	if err != nil {
		return c.errf("%v", err)
	}
	targets, err := c.conflictedTargets(s, fs.Args())
	if err != nil {
		return c.errf("%v", err)
	}

	type fileStatus struct {
		Path       string `json:"path"`
		Conflicts  int    `json:"conflicts"`
		Replayable int    `json:"replayable"`
	}
	var files []fileStatus
	totalConflicts, totalReplayable := 0, 0
	for _, t := range targets {
		st := fileStatus{Path: t.Rel}
		for _, h := range t.File.Hunks() {
			st.Conflicts++
			if known[fingerprint.Hunk(h)] {
				st.Replayable++
			}
		}
		totalConflicts += st.Conflicts
		totalReplayable += st.Replayable
		files = append(files, st)
	}

	if *format == "json" {
		return c.printJSON(map[string]any{
			"tool":           "rerekit",
			"schema_version": 1,
			"resolutions":    len(resolutions),
			"pending":        len(pendings),
			"files":          jsonSlice(files),
		})
	}
	for _, f := range files {
		fmt.Fprintf(c.stdout, "%s: %s, %d replayable\n", f.Path, count(f.Conflicts, "conflict"), f.Replayable)
	}
	fmt.Fprintf(c.stdout, "rerekit status: %s, %s (%d replayable), %s, %s\n",
		count(len(files), "conflicted file"),
		count(totalConflicts, "conflict"), totalReplayable,
		count(len(pendings), "pending snapshot"),
		count(len(resolutions), "recorded resolution"))
	return ExitOK
}

// ---- list ----

func (c *ctx) cmdList(args []string) int {
	fs := c.newFlagSet("list")
	format := fs.String("format", "text", "output format: text or json")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(c.stderr, "rerekit list: unknown --format %q (want text or json)\n", *format)
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(c.stderr, "rerekit list: takes no path arguments (use \"rerekit status [paths]\" for per-file state)")
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	resolutions, err := s.ListResolutions()
	if err != nil {
		return c.errf("%v", err)
	}

	if *format == "json" {
		type item struct {
			ID              string `json:"id"`
			Fingerprint     string `json:"fingerprint"`
			Path            string `json:"path"`
			OursLines       int    `json:"ours_lines"`
			TheirsLines     int    `json:"theirs_lines"`
			ResolutionLines int    `json:"resolution_lines"`
			HasBase         bool   `json:"has_base"`
		}
		items := make([]item, 0, len(resolutions))
		for _, r := range resolutions {
			items = append(items, item{
				ID: r.ID(), Fingerprint: r.Fingerprint, Path: r.Path,
				OursLines: len(r.Ours), TheirsLines: len(r.Theirs),
				ResolutionLines: len(r.Resolution), HasBase: r.HasBase,
			})
		}
		return c.printJSON(map[string]any{
			"tool":           "rerekit",
			"schema_version": 1,
			"resolutions":    items,
		})
	}
	if len(resolutions) == 0 {
		fmt.Fprintln(c.stdout, "rerekit list: no resolutions recorded")
		return ExitOK
	}
	fmt.Fprintf(c.stdout, "%-12s  %5s  %6s  %5s  %s\n", "ID", "OURS", "THEIRS", "RES", "PATH")
	for _, r := range resolutions {
		fmt.Fprintf(c.stdout, "%-12s  %5d  %6d  %5d  %s\n",
			r.ID(), len(r.Ours), len(r.Theirs), len(r.Resolution), r.Path)
	}
	return ExitOK
}

// ---- show ----

func (c *ctx) cmdShow(args []string) int {
	fs := c.newFlagSet("show")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(c.stderr, "rerekit show: expected exactly one resolution ID")
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	data, err := s.ResolutionSource(fs.Arg(0))
	if err != nil {
		return c.errf("%v", err)
	}
	fmt.Fprint(c.stdout, string(data))
	return ExitOK
}

// ---- forget ----

func (c *ctx) cmdForget(args []string) int {
	fs := c.newFlagSet("forget")
	all := fs.Bool("all", false, "remove every recorded resolution")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if *all == (fs.NArg() > 0) {
		fmt.Fprintln(c.stderr, "rerekit forget: pass one or more resolution IDs, or --all")
		return ExitUsage
	}
	s, err := c.openStore()
	if err != nil {
		return c.errf("%v", err)
	}
	ids := fs.Args()
	if *all {
		resolutions, err := s.ListResolutions()
		if err != nil {
			return c.errf("%v", err)
		}
		ids = ids[:0]
		for _, r := range resolutions {
			ids = append(ids, r.ID())
		}
	}
	for _, id := range ids {
		if err := s.DeleteResolution(id); err != nil {
			return c.errf("%v", err)
		}
		fmt.Fprintf(c.stdout, "forgot %s\n", id)
	}
	fmt.Fprintf(c.stdout, "rerekit forget: %s removed\n", count(len(ids), "resolution"))
	return ExitOK
}
