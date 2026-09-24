package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const probeUsage = `usage: aphrollo gate probe discard [--apply] <file>...

discard restores exactly the named files to HEAD: the sanctioned route for
stripping a refused probe arm. Dry run by default: it prints what each file
would lose and the backup path it would use. --apply writes the full diff
(untracked files included) to that backup first, then restores tracked files
and removes untracked ones. Refused: a file with staged content, a path
outside the repo, a directory, a glob.
`

// runGateProbe is `aphrollo gate probe <verb>`; discard is its one verb.
func runGateProbe(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "discard" {
		fmt.Fprint(stderr, probeUsage)
		return 2
	}
	realGit, err := resolveRealGit()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate probe discard: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate probe discard: %v\n", err)
		return 1
	}
	return probeDiscard(realGit, cwd, args[1:], stdout, stderr)
}

// probeFile is one named file and what discarding it would throw away.
type probeFile struct {
	rel       string // repo-relative, slash-separated
	changed   bool   // tracked, and the working copy differs from HEAD
	untracked bool
	added     string // numstat's counts, "-" for a binary file
	removed   string
	size      int64 // an untracked file's size in bytes
}

// summary is the per-file line both the report and the gate log carry.
func (f probeFile) summary() string {
	switch {
	case f.changed:
		return fmt.Sprintf("%s  +%s/-%s lines vs HEAD", f.rel, f.added, f.removed)
	case f.untracked:
		return fmt.Sprintf("%s  untracked, %d bytes", f.rel, f.size)
	}
	return f.rel + "  no change vs HEAD"
}

func (f probeFile) logToken() string {
	if f.untracked {
		return fmt.Sprintf("%s:untracked:%dB", f.rel, f.size)
	}
	return fmt.Sprintf("%s:+%s/-%s", f.rel, f.added, f.removed)
}

// probeDiscard is the testable core. It runs realGit directly — the binary
// the git shim itself resolves — so the shim's discard wall never judges
// this sanctioned call, and no environment switch opens that door for any
// other caller.
func probeDiscard(realGit, cwd string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("probe discard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "back the diff up, then discard (default: print the plan and stop)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprint(stderr, probeUsage)
		return 2
	}
	top, err := runGitCapture(realGit, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintf(stderr, "gate: refused — probe discard: %s is not inside a git repo\n", cwd)
		return 1
	}
	root := filepath.Clean(filepath.FromSlash(strings.TrimSpace(top)))
	rels, refusals := probeRelPaths(root, cwd, fs.Args())
	if len(refusals) > 0 {
		for _, r := range refusals {
			fmt.Fprintln(stderr, "gate: refused — probe discard: "+r)
		}
		return 1
	}
	if staged, err := probeGit(realGit, root, "diff", "--cached", "--name-only", "-z", "HEAD", "--"); err != nil {
		return probeFail(stderr, err)
	} else if hit := intersect(strings.Split(staged, "\x00"), rels); len(hit) > 0 {
		fmt.Fprintf(stderr, "gate: refused — probe discard: %s has staged content, and staged work is never thrown away; commit or unstage it first\n",
			strings.Join(hit, ", "))
		return 1
	}
	files, err := probeClassify(realGit, root, rels)
	if err != nil {
		return probeFail(stderr, err)
	}
	backup, err := probeBackupPath(root, time.Now())
	if err != nil {
		return probeFail(stderr, err)
	}
	return probeReport(realGit, root, backup, files, *apply, stdout, stderr)
}

