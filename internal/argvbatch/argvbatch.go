// Package argvbatch holds a command line built from a changed-path list
// within what Windows can start. CreateProcess refuses a line over 32 767
// characters, and a .cmd shim runs through cmd.exe, whose limit is 8 191; a
// command that puts every changed path on its line crosses either on a
// large enough diff.
//
// A call whose runs are independent per path splits the list into batches
// (Split) and runs once per batch (Run). A call whose runs are not — the
// gate's scoped test runners, whose verdict judges read one run's output —
// holds its line to Budget some other way.
package argvbatch

import (
	"slices"
	"strings"
)

// Budget is the longest command line a caller builds, in characters:
// cmd.exe's 8 191 less room for what is applied after it — an absolute
// binary and entry script in place of a bare name, CI flags a Go run gains,
// and the quotes Windows adds around a path with a space in it.
const Budget = 6000

// Split splits files into argument lists that each start with prefix and
// keep their words, joined by single spaces, within budget characters.
// Files keep their order and each lands in exactly one batch; a file too
// long for any batch gets one of its own rather than being dropped. No files
// is no batch.
func Split(prefix, files []string, budget int) [][]string {
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

// Run calls run once per batch of paths within Budget, each batch's
// arguments being prefix followed by its paths, and returns the outputs
// joined in order. No paths runs prefix alone once, the call a caller made
// before it batched. The first failing call ends the run: its own output
// and error come back, never a partial join.
func Run(prefix, paths []string, run func(args []string) (string, error)) (string, error) {
	batches := Split(prefix, paths, Budget)
	if len(batches) == 0 {
		batches = [][]string{prefix}
	}
	var out strings.Builder
	for _, args := range batches {
		o, err := run(args)
		if err != nil {
			return o, err
		}
		out.WriteString(o)
	}
	return out.String(), nil
}
