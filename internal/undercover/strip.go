package undercover

import (
	"regexp"
	"strings"
)

// Some environments append an attribution footer to a PR body or comment
// AFTER the text leaves the client, where no pre-post check can see it. The
// CI job reads what landed and strips that footer; a tell anywhere else is
// refused, never edited.

// footerShapes are the only lines StripFooter removes, each a tell by its
// shape alone: a Co-authored-by trailer naming a vendor address, a
// "Generated with/by <tool>" line with or without a link, a vendor session
// URL alone on its line, and the session trailer carrying one. A human line
// that merely mentions a tool (`Note: this also fixes the Claude Code shim`)
// is none of these: it fails the check and is never edited.
var footerShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^co-authored-by:.*` + vendorAddressExpr),
	regexp.MustCompile(generatedWith),
	regexp.MustCompile(`(?i)^(?:claude-session:\s*)?[<(\[_*]*` + vendorSessionURL + `[>)\]_*.]*$`),
}

// vendorSessionURL is a link into a vendor's own session or product pages.
const vendorSessionURL = `https?://(?:[\w-]+\.)*(?:claude\.ai|claude\.com|anthropic\.com)(?:/\S*)?`

// separator is a markdown rule or a blank line between the prose and a footer.
var separator = regexp.MustCompile(`^(?:-{3,}|\*{3,}|_{3,})?$`)

// StripFooter removes the trailing run of footer lines that carry a tell,
// with the blank lines and rules directly above them, and answers the kept text and
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
