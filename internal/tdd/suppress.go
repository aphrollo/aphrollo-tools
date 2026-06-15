package tdd

import "regexp"

// Suppression detectors catch an edit silencing a quality gate — the linter, the
// type checker, coverage. Unlike the test-oracle smells, these run against the
// DIRECTIVES view (string literals blanked, comments preserved), because the
// directives live in comments (//nolint, // @ts-ignore, # type: ignore). A
// directive quoted in a string is blanked and cannot trip.
//
// Suppressions are suppressionCat, not smellCat: a reviewed suppression is a
// legitimate (if rare) choice, so they only WARN at edit time and BLOCK at
// commit, where someone is about to vouch for the change. The commit gate
// additionally only inspects newly-ADDED lines (see precommit.go), so a
// suppression that already lived in the file never blocks a later, unrelated
// commit — only one this change introduces.

const (
	lintSuppressReason = "Edit introduces a linter suppression (//nolint, eslint-disable, # noqa, # pylint: disable, # rubocop: disable). " +
		"Silencing the linter hides the finding instead of fixing it. Remove the suppression and address the warning, or justify it in review."
	typeSuppressReason = "Edit introduces a type-checker suppression (// @ts-ignore, // @ts-nocheck, # type: ignore, # pyright: ignore). " +
		"Silencing the type checker buries a real type error. Fix the type, or justify the suppression in review."
	coverageSuppressReason = "Edit introduces a coverage suppression (istanbul ignore, c8/v8 ignore, # pragma: no cover). " +
		"Excluding code from coverage masks an untested path. Remove the marker and add a test for the path instead."
)

// lintSuppressRe matches linter-silencing directives across the supported
// ecosystems. eslint-disable is matched bare because the token appears only in
// that directive (all four forms: line, next-line, block-open, block-close).
var lintSuppressRe = regexp.MustCompile(`//\s*nolint|eslint-disable|#\s*noqa|#\s*pylint:\s*disable|#\s*rubocop:\s*disable|#\s*flake8`)

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
		hit: func(v view) bool { return lintSuppressRe.MatchString(v.directives) },
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
