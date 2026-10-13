package cli

import (
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/report"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// attributionHostFn is the repo's GitHub host for the escape attribution; ok is false when
// the repo has none (no GitHub origin). A test replaces it with a Fake.
var attributionHostFn = func(root string) (h host.Host, ok bool) {
	c, err := git.New(root, git.Options{})
	if err == nil {
		if _, _, found := github.OwnerRepo(c.RemoteURL("origin")); found {
			h, ok = github.New(github.Options{Dir: root, Timeout: 60 * time.Second}), true
		}
	}
	return h, ok
}

// hasOffLane reports whether the log holds a lane in the off arm.
func hasOffLane(events []tdd.Event) bool {
	for _, e := range events {
		if e.Kind == "lane-arm" && e.Detail["arm"] == tddarm.ArmOff {
			return true
		}
	}
	return false
}

// escapeAttribution is the escapes the tracker's issues and main's red runs blame on a
// lane, and what it could not read. A log with no off lane has no on/off comparison to
// feed, so it asks the host nothing.
func escapeAttribution(root string, events []tdd.Event) ([]measure.Attribution, []string) {
	if !hasOffLane(events) {
		return nil, nil
	}
	h, ok := attributionHostFn(root)
	if !ok {
		return nil, []string{"no GitHub remote: escapes named in issues and red runs on main were not read"}
	}
	trunk := tddarm.Trunk(root)
	if trunk == "" {
		trunk = "main"
	}
	in, notes := report.GatherAttribution(h, trunk, events)
	return measure.AttributeEscapes(in), notes
}
