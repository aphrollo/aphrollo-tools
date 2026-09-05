package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// discardCost is what a discarding invocation would destroy, measured before
// git runs. A counter reads zero when the form does not apply to it (e.g. a
// `stash drop` never touches Files). Err is fail-closed: a measurement that
// could not run at all (a broken git, an index.lock collision) sets Err
// rather than leaving every counter at zero, since a git that cannot measure
// also cannot be trusted to run the discard safely -- zero() reports false
// whenever Err is set, first error wins when more than one measurement
// fails.
type discardCost struct {
	Files, Insertions, Deletions int
	Untracked                    int
	Stashes                      int
	UnmergedCommits              int
	Worktree                     string
	Err                          error
}

// zero reports whether every counter is zero -- nothing this invocation
// would destroy, so the wall must pass it silently. A measurement failure is
// never zero: it is refused, not passed silently.
func (c discardCost) zero() bool {
	if c.Err != nil {
		return false
	}
	return c.Files == 0 && c.Insertions == 0 && c.Deletions == 0 &&
		c.Untracked == 0 && c.Stashes == 0 && c.UnmergedCommits == 0
}

// discardIntent classifies rest (the verb and its own args, global options
// already stripped by gitGlobalArgs) as one of the discarding forms the
// discard wall guards, or reports ok=false for everything else -- including
// forms the primary-checkout wall or the shim's own lock already own (a bare
// `git reset`, a branch-moving `checkout <branch>`).
func discardIntent(rest []string) (form string, paths []string, ok bool) {
	if len(rest) == 0 {
		return "", nil, false
	}
	switch rest[0] {
	case "reset":
		return resetIntent(rest[1:])
	case "checkout":
		return checkoutIntent(rest[1:])
	case "restore":
		return restoreIntent(rest[1:])
	case "clean":
		return cleanIntent(rest[1:])
	case "stash":
		return stashIntent(rest[1:])
	case "branch":
		return branchIntent(rest[1:])
	case "worktree":
		return worktreeIntent(rest[1:])
	}
	return "", nil, false
}

func resetIntent(args []string) (string, []string, bool) {
	if containsToken(args, "--hard") {
		return "reset --hard", nil, true
	}
	if containsToken(args, "--merge") {
		return "reset --merge", nil, true
	}
	return "", nil, false
}

// checkoutIntent: `--` followed by pathspecs, or a bare `.`, discards the
// named paths; `-f`/`--force` alone (no `--`, no `.`) discards the whole
// tree by moving the branch over it. A plain `checkout <branch>` is the
// primary wall's business, not this one's.
func checkoutIntent(args []string) (string, []string, bool) {
	for i, a := range args {
		if a == "--" {
			return "checkout -- <paths>", append([]string{}, args[i+1:]...), true
		}
	}
	if containsToken(args, ".") {
		return "checkout -- <paths>", []string{"."}, true
	}
	if containsToken(args, "-f") || containsToken(args, "--force") {
		return "checkout -f", nil, true
	}
	return "", nil, false
}

// restoreIntent: `--staged`/`-S` alone touches only the index (reversible
// with another `restore --staged`), so it is not ok; `--staged --worktree`
// (or the bundled `-SW`) touches the working tree too and is.
// `--source=<ref>`/`-s <ref>` never changes the classification either way:
// whichever tree restore writes into, it still overwrites it.
func restoreIntent(args []string) (string, []string, bool) {
	opts, ddPaths := splitDashDash(args)
	staged, worktree := false, false
	for _, a := range opts {
		switch {
		case a == "--staged":
			staged = true
		case a == "--worktree":
			worktree = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			for _, ch := range a[1:] {
				switch ch {
				case 'S':
					staged = true
				case 'W':
					worktree = true
				}
			}
		}
	}
	if staged && !worktree {
		return "", nil, false
	}
	return "restore <paths>", append(nonFlagOperands(opts), ddPaths...), true
}

