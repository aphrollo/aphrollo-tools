package postedit

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// lawRefusalNote judges the declared laws over the files an edit left on
// disk (absolute paths, all in one repo) and renders what the commit gate
// would refuse, each finding with its escape; "" when nothing. The laws and
// the rendering are lawgate's (editLawRefusals); this only finds the repo,
// makes the paths relative and logs a refusal with how long the judging
// took.
func lawRefusalNote(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	root := repoRootNear(filepath.Dir(targets[0]))
	if root == "" {
		return ""
	}
	var rels []string
	for _, target := range targets {
		rel, err := filepath.Rel(root, target)
		if err != nil {
			continue
		}
		slash := filepath.ToSlash(rel)
		if strings.HasPrefix(slash, "../") {
			continue
		}
		rels = append(rels, slash)
	}
	started := time.Now()
	lines, hits := editLawRefusalHits(root, rels)
	if len(lines) == 0 {
		return ""
	}
	logLawGuides(root, "postedit", hits)
	AppendGateLog("postedit", root, LogToken(rels[0]), fmt.Sprintf("ratchet-would-refuse:%d", len(lines)), time.Since(started))
	return "ratchet would refuse the commit: " + strings.Join(lines, "; ")
}
