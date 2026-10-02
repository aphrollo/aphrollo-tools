package cli

import (
	"fmt"
	"regexp"

	"github.com/aphrollo/aphrollo-tools/internal/rollback"
)

// updateTarget is the commit an update installs, and the ref the operator named
// for it: "" when the box follows its branch and no ref was given.
type updateTarget struct {
	Commit string
	Ref    string
}

// describe names the target the way a pin does: a tag with its commit, a bare
// sha once.
func (t updateTarget) describe() string {
	return rollback.Pin{Ref: t.Ref, Commit: t.Commit}.Describe()
}

// gitFn runs git in the update's checkout and answers its trimmed stdout.
type gitFn func(args ...string) (string, error)

// shaRef is what update --to accepts as a commit: an abbreviated or full sha.
var shaRef = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// resolveUpdateTarget finds the commit an update installs. With no --to that is
// the tip of remoteBranch. With one it is the tag or sha it names, which must
// already be merged on remoteBranch: `update` moves the box to what origin has
// accepted and never to a local branch or an unpushed commit.
func resolveUpdateTarget(git gitFn, to, remoteBranch string) (updateTarget, error) {
	if to == "" {
		head, err := git("rev-parse", remoteBranch)
		return updateTarget{Commit: head}, err
	}
	var t updateTarget
	if commit, err := git("rev-parse", "--verify", "--quiet", "refs/tags/"+to+"^{commit}"); err == nil {
		t = updateTarget{Commit: commit, Ref: to}
	} else if shaRef.MatchString(to) {
		commit, err := git("rev-parse", "--verify", "--quiet", to+"^{commit}")
		if err != nil {
			return updateTarget{}, fmt.Errorf("no commit %s in the repo after fetching the remote", to)
		}
		t = updateTarget{Commit: commit, Ref: shortSHA(commit)}
	} else {
		return updateTarget{}, fmt.Errorf("%q is neither a tag nor a commit sha", to)
	}
	ahead, err := git("rev-list", "--count", remoteBranch+".."+t.Commit)
	if err != nil {
		return updateTarget{}, err
	}
	if ahead != "0" {
		return updateTarget{}, fmt.Errorf("%s is not on %s: update --to installs only what is merged there", t.describe(), remoteBranch)
	}
	return t, nil
}
