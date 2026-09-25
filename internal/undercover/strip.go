package undercover

import (
	"regexp"
	"strings"
)

// Some environments append an attribution footer to a PR body or comment
// AFTER the text leaves the client, where no pre-post check can see it. The
// CI job reads what landed and strips that footer; a tell anywhere else is
// refused, never edited.

// footerShapes are the line shapes a footer takes: a `Key: value` trailer, a
// "Generated with/by" line, or a line that is only a URL or a markdown link.
// A line is stripped only when it has one of these shapes AND carries a tell.
var footerShapes = []*regexp.Regexp{
	regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:\s`),
	regexp.MustCompile(`(?i)^[\W_]*generated\s+(?:with|by)\b`),
	regexp.MustCompile(`^[\W_]*(?:https?://\S+|\[[^\]]*\]\([^)]*\))[\W_]*$`),
}

// separator is a markdown rule or a blank line between the prose and a footer.
var separator = regexp.MustCompile(`^(?:-{3,}|\*{3,}|_{3,})?$`)

// StripFooter removes the trailing run of footer lines that carry a tell,
// with the blank lines and rules around them, and answers the kept text and
// the stripped lines. The run ends at the first line from the bottom that is
// not such a footer; a tell above it stays for the caller to refuse. A body
// with nothing to strip comes back byte for byte.
func StripFooter(body string, tells List) (kept string, stripped []string) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	cut := len(lines)
	var found []string
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if separator.MatchString(line) {
			continue
		}
		if !footerShaped(line) {
			break
		}
		if _, hit := tells.Line(line); !hit {
			break
		}
		cut = i
		found = append([]string{line}, found...)
	}
	if len(found) == 0 {
		return body, nil
	}
	head := lines[:cut]
	for len(head) > 0 && separator.MatchString(strings.TrimSpace(head[len(head)-1])) {
		head = head[:len(head)-1]
	}
	return strings.Join(head, "\n"), found
}

func footerShaped(line string) bool {
	for _, re := range footerShapes {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}
