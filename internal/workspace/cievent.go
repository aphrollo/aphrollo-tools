package workspace

import (
	"strconv"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// recordSettledCI writes the one ci event a commit earns: its first settled
// result, green or red. A pending read says nothing and is never recorded, and
// a commit read again by a later verb (a re-push, merge after wait) is not
// counted twice, so "CI green on first run" is the first ci event of a lane's
// commits and nothing else. sha is the commit the checks belong to; pr is 0
// when no PR is known.
func recordSettledCI(wt, sha string, pr int, state string) {
	if sha == "" || (state != "green" && state != "red") {
		return
	}
	detail := map[string]string{"sha": sha}
	if pr > 0 {
		detail["pr"] = strconv.Itoa(pr)
	}
	tdd.AppendEventOnce(tdd.Event{Kind: "ci", Root: wt, Verdict: state, Detail: detail}, "sha")
}
