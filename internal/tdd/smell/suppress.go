package smell

import (
	"regexp"
	"strings"
)

// Suppression detectors catch an edit silencing a quality gate — the linter, the
// type checker, coverage. Unlike the test-oracle smells, these run against the
// DIRECTIVES view (string literals blanked, comments preserved), because the
// directives live in comments (//nolint, // @ts-ignore, # type: ignore). A
// directive quoted in a string is blanked and cannot trip.
// (reason: named here as the vocabulary this file recognizes.)
//
// Suppressions are suppressionCat, not smellCat: a reviewed suppression is a
// legitimate (if rare) choice, so they only WARN at edit time and BLOCK at
// commit, where someone is about to vouch for the change. The commit gate
// additionally only inspects newly-ADDED lines (see precommit.go), so a
// suppression that already lived in the file never blocks a later, unrelated
// commit — only one this change introduces.

const (
	lintSuppressReason = "Edit introduces a linter suppression (//nolint, eslint-disable, # noqa, # pylint: disable, # rubocop: disable). Silencing the linter hides the finding instead of fixing it. Remove the suppression and address the warning, or justify it in review; an eslint-disable carrying a ` -- <description>` is admitted."
	typeSuppressReason = "Edit introduces a type-checker suppression (// @ts-ignore, // @ts-nocheck, # type: ignore, # pyright: ignore). " +
		"Silencing the type checker buries a real type error. Fix the type, or justify the suppression in review."
	coverageSuppressReason = "Edit introduces a coverage suppression (istanbul ignore, c8/v8 ignore, # pragma: no cover). " +
		"Excluding code from coverage masks an untested path. Remove the marker and add a test for the path instead."
)

// lintSuppressRe matches linter-silencing directives across the supported
// ecosystems, except the JavaScript linter's disable directive, which
// lintDisableRe judges on its own.
var lintSuppressRe = regexp.MustCompile(`//\s*nolint|#\s*noqa|#\s*pylint:\s*disable|#\s*rubocop:\s*disable|#\s*flake8`)

// lintDisableRe matches the JavaScript linter's disable token, bare because
// it appears only in that directive (all its forms: line, next-line, block).
var lintDisableRe = regexp.MustCompile(`eslint-disable`)

// lintDisableDescriptionRe matches the directive's own description separator,
// two or more dashes set off by whitespace on both sides, followed by text.
// The linter reads what follows as the reason the rule is disabled.
var lintDisableDescriptionRe = regexp.MustCompile(`\s-{2,}\s+\S`)

// lintSuppressed reports whether directives carry a linter suppression that
// is not justified in place. A JavaScript lint disable followed, within its own
// comment, by a ` -- <description>` says why, so it is admitted; any other
// form, or a bare disable beside a described one, still counts.
func lintSuppressed(directives string) bool {
	if lintSuppressRe.MatchString(directives) {
		return true
	}
	for _, loc := range lintDisableRe.FindAllStringIndex(directives, -1) {
		if !describedDisable(directives[loc[1]:]) {
			return true
		}
	}
	return false
}

// describedDisable reports whether the directive text after a JavaScript
// lint-disable token carries a description. The directive ends at its
// line's end or at the block comment's closer, so a separator further on
// belongs to other text.
func describedDisable(rest string) bool {
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, "*/"); i >= 0 {
		rest = rest[:i]
	}
	return lintDisableDescriptionRe.MatchString(rest)
}

// typeSuppressRe matches type-checker-silencing directives. @ts-expect-error is
// deliberately excluded: it asserts a following error and is a legitimate way to
// test type behavior, so blocking it at commit would punish correct usage.
var typeSuppressRe = regexp.MustCompile(`@ts-ignore|@ts-nocheck|#\s*type:\s*ignore|#\s*pyright:\s*ignore`)

// coverageSuppressRe matches coverage-exclusion markers (Istanbul, c8/v8, the
// Python coverage pragma).
var coverageSuppressRe = regexp.MustCompile(`istanbul\s+ignore|\b[cv]8\s+ignore|#\s*pragma:\s*no\s*cover`)

var (
	lintSuppressPolicy = policy{
		name: "lint-suppress", category: suppressionCat, reason: lintSuppressReason,
		hit: func(v view) bool { return lintSuppressed(v.directives) },
	}
	typeSuppressPolicy = policy{
		name: "type-suppress", category: suppressionCat, reason: typeSuppressReason,
		hit: func(v view) bool { return typeSuppressRe.MatchString(v.directives) },
	}
	coverageSuppressPolicy = policy{
		name: "coverage-suppress", category: suppressionCat, reason: coverageSuppressReason,
		hit: func(v view) bool { return coverageSuppressRe.MatchString(v.directives) },
	}
)

// suppressionPolicies gate any code file — source or test. A suppression
// silences a quality gate regardless of which kind of file it lands in, so
// unlike the oracle smells these are not test-file-only.
var suppressionPolicies = []policy{lintSuppressPolicy, typeSuppressPolicy, coverageSuppressPolicy}
