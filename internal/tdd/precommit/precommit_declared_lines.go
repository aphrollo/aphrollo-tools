package precommit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// A command declared with baseline = "lines" fails only over output lines
// HEAD's run did not print. Run on a tree whose inputs, lockfiles and tool
// equal HEAD's, it prints what HEAD's run prints: no new finding is possible,
// so the verdict is green without running it. No record is kept: the two trees
// are compared as they are, the base from git's objects, with no checkout.
//
// Any doubt runs the command: no inputs, a glob that matches nothing on either
// side or selects an ignored file, a tool that cannot be found, a base that
// cannot be read.

const (
	missBaseUnreadable = "the base could not be compared (%v)"
	missBaseNoMatch    = "a glob matches no file at the base (%s)"
	missBaseDiffers    = "inputs differ from the base (%s)"
)

// linesBaseReuse answers whether a baselined command may be skipped: its tree
// equals the base's. It says so, or at the merge gate why not.
func linesBaseReuse(gateName, root string, r Runner, c declaredCommand) bool {
	b := linesBaseCompare(root, c)
	switch {
	case b.equal:
		fmt.Fprintf(stderrFor(root), "[reuse] %s: inputs equal the base's — no new findings possible (%s)\n", cmdString(r), b.short)
	case gateName == premergeDisplayName:
		fmt.Fprintf(stderrFor(root), "[run] %s: no reuse — %s\n", cmdString(r), b.why)
	}
	return b.equal
}

// linesBase is how the judged tree compares with the base.
type linesBase struct {
	// equal is true when the command's inputs, lockfiles and tool are those of
	// the base; short names them.
	equal bool
	short string
	// why says, when equal is false, why the command runs.
	why string
}

// linesBaseCompare compares c's key components in root with those of HEAD.
func linesBaseCompare(root string, c declaredCommand) linesBase {
	doubt := func(why string) linesBase { return linesBase{why: why} }
	globs, err := normalizeInputGlobs(c.Inputs)
	if err != nil {
		return doubt(keyErrReason(err))
	}
	if _, err := toolStamp(root, c.Argv[0]); err != nil {
		return doubt(keyErrReason(&toolError{program: c.Argv[0], err: err}))
	}
	// The judged tree: every glob selects a file, none an ignored one.
	if _, _, err := inputsParts(root, c.Inputs); err != nil {
		return doubt(keyErrReason(err))
	}
	// The base tree: a listing with each blob's id, and every glob selects one.
	listing, err := git(root, "-c", "core.quotepath=off", "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return doubt(fmt.Sprintf(missBaseUnreadable, strings.TrimSpace(listing+" "+err.Error())))
	}
	base := map[string]string{}
	for _, entry := range strings.Split(listing, "\x00") {
		meta, path, found := strings.Cut(entry, "\t")
		if !found {
			continue
		}
		if f := strings.Fields(meta); len(f) == 3 {
			base[path] = f[2]
		}
	}
	basePaths := sortedKeys(base)
	for _, g := range globs {
		if !slices.ContainsFunc(basePaths, func(p string) bool { return ratchet.MatchGlob(g, p) }) {
			return doubt(fmt.Sprintf(missBaseNoMatch, g))
		}
	}
	// What differs between HEAD and the tree the command would read: tracked
	// files changed against HEAD, and files git does not track yet.
	changed, err := linesChanged(root)
	if err != nil {
		return doubt(fmt.Sprintf(missBaseUnreadable, err))
	}
	tool := ""
	if strings.ContainsAny(c.Argv[0], `/\`) && !filepath.IsAbs(c.Argv[0]) {
		tool = filepath.ToSlash(filepath.Clean(c.Argv[0]))
	}
	var parts []string
	hit := func(name string, match func(string) bool) {
		if slices.ContainsFunc(changed, match) {
			parts = append(parts, name)
		}
	}
	hit("inputs", func(p string) bool {
		return slices.ContainsFunc(globs, func(g string) bool { return ratchet.MatchGlob(g, p) })
	})
	hit("lockfiles", func(p string) bool { return slices.Contains(lockfiles, p) })
	if tool != "" {
		hit("tool", func(p string) bool { return p == tool })
	}
	if len(parts) > 0 {
		return doubt(fmt.Sprintf(missBaseDiffers, strings.Join(parts, ", ")))
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", strings.Join(globs, "\x00"))
	for _, p := range basePaths {
		if slices.ContainsFunc(globs, func(g string) bool { return ratchet.MatchGlob(g, p) }) || slices.Contains(lockfiles, p) {
			fmt.Fprintf(h, "%s\x00%s\n", p, base[p])
		}
	}
	return linesBase{equal: true, short: hex.EncodeToString(h.Sum(nil))[:8]}
}

// linesChanged is the files under root, as slash paths from it, that differ
// from HEAD: staged or unstaged changes (a rename counts as the old path and
// the new one), and files git does not track and does not ignore.
func linesChanged(root string) ([]string, error) {
	tracked, err := git(root, "-c", "core.quotepath=off", "diff", "--no-renames", "--name-only", "-z", "--relative", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range append(strings.Split(tracked, "\x00"), strings.Split(untracked, "\x00")...) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
