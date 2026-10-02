package workspace

import (
	"errors"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A merge's CI is one choice: ci = auto | local | github in the repo's
// aphrollo.toml, beaten by a per-merge --ci flag. auto judges by GitHub's
// checks when its jobs start and by local CI when they never do (a billing
// lock, a spending limit; #1064, #1081); github is the old behaviour; local
// never waits on GitHub. Every merge prints which CI judged it and why.

// readCIMode and localCI are the seams over the repo's declared choice and the
// local CI run, so merge tests drive the choice without a repo or a suite.
var (
	readCIMode = tdd.ReadCIMode
	localCI    = func(t *Target, log io.Writer) (tdd.LocalCIVerdict, error) {
		return tdd.LocalCI(t.Worktree, log)
	}
)

// ciChoice is the mode a merge runs under and where it came from.
type ciChoice struct {
	mode   string
	source string
}

// chooseCI resolves the mode: the --ci flag when given, else the repo's
// setting. An unknown value is refused, never defaulted.
func chooseCI(repo, flag string) (ciChoice, error) {
	if flag != "" {
		mode, err := tdd.NormalizeCIMode(flag)
		if err != nil {
			return ciChoice{}, fmt.Errorf("--ci: %w", err)
		}
		return ciChoice{mode: mode, source: "--ci " + mode}, nil
	}
	mode, err := readCIMode(repo)
	if err != nil {
		return ciChoice{}, err
	}
	return ciChoice{mode: mode, source: "ci = " + mode + " in aphrollo.toml"}, nil
}

// ciUnavailableError is the wait's refusal when hosted CI never started its
// jobs. Under auto it is the signal to fall back to local CI; the text is the
// refusal every other mode prints.
type ciUnavailableError struct{ msg string }

func (e *ciUnavailableError) Error() string { return e.msg }

func isCIUnavailable(err error) bool {
	var u *ciUnavailableError
	return errors.As(err, &u)
}

// runLocalCI is the local CI step of a merge: it judges the merge result of
// the lane and refuses the merge on a red.
func runLocalCI(t *Target, sha string, pr int, stdout, stderr io.Writer) error {
	v, err := localCI(t, stderr)
	if err != nil {
		if v.Red {
			recordSettledCIBy(t.Worktree, sha, pr, "red", tdd.CILocal)
		}
		return err
	}
	recordSettledCIBy(t.Worktree, sha, pr, "green", tdd.CILocal)
	switch {
	case v.Landed:
		fmt.Fprintf(stdout, "ci: local — trunk already holds this lane, nothing to judge\n")
	case v.Reused:
		fmt.Fprintf(stdout, "ci: local — merge result %s already judged green, verdict reused\n", short(v.Tree))
	default:
		fmt.Fprintf(stdout, "ci: local — merge result %s judged green\n", short(v.Tree))
	}
	return nil
}
