package tdd

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// fileAdd is the set of added line numbers (1-based, in the post-image) for one
// file in a staged diff.
type fileAdd struct {
	path  string
	added map[int]bool
}

// hunkNewStart returns the new-file starting line of a `@@ -a,b +c,d @@` header,
// or 0 if it can't be parsed.
func hunkNewStart(header string) int {
	m := hunkNewStartRe.FindStringSubmatch(header)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// hunkNewStartRe captures the new-file start line from a `@@ -a,b +c,d @@`
// header — the digits right after the `+`, before any `,count`.
var hunkNewStartRe = regexp.MustCompile(`\+(\d+)`)

// splitKinds partitions repo-relative staged paths into test and source files,
// ignoring everything else.
func splitKinds(paths []string) (tests, srcs []string) {
	for _, p := range paths {
		switch ClassifyFile(p) {
		case Test:
			tests = append(tests, p)
		case Source:
			srcs = append(srcs, p)
		}
	}
	return tests, srcs
}

// stagedAdds parses `git diff --cached -U0` into the added line NUMBERS per
// file, keyed by the `+++ b/<path>` header and skipping deletions
// (`+++ /dev/null`). Line numbers come from each hunk's `@@ … +start[,count] @@`
// new-side range, advanced as `+` lines are consumed, so the caller can mask the
// full post-image and judge only these lines — far more robust than the old
// approach of masking the deletion-stripped added-line text on its own.
func stagedAdds(repoRoot string) []fileAdd {
	out, err := git(repoRoot, "diff", "--cached", "--unified=0", "--no-color")
	if err != nil {
		return nil
	}
	var (
		adds  []fileAdd
		cur   string
		lines map[int]bool
		newNo int // next new-file line number within the current hunk
	)
	flush := func() {
		if cur != "" && len(lines) > 0 {
			adds = append(adds, fileAdd{path: cur, added: lines})
		}
		lines = nil
	}
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			lines = map[int]bool{}
		case strings.HasPrefix(line, "+++"): // +++ /dev/null (deletion)
			flush()
			cur = ""
		case strings.HasPrefix(line, "@@"):
			newNo = hunkNewStart(line)
		case strings.HasPrefix(line, "+"):
			if lines != nil && newNo > 0 {
				lines[newNo] = true
				newNo++
			}
		case strings.HasPrefix(line, "-"):
			// deletions don't advance the new-file line counter
		default:
			// context line (none at -U0) advances the new-file counter
			if newNo > 0 {
				newNo++
			}
		}
	}
	flush()
	return adds
}

// stagedTestsAddDeclIn reports whether any of testFiles (a project root's OWN
// staged test files — repo-root-relative, matching stagedAdds' paths) has an
// added line carrying a test declaration. A test file in a language the
// table doesn't know errs toward true — fail-first then runs and, at worst,
// costs a suite, never a wrong verdict. Scoped to testFiles (not every staged
// test in the commit) so a multi-root commit's fail-first trigger for one
// root is never decided by a DIFFERENT root's test declarations.
func stagedTestsAddDeclIn(repoRoot string, testFiles []string) bool {
	if len(testFiles) == 0 {
		return false
	}
	want := make(map[string]bool, len(testFiles))
	for _, f := range testFiles {
		want[f] = true
	}
	for _, fa := range stagedAdds(repoRoot) {
		if !want[fa.path] || ClassifyFile(fa.path) != Test {
			continue
		}
		res, known := testDeclRes[strings.ToLower(filepath.Ext(fa.path))]
		if !known {
			return true
		}
		post, err := git(repoRoot, "show", ":"+fa.path)
		if err != nil {
			return true // can't read the post-image → judge conservatively
		}
		lines := strings.Split(post, "\n")
		for no := range fa.added {
			if no < 1 || no > len(lines) {
				continue
			}
			for _, re := range res {
				if re.MatchString(lines[no-1]) {
					return true
				}
			}
		}
	}
	return false
}
