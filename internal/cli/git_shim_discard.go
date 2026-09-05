package cli

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// discardCost is what a discarding invocation would destroy, measured before
// git runs. Every counter reads zero when the measurement itself fails (a
// git error is not evidence of anything to refuse) or when the form does not
// apply to that counter (e.g. a `stash drop` never touches Files).
type discardCost struct {
	Files, Insertions, Deletions int
	Untracked                    int
	Stashes                      int
	UnmergedCommits              int
	Worktree                     string
}

// zero reports whether every counter is zero -- nothing this invocation
// would destroy, so the wall must pass it silently.
func (c discardCost) zero() bool {
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

// restoreIntent: `--staged` alone touches only the index (reversible with
// another `restore --staged`), so it is not ok; `--staged --worktree`
// touches the working tree too and is.
func restoreIntent(args []string) (string, []string, bool) {
	staged := containsToken(args, "--staged")
	worktree := containsToken(args, "--worktree")
	if staged && !worktree {
		return "", nil, false
	}
	return "restore <paths>", nonFlagOperands(args), true
}

// cleanIntent parses git clean's combinable short flags (`-fd`, `-fdx`,
// `-ff`, ...) character by character: `f` (force) must be present and `n`
// (dry-run) must not, wherever in the cluster they appear.
func cleanIntent(args []string) (string, []string, bool) {
	var flags, paths []string
	hasForce, hasDryRun := false, false
	for _, a := range args {
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
		return "stash " + args[0], nonFlagOperands(args[1:]), true
	}
	return "", nil, false
}

// branchIntent: any spelling of a forced delete (`-D`, `--delete --force`,
// `-d -f`) normalizes to the one form the refusal prints, `branch -D <b>`;
// a plain `-d` (git refuses it itself when unmerged) is not ok.
func branchIntent(args []string) (string, []string, bool) {
	deleting, forced := false, false
	var name string
	for _, a := range args {
		switch a {
		case "-D":
			deleting, forced = true, true
		case "-d", "--delete":
			deleting = true
		case "-f", "--force":
			forced = true
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
		return worktreeCost(realGit, paths)
	case strings.HasPrefix(form, "clean "):
		return cleanCost(realGit, workDir, form, paths)
	case strings.HasPrefix(form, "stash "):
		return stashCost(realGit, workDir, paths)
	case strings.HasPrefix(form, "branch -D "):
		return branchCost(realGit, workDir, form)
	default:
		// reset --hard, reset --merge, checkout -f, checkout -- <paths>,
		// restore <paths>: all measured the same way, over the whole tree
		// for the first three and over the named paths for the last two.
		scope := paths
		if form == "reset --hard" || form == "reset --merge" || form == "checkout -f" {
			scope = nil
		}
		c := diffCost(realGit, workDir, scope)
		c.Untracked = untrackedCost(realGit, workDir, scope, false)
		return c
	}
}

func worktreeCost(realGit string, paths []string) discardCost {
	if len(paths) == 0 {
		return discardCost{}
	}
	target := paths[0]
	c := diffCost(realGit, target, nil)
	c.Untracked = untrackedCost(realGit, target, nil, false)
	c.Worktree = target
	return c
}

func cleanCost(realGit, workDir, form string, paths []string) discardCost {
	includeIgnored := strings.Contains(form, "x")
	return discardCost{Untracked: untrackedCost(realGit, workDir, paths, includeIgnored)}
}

func stashCost(realGit, workDir string, paths []string) discardCost {
	if len(paths) > 0 {
		if _, err := runGitCapture(realGit, workDir, "rev-parse", "--verify", "--quiet", paths[0]); err == nil {
			return discardCost{Stashes: 1}
		}
		return discardCost{}
	}
	out, err := runGitCapture(realGit, workDir, "stash", "list")
	if err != nil {
		return discardCost{}
	}
	return discardCost{Stashes: countLines(out)}
}

func branchCost(realGit, workDir, form string) discardCost {
	name := strings.TrimPrefix(form, "branch -D ")
	out, err := runGitCapture(realGit, workDir, "rev-list", "--count", "HEAD.."+name)
	if err != nil {
		return discardCost{}
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return discardCost{UnmergedCommits: n}
}

// diffCost runs `git diff --shortstat HEAD -- <paths>`, falling back to the
// same command without HEAD in a repo that has none yet (an unborn branch: a
// worktree nobody has committed into, which can still hold discardable
// staged/working changes).
func diffCost(realGit, workDir string, paths []string) discardCost {
	args := append([]string{"diff", "--shortstat", "HEAD"}, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		args = append([]string{"diff", "--shortstat"}, pathArgs(paths)...)
		out, err = runGitCapture(realGit, workDir, args...)
		if err != nil {
			return discardCost{}
		}
	}
	files, ins, del := parseShortstat(out)
	return discardCost{Files: files, Insertions: ins, Deletions: del}
}

// untrackedCost runs `git ls-files --others [--exclude-standard] -- <paths>`
// and counts the lines; includeIgnored drops --exclude-standard, per
// `clean`'s `-x`.
func untrackedCost(realGit, workDir string, paths []string, includeIgnored bool) int {
	args := []string{"ls-files", "--others"}
	if !includeIgnored {
		args = append(args, "--exclude-standard")
	}
	args = append(args, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		return 0
	}
	return countLines(out)
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
