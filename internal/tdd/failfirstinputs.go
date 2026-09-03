package tdd

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
		if ClassifyFile(p) == Test || isProofWithheldCode(p) {
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
// .ron is deliberately excluded. ClassifyFile calls it Source so that a cargo
// run is scoped to its owning package, not because it is an implementation:
// a RON file is a table a test reads, and it belongs on the input side here.
func isProofWithheldCode(p string) bool {
	ext := strings.ToLower(path.Ext(filepath.ToSlash(p)))
	return ext != ".ron" && sourceExts[ext]
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
func namesPath(text, p string) bool {
	if text == "" {
		return false
	}
	slash := filepath.ToSlash(p)
	return strings.Contains(text, slash) || strings.Contains(text, path.Base(slash))
}

// embeddedByGo reports whether a Go package compiles p in with //go:embed.
// The Go build reads it, so every test in that package reads it too, without
// ever naming the file.
func embeddedByGo(repoRoot, p string) bool {
	return goEmbedsFile(filepath.Join(repoRoot, filepath.FromSlash(p)))
}
