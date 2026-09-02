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
			code[i], _ = splitTrailingComment(raw[i])
		}
	}
	switch l.Matcher.Kind {
	case KindLineCount:
		return l.lineCountHits(file, raw)
	case KindRegexAbsent:
		return l.regexAbsentHits(file, raw, code)
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
		if !l.Matcher.Pattern.MatchString(line) || l.escaped(raw, i) {
			continue
		}
		hits = append(hits, l.hit(file, i+1, strings.TrimSpace(raw[i])))
	}
	return hits
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
		if !l.Matcher.Trigger.MatchString(line) || l.escaped(raw, i) {
			continue
		}
		if l.markerAbove(raw, i) {
			continue
		}
		hits = append(hits, l.hit(file, i+1, strings.TrimSpace(raw[i])))
	}
	return hits
}

// markerAbove looks for the required marker on the trigger's own line or
// within `lines` lines above it.
func (l Law) markerAbove(raw []string, idx int) bool {
	for i := idx; i >= 0 && i >= idx-l.Matcher.Lines; i-- {
		if l.Matcher.Marker.MatchString(raw[i]) {
			return true
		}
	}
	return false
}

func (l Law) docPathHits(file string, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
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
	for i := idx; i >= 0 && i >= idx-l.EscapeLines; i-- {
		if strings.Contains(raw[i], l.Escape) {
			return true
		}
	}
	return false
}

func splitLines(content string) []string {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// splitTrailingComment splits a source line at its trailing `//` comment. A
// `//` inside a string literal is code, decided by a quote-parity scan that
// honours `\"` and the `'"'` char literal — so a URL in a string never reads
// as a comment.
func splitTrailingComment(line string) (code, comment string) {
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
		case '/':
			if !inString && i+1 < len(line) && line[i+1] == '/' {
				return line[:i], line[i:]
			}
		}
	}
	return line, ""
}
