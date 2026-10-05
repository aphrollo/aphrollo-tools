// Command replay is the release replay: it runs the newest release's binary (the
// previous one) and the candidate's read-only over trees that did not change, and
// fails when the candidate reports a hit the previous release did not, which is
// how a lexer fix once handed a consumer 947 false hits on a tree nobody had
// touched (#1023).
//
//	go run ./tools/replay
//
// The trees are this repository at the candidate commit and a synthetic one
// generated from a seed to the scale and file mix of a TypeScript and Python
// consumer. Each is read by `ratchet check --dry` (cold, then warm), `docs
// check` and the gate's edit-time judgement of a fixed set of files, by the
// candidate and then by the previous release on one state store: the previous
// release must read what the candidate wrote. Exit 0: no new hit. Exit 1: the
// candidate adds hits, cannot read a tree, or leaves the previous release unable
// to read its state. Exit 2: the replay itself could not run.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type config struct {
	Repo, Prev, Work, HeadBin, PrevBin, Only string
	Seed                                     uint64
	Lines, Files                             int
	Keep                                     bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := config{}
	fs.StringVar(&cfg.Repo, "repo", ".", "the candidate checkout: built as the candidate binary, and read as a tree")
	fs.StringVar(&cfg.Prev, "prev", "", "the release to replay against, a ref (default: the newest v* tag that is not the candidate's own commit)")
	fs.StringVar(&cfg.Work, "work", "", "scratch directory (default: a temporary one, removed at the end)")
	fs.StringVar(&cfg.HeadBin, "head-bin", "", "a prebuilt candidate binary (default: built from -repo)")
	fs.StringVar(&cfg.PrevBin, "prev-bin", "", "a prebuilt previous binary (default: built from -prev)")
	fs.StringVar(&cfg.Only, "only", "", "one tree to read: self or synthetic (default: both)")
	fs.Uint64Var(&cfg.Seed, "seed", 1, "seed of the synthetic tree")
	fs.IntVar(&cfg.Lines, "lines", 200000, "line budget of the synthetic tree, a stand-in for a consumer of fanvue's scale")
	fs.IntVar(&cfg.Files, "files", 24, "files of each tree the gate judges as edits")
	fs.BoolVar(&cfg.Keep, "keep", false, "leave the scratch directory in place (default: what the replay made is removed when it passes; a failed replay always keeps it and prints its path)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "replay: unexpected argument %q\n", fs.Arg(0))
		return 2
	case cfg.Only != "" && cfg.Only != "self" && cfg.Only != "synthetic":
		fmt.Fprintf(stderr, "replay: -only is self or synthetic, not %q\n", cfg.Only)
		return 2
	case cfg.Files < 1 || cfg.Lines < 1000:
		fmt.Fprintln(stderr, "replay: -files needs at least 1 and -lines at least 1000")
		return 2
	}
	failed, err := replay(cfg, stdout)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "replay: %v\n", err)
		return 2
	case failed:
		return 1
	}
	return 0
}

