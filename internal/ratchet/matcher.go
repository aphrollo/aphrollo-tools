package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// HitsIn applies a law's matcher to one in-scope file's content. It is pure:
// the same path and content always produce the same hits, which is what makes
// the pre-edit path (content that is not on disk yet) and the cached tree scan
// the same code. Registry laws are the exception — they judge the WHOLE scope
// at once and are answered by the checker, not here.
func (l Law) HitsIn(file, content string) []Hit {
	raw := splitLines(content)
	code := raw
	if l.CodeOnly {
		code = make([]string, len(raw))
		for i := range raw {
			code[i], _ = splitTrailingComment(raw[i], l.commentPrefix())
		}
	}
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
	case KindDocPathResolves:
		return l.docPathHits(file, code)
	}
	return nil
}

func (l Law) lineCountHits(file string, raw []string) []Hit {
	n := len(raw)
	if n <= l.Matcher.Max {
		return nil
	}
	return []Hit{{
		Law:    l.Name,
		File:   file,
		Key:    file,
		What:   fmt.Sprintf("%d lines (max %d)", n, l.Matcher.Max),
		Weight: n,
	}}
}

func (l Law) regexAbsentHits(file string, raw, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) || !l.Matcher.Pattern.MatchString(line) || l.escaped(raw, i) {
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

func (l Law) markerHits(file string, raw, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) || !l.Matcher.Trigger.MatchString(line) || l.escaped(raw, i) {
			continue
		}
		if l.markerAbove(raw, i) {
			continue
		}
		hits = append(hits, l.hit(file, i+1, strings.TrimSpace(raw[i])))
	}
	return hits
}

// markerAbove looks for the required marker on the trigger's own line and in
// the window above it: the contiguous comment run when the law asks for one,
// `lines` lines otherwise.
func (l Law) markerAbove(raw []string, idx int) bool {
	return l.foundNear(raw, idx, l.Matcher.Lines, l.Matcher.Direction, func(line string) bool {
		return l.Matcher.Marker.MatchString(line)
	})
}

// foundAbove scans the trigger's own line and then upward. A contiguous law
// stops at the first line that is neither a comment nor a single-line
// attribute, so a marker never reaches across code it does not describe.
func (l Law) foundNear(raw []string, idx, lines int, dir Direction, match func(string) bool) bool {
	if idx < len(raw) && match(raw[idx]) {
		return true
	}
	if dir != DirectionBelow && l.scanRun(raw, idx, lines, -1, match) {
		return true
	}
	return (dir == DirectionBelow || dir == DirectionBoth) && l.scanRun(raw, idx, lines, 1, match)
}

// foundAbove is the escape window: an escape comment only ever sits above.
func (l Law) foundAbove(raw []string, idx, lines int, match func(string) bool) bool {
	return l.foundNear(raw, idx, lines, DirectionAbove, match)
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

func (l Law) docPathHits(file string, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) {
			continue
		}
		for _, m := range l.Matcher.Pattern.FindAllStringSubmatch(line, -1) {
			cited := m[len(m)-1]
			if l.docResolves(file, cited) {
				continue
			}
			hits = append(hits, l.hit(file, i+1, cited))
		}
	}
	return hits
}

// docResolves accepts a citation that resolves at the repo root or inside the
// citing file's own unit (`crates/<x>/`, `tools/<x>/`) — the locality
// convention a workspace-root-only check would call dangling.
func (l Law) docResolves(file, cited string) bool {
	root := l.Root
	if root == "" {
		root = "."
	}
	if isFile(filepath.Join(root, filepath.FromSlash(cited))) {
		return true
	}
	parts := strings.Split(normalizeSlashes(file), "/")
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "crates" && parts[0] != "tools" {
		return false
	}
	return isFile(filepath.Join(root, parts[0], parts[1], filepath.FromSlash(cited)))
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
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
func (l Law) escaped(raw []string, idx int) bool {
	if l.Escape == "" {
		return false
	}
	return l.foundAbove(raw, idx, l.EscapeLines, func(line string) bool {
		return strings.Contains(line, l.Escape)
	})
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
