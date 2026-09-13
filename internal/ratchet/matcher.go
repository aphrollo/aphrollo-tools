package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/mask"
)

// Hit is one offence: where it is, what it is, and the identity the baseline
// knows it by. Line is for the human reading the output — it is deliberately
// NOT part of Key, so inserting a line above an offence is not a regression.
type Hit struct {
	Law    string `json:"law"`
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Key    string `json:"key"`
	What   string `json:"what"`
	Weight int    `json:"weight"`
}

// FileLines is one file's content already split into lines — computed ONCE
// per file, then shared across every law that scans it, rather than once per
// (law, file) pair. code is filled lazily: one slice per DISTINCT VIEW a law
// asks for (comment prefix, string-masked or not), cached so a second law
// sharing that view — the overwhelmingly common case, since one file has one
// language — never re-runs the strip or the masking lexer over it.
type FileLines struct {
	raw  []string
	code map[string][]string
}

// newFileLines splits content once. Cheap enough to call for a single
// HitsIn as well as a scanner's per-file loop.
func newFileLines(content string) *FileLines {
	return &FileLines{raw: splitLines(content)}
}

// codeFor returns l's view of the file — comment-stripped for a CodeOnly law,
// string-blanked for a MaskStrings one, both for a law asking for both —
// computed once per distinct view and reused for every later law sharing it.
func (fl *FileLines) codeFor(l Law) []string {
	if !l.CodeOnly && !l.MaskStrings {
		return fl.raw
	}
	prefix := l.commentPrefix()
	view := prefix
	if l.MaskStrings {
		view += "\x00mask"
	}
	if !l.CodeOnly {
		view += "\x00keep-comments"
	}
	if code, ok := fl.code[view]; ok {
		return code
	}
	lines := fl.raw
	if l.MaskStrings {
		lines = maskStringLines(lines)
	}
	code := lines
	if l.CodeOnly {
		code = make([]string, len(lines))
		for i := range lines {
			code[i], _ = splitTrailingComment(lines[i], prefix)
		}
	}
	if fl.code == nil {
		fl.code = make(map[string][]string)
	}
	fl.code[view] = code
	return code
}

// maskStringLines blanks the CONTENTS of every string literal, keeping the
// comments — the view a law about DIRECTIVES needs, and the same one this
// repo's edit-time detectors have always judged against.
//
// The lexer runs over the whole file, not line by line: a Go raw string or a
// block comment spans lines, and a per-line pass would read the tail of one
// as code. It preserves length and newlines, so the masked slice has the same
// lines in the same order and a hit still reports the line the reader sees.
func maskStringLines(raw []string) []string {
	masked := splitLines(mask.Tokens(strings.Join(raw, "\n")+"\n", true, false, false))
	if len(masked) != len(raw) {
		// Cannot happen — the lexer only ever replaces bytes with spaces — but
		// a view that has silently lost a line would misattribute every hit
		// below it, so the raw lines are the safe answer.
		return raw
	}
	return masked
}

// HitsIn applies a law's matcher to one in-scope file's content. It is pure:
// the same path and content always produce the same hits, which is what makes
// the pre-edit path (content that is not on disk yet) and the cached tree scan
// the same code. Registry laws are the exception — they judge the WHOLE scope
// at once and are answered by the checker, not here.
func (l Law) HitsIn(file, content string) []Hit {
	return l.hitsInLines(file, newFileLines(content))
}

// hitsInLines is HitsIn's lower-level entry point: fl is already split (and,
// lazily, comment-stripped) by the caller, so a scan over many laws applying
// to the SAME file pays that split/strip cost once, not once per law.
func (l Law) hitsInLines(file string, fl *FileLines) []Hit {
	raw := fl.raw
	code := fl.codeFor(l)
	switch l.Matcher.Kind {
	case KindLineCount:
		return l.lineCountHits(file, raw)
	case KindRegexAbsent:
		return l.regexAbsentHits(file, raw, code)
	case KindPathRegexAbsent:
		return l.pathHits(file)
	case KindRegexPresent:
		return l.regexPresentHits(file, code)
	case KindMarkerWithinLines:
		return l.markerHits(file, raw, code)
	case KindRegexNear:
		return l.regexNearHits(file, raw, code)
	case KindDocPathResolves:
		return l.docPathHits(file, code)
	}
	return nil
}

func (l Law) lineCountHits(file string, raw []string) []Hit {
	if l.Matcher.UnitSplit != nil {
		if idx := splitUnitIndex(raw, l.Matcher.UnitSplit); idx >= 0 {
			var hits []Hit
			if h := l.lineCountUnit(file, file, raw[:idx]); h != nil {
				hits = append(hits, *h)
			}
			if h := l.lineCountUnit(file, file+"#tests", raw[idx:]); h != nil {
				hits = append(hits, *h)
			}
			return hits
		}
	}
	if h := l.lineCountUnit(file, file, raw); h != nil {
		return []Hit{*h}
	}
	return nil
}

