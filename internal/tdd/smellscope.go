package tdd

import (
	"os"
	"strings"
)

// A smell check judged the WHOLE file an edit touched, so a file that
// legitimately skips (a platform guard — fourteen of them in this repo alone)
// denied every later edit to itself, and the only way to keep working in it
// was to stop. Two things fix that: judge the lines this edit ADDS, and give
// the two kinds that have legitimate uses an escape that says why. An escape
// is recorded, so a waiver is a number in the stats rather than a habit.

// escapeSkip / escapeSleep / escapeAnyError / escapePanicOnly are the
// markers, matched in the comment-preserving view so a quoted one cannot
// admit anything. They read as a sentence in any comment syntax:
// `// skip-ok: <why>`, `# real-time: <why>`, `// any-error-ok: <why>`,
// `// panic-only-ok: <why>`.
const (
	escapeSkip      = "skip-ok:"
	escapeSleep     = "real-time:"
	escapeAnyError  = "any-error-ok:"
	escapePanicOnly = "panic-only-ok:"
)

// editImages reconstructs what the file holds BEFORE and AFTER an edit. A tool
// shape this does not model, or an edit whose old text is not on disk, falls
// back to "everything the edit introduces is new" — the pre-existing
// behaviour, and the safe direction.
func editImages(in preToolUseInput, path string) (pre, post string) {
	added := newContent(in)
	if path == "" {
		return "", added
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", added
	}
	pre = string(data)
	switch in.ToolName {
	case "Write":
		return pre, in.ToolInput.Content
	case "Edit":
		post = applyEdit(pre, in.ToolInput.OldString, in.ToolInput.NewString, in.ToolInput.ReplaceAll)
	case "MultiEdit":
		post = pre
		for _, e := range in.ToolInput.Edits {
			post = applyEdit(post, e.OldString, e.NewString, e.ReplaceAll)
		}
	default:
		return "", added
	}
	if post == pre {
		return "", added
	}
	return pre, post
}

// addedLines are the 1-based lines of post that pre did not already carry, by
// multiplicity: a line present twice after and once before is one new line.
// Comparing counts rather than positions means a pure move never reads as an
// addition, which is the whole point — an edit elsewhere in the file must not
// re-judge what was already there.
func addedLines(pre, post string) map[int]bool {
	remaining := map[string]int{}
	for _, line := range strings.Split(pre, "\n") {
		remaining[strings.TrimSpace(line)]++
	}
	added := map[int]bool{}
	for i, line := range strings.Split(post, "\n") {
		key := strings.TrimSpace(line)
		if remaining[key] > 0 {
			remaining[key]--
			continue
		}
		added[i+1] = true
	}
	return added
}

// escapedLines are the lines a marker admits: the line carrying it, and the
// line below it (a reason too long for the code's own line goes above it).
func escapedLines(directives, marker string) map[int]bool {
	out := map[int]bool{}
	for i, line := range strings.Split(directives, "\n") {
		if strings.Contains(line, marker) {
			out[i+1] = true
			out[i+2] = true
		}
	}
	return out
}

// evaluateAdded runs policies over exactly the given lines of the post-image,
// honouring each policy's escape marker. The returned Decision carries every
// admitted escape so the caller can record it: a waiver nobody counts is a
// waiver nobody manages.
func evaluateAdded(post string, lines map[int]bool, l lang, policies []policy, p phase) Decision {
	full := newView(post, l)
	best := Decision{Action: Allow}
	var escapes []string
	for _, pol := range policies {
		judged := lines
		if pol.escape != "" {
			admitted := intersectLines(lines, escapedLines(full.directives, pol.escape))
			if len(admitted) > 0 && pol.hit(restrict(full, admitted)) {
				escapes = append(escapes, "smell-escape:"+pol.name)
			}
			judged = withoutLines(lines, admitted)
		}
		if len(judged) == 0 || !pol.hit(restrict(full, judged)) {
			continue
		}
		if a := actionFor(pol.category, p); a > best.Action {
			best = Decision{Action: a, Reason: pol.reason, Policy: pol.name}
		}
	}
	best.Escapes = escapes
	return best
}

// restrict is one policy's view of a line subset.
func restrict(full view, lines map[int]bool) view {
	return view{
		code:       keepLines(full.code, lines),
		directives: keepLines(full.directives, lines),
	}
}

func intersectLines(a, b map[int]bool) map[int]bool {
	out := map[int]bool{}
	for n := range a {
		if b[n] {
			out[n] = true
		}
	}
	return out
}

func withoutLines(a, drop map[int]bool) map[int]bool {
	out := map[int]bool{}
	for n := range a {
		if !drop[n] {
			out[n] = true
		}
	}
	return out
}
