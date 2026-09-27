package undercover

import "strings"

// The undercover checks refuse ordinary technical prose along with the
// tells they exist to catch: a changelog line naming a model version, a
// commit quoting this table's own fixture text. There is no way to admit
// such a line except widening a tell itself, which is how a false positive
// becomes a false negative for everyone. `undercover-allow` is the scoped
// way through: a repo names the EXACT line and the reason it is ordinary,
// in its own config, never inline in the text that gets published — so a
// commit, PR or issue never carries the marker itself, only the words a
// human would write anyway.

// allowEntry is one `undercover-allow` line, parsed into the exact text it
// admits and the reason it carries.
type allowEntry struct {
	text, reason string
}

// parseAllow reads `undercover-allow` entries of the form "<exact line> #
// <reason>" — the same shape `mutation-accept` already uses in aphrollo.toml.
// An entry with no reason after the "#", or whose text alone still carries
// an Attribution tell, is dropped: a reason is not decoration, and no entry
// admits an identity, a trailer or a vendor session link no matter what it
// claims. strings.Cut sets reason ("" when the "#" is absent (found is
// false), so a missing separator is already caught by the reason check —
// there is no line the found flag alone would drop.
func parseAllow(raw []string) []allowEntry {
	var out []allowEntry
	for _, r := range raw {
		text, reason, _ := strings.Cut(r, "#")
		text = strings.TrimSpace(text)
		reason = strings.TrimSpace(reason)
		if text == "" || reason == "" {
			continue
		}
		if attributionHit(text) {
			continue
		}
		out = append(out, allowEntry{text: text, reason: reason})
	}
	return out
}

// attributionHit reports whether text alone already carries a tell this
// package never lets an allow-list entry suppress.
func attributionHit(text string) bool {
	for _, t := range Tells {
		if t.Attribution && t.Line != nil && t.Line.MatchString(text) {
			return true
		}
	}
	return false
}

// allowed reports whether the workspace's own config names line, trimmed,
// as an exact ordinary line.
func (l List) allowed(line string) bool {
	line = strings.TrimSpace(line)
	for _, a := range l.allow {
		if a.text == line {
			return true
		}
	}
	return false
}
