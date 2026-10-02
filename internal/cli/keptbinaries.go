package cli

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rollback"
)

// The binaries update has replaced stay beside the installed one as
// `aphrollo.stale-<n>.exe`, where n is the time they were put aside. The last
// KeepBinaries of them, the installed one counted, stay for rollback; older
// ones are reclaimed, unless something is still running them.

// keptStale is how many stale copies stay beside the installed binary.
const keptStale = rollback.KeepBinaries - 1

// copyInUseFn answers whether a stale copy is still in use. A var so a test can
// state either answer: a running binary cannot be produced on demand on every
// host.
var copyInUseFn = copyInUse

// copyInUse reports whether path is held by a process, or cannot be told apart
// from one. A binary that is executing cannot be opened for writing: Windows
// refuses with a sharing violation, Linux and macOS with ETXTBSY. A copy that
// will not open for writing for any other reason is also kept: deleting a
// binary that may be running is never a guess worth making.
func copyInUse(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return true
	}
	_ = f.Close()
	return false
}

// staleNumber reads the time out of a stale copy's name. ok is false for a file
// that is not a stale copy of binName; a stale copy whose name carries no number
// counts as older than any that does.
func staleNumber(name, binName string) (n int64, ok bool) {
	ext := filepath.Ext(binName)
	rest, ok := strings.CutPrefix(name, strings.TrimSuffix(binName, ext)+stalePrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(rest, ext), 10, 64)
	if err != nil {
		return -1, true
	}
	return n, true
}

// staleCopyNames lists the stale copies of binName in dir, newest first.
func staleCopyNames(dir, binName string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if _, ok := staleNumber(e.Name(), binName); ok {
			names = append(names, e.Name())
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		na, _ := staleNumber(a, binName)
		nb, _ := staleNumber(b, binName)
		return cmp.Or(cmp.Compare(nb, na), cmp.Compare(b, a))
	})
	return names
}

// nextStalePath names the copy bin is about to become. Its number is the time
// now, and always later than every copy already there: two swaps in one second,
// or a clock that stepped back, still leave the newest copy sorting newest.
func nextStalePath(bin string, now time.Time) string {
	n := now.Unix()
	for _, name := range staleCopyNames(filepath.Dir(bin), filepath.Base(bin)) {
		if have, _ := staleNumber(name, filepath.Base(bin)); have >= n {
			n = have + 1
		}
	}
	return siblingPath(bin, fmt.Sprintf("%s%d", stalePrefix, n))
}

// pruneKeptBinaries reclaims the stale copies of binName in dir beyond the newest
// keptStale, and reports how many it removed and how many it left because
// something holds them. A copy that will not go is not an error: failing an
// upgrade that has landed over a file the OS is holding would make the
// operation impossible to perform from the binary being upgraded.
func pruneKeptBinaries(dir, binName string) (removed, held int) {
	stale := staleCopyNames(dir, binName)
	for _, name := range stale[min(keptStale, len(stale)):] {
		path := filepath.Join(dir, name)
		if copyInUseFn(path) || os.Remove(path) != nil {
			held++
			continue
		}
		removed++
	}
	return removed, held
}