func replay(cfg config, stdout io.Writer) (failed bool, err error) {
	work := cfg.Work
	made := work != ""
	if work == "" {
		if work, err = os.MkdirTemp("", "replay-"); err != nil {
			return false, err
		}
		made = true
	}
	if work, err = filepath.Abs(work); err != nil {
		return false, err
	}
	if cfg.Work != "" {
		_, statErr := os.Stat(work)
		made = os.IsNotExist(statErr)
	}
	claim, err := claimWork(work, made)
	if err != nil {
		return false, err
	}
	defer func() { claim.settle(err == nil && !failed, cfg.Keep, stdout) }()
	if cfg.Repo, err = filepath.Abs(cfg.Repo); err != nil {
		return false, err
	}
	r0 := runner{area: filepath.Join(work, "area")}
	cand := bin{"candidate", cfg.HeadBin}
	if cand.Path == "" {
		cand.Path = filepath.Join(work, "bin", exeName("candidate"))
		if err := build(cfg.Repo, cand.Path); err != nil {
			return false, err
		}
	}
	ref, err := r0.previousRef(cfg.Repo, cfg.Prev)
	if err != nil {
		return false, err
	}
	prev := bin{"previous", cfg.PrevBin}
	if prev.Path == "" {
		prev.Path = filepath.Join(work, "bin", exeName("previous"))
		if err := r0.buildRelease(cfg.Repo, ref, filepath.Join(work, "previous-src"), prev.Path); err != nil {
			return false, err
		}
	}
	head, err := r0.git(cfg.Repo, "rev-parse", "--short", "HEAD")
	if err != nil {
		return false, err
	}
	fmt.Fprintf(stdout, "replay: previous %s, candidate %s, on %s/%s\n", ref, strings.TrimSpace(head), runtime.GOOS, runtime.GOARCH)

	for _, name := range []string{"self", "synthetic"} {
		if cfg.Only != "" && cfg.Only != name {
			continue
		}
		r := runner{area: filepath.Join(work, name+"-store")}
		dir := filepath.Join(work, name, "tree")
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return false, err
		}
		var t tree
		if name == "self" {
			t, err = r.selfTree(cfg.Repo, dir, cfg.Files)
		} else {
			t, err = r.syntheticTree(prev, dir, cfg.Seed, cfg.Lines, cfg.Files)
		}
		if err != nil {
			return false, fmt.Errorf("%s tree: %w", name, err)
		}
		start := time.Now()
		reps := compareTree(r.run, prev, cand, t)
		fmt.Fprintf(stdout, "replay: %s tree read in %s\n", name, time.Since(start).Round(time.Second))
		if dirty, err := r.untouched(dir); err != nil {
			return false, err
		} else if dirty != "" {
			reps = append(reps, legReport{Tree: name, Leg: "read-only", Failure: "a read-only command changed the tree: " + firstLines(dirty, 4)})
		}
		fmt.Fprint(stdout, render(reps))
		for _, rep := range reps {
			failed = failed || rep.failed()
		}
	}
	return failed, nil
}

// replayWorkDirs are the directories a replay creates under its work directory:
// the two binaries, the clone the previous release is built from, and a tree and
// a state store for each of the two trees it reads. A clone of this repository
// is gigabytes, and a CI step that named its own -work left them on the runner.
var replayWorkDirs = []string{"area", "bin", "previous-src", "self", "synthetic", "self-store", "synthetic-store"}

// workClaim is the paths a replay will create under its work directory, written
// down before it creates any. A path the directory already held is refused, so
// every path the claim names is the replay's own and nothing else is ever
// removed: not a fixed name that was there before, not anything else in a
// directory the caller named.
type workClaim struct {
	dir   string
	made  bool // the replay made the directory itself
	paths []string
}

func claimWork(dir string, made bool) (*workClaim, error) {
	c := &workClaim{dir: dir, made: made}
	for _, name := range replayWorkDirs {
		p := filepath.Join(dir, name)
		if _, err := os.Lstat(p); err == nil {
			return nil, fmt.Errorf("work directory %s already holds %s: give an empty -work, or none", dir, name)
		}
		c.paths = append(c.paths, p)
	}
	return c, nil
}

// settle ends the run's use of the work directory. A replay that passed removes
// the claimed paths, and the directory when it made it and nothing else is in
// it. One that failed, or was asked to -keep, leaves everything where it is and
// says where, because what failed is in there.
func (c *workClaim) settle(passed, keep bool, stdout io.Writer) {
	if !passed || keep {
		fmt.Fprintf(stdout, "replay: work directory kept: %s\n", c.dir)
		return
	}
	for _, p := range c.paths {
		_ = os.RemoveAll(p)
	}
	if c.made {
		_ = os.Remove(c.dir)
	}
}
