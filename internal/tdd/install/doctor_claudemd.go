package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The managed CLAUDE.md block is the one place a session reads the gate's
// rules, and it is written ONCE — by the install that happened to run in that
// repo, months ago. Nothing re-rendered it and nothing judged it, so a repo
// kept guidance this tool had retired: issue #584 found a block still
// describing a merge step deleted months earlier and naming two verbs that now
// exit "unknown verb", which cost a supervising session a lane chasing a
// failure that could no longer happen.
//
// Install already refreshes an existing block on every run (WriteClaudeMD
// rewrites whatever is between the markers), so the missing half was never the
// write — it was that nobody knew a repo was behind. This check is that half,
// and only that half: it READS. A check that edited CLAUDE.md as a side effect
// would be a mutation nobody asked for, in a file the repo owns outside the
// markers.
//
// What it judges is CONTENT, entry by entry, with whitespace collapsed: a
// block somebody re-wrapped, or an editor gave CRLF and trailing spaces, says
// exactly what the template says and is not a finding. The queue-dir path is
// masked for the same reason — it is a fact about the box that wrote the
// block, not about the template's currency, and a repo judged on a CI runner
// would otherwise read as stale on every run. The finding NAMES the first
// entry that differs rather than printing a diff of the whole block: the
// operator's next move is `aphrollo install`, and thirteen bullets of context
// does not change it.

// claudeMDLabelLimit caps an entry's label when it has no bold lead to be
// named by. Long enough to recognise the bullet, short enough that the
// finding stays one line.
const claudeMDLabelLimit = 56

// claudeMDQueueDir matches the one place the template interpolates a path
// from the box it runs on. Anchored on the sentence around it, so a template
// that stops saying this stops masking it — and a block still carrying the
// old sentence is then correctly stale.
var claudeMDQueueDir = regexp.MustCompile("prints a path under `[^`]*`")

// doctorClaudeMD compares the managed block in the repo's CLAUDE.md with the
// block this build would write there. ok=false means the check does not
// apply: no repo, no CLAUDE.md, or a CLAUDE.md with no managed block — a repo
// that never opted in is not behind on anything, and a passing line about a
// block it does not have would be a lie in the friendly direction.
func doctorClaudeMD(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "CLAUDE.md block"}
	if in.Repo == "" {
		return c, false
	}
	path := filepath.Join(in.Repo, "CLAUDE.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			// An unreadable CLAUDE.md is a fact about the file system, not a
			// stale block: say so and pass, the way doctorForeignHooks does
			// with a hooks dir it cannot list.
			c.OK, c.Warn = true, true
			c.Detail = fmt.Sprintf("could not read %s (%v)", path, err)
			return c, true
		}
		return c, false
	}
	have, ok := claudeMDBlockBody(string(data))
	if !ok {
		return c, false
	}
	want, _ := claudeMDBlockBody(managedBlockFor(in.Repo, in.ShimDir))

	wantEntries, haveEntries := claudeMDEntries(want), claudeMDEntries(have)
	diffs, first := claudeMDDrift(wantEntries, haveEntries)
	if diffs == 0 {
		c.OK = true
		c.Detail = fmt.Sprintf("current (%d entries)", len(wantEntries))
		return c, true
	}
	c.Detail = fmt.Sprintf("%s is stale: %d of %d entries differ from the block this build writes — %s; %s",
		path, diffs, len(wantEntries), first, claudeMDRemedy(in.Repo))
	return c, true
}

// claudeMDRemedy is the fix, which depends on where the repo is standing. The
// merge-only primary cannot take the write (its git shim refuses the commit
// that would carry it), so install refuses it there too — pointing at
// `aphrollo install --repo <primary>` would be advice that cannot be
// followed.
func claudeMDRemedy(repo string) string {
	if _, ok := PrimaryMergeOnly(repo); ok {
		return "this is the merge-only primary, so land the refresh through a lane (`aphrollo install --repo <lane>`)"
	}
	return fmt.Sprintf("run `aphrollo install --repo %s` to refresh it", repo)
}

