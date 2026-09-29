package smell

import (
	"maps"
	"os"
	"slices"
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

// introducedLines are the 1-based lines of post that pre did not already
// carry, compared by content in the directives view, never by diff position.
// A suppression that only moved within the file, or that a diff aligned
// against the wrong line, reads the same before and after and is not
// introduced. Comparing the directives view rather than the raw text keeps a
// directive that sat blank inside a string before and is a live comment after
// counted as new.
func introducedLines(pre, post string, l lang) map[int]bool {
	return addedLines(l.mask(pre, false), l.mask(post, false))
}

// removedTexts are the trimmed directives-view texts of the lines of pre
// that post does not carry, counted by multiplicity: what a change took out of
// a file, the mirror of introducedLines. A directive that sat blank inside a
// string is blank here too, so its removal carries no directive text.
func removedTexts(pre, post string, l lang) map[string]int {
	preLines := strings.Split(l.mask(pre, false), "\n")
	out := map[string]int{}
	for n := range addedLines(l.mask(post, false), l.mask(pre, false)) {
		out[strings.TrimSpace(preLines[n-1])]++
	}
	return out
}

// absorbMoved returns the introduced lines of post whose directives-view text
// the pool of removed texts does not hold. Each line absorbed takes one count
// from the pool, lowest line first, so one removal pays for at most one
// addition anywhere in the commit: a suppression moved between files is not
// introduced, and a second copy of it still is.
func absorbMoved(post string, l lang, introduced map[int]bool, pool map[string]int) map[int]bool {
	lines := strings.Split(l.mask(post, false), "\n")
	out := map[int]bool{}
	for _, n := range slices.Sorted(maps.Keys(introduced)) {
		if key := strings.TrimSpace(lines[n-1]); pool[key] > 0 {
			pool[key]--
			continue
		}
		out[n] = true
	}
	return out
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
	return evaluateCovered(post, lines, nil, l, policies, p)
}

// evaluateCovered is evaluateAdded with the lines whose suppression directives
// the change only carried over from lines it removed (coveredDirectives): the
// policies that judge directives skip them, every other policy still reads
// them.
func evaluateCovered(post string, lines, covered map[int]bool, l lang, policies []policy, p phase) Decision {
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
		if pol.directive {
			judged = withoutLines(judged, covered)
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
		Code:       keepLines(full.Code, lines),
		directives: keepLines(full.directives, lines),
		whole:      full.whole,
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