// splitUnitIndex is the index of the first line matching the unit_split
// regex, or -1 when none does. That line opens the second unit.
func splitUnitIndex(raw []string, re *regexp.Regexp) int {
	for i, line := range raw {
		if re.MatchString(line) {
			return i
		}
	}
	return -1
}

// lineCountUnit judges one unit (the whole file, or one half of a split) and
// reports it under key, which carries the `#tests` suffix for the second half.
func (l Law) lineCountUnit(file, key string, raw []string) *Hit {
	n := l.countUnit(file, raw)
	if n <= l.Matcher.Max {
		return nil
	}
	return &Hit{
		Law:    l.Name,
		File:   file,
		Key:    key,
		What:   fmt.Sprintf("%d lines (max %d)", n, l.Matcher.Max),
		Weight: n,
	}
}

// countUnit is the raw measurement one unit carries, with NO ceiling applied
// — the count a UNDER-max file has too, which lineModeNotes needs in order to
// see a stale baseline that no longer produces a hit at all.
func (l Law) countUnit(file string, raw []string) int {
	counted := raw
	if l.Matcher.LineMode == LineCountCode {
		counted = codeLines(raw, file)
	}
	return len(counted)
}

// lineCountMeasures is countUnit for every unit lineCountHits would judge —
// one entry for a whole file, two (keyed `file` and `file#tests`) when
// unit_split matches — with no ceiling applied.
func (l Law) lineCountMeasures(file string, raw []string) map[string]int {
	if l.Matcher.UnitSplit != nil {
		if idx := splitUnitIndex(raw, l.Matcher.UnitSplit); idx >= 0 {
			return map[string]int{
				file:            l.countUnit(file, raw[:idx]),
				file + "#tests": l.countUnit(file, raw[idx:]),
			}
		}
	}
	return map[string]int{file: l.countUnit(file, raw)}
}

// commentSyntax is the comment opener(s) this law's "code" line-count mode
// judges a file by, decided by extension: `#` for .py/.sh/.toml (no block
// comment), `//` + `/* */` for everything else (.rs/.go/.ts and unlisted
// C-like extensions default here rather than crashing on an unknown one).
func commentSyntax(file string) (line, blockOpen, blockClose string) {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".py", ".sh", ".toml":
		return "#", "", ""
	default:
		return "//", "/*", "*/"
	}
}

// codeLines drops every blank line and every comment-only line — one opened
// by the line-comment prefix, or fully inside a block-comment span — leaving
// only lines a "code" line-count law counts. It is a heuristic scanner, not a
// parser: a block comment that opens after real code on the same line is
// left as code and never tracked into the next line, which is the one shape
// this law needs (a full-line block comment run) rather than every shape a
// compiler would need.
func codeLines(raw []string, file string) []string {
	lineComment, blockOpen, blockClose := commentSyntax(file)
	out := make([]string, 0, len(raw))
	inBlock := false
	for _, l := range raw {
		t := strings.TrimSpace(l)
		if inBlock {
			idx := strings.Index(t, blockClose)
			if idx < 0 {
				continue
			}
			inBlock = false
			rest := strings.TrimSpace(t[idx+len(blockClose):])
			if rest == "" || strings.HasPrefix(rest, lineComment) {
				continue
			}
			out = append(out, l)
			continue
		}
		if t == "" || strings.HasPrefix(t, lineComment) {
			continue
		}
		if blockOpen != "" && strings.HasPrefix(t, blockOpen) {
			rest := t[len(blockOpen):]
			if idx := strings.Index(rest, blockClose); idx >= 0 {
				after := strings.TrimSpace(rest[idx+len(blockClose):])
				if after == "" || strings.HasPrefix(after, lineComment) {
					continue
				}
				out = append(out, l)
				continue
			}
			inBlock = true
			continue
		}
		out = append(out, l)
	}
	return out
}

func (l Law) regexAbsentHits(file string, raw, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) || !l.Matcher.Pattern.MatchString(line) || l.escaped(file, raw, i) {
			continue
		}
		n := 1
		if l.Matcher.Count == CountMatches {
			n = len(l.Matcher.Pattern.FindAllStringIndex(line, -1))
		}
		for range n {
			hits = append(hits, l.hit(file, i+1, strings.TrimSpace(raw[i])))
		}
	}
	return hits
}

// commentPrefix is what opens a comment in the language this law scans.
func (l Law) commentPrefix() string {
	if l.CommentPrefix == "" {
		return "//"
	}
	return l.CommentPrefix
}

