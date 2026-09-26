package cli

import "github.com/aphrollo/aphrollo-tools/internal/undercover"

// The shim's half of the undercover ref wall: a branch named for the tool
// stays in the remote, in the PR head and in every merge subject once it is
// created and pushed, so the verbs that CREATE a ref and the push that sends
// one are refused before git runs. Deleting or listing a tell branch passes:
// that is the cleanup. The pre-push hook is the same wall for a push that
// bypasses the shim.

// undercoverRefusalLine returns the refusal for an invocation that would
// create or push a ref whose name carries a tell, "" when the invocation is
// fine or the repo never set `undercover = true`. rest is the alias-expanded
// classification form, the same one primaryRefusalLine reads.
func undercoverRefusalLine(realGit string, rest []string, workDir string) string {
	kind, names, pushesCurrent := undercover.RefArgs(rest)
	if len(names) == 0 && !pushesCurrent {
		return ""
	}
	top := gitShimOut(realGit, workDir, "rev-parse", "--show-toplevel")
	if top == "" {
		return ""
	}
	tells, on := undercover.Load(top)
	if !on {
		return ""
	}
	if pushesCurrent {
		if cur := gitShimOut(realGit, workDir, "symbolic-ref", "--short", "-q", "HEAD"); cur != "" {
			names = append(names, cur)
		}
	}
	for _, name := range names {
		if tell, hit := tells.RefName(name); hit {
			return "gate: " + undercover.RefRefusal(kind, name, tell)
		}
	}
	return ""
}
