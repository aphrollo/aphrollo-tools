package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/store"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// The state directory's retention (architecture §3 "Storage"): `gate gc` runs
// store.Retain over each repo's state directory and the shared directories
// that are not under the store yet, and, with --dry, prints their sizes. The
// session-start sweep runs this same command at most once a day (see
// internal/tdd/gc), so the sweep needs no trigger of its own.

// sharedStateRules are the directories every repo shares: today the ratchet
// cache (LRU 200 MB), which is not under the store yet and is swept where it
// is. The deferred-job files in <state>/deferred need no rule here: the gate's
// own sweep (tdd.ScanGC, and the one a hook runs) already takes them at 24 h,
// and kills the process a stale record names before it deletes the record.
func sharedStateRules() []store.DirRule {
	base := tdd.StateDir()
	if base == "" {
		return nil
	}
	return []store.DirRule{
		{Name: "cache", Dir: filepath.Join(base, "ratchet-cache"), MaxBytes: store.CacheMaxBytes},
	}
}

// repoStateRules are the per-repo directories of the layout table that hold
// loose files: jobs/ and out/ (neither exists yet).
func repoStateRules(dir string) []store.DirRule {
	return []store.DirRule{
		{Name: "jobs", Dir: filepath.Join(dir, "jobs"), MaxAge: store.JobMaxAge},
		{Name: "out", Dir: filepath.Join(dir, "out"), MaxAge: store.JobMaxAge, MaxBytes: store.OutMaxBytes},
	}
}

// retainState sweeps (or with dry, plans) the state of every repo in repos and
// the shared directories, prints what it removed or would and, when dry, the
// sizes, and answers the bytes removed and the files counted.
func retainState(repos []string, dry bool, stdout, stderr io.Writer) (freed int64, files int) {
	seen := map[string]bool{}
	report := func(rep store.RetentionReport) {
		for _, r := range rep.Removed {
			fmt.Fprintf(stdout, "%s  %9s  %s\n", r.Path, core.FormatBytes(r.Size), r.Reason)
		}
		for _, err := range rep.Errors {
			fmt.Fprintf(stderr, "aphrollo gate gc: state: %v\n", err)
		}
		freed += rep.Bytes()
		files += len(rep.Removed)
	}
	for _, repo := range repos {
		dir := core.EventLogDir(repo)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		s, err := store.Open(dir, store.Options{})
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo gate gc: state %s: %v\n", dir, err)
			continue
		}
		rules := repoStateRules(dir)
		rep, err := s.Retain(context.Background(), store.RetentionOptions{Now: time.Now(), Dry: dry, Rules: rules})
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo gate gc: state %s: %v\n", dir, err)
		}
		report(rep)
		if dry {
			printSizes(stdout, dir, s.StateSizes(rules))
		}
	}
	shared := sharedStateRules()
	report(store.RetainDirs(store.RetentionOptions{Now: time.Now(), Dry: dry, Rules: shared}))
	if dry && len(shared) > 0 {
		printSizes(stdout, "shared state "+tdd.StateDir(), store.DirSizes(shared))
	}
	return freed, files
}

func printSizes(w io.Writer, title string, lines []store.SizeLine) {
	fmt.Fprintf(w, "state sizes, %s\n", title)
	for _, l := range lines {
		cap := ""
		if l.Cap > 0 {
			cap = " / cap " + core.FormatBytes(l.Cap)
		}
		fmt.Fprintf(w, "  %-9s %6d files  %10s%s\n", l.Name, l.Files, core.FormatBytes(l.Bytes), cap)
	}
}
