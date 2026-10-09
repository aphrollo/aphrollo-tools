package precommit

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// duplicationSignatures are the phrases a test run prints when one module was
// loaded from two places: an install that mixes trees, not code that is wrong.
// Data: a new signature is a row here. Matched case-insensitively; the first
// listed that the output holds is the one named.
var duplicationSignatures = []string{
	"more than one copy of React",
	"Invalid hook call",
	"Multiple instances of",
	"two copies of",
	"duplicate module",
}

// nodeModulesPathRe finds the node_modules directory a stack frame or error
// names, with everything before it.
var nodeModulesPathRe = regexp.MustCompile(`[^\s"'()<>:]*node_modules[/\\]`)

// moduleDuplication reports the duplication signature a failed run carries
// ("" for an ordinary failure) and the distinct node_modules directories its
// output names that lie outside the checkout at repoRoot. Relative paths are
// read from root, the directory the suite ran in. outside is the evidence: a
// stack that loads react from one lane and react-dom from another.
func moduleDuplication(repoRoot, root string, res SuiteResult) (signature string, outside []string) {
	low := strings.ToLower(res.Output)
	for _, s := range duplicationSignatures {
		if strings.Contains(low, strings.ToLower(s)) {
			signature = s
			break
		}
	}
	if signature == "" {
		return "", nil
	}
	for _, m := range nodeModulesPathRe.FindAllString(res.Output, -1) {
		dir := strings.TrimRight(m, `/\`)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, filepath.FromSlash(dir))
		}
		dir = filepath.Clean(dir)
		if rel, err := filepath.Rel(repoRoot, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if !slices.Contains(outside, dir) {
			outside = append(outside, dir)
		}
	}
	return signature, outside
}

// duplicationCause says why the suite measured nothing: the signature, and the
// node_modules directories outside the checkout it loaded from.
func duplicationCause(signature string, outside []string) string {
	where := "the output names no node_modules outside the checkout"
	if len(outside) > 0 {
		shown := outside
		if len(shown) > 4 {
			shown = shown[:4]
		}
		where = "modules resolved outside the checkout from " + strings.Join(shown, ", ")
		if len(outside) > len(shown) {
			where += fmt.Sprintf(" and %d more", len(outside)-len(shown))
		}
	}
	return fmt.Sprintf("the run died of a duplicated module (%q): %s; the install is mixed, not the code wrong", signature, where)
}
