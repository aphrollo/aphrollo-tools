package smell

import (
	"maps"
	"regexp"
	"slices"
	"strings"
)

// A changed line that still carries a suppression its old line already
// carried is not introducing one: documenting an existing noqa
// with a reason edits the line but silences nothing new, and a gate that
// refuses it contradicts the law demanding the reason. A suppression is
// identified by its directive and the codes it names, not by the line around
// it, so the check pairs the directives an edit takes out with the ones it puts
// in. A NEW code on an existing directive, or a directive the removed lines
// never carried, is still introduced.

// directiveKinds cover every directive the lint, type and coverage detectors
// recognise. The code list is deliberately narrow — the shape a linter itself
// reads as codes — so a reason written after the directive is never mistaken
// for one. Group 1 of each is the directive itself, group 2 the code list it
// names, when it names any.
var directiveKinds = []*regexp.Regexp{
	regexp.MustCompile(`(//\s*nolint)(?::([\w\-]+(?:,[\w\-]+)*))?`),
	regexp.MustCompile(`(#\s*noqa)(?::\s*([A-Z]+\d+(?:[,\s]+[A-Z]+\d+)*))?`),
	regexp.MustCompile(`(#\s*flake8)(?::\s*(\w+))?`),
	regexp.MustCompile(`(#\s*pylint:\s*disable)(?:\s*=\s*([\w\-]+(?:\s*,\s*[\w\-]+)*))?`),
	regexp.MustCompile(`(#\s*rubocop:\s*disable)(?:\s+([A-Z][\w/]*(?:\s*,\s*[A-Z][\w/]*)*))?`),
	regexp.MustCompile(`(eslint-disable(?:-next-line|-line)?)(?:\s+([@A-Za-z][\w@/\-.]*(?:\s*,\s*[@A-Za-z][\w@/\-.]*)*))?`),
	regexp.MustCompile(`(@ts-ignore)`),
	regexp.MustCompile(`(@ts-nocheck)`),
	regexp.MustCompile(`(#\s*type:\s*ignore)(?:\[([^\]]*)\])?`),
	regexp.MustCompile(`(#\s*pyright:\s*ignore)(?:\[([^\]]*)\])?`),
	regexp.MustCompile(`(istanbul\s+ignore(?:\s+(?:next|else|if|file))?)`),
	regexp.MustCompile(`(\b[cv]8\s+ignore(?:\s+(?:next|start|stop|file))?)`),
	regexp.MustCompile(`(#\s*pragma:\s*no\s*cover)`),
}

var (
	spaceRe    = regexp.MustCompile(`\s+`)
	codeSplit  = regexp.MustCompile(`[,\s]+`)
	tokenSplit = "\x00"
)

// directiveToken is one suppression on a line: the directive, and one code it
// names ("" for a directive naming none, which covers every code).
type directiveToken struct {
	kind, code string
}

func (t directiveToken) key() string { return t.kind + tokenSplit + t.code }

// directiveTokens lists every suppression a directives-view line carries, one
// token per named code.
func directiveTokens(line string) []directiveToken {
	var out []directiveToken
	for _, re := range directiveKinds {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			kind := spaceRe.ReplaceAllString(m[1], "")
			codes := codeSplit.Split(strings.TrimSpace(directiveCodes(m)), -1)
			named := false
			for _, c := range codes {
				if c != "" {
					named = true
					out = append(out, directiveToken{kind, c})
				}
			}
			if !named {
				out = append(out, directiveToken{kind, ""})
			}
		}
	}
	return out
}

// directiveCodes is the code list a match carries, or "" when the directive
// has no code group.
func directiveCodes(m []string) string {
	if len(m) > 2 {
		return m[2]
	}
	return ""
}

// removedDirectives counts, by multiplicity, the suppression tokens on the
// lines of pre that post no longer carries: what a change takes out, the pool
// an added line's directives are paid from.
func removedDirectives(pre, post string, l lang) map[string]int {
	preLines := strings.Split(l.mask(pre, false), "\n")
	out := map[string]int{}
	for n := range addedLines(l.mask(post, false), l.mask(pre, false)) {
		for _, t := range directiveTokens(preLines[n-1]) {
			out[t.key()]++
		}
	}
	return out
}

// coveredDirectives returns the lines of judged whose every suppression token
// the pool already holds: the exact token, or the same directive naming no
// code. Each covered line pays its tokens out of the pool, lowest line first,
// so one removed directive vouches for one added one. introduced is every line
// the change adds; those absorbMoved already paid for (introduced but not
// judged) spend their tokens first, so a removal never pays twice.
func coveredDirectives(post string, l lang, introduced, judged map[int]bool, pool map[string]int) map[int]bool {
	masked := strings.Split(l.mask(post, false), "\n")
	for _, n := range slices.Sorted(maps.Keys(withoutLines(introduced, judged))) {
		for _, t := range directiveTokens(masked[n-1]) {
			spend(pool, t)
		}
	}
	out := map[int]bool{}
	for _, n := range slices.Sorted(maps.Keys(judged)) {
		tokens := directiveTokens(masked[n-1])
		if len(tokens) == 0 {
			continue
		}
		trial := maps.Clone(pool)
		paid := true
		for _, t := range tokens {
			paid = spend(trial, t) && paid
		}
		if paid {
			maps.Copy(pool, trial)
			out[n] = true
		}
	}
	return out
}

// spend takes one token out of the pool — the exact one, else the bare
// directive that covers every code — and reports whether it found one.
func spend(pool map[string]int, t directiveToken) bool {
	for _, k := range []string{t.key(), directiveToken{t.kind, ""}.key()} {
		if pool[k] > 0 {
			pool[k]--
			return true
		}
	}
	return false
}

// editCovered is coveredDirectives for a single edit: the pool is what the
// edit itself removes from the one file.
func editCovered(pre, post string, l lang, lines map[int]bool) map[int]bool {
	return coveredDirectives(post, l, lines, lines, removedDirectives(pre, post, l))
}