// cleanIntent parses git clean's combinable short flags (`-fd`, `-fdx`,
// `-ff`, ...) character by character: `f` (force) must be present and `n`
// (dry-run) must not, wherever in the cluster they appear. Anything after a
// `--` is always a path, never a flag.
func cleanIntent(args []string) (string, []string, bool) {
	opts, ddPaths := splitDashDash(args)
	var flags, paths []string
	hasForce, hasDryRun := false, false
	for _, a := range opts {
		switch {
		case a == "--force":
			hasForce = true
			flags = append(flags, a)
		case a == "--dry-run":
			hasDryRun = true
			flags = append(flags, a)
		case strings.HasPrefix(a, "--"):
			flags = append(flags, a)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			flags = append(flags, a)
			for _, ch := range a[1:] {
				switch ch {
				case 'f':
					hasForce = true
				case 'n':
					hasDryRun = true
				}
			}
		default:
			paths = append(paths, a)
		}
	}
	paths = append(paths, ddPaths...)
	if !hasForce || hasDryRun {
		return "", nil, false
	}
	return "clean " + strings.Join(flags, " "), paths, true
}

// stashIntent: only `drop` and `clear` discard; `push` (reversible with
// `pop`/`apply`) is out of scope by the spec's own boundary.
func stashIntent(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	switch args[0] {
	case "drop", "clear":
		opts, ddPaths := splitDashDash(args[1:])
		return "stash " + args[0], append(nonFlagOperands(opts), ddPaths...), true
	}
	return "", nil, false
}

// branchIntent: any spelling of a forced delete (`-D`, `--delete --force`,
// `-d -f`, or git's own bundled short options `-fD`/`-Df`) normalizes to the
// one form the refusal prints, `branch -D <b>`; a plain `-d` (git refuses it
// itself when unmerged) is not ok.
func branchIntent(args []string) (string, []string, bool) {
	deleting, forced := false, false
	var name string
	for _, a := range args {
		switch {
		case a == "-D":
			deleting, forced = true, true
		case a == "-d" || a == "--delete":
			deleting = true
		case a == "-f" || a == "--force":
			forced = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			if d, f, ok := branchDeleteBundle(a[1:]); ok {
				deleting = deleting || d
				forced = forced || f
			}
		default:
			if !strings.HasPrefix(a, "-") {
				name = a
			}
		}
	}
	if !deleting || !forced || name == "" {
		return "", nil, false
	}
	return "branch -D " + name, nil, true
}

// branchDeleteBundle parses the characters of a bundled short-option token
// (the "fD" of "-fD"): each of 'd', 'D', 'f' contributes its usual meaning;
// any other character means the bundle is not one of ours (ok=false), so the
// token is left alone rather than misread.
func branchDeleteBundle(chars string) (deleting, forced, ok bool) {
	for _, ch := range chars {
		switch ch {
		case 'd':
			deleting = true
		case 'D':
			deleting, forced = true, true
		case 'f':
			forced = true
		default:
			return false, false, false
		}
	}
	return deleting, forced, true
}

// worktreeIntent: only `remove --force`/`-f` is a discard (an unforced
// remove already refuses itself on a dirty worktree); the path becomes
// paths[0] since the form itself, unlike branch's, names no target.
func worktreeIntent(args []string) (string, []string, bool) {
	if len(args) == 0 || args[0] != "remove" {
		return "", nil, false
	}
	forced := false
	var path string
	for _, a := range args[1:] {
		switch a {
		case "-f", "--force":
			forced = true
		default:
			if !strings.HasPrefix(a, "-") {
				path = a
			}
		}
	}
	if !forced || path == "" {
		return "", nil, false
	}
	return "worktree remove --force", []string{path}, true
}

// splitDashDash splits rest at the first "--" (git's own end-of-options
// marker) into opts (tokens before it, still flag candidates) and paths
// (tokens after it, always paths, never flags -- `-weird` after a `--` is a
// filename, not an option). rest with no "--" is entirely opts.
func splitDashDash(rest []string) (opts, paths []string) {
	for i, a := range rest {
		if a == "--" {
			return rest[:i], append([]string{}, rest[i+1:]...)
		}
	}
	return rest, nil
}

