package measure

import (
	"fmt"
	"strings"
)

// The token caps of the architecture's section 5: what the managed block and
// the skill may cost a session, and what an agent's own brief may cost.
const (
	BriefCap         = 400
	SubagentBriefCap = 250
)

// Brief is one rendered text a session pays tokens for. Subagent marks an
// agent's own brief, which has the lower cap.
type Brief struct {
	Name     string
	Subagent bool
	Bytes    int
}

// BriefLine is a Brief measured against its cap.
type BriefLine struct {
	Name   string `json:"name"`
	Bytes  int    `json:"bytes"`
	Tokens int    `json:"tokens"`
	Cap    int    `json:"cap"`
	Over   bool   `json:"over"`
}

// Tokens is the token estimate of n rendered bytes: bytes divided by four,
// rounded up so a text just past the cap is never read as under it.
func Tokens(n int) int { return (n + 3) / 4 }

// CheckBriefs measures each brief against its cap, in the order given.
func CheckBriefs(in []Brief) []BriefLine {
	out := make([]BriefLine, len(in))
	for i, b := range in {
		limit := BriefCap
		if b.Subagent {
			limit = SubagentBriefCap
		}
		tokens := Tokens(b.Bytes)
		out[i] = BriefLine{Name: b.Name, Bytes: b.Bytes, Tokens: tokens, Cap: limit, Over: tokens > limit}
	}
	return out
}

// BriefsText prints one line per brief and marks the ones over their cap.
func BriefsText(lines []BriefLine) string {
	var b strings.Builder
	for _, l := range lines {
		mark := ""
		if l.Over {
			mark = "  OVER"
		}
		fmt.Fprintf(&b, "%-22s %6d bytes  %5d tokens  cap %d%s\n", l.Name, l.Bytes, l.Tokens, l.Cap, mark)
	}
	return b.String()
}
