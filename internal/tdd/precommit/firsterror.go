package precommit

import (
	"regexp"
	"strings"
)

// goCompileErrorRe is a Go toolchain error line: `path/file.go:12:3: msg`,
// optionally behind vet's `vet: ` prefix.
var goCompileErrorRe = regexp.MustCompile(`^(vet: )?\S+\.go:\d+(:\d+)?: `)

// firstError is the first compile error in a build's output, with the
// location rustc prints under it, or "" when there is none (issue #791).
// Cargo builds crates in parallel and prints each one's diagnostics as it
// finishes, so the head of the output is routinely another crate's warnings
// and the tail another crate's too; the error that stopped the build can sit
// anywhere between.
func firstError(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if goCompileErrorRe.MatchString(trimmed) {
			return trimmed
		}
		if !strings.HasPrefix(trimmed, "error:") && !strings.HasPrefix(trimmed, "error[") {
			continue
		}
		if i+1 < len(lines) {
			if loc, ok := strings.CutPrefix(strings.TrimSpace(lines[i+1]), "--> "); ok {
				return trimmed + " (" + loc + ")"
			}
		}
		return trimmed
	}
	return ""
}
