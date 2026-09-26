package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// runGatePrepush is the pre-push hook: git hands it one line per ref being
// pushed — `<local ref> <local oid> <remote ref> <remote oid>` — and it
// refuses the push when either name carries an undercover tell, or when a
// commit the push sends does (prepushCommitRefusal). It is the
// wall for a push that never went through the git shim. A deletion (local
// ref `(delete)`) sends no name and passes: removing a leaked branch is the
// cleanup. Outside a repo, or in one that never set `undercover = true`, it
// reads nothing and passes.
func runGatePrepush(stdin io.Reader, stderr io.Writer, root string) int {
	if root == "" {
		return 0
	}
	tells, on := undercover.Load(root)
	if !on {
		return 0
	}
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] == "(delete)" {
			continue
		}
		for _, side := range []struct{ kind, ref string }{{"local ref", f[0]}, {"remote ref", f[2]}} {
			name := strings.TrimPrefix(strings.TrimPrefix(side.ref, "refs/heads/"), "refs/tags/")
			if tell, hit := tells.RefName(name); hit {
				fmt.Fprintln(stderr, "gate prepush: "+undercover.RefRefusal(side.kind, name, tell))
				return 1
			}
		}
		if line := prepushCommitRefusal(root, tells, f[1], f[3]); line != "" {
			fmt.Fprintln(stderr, line)
			return 1
		}
	}
	return 0
}

// prepushCommitRefusal judges every commit the push sends for one ref: its
// author, its committer and its Co-authored-by trailers. The range is what
// the remote does not hold yet — from the remote's old tip, or, for a new
// ref (or an old tip this clone never saw), everything no remote-tracking
// ref reaches. "" when the range is clean or git cannot list it.
func prepushCommitRefusal(root string, tells undercover.List, localOid, remoteOid string) string {
	realGit, err := resolveRealGit()
	if err != nil {
		return ""
	}
	// A new ref's remote oid is all zeros, which git cannot resolve either:
	// the one fallback serves both.
	env := append(os.Environ(), tdd.GitQueuedEnv+"=1")
	sha, h, hit, err := tells.RangeTell(realGit, root, env, remoteOid+".."+localOid)
	if err != nil {
		sha, h, hit, _ = tells.RangeTell(realGit, root, env, localOid, "--not", "--remotes")
	}
	if !hit {
		return ""
	}
	return fmt.Sprintf("gate prepush: undercover: commit %.12s's %s %q carries %q, which this repo keeps out of its history.\n"+
		"Set your own identity (git config user.name \"<name>\" && git config user.email \"<address>\"), drop any such Co-authored-by trailer, "+
		"then rewrite the unpushed commits, e.g. git rebase --exec 'git commit --amend --no-edit --reset-author' <base>.",
		sha, h.Field, h.Value, h.Tell)
}
