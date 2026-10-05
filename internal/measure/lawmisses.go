package measure

import (
	"strings"
	"time"
)

// LawMisses is the roadmap measure "commit refusals the edit check missed": of
// the (law, file) pairs a commit refused on a law, how many no edit-time deny or
// guide had named earlier on the same lane. It trends to 0 when the edit stage
// judges what the commit judges.
type LawMisses struct {
	// WindowSecs is the window the refusals fall in; 0 is the whole log.
	WindowSecs float64 `json:"window_secs"`
	// Refusals are the commit gate's law refusals in the window.
	Refusals int `json:"refusals"`
	// Pairs are the (law, file) pairs those refusals recorded (at most ten each).
	Pairs int `json:"pairs"`
	// Missed are the pairs with no earlier edit-time event of the lane.
	Missed int `json:"missed"`
	// Unattributed are refusals that recorded no pair, as an older log wrote them.
	Unattributed int `json:"unattributed"`
}

// MissesNote is what the measure cannot see, printed beside it: a module-graph
// law is not judged after a write through the shell (a go.mod edit), so its
// refusal reaches the commit unannounced by design.
const MissesNote = "dep-graph laws are not judged after a shell write (a go.mod edit): the commit judges them, so such a refusal counts as missed"

// editSeenPair is the (law, file) an edit-time deny or guide event names, "" when
// the event is about no law.
func editSeenPair(e stamped) string {
	if e.Kind != "deny" && e.Kind != "guide" {
		return ""
	}
	law, ok := strings.CutPrefix(e.Detail["rule"], "ratchet:")
	file := strings.ReplaceAll(e.Detail["file"], "\\", "/")
	if !ok || file == "" {
		return ""
	}
	return law + "|" + file
}

func foldLawMisses(s scope, window time.Duration) LawMisses {
	m := LawMisses{WindowSecs: window.Seconds()}
	seen := map[string]map[string]bool{}
	for _, e := range s.evs {
		if pair := editSeenPair(e); pair != "" {
			if seen[e.Lane] == nil {
				seen[e.Lane] = map[string]bool{}
			}
			seen[e.Lane][pair] = true
			continue
		}
		if e.Kind != "commit_gate" || e.Verdict != "ratchet-rejected" || !s.in(e.at) {
			continue
		}
		m.Refusals++
		hits := e.Detail["hits"]
		if hits == "" {
			m.Unattributed++
			continue
		}
		for pair := range strings.SplitSeq(hits, ";") {
			m.Pairs++
			if !seen[e.Lane][pair] {
				m.Missed++
			}
		}
	}
	return m
}