// claudeMDBlockBody returns the text between the markers. ok=false when the
// pair is not there in order: one marker alone means the file was hand-edited
// mid-block, which install repairs by rewriting the whole block, and there is
// no coherent "what this repo has" to judge in the meantime.
func claudeMDBlockBody(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	start := strings.Index(text, claudeMDBegin)
	end := strings.Index(text, claudeMDEnd)
	if start < 0 || end <= start {
		return "", false
	}
	return text[start+len(claudeMDBegin) : end], true
}

// claudeMDEntries splits a block body into the units it is written in: the
// heading, one per top-level bullet, and the closing paragraph. Comparing
// entries rather than lines is what lets a re-wrapped block pass — a wrap
// moves words between LINES and never between bullets — and it is also what
// gives the finding something to name.
// An entry ends at a blank line or at the next top-level bullet; a line that
// is neither continues the one before it. Blankness is judged after trimming,
// because a re-wrapped block carries whitespace-only separator lines and a
// paragraph split on the exact bytes "\n\n" would miss them — merging the
// closing paragraph into the last bullet on one side of the comparison and
// not the other, which reads as stale when nothing is.
func claudeMDEntries(body string) []string {
	var entries []string
	fresh := true
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			fresh = true
		case fresh || strings.HasPrefix(line, "- "):
			entries = append(entries, line)
			fresh = false
		default:
			entries[len(entries)-1] += "\n" + line
		}
	}
	return entries
}

// claudeMDDrift counts the entries that differ and describes the FIRST one in
// the terms the operator needs: which bullet, and which way it went wrong.
func claudeMDDrift(want, have []string) (int, string) {
	diffs, first := 0, ""
	for i := 0; i < max(len(want), len(have)); i++ {
		var desc string
		switch {
		case i >= len(have):
			desc = fmt.Sprintf("the block is missing %q", claudeMDLabel(want[i]))
		case i >= len(want):
			desc = fmt.Sprintf("the block still carries %q, which the template dropped", claudeMDLabel(have[i]))
		case claudeMDNormalize(want[i]) == claudeMDNormalize(have[i]):
			continue
		case claudeMDLabel(want[i]) == claudeMDLabel(have[i]):
			desc = fmt.Sprintf("%q no longer says what the template says", claudeMDLabel(want[i]))
		default:
			desc = fmt.Sprintf("the block has %q where the template has %q", claudeMDLabel(have[i]), claudeMDLabel(want[i]))
		}
		diffs++
		if first == "" {
			first = desc
		}
	}
	return diffs, first
}

// claudeMDNormalize is the form two entries are compared in: every run of
// whitespace collapsed to one space (so wrapping, indentation, CRLF and
// trailing spaces cannot make a block look stale) and the box's queue dir
// masked.
func claudeMDNormalize(entry string) string {
	return claudeMDQueueDir.ReplaceAllString(strings.Join(strings.Fields(entry), " "), "prints a path under `<queue dir>`")
}

// claudeMDLabel names an entry the way the block itself does: by its bold
// lead, which every bullet in the template has. Everything else (the heading,
// the closing paragraph) is named by its opening words, truncated on a RUNE
// boundary — the block is full of em dashes, and a byte-truncated one would
// put invalid UTF-8 in the report.
func claudeMDLabel(entry string) string {
	s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(entry), "-# "))
	if rest, ok := strings.CutPrefix(s, "**"); ok {
		if lead, _, found := strings.Cut(rest, "**"); found {
			return strings.TrimRight(strings.Join(strings.Fields(lead), " "), ":")
		}
	}
	words := []rune(strings.Join(strings.Fields(s), " "))
	if len(words) > claudeMDLabelLimit {
		return string(words[:claudeMDLabelLimit]) + "…"
	}
	return string(words)
}
