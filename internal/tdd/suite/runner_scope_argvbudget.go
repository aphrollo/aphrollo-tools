package suite

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// Issue #951: narrowToStaged puts the changed set on the command line —
// `vitest related <every changed file>`, `jest --findRelatedTests <…>`, one
// `go test` package per changed directory and its importers — and on a large
// PR that line passes what Windows can start. CreateProcess refuses a line
// over 32 767 characters, and `npx` there is a .cmd shim that runs through
// cmd.exe, whose limit is 8 191.
//
// Past the budget the gate runs the runner's full suite instead. The full
// suite is a superset of every related selection, so the verdict loses no
// ground, only time, and only on the PRs large enough to cross it. The
// alternatives were weighed and refused: splitting the files across several
// runs re-runs every test two batches both reach, and merges verdicts the
// vacuous-run and empty-selection judges each read from one run's output;
// neither vitest nor jest reads its related files from a file or stdin.
//
// A check whose runs are already independent per file takes the other road:
// ESLint's runs are each compared with the same run at HEAD on their own, so
// its changed files split into batches (argvBatches) at no cost in ground.

// stagedArgvBudget is the longest command line narrowToStaged builds, in
// characters. cmd.exe's 8 191 less room for what is applied after it:
// nodeToolRunner replacing `npx` with the absolute node binary and the
// tool's entry script, CI flags a Go run gains, and the quotes Windows adds
// around a path with a space in it.
const stagedArgvBudget = 6000

// stagedArgvFallbacks records each fallback already announced by this
// process, so a gate that resolves the same root's runner at more than one
// stage says it once.
var stagedArgvFallbacks sync.Map

// narrowToStaged is narrowToStagedUnbounded held to stagedArgvBudget: a
// scoped runner whose command line would pass it comes back as r itself, the
// full suite, still reported as narrowed so a caller that owes a suite for
// the files runs one. The fallback is printed with the numbers that decided
// it.
func narrowToStaged(r Runner, root string, files []string) (Runner, bool) {
	scoped, ok := narrowToStagedUnbounded(r, root, files)
	line := strings.Join(append([]string{scoped.Cmd}, scoped.Args...), " ")
	if len(line) <= stagedArgvBudget {
		return scoped, ok
	}
	if _, said := stagedArgvFallbacks.LoadOrStore(root+"\x00"+line, true); !said {
		fmt.Fprintf(os.Stderr, "gate: %s — the related-tests command for %d changed files is %d chars, past the %d-char command-line budget; running the full suite (%s) instead\n",
			root, len(files), len(line), stagedArgvBudget, cmdString(r))
	}
	return r, true
}

// argvBatches splits files into argument lists that each start with prefix
// and keep their words, joined by single spaces, within budget characters.
// Files keep their order and each lands in exactly one batch; a file too long
// for any batch gets one of its own rather than being dropped. No files is no
// batch.
func argvBatches(prefix, files []string, budget int) [][]string {
	var out [][]string
	base := len(strings.Join(prefix, " "))
	size := base
	for _, f := range files {
		if len(out) == 0 || size+1+len(f) > budget {
			out = append(out, slices.Clone(prefix))
			size = base
		}
		out[len(out)-1] = append(out[len(out)-1], f)
		size += 1 + len(f)
	}
	return out
}