// nonFlagOperands returns every arg that does not start with "-", in order.
func nonFlagOperands(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// discardCostOf measures what a classified discarding invocation would
// destroy. Named discardCostOf rather than discardCost: Go forbids a
// function and a type sharing one identifier in the same package block, and
// the discardCost struct above already claims that name.
// paths and its meaning are exactly what discardIntent returned for that
// form: nil/whole-tree for reset and checkout -f, the named paths for
// checkout -- <paths>/restore <paths>/clean, the target worktree for
// worktree remove --force, and the branch name riding inside form itself for
// branch -D <b> (that form has no separate path to carry it).
func discardCostOf(realGit, workDir, form string, paths []string) discardCost {
	switch {
	case form == "worktree remove --force":
		return worktreeCost(realGit, workDir, paths)
	case strings.HasPrefix(form, "clean "):
		return cleanCost(realGit, workDir, form, paths)
	case strings.HasPrefix(form, "stash "):
		return stashCost(realGit, workDir, paths)
	case strings.HasPrefix(form, "branch -D "):
		return branchCost(realGit, workDir, form)
	default:
		// reset --hard and reset --merge overwrite the worktree AND the
		// index from HEAD, so they lose staged work too and are measured
		// against HEAD; checkout -f the same, over the whole tree.
		// checkout -- <paths> and restore <paths> only overwrite the
		// worktree from what is already staged, so staged-but-uncommitted
		// work is not part of what they would destroy -- measured against
		// the index instead.
		scope := paths
		if form == "reset --hard" || form == "reset --merge" || form == "checkout -f" {
			scope = nil
		}
		var c discardCost
		if form == "checkout -- <paths>" || form == "restore <paths>" {
			c = diffCostIndexOnly(realGit, workDir, scope)
		} else {
			c = diffCost(realGit, workDir, scope)
		}
		if c.Err != nil {
			return c
		}
		untracked, err := untrackedCost(realGit, workDir, scope, false)
		if err != nil {
			c.Err = err
			return c
		}
		c.Untracked = untracked
		return c
	}
}

// worktreeCost measures inside the target worktree named by paths[0],
// resolving it against workDir first when it is relative: a relative
// cmd.Dir resolves against the CALLING process's own cwd, not workDir, so a
// relative worktree path (git accepts one) has to be joined onto workDir
// before it becomes a usable cmd.Dir.
func worktreeCost(realGit, workDir string, paths []string) discardCost {
	if len(paths) == 0 {
		return discardCost{}
	}
	target := paths[0]
	if !filepath.IsAbs(target) {
		target = filepath.Join(workDir, target)
	}
	c := diffCost(realGit, target, nil)
	c.Worktree = target
	if c.Err != nil {
		return c
	}
	untracked, err := untrackedCost(realGit, target, nil, false)
	if err != nil {
		c.Err = err
		return c
	}
	c.Untracked = untracked
	return c
}

func cleanCost(realGit, workDir, form string, paths []string) discardCost {
	includeIgnored := strings.Contains(form, "x")
	untracked, err := untrackedCost(realGit, workDir, paths, includeIgnored)
	if err != nil {
		return discardCost{Err: err}
	}
	return discardCost{Untracked: untracked}
}

func stashCost(realGit, workDir string, paths []string) discardCost {
	if len(paths) > 0 {
		if _, err := runGitCapture(realGit, workDir, "rev-parse", "--verify", "--quiet", paths[0]); err == nil {
			return discardCost{Stashes: 1}
		}
		// rev-parse failing here means paths[0] is not a valid ref -- a
		// legitimate "nothing to drop by that name", not a measurement
		// failure, so this stays a real zero rather than Err.
		return discardCost{}
	}
	out, err := runGitCapture(realGit, workDir, "stash", "list")
	if err != nil {
		return discardCost{Err: err}
	}
	return discardCost{Stashes: countLines(out)}
}

func branchCost(realGit, workDir, form string) discardCost {
	name := strings.TrimPrefix(form, "branch -D ")
	out, err := runGitCapture(realGit, workDir, "rev-list", "--count", "HEAD.."+name)
	if err != nil {
		return discardCost{Err: err}
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return discardCost{UnmergedCommits: n}
}

// diffCost runs `git diff --shortstat HEAD -- <paths>`, falling back to the
// same command without HEAD in a repo that has none yet (an unborn branch: a
// worktree nobody has committed into, which can still hold discardable
// staged/working changes). Only the fallback's own failure is a measurement
// error -- the first attempt failing is expected on an unborn branch, not
// evidence git is broken.
func diffCost(realGit, workDir string, paths []string) discardCost {
	args := append([]string{"diff", "--shortstat", "HEAD"}, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		args = append([]string{"diff", "--shortstat"}, pathArgs(paths)...)
		out, err = runGitCapture(realGit, workDir, args...)
		if err != nil {
			return discardCost{Err: err}
		}
	}
	files, ins, del := parseShortstat(out)
	return discardCost{Files: files, Insertions: ins, Deletions: del}
}

// diffCostIndexOnly runs `git diff --shortstat -- <paths>`, comparing the
// worktree against the INDEX only (no HEAD): what `checkout -- <paths>` and
// `restore <paths>` actually overwrite, since both replace the worktree copy
// from the index and leave anything already staged untouched.
func diffCostIndexOnly(realGit, workDir string, paths []string) discardCost {
	args := append([]string{"diff", "--shortstat"}, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		return discardCost{Err: err}
	}
	files, ins, del := parseShortstat(out)
	return discardCost{Files: files, Insertions: ins, Deletions: del}
}

// untrackedCost runs `git ls-files --others [--exclude-standard] -- <paths>`
// and counts the lines; includeIgnored drops --exclude-standard, per
// `clean`'s `-x`.
func untrackedCost(realGit, workDir string, paths []string, includeIgnored bool) (int, error) {
	args := []string{"ls-files", "--others"}
	if !includeIgnored {
		args = append(args, "--exclude-standard")
	}
	args = append(args, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		return 0, err
	}
	return countLines(out), nil
}

// pathArgs renders paths as a trailing `-- <paths>` pathspec, or nothing for
// a whole-tree measurement.
func pathArgs(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return append([]string{"--"}, paths...)
}

// runGitCapture runs realGit in workDir and returns stdout, marked as
// already queued so it never contends with a lock this process itself may
// be holding.
func runGitCapture(realGit, workDir string, args ...string) (string, error) {
	cmd := exec.Command(realGit, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	out, err := cmd.Output()
	return string(out), err
}

var (
	shortstatFilesRe = regexp.MustCompile(`(\d+) files? changed`)
	shortstatInsRe   = regexp.MustCompile(`(\d+) insertions?\(\+\)`)
	shortstatDelRe   = regexp.MustCompile(`(\d+) deletions?\(-\)`)
)

// parseShortstat pulls the three numbers out of a `--shortstat` line such as
// " 3 files changed, 212 insertions(+), 40 deletions(-)"; any absent number
// (a diff with no insertions, say) reads as zero rather than an error.
func parseShortstat(s string) (files, insertions, deletions int) {
	if m := shortstatFilesRe.FindStringSubmatch(s); m != nil {
		files, _ = strconv.Atoi(m[1])
	}
	if m := shortstatInsRe.FindStringSubmatch(s); m != nil {
		insertions, _ = strconv.Atoi(m[1])
	}
	if m := shortstatDelRe.FindStringSubmatch(s); m != nil {
		deletions, _ = strconv.Atoi(m[1])
	}
	return files, insertions, deletions
}

// countLines counts non-empty trailing-newline-terminated lines in s -- git's
// own line-per-entry output for ls-files and stash list.
func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// discardRefusalTail is the one constant every refusal line ends with,
// naming both overrides.
const discardRefusalTail = "; aphrollo gate allow discard arms one command, APHROLLO_DISCARD=1 for scripts"

// discardRefusalLine renders the refusal for a classified, non-zero-cost
// discard. The shape depends on which counters that form actually measures:
// reset/checkout/restore show the file/diff numbers (never untracked --
// none of those forms touch untracked files); clean shows only the
// untracked count; stash shows the stash-entry count; branch shows the
// unmerged-commit count; worktree remove shows the file/diff numbers plus
// the worktree they were measured in.
func discardRefusalLine(form string, c discardCost) string {
	if c.Err != nil {
		return fmt.Sprintf("gate: refused — %s: could not measure what it would discard (%s); retry, or APHROLLO_DISCARD=1 to bypass", form, firstErrorLine(c.Err))
	}
	var body string
	switch {
	case form == "worktree remove --force":
		body = fmt.Sprintf("%s discards %d file(s), +%d/-%d uncommitted in %s", form, c.Files, c.Insertions, c.Deletions, c.Worktree)
	case strings.HasPrefix(form, "clean "):
		body = fmt.Sprintf("%s discards %d untracked file(s)", form, c.Untracked)
	case strings.HasPrefix(form, "stash "):
		body = fmt.Sprintf("%s discards %d stash entry(ies)", form, c.Stashes)
	case strings.HasPrefix(form, "branch -D "):
		body = fmt.Sprintf("%s discards %d unmerged commit(s)", form, c.UnmergedCommits)
	default:
		body = fmt.Sprintf("%s discards %d file(s), +%d/-%d uncommitted", form, c.Files, c.Insertions, c.Deletions)
	}
	return "gate: refused — " + body + discardRefusalTail
}

// firstErrorLine returns the first line of err's message: a broken git's
// stderr (folded into the exec error on some platforms) can run to several
// lines, and the refusal names the failure, not a paragraph of it.
func firstErrorLine(err error) string {
	line := err.Error()
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}
