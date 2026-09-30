package lang

import "strings"

// Suppressed reports whether directives, a file's comment-preserving view,
// carry a suppression of the kind that rows name and that is not justified in
// place. A directive whose row names a reason syntax, such as a JavaScript lint
// disable followed within its own comment by a ` -- <description>`, says why
// and is admitted; any other form, or a bare disable beside a described one,
// still counts.
func Suppressed(rows []Language, kind, directives string) bool {
	closers := blockClosers(rows)
	for _, row := range rows {
		for _, d := range row.Suppress {
			if d.Kind != kind {
				continue
			}
			for _, loc := range d.Pattern.FindAllStringIndex(directives, -1) {
				if d.Reason == nil || !Reasoned(d, directives[loc[1]:], closers) {
					return true
				}
			}
		}
	}
	return false
}

// Reasoned reports whether the directive text after a directive's token
// carries its reason. The directive ends at its line's end or at a block
// comment's closer, so a separator further on belongs to other text.
func Reasoned(d Directive, rest string, closers []string) bool {
	if d.Reason == nil {
		return false
	}
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	for _, closer := range closers {
		if i := strings.Index(rest, closer); i >= 0 {
			rest = rest[:i]
		}
	}
	return d.Reason.MatchString(rest)
}

// blockClosers lists the block comment closers of rows, each once.
func blockClosers(rows []Language) []string {
	var closers []string
	seen := map[string]bool{}
	for _, row := range rows {
		for _, b := range row.BlockComments {
			if !seen[b.Close] {
				seen[b.Close] = true
				closers = append(closers, b.Close)
			}
		}
	}
	return closers
}
