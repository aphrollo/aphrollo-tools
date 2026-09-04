package cli

import (
	"os"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// onlyNamesUpstream reports whether a pull or merge names nothing but the
// current branch's own upstream.
//
// The merge-only wall refuses a pull or merge that could fast-forward,
// because a fast-forward fires no hook and would land lane commits on main
// unjudged. Fast-forwarding main to its OWN UPSTREAM is the one case the
// reasoning does not cover: those commits were judged on the way INTO
// origin/main, and the local gate has no say over them either way. Refusing
// it broke the ordinary loop of a PR-only repository — the primary stayed
// behind origin/main and `gate self-install` built the stale tree — leaving
// APHROLLO_PRIMARY_EDITS=1, which switches the whole wall off, as the only
// way through.
//
// A pull with no operand takes the upstream by definition. With operands, all
// of them must resolve to it: `origin main` (pull's two-token form),
// `origin/main` (merge's), or a bare `@{u}`. Anything else — a lane branch, a
// second ref, a sha — is refused exactly as before.
func onlyNamesUpstream(realGit, workDir string, args []string) bool {
	upstream := gitShimOut(realGit, workDir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if upstream == "" {
		return false // no upstream to fast-forward to; the wall stands
	}
	remote, branch, ok := strings.Cut(upstream, "/")
	if !ok {
		return false
	}

	var operands []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		operands = append(operands, a)
	}
	switch len(operands) {
	case 0:
		return true // `git pull` alone is the upstream by definition
	case 1:
		return operands[0] == upstream || operands[0] == "@{upstream}" || operands[0] == "@{u}"
	case 2:
		return operands[0] == remote && operands[1] == branch
	}
	return false
}

// gitShimOut asks the real git a question, with the queue bypass set so the
// shim's own classification never waits on a build slot.
func gitShimOut(realGit, workDir string, args ...string) string {
	cmd := exec.Command(realGit, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
