package merge

import (
	"bytes"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// identityRefusal refuses a commit whose author or committer carries a tell:
// the message can be clean while the name on the commit is the tool's own.
// git resolves the identity exactly as the commit will — config, `-c`
// overrides, then the GIT_AUTHOR_* and GIT_COMMITTER_* environment, which is
// why this runs git with the hook's own environment rather than the scrubbed
// one. An identity git cannot resolve is git's own refusal to make, not this
// gate's, and so is a directory that is not a repository: no commit is made
// there. "" when there is nothing to refuse.
func identityRefusal(repoRoot string, tells undercover.List) string {
	if _, err := identGit(repoRoot, "rev-parse", "--git-dir"); err != nil {
		return ""
	}
	for _, role := range []struct{ name, variable string }{
		{"author", "GIT_AUTHOR_IDENT"},
		{"committer", "GIT_COMMITTER_IDENT"},
	} {
		ident, err := identGit(repoRoot, "var", role.variable)
		if err != nil {
			continue
		}
		if tell, hit := tells.Ident(ident); hit {
			AppendGateLog("commitmsg", LogToken(repoRoot), "commit-msg", "commitmsg-rejected:identity-"+LogToken(tell), 0)
			return "gate commit-msg: " + undercover.IdentRefusal(role.name, strings.TrimSpace(ident), tell)
		}
	}
	return ""
}

// identGit runs one read-only git command in the hook's own environment and
// answers its stdout alone: `git var` warns on stderr while still answering.
func identGit(dir string, args ...string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.String(), err
}
