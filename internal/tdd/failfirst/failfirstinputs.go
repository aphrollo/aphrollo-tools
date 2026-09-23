package failfirst

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The fail-first proof rebuilds HEAD in a throwaway worktree and applies the
// staged TESTS onto it: the tests are present, the new implementation is not,
// and a suite that passes there never went red. That is right for code and
// wrong for DATA. A staged //go:embed template, a .ron table, a golden file
// under tests/ is an INPUT the tests read, and withholding it makes the new
// tests fail because the data is stale -- a red the gate then records as
// proof, when it proves nothing about the code at all.
//
// So the proof carries the staged data beside the staged tests, narrowed to
// the data a staged test actually names. The narrowing is what keeps the
// stage honest in the other direction: a data file nothing names may BE the
// change under test, and applying that would turn a correct commit into a
// fail-first violation.

// proofInputs is the staged data the fail-first worktree must carry beside
// tests. Paths are repo-relative, in staged order.
func proofInputs(repoRoot string, tests []string) []string {
	var candidates []string
	for _, p := range stagedFiles(repoRoot) {
		if ClassifyFile(p) == Test || isProofWithheldCode(repoRoot, p) {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return nil
	}
	named := stagedTestText(repoRoot, tests)
	var out []string
	for _, p := range candidates {
		if embeddedByGo(repoRoot, p) || namesPath(named, p) {
			out = append(out, p)
		}
	}
	return out
}

// isProofWithheldCode reports whether p is the kind of file the proof exists
// to withhold: source in a language the gates compile or run.
//
// .ron is judged by whether THIS commit wrote it. A RON table is data to a
// reader and the implementation to this gate: when the change IS the table,
// carrying it into the proof tree makes the new test pass there and rejects a
// correct commit as a fail-first violation. A table the commit only MOVED is
// the other case -- unchanged data the test reads, whose absence would fail
// the test for a stale path rather than for missing code.
//
// A //go:embed file is judged the same way: compiled into the binary, a
// template the commit wrote is the implementation its test proves, and one it
// only moved is unchanged data.
func isProofWithheldCode(repoRoot, p string) bool {
	ext := strings.ToLower(path.Ext(filepath.ToSlash(p)))
	if isCIWorkflow(p) || embeddedByGo(repoRoot, p) {
		return stagedContentChanged(repoRoot, p)
	}
	if !sourceExts[ext] {
		return false
	}
	if ext == ".ron" {
		return stagedContentChanged(repoRoot, p)
	}
	return true
}

// isCIWorkflow reports whether p is a GitHub Actions workflow. A workflow is
// never test data: a test that reads one pins the CI configuration it states,
// so the workflow is the implementation that test proves, judged like a .ron
// table -- withheld when this commit wrote it, carried when it only moved.
func isCIWorkflow(p string) bool {
	seg := strings.SplitN(filepath.ToSlash(p), "/", 3)
	return len(seg) == 3 && seg[0] == ".github" && seg[1] == "workflows"
}

// stagedContentChanged reports whether the staged version of p differs in
// CONTENT from HEAD. `--raw -M` answers it exactly: it prints the source and
// destination blob ids per staged path, and a pure rename carries the SAME id
// on both sides. A path this cannot find a record for is changed, which is
// the safe direction -- withholding costs a red the commit gate can explain,
// carrying costs a correct commit.
func stagedContentChanged(repoRoot, p string) bool {
	out, err := git(repoRoot, "diff", "--cached", "--raw", "-M", "HEAD")
	if err != nil {
		return true
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 {
			continue
		}
		if filepath.ToSlash(fields[len(fields)-1]) != filepath.ToSlash(p) {
			continue
		}
		meta := strings.Fields(fields[0])
		if len(meta) < 4 {
			return true
		}
		// ":<oldmode> <newmode> <oldsha> <newsha> <status>"
		return meta[2] != meta[3]
	}
	return true
}

// stagedTestText concatenates the staged test files as they stand in the
// worktree, which is the text the proof is about to run.
func stagedTestText(repoRoot string, tests []string) string {
	var b strings.Builder
	for _, t := range tests {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(t)))
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// namesPath reports whether the tests mention p, by repo-relative path or by
// file name. A test names its fixture one way or the other; anything cleverer
// would need to run the code to find out.
//
// The name has to stand WHOLE: `items.ron` inside `myitems.ron`, inside
// `items.ron.bak`, or a path inside a longer path is a different file, and
// carrying a file the tests never read is how the change under test itself
// rides into the proof tree. A base with no extension is not evidence at all
// -- `data` or `README` matches ordinary prose -- so only the full path
// counts for one.
func namesPath(text, p string) bool {
	if text == "" {
		return false
	}
	slash := filepath.ToSlash(p)
	if mentionsWhole(text, slash) {
		return true
	}
	base := path.Base(slash)
	return path.Ext(base) != "" && mentionsWhole(text, base)
}

// mentionsWhole reports whether needle appears in text bounded on both sides:
// the neighbouring character may not continue a path or a file name.
func mentionsWhole(text, needle string) bool {
	if needle == "" {
		return false
	}
	for at := 0; ; {
		i := strings.Index(text[at:], needle)
		if i < 0 {
			return false
		}
		i += at
		end := i + len(needle)
		if !continuesName(text, i-1) && !continuesName(text, end) {
			return true
		}
		at = i + 1
	}
}

// continuesName reports whether the byte at i keeps a path or file name going.
// A quote, a space, a bracket or a comma ends one; a letter, digit, dot, dash,
// underscore or separator does not.
func continuesName(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	c := text[i]
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("._-/\\", c) >= 0
}

// embeddedByGo reports whether a Go package compiles p in with //go:embed.
// The Go build reads it, so every test in that package reads it too, without
// ever naming the file.
func embeddedByGo(repoRoot, p string) bool {
	return goEmbedsFile(filepath.Join(repoRoot, filepath.FromSlash(p)))
}
