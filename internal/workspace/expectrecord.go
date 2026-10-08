package workspace

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/expect"
	"github.com/aphrollo/aphrollo-tools/internal/release"
)

// newestReleaseTag is the newest release tag of the repo at wt, "" when it has
// none or cannot be read.
var newestReleaseTag = func(wt string) string {
	out, err := wtGit(wt, "tag", "--list", "v*")
	if err != nil {
		return ""
	}
	tag, _, _ := release.NewestRelease(strings.Fields(string(out)))
	return tag
}

// addExpectation puts the PR's `expect:` lines into a merge event's detail,
// with the release the PR lands after (the newest tag when it merged, without
// its v): the report reads the events of the binaries newer than it. A PR body
// that cannot be read, or says nothing, records nothing.
func addExpectation(d map[string]string, wt, branch string) {
	_, body, err := ghPRText(wt, branch)
	if err != nil {
		return
	}
	es, err := expect.Parse(body)
	if err != nil || len(es) == 0 {
		return
	}
	d["expect"] = expect.Encode(es)
	d["after"] = strings.TrimPrefix(newestReleaseTag(wt), "v")
}