// probeReport prints the plan and, under --apply, carries it out.
func probeReport(realGit, root, backup string, files []probeFile, apply bool, stdout, stderr io.Writer) int {
	mode := " (dry run)"
	if apply {
		mode = ""
	}
	fmt.Fprintf(stdout, "probe discard%s in %s:\n", mode, root)
	var doomed []probeFile
	for _, f := range files {
		fmt.Fprintln(stdout, "  "+f.summary())
		if f.changed || f.untracked {
			doomed = append(doomed, f)
		}
	}
	if len(doomed) == 0 {
		fmt.Fprintln(stdout, "nothing to discard")
		return 0
	}
	if !apply {
		fmt.Fprintf(stdout, "backup: %s (dry run: not written)\n", backup)
		fmt.Fprintln(stdout, "re-run with --apply to back the diff up and discard")
		return 0
	}
	if err := probeWriteBackup(realGit, root, backup, doomed); err != nil {
		fmt.Fprintf(stderr, "gate: refused — probe discard: could not write the backup, nothing discarded: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "backup: %s\n", backup)
	if err := probeRestore(realGit, root, doomed); err != nil {
		fmt.Fprintf(stderr, "probe discard: %v; the full diff is in %s\n", err, backup)
		return 1
	}
	var tokens []string
	for _, f := range doomed {
		tokens = append(tokens, f.logToken())
	}
	tdd.AppendGateLog("probe-discard", root, "probe-discard",
		"discarded:"+strings.Join(tokens, ",")+";backup="+backup, 0)
	fmt.Fprintf(stdout, "discarded %d file(s); undo with: git -C %s apply %s\n", len(doomed), root, backup)
	return 0
}

// probeRelPaths turns each argument into a repo-relative path, or a refusal
// naming it: a glob, a path outside the repo, a directory. Exact files only,
// so what is discarded is exactly what was named.
func probeRelPaths(root, cwd string, args []string) (rels, refusals []string) {
	realRoot := evalExisting(root)
	for _, arg := range args {
		if strings.ContainsAny(arg, "*?[") {
			refusals = append(refusals, arg+" is a glob; name exact files")
			continue
		}
		abs := arg
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		// The parent is resolved, never the file: a tracked symlink is the
		// file being discarded, not whatever it points at.
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(realRoot, filepath.Join(evalExisting(filepath.Dir(abs)), filepath.Base(abs)))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			refusals = append(refusals, arg+" is outside the repo at "+root)
			continue
		}
		if info, err := os.Lstat(abs); err == nil && info.IsDir() {
			refusals = append(refusals, arg+" is a directory; name exact files")
			continue
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	return rels, refusals
}

// evalExisting resolves symlinks in the longest existing prefix of path, so
// a file that does not exist (a tracked file the arm deleted) still compares
// against the repo root on the same resolved footing.
func evalExisting(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	// Dir is a fixpoint only at the volume root: nothing left to climb.
	if len(parent) >= len(path) {
		return path
	}
	return filepath.Join(evalExisting(parent), filepath.Base(path))
}

// probeClassify measures each file against HEAD. Staged content is refused
// before this runs, so the index equals HEAD for every one of them.
func probeClassify(realGit, root string, rels []string) ([]probeFile, error) {
	var files []probeFile
	for _, rel := range rels {
		f := probeFile{rel: rel}
		numstat, err := probeGit(realGit, root, "diff", "--numstat", "HEAD", "--", rel)
		if err != nil {
			return nil, err
		}
		// numstat's line is "<added>\t<removed>\t<path>"; empty when the
		// file matches HEAD.
		if added, rest, ok := strings.Cut(strings.TrimSpace(numstat), "\t"); ok {
			removed, _, _ := strings.Cut(rest, "\t")
			f.changed, f.added, f.removed = true, added, removed
		} else {
			others, err := probeGit(realGit, root, "ls-files", "--others", "--exclude-standard", "--", rel)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(others) != "" {
				info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil {
					return nil, err
				}
				f.untracked, f.size = true, info.Size()
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// probeRestore puts tracked files back to HEAD and removes untracked ones.
// The untracked removals come after git has run, so a git failure leaves
// every untracked file in place.
func probeRestore(realGit, root string, doomed []probeFile) error {
	var tracked []string
	for _, f := range doomed {
		if f.changed {
			tracked = append(tracked, f.rel)
		}
	}
	if len(tracked) > 0 {
		args := append([]string{"restore", "--source=HEAD", "--worktree", "--"}, tracked...)
		if _, err := probeGit(realGit, root, args...); err != nil {
			return fmt.Errorf("git restore failed: %w", err)
		}
	}
	for _, f := range doomed {
		if f.untracked {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(f.rel))); err != nil {
				return err
			}
		}
	}
	return nil
}

// probeGit runs realGit in root with literal pathspecs: a named file is that
// file, never a pattern git expands.
func probeGit(realGit, root string, args ...string) (string, error) {
	return runGitCapture(realGit, root, append([]string{"--literal-pathspecs"}, args...)...)
}

func probeFail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "probe discard: %s; nothing discarded\n", firstErrorLine(err))
	return 1
}

// intersect returns the members of names that appear in want, in names'
// order.
func intersect(names, want []string) []string {
	set := map[string]bool{}
	for _, w := range want {
		set[w] = true
	}
	var out []string
	for _, n := range names {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}
