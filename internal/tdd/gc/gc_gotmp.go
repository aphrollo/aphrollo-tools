package gc

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Category (l) (gc.go's header): a stale entry directly under the gate's own
// go-scratch directory, GoTmpRootDir (gotmpdir.go) -- what suiteEnv points
// GOTMPDIR, TMPDIR, TMP and TEMP at for every `go` runner. Every t.TempDir(),
// every os.MkdirTemp, and every compiled test binary the go tool stages
// before running it lands there, one top-level directory per suite run.
// Cleanup runs as part of the test that created it, so a run a timeout, a
// panic, or the build-slot reaper killed never gets to run its own -- and
// the directory stays. Measured on one box: 4.6 GB across 1,331 entries, 803
// of them untouched for over three days, with `aphrollo gate gc` reporting
// "nothing reclaimable" against all of it -- none of the other categories
// look here.
//
// gcGoTmpMutantsDir is excluded by name for the same reason gcProtectedNames
// (above) protects deps/, build/ and .fingerprint/: it is not test litter at
// all but the mutation runner's OWN working area, sharing this root only
// because mutantsCopyDirs (gc_mutantsdirs.go) resolves it beside the same
// go-scratch space. gcMutantsRunDirs / gcMutantsTrees already own its
// lifecycle; proposing it here too would race a live run.
const gcGoTmpMutantsDir = ".mutants"

// gcGoTmpLitter proposes every entry directly under repo's resolved gotmp
// root -- never gotmp itself, and never anything nested deeper than one
// level -- whose newest file is older than olderThan.
//
// DefaultGCAge (3 days) is safe here in a way it is not automatically safe
// everywhere it is reused: a temp dir a running `go test` is still writing
// into is minutes old, never days, because goTmpEnv (gotmpdir.go) hands each
// suite run a fresh directory of its own rather than reusing one, so age
// alone already tells a finished run from a live one.
//
// repo resolving to no gotmp root at all -- outside any git checkout, or the
// directory simply not created yet -- scans clean rather than erroring:
// GoTmpRootDir already returns "" for exactly that case (see
// gotmpdir_outside_test.go, issue #532), and os.ReadDir failing on a
// directory that does not exist is not a leak, it is one that was never
// made.
func gcGoTmpLitter(repo string, olderThan time.Duration, now time.Time) []GCCandidate {
	root := GoTmpRootDir(repo)
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		if !e.IsDir() || e.Name() == gcGoTmpMutantsDir {
			continue
		}
		path := filepath.Join(root, e.Name())
		newest, size := dirNewestAndSize(path)
		idle := now.Sub(newest)
		if newest.IsZero() || idle < olderThan {
			continue
		}
		out = append(out, GCCandidate{
			Path:   path,
			Size:   size,
			Reason: "go test scratch dir, idle " + formatDays(idle),
			Kind:   GCKindGoTmp,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
