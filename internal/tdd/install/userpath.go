package install

import (
	"fmt"
	"strings"
)

// winPathKey normalizes a Windows directory for comparison on any host:
// quotes trimmed, forward slashes turned to backslashes, trailing separators
// dropped, case folded. samePath cannot serve here because it follows the
// host OS, and the user PATH is a Windows value wherever it is parsed.
func winPathKey(p string) string {
	p = strings.Trim(strings.TrimSpace(p), `"`)
	p = strings.ReplaceAll(p, "/", `\`)
	p = strings.TrimRight(p, `\`)
	return strings.ToLower(p)
}

// ConvergeUserPath rewrites the raw user PATH value (";"-joined, variables
// unexpanded) so every dir in required appears exactly once and ahead of
// everything else, in the order given, and every other directory appears
// once, in its original order. A required dir already present keeps the
// spelling it had (so %USERPROFILE%\bin is not rewritten to a literal path on
// each run). expand resolves %VAR% only for comparison. changed=false means
// the value is already converged and must not be written back.
func ConvergeUserPath(raw string, required []string, expand func(string) string) (string, bool) {
	entries := strings.Split(raw, ";")
	spelling := make([]string, len(required))
	for i, r := range required {
		spelling[i] = r
		for _, e := range entries {
			if e != "" && winPathKey(expand(e)) == winPathKey(r) {
				spelling[i] = e
				break
			}
		}
	}
	out := append([]string(nil), spelling...)
	seen := map[string]bool{}
	for _, r := range required {
		seen[winPathKey(r)] = true
	}
	for _, e := range entries {
		k := winPathKey(expand(e))
		if e == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	got := strings.Join(out, ";")
	return got, got != raw
}

// isGitDir reports whether dir is one of the directories Git for Windows
// puts a git.exe in.
func isGitDir(dir string) bool {
	k := winPathKey(dir)
	for _, suffix := range []string{`\git\cmd`, `\git\bin`, `\git\mingw64\bin`, `\git\usr\bin`} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// AuditUserPath lists what is wrong with the (expanded) user PATH entries
// against the dirs install manages: shimDir and binDir each present exactly
// once, and shimDir ahead of every Git directory. nil means healthy.
func AuditUserPath(entries []string, shimDir, binDir string) []string {
	var problems []string
	for _, dir := range []string{shimDir, binDir} {
		n := 0
		for _, e := range entries {
			if winPathKey(e) == winPathKey(dir) {
				n++
			}
		}
		switch {
		case n == 0:
			problems = append(problems, dir+" is missing from the user PATH")
		case n > 1:
			problems = append(problems, fmt.Sprintf("%s appears %d times in the user PATH", dir, n))
		}
	}
	gitDir := ""
	for _, e := range entries {
		if winPathKey(e) == winPathKey(shimDir) {
			if gitDir != "" {
				problems = append(problems, fmt.Sprintf("%s comes after %s, so git resolves to Git's own binary", shimDir, gitDir))
			}
			break
		}
		if gitDir == "" && isGitDir(e) {
			gitDir = e
		}
	}
	return problems
}

// winDir is the directory part of a Windows file path, split on either
// separator so it gives the same answer whatever OS parses it.
func winDir(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[:i]
	}
	return "."
}