// excluded reports whether a line is disqualified from ever being a trigger.
func (l Law) excluded(line string) bool {
	return l.TriggerExclude != nil && l.TriggerExclude.MatchString(line)
}

// pathHits judges the PATH, not the contents: a file whose NAME carries a
// plan-item stamp or a serial letter is the offence, and no amount of reading
// it would show that.
func (l Law) pathHits(file string) []Hit {
	if l.excluded(file) || !l.Matcher.Pattern.MatchString(file) {
		return nil
	}
	return []Hit{{
		Law: l.Name, File: file, Key: file, Weight: 1,
		What: "path matches " + l.Matcher.Pattern.String(),
	}}
}

func (l Law) regexPresentHits(file string, code []string) []Hit {
	if l.Matcher.Pattern.MatchString(strings.Join(code, "\n")) {
		return nil
	}
	return []Hit{{
		Law:    l.Name,
		File:   file,
		Key:    file,
		What:   "does not contain " + l.Matcher.Pattern.String(),
		Weight: 1,
	}}
}

// foundNear scans the trigger's own line and then outward, in the law's
// direction. A contiguous law stops at the first line that is neither a
// comment nor a single-line attribute. It serves KindRegexNear, whose
// context is a CO-OCCURRENCE in the surrounding code rather than a marker
// vouching for one declaration — marker-within-lines has its own upward walk
// (markerInOwnBlock) for exactly that reason.
func (l Law) foundNear(raw []string, idx, lines int, dir Direction, match func(string) bool) bool {
	if idx < len(raw) && match(raw[idx]) {
		return true
	}
	if dir != DirectionBelow && l.scanRun(raw, idx, lines, -1, match) {
		return true
	}
	return (dir == DirectionBelow || dir == DirectionBoth) && l.scanRun(raw, idx, lines, 1, match)
}

// scanRun walks away from the trigger one line at a time in one direction,
// stopping at the window edge or — for a contiguous law — at the first line
// that is neither a comment nor a single-line attribute.
func (l Law) scanRun(raw []string, idx, lines, step int, match func(string) bool) bool {
	for i := idx + step; i >= 0 && i < len(raw); i += step {
		if l.Contiguous {
			if !inCommentRun(raw[i], l.commentPrefix()) {
				return false
			}
		} else if i < idx-lines || i > idx+lines {
			return false
		}
		if match(raw[i]) {
			return true
		}
	}
	return false
}

// inCommentRun reports whether a line continues the comment block above a
// trigger: a comment, or a single-line attribute that sits between the comment
// and the item it decorates.
func inCommentRun(line, prefix string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, prefix) {
		return true
	}
	return (strings.HasPrefix(t, "#[") || strings.HasPrefix(t, "#![")) && strings.HasSuffix(t, "]")
}

// regexNearHits is markerHits' complement: a trigger is a hit only when the
// context pattern co-occurs within the window, rather than being excused by
// it. A trigger with no co-occurring context is never a hit — that is the
// whole reason this kind exists (see law.go's KindRegexNear doc).
func (l Law) regexNearHits(file string, raw, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) || !l.Matcher.Trigger.MatchString(line) || l.escaped(file, raw, i) {
			continue
		}
		if !l.contextNear(raw, i) {
			continue
		}
		hits = append(hits, l.hit(file, i+1, strings.TrimSpace(raw[i])))
	}
	return hits
}

// contextNear looks for the required context on the trigger's own line and
// in the window around it, in the configured direction — the same window
// walk markerAbove uses, with the polarity of the RESULT flipped by the
// caller rather than by this function.
func (l Law) contextNear(raw []string, idx int) bool {
	return l.foundNear(raw, idx, l.Matcher.Lines, l.Matcher.Direction, func(line string) bool {
		return l.Matcher.Context.MatchString(line)
	})
}

func (l Law) docPathHits(file string, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) {
			continue
		}
		for _, loc := range l.Matcher.Pattern.FindAllStringSubmatchIndex(line, -1) {
			// The LAST capture group is the citation, by convention (a law's
			// pattern may wrap it in non-capturing alternation groups first).
			gStart, gEnd := loc[len(loc)-2], loc[len(loc)-1]
			if gStart < 0 {
				continue
			}
			if gEnd < len(line) && isPathContinuation(line[gEnd]) {
				// A regex has no notion of "the whole backtick-quoted token" —
				// it just finds the longest run this pattern can describe, which
				// for `refs/notes/gate` is the PREFIX `refs/notes/` (a valid
				// Form-B shape on its own). A citation is the whole token, so a
				// match immediately followed by more identifier or glob
				// characters is a false start, not a shorter citation.
				continue
			}
			cited := line[gStart:gEnd]
			if l.docResolves(file, cited) {
				continue
			}
			hits = append(hits, l.hit(file, i+1, cited))
		}
	}
	return hits
}

// isPathContinuation reports whether b could continue the SAME path token a
// doc-path-resolves match just ended on — an identifier character, or a glob
// character (`.ratchet/laws/*.toml` must never be read as citing the bare
// directory `.ratchet/laws/`, with the glob silently dropped).
func isPathContinuation(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte(".-_/*?{}<>", b) >= 0
}

// docResolves accepts a citation that resolves relative to the CITING file's
// own directory, at the repo root, or inside the citing file's own unit
// (`crates/<x>/`, `tools/<x>/`) — the locality convention a workspace-root-only
// check would call dangling. Citing-file-relative is tried first because it is
// the form a markdown link is actually written in. A trailing-slash citation
// names a DIRECTORY, so resolution accepts either a file or a directory —
// unlike scope.include's ExplicitPaths, which names one file and only one.
func (l Law) docResolves(file, cited string) bool {
	citeDir := filepath.Dir(filepath.FromSlash(file))
	if l.citedResolves(filepath.Join(citeDir, filepath.FromSlash(cited))) {
		return true
	}
	if l.citedResolves(filepath.FromSlash(cited)) {
		return true
	}
	parts := strings.Split(normalizeSlashes(file), "/")
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "crates" && parts[0] != "tools" {
		return false
	}
	return l.citedResolves(filepath.Join(parts[0], parts[1], filepath.FromSlash(cited)))
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// pathExists accepts a file OR a directory — what a doc citation may name.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (l Law) hit(file string, line int, what string) Hit {
	key := file
	if l.Matcher.Key == KeyLineContent {
		key = file + " | " + what
	}
	return Hit{Law: l.Name, File: file, Line: line, Key: key, What: what, Weight: 1}
}

// escaped reports whether the law's escape comment sits on the offending line
// or within EscapeLines lines above it.
// escaped reports whether l's escape token genuinely opens a comment near
// the trigger — never a bare substring match, which would let the token
// forge from inside a string literal, a log message, or unrelated prose. On
// the trigger's own line the token must sit past where code ends (the same
// quote-aware split code_only pattern matching already trusts); on a line
// above, the trimmed line must itself open a comment — what inCommentRun
// already tests — regardless of Contiguous. The token alone is not enough
// either: escapeCarriesReason demands non-empty text after it, so a bare
// `// nan-safe:` with nothing else on the line does not suppress.
func (l Law) escaped(file string, raw []string, idx int) bool {
	if l.Escape == "" {
		return false
	}
	prefix, prose := escapePrefixFor(l, file)
	if prose {
		return l.escapedInProse(raw, idx)
	}
	if idx >= 0 && idx < len(raw) {
		_, comment := splitTrailingComment(raw[idx], prefix)
		if escapeCarriesReason(comment, l.Escape) {
			return true
		}
	}
	for i := idx - 1; i >= 0; i-- {
		if l.Contiguous {
			if !inCommentRun(raw[i], prefix) {
				return false
			}
		} else if i < idx-l.EscapeLines {
			return false
		}
		t := strings.TrimSpace(raw[i])
		if strings.HasPrefix(t, prefix) && escapeCarriesReason(raw[i], l.Escape) {
			return true
		}
	}
	return false
}

// escapeCarriesReason reports whether text holds token followed by
// non-empty text on the same line — the design contract's `escape: <token>
// <why>`, mirrored from how `mutation-accept` refuses an entry without a
// `# why`. A comment that stops at the token (`// nan-safe:` with nothing
// after it, possibly trailing whitespace) is presence with no reviewed
// reason, so it must not read as an escape at all.
func escapeCarriesReason(text, token string) bool {
	i := strings.Index(text, token)
	if i < 0 {
		return false
	}
	return strings.TrimSpace(text[i+len(token):]) != ""
}

func splitLines(content string) []string {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// splitTrailingComment splits a line at its trailing comment, opened by the
// law's own prefix (`//` for source, `#` for TOML and shell). A prefix inside
// a string literal is code, decided by a quote-parity scan that honours an
// escaped quote and the char-literal quote — so a URL in a string never reads
// as a comment.
func splitTrailingComment(line, prefix string) (code, comment string) {
	inString := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inString {
				i++
			}
		case '"':
			inString = !inString
		case '\'':
			if !inString && i+2 < len(line) && line[i+1] == '"' && line[i+2] == '\'' {
				i += 2
			}
		default:
			if !inString && strings.HasPrefix(line[i:], prefix) {
				return line[:i], line[i:]
			}
		}
	}
	return line, ""
}
