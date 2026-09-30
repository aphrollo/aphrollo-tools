package smell

import (
	"sync"

	langtable "github.com/aphrollo/aphrollo-tools/internal/lang"
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

// The directives themselves are rows of the language table (internal/lang):
// each language lists the comments that silence its linter, type checker and
// coverage tool, with the syntax of the reason that admits one. Every row's
// directives are read in every file, since one file can carry another
// language's comment and a language with no row of its own is still checked;
// adding a language adds its directives with no change here.

// embeddedRows is the embedded table's rows; an embedded table that does not
// load has none, and every test of the table reports that failure.
var embeddedRows = sync.OnceValue(func() []langtable.Language {
	tbl, err := langtable.Defaults()
	if err != nil {
		return nil
	}
	return tbl.Rows()
})

// suppressed reports whether the view's directives carry a suppression of the
// kind, that is not justified in place, in any row's vocabulary: the rows of
// the table the view's file was read by, else the embedded ones.
func suppressed(kind string, v view) bool {
	rows := v.rows
	if rows == nil {
		rows = embeddedRows()
	}
	return langtable.Suppressed(rows, kind, v.directives)
}

var (
	lintSuppressPolicy = policy{
		name: "lint-suppress", category: suppressionCat, reason: lintSuppressReason, directive: true,
		hit: func(v view) bool { return suppressed(langtable.KindLint, v) },
	}
	typeSuppressPolicy = policy{
		name: "type-suppress", category: suppressionCat, reason: typeSuppressReason, directive: true,
		hit: func(v view) bool { return suppressed(langtable.KindType, v) },
	}
	coverageSuppressPolicy = policy{
		name: "coverage-suppress", category: suppressionCat, reason: coverageSuppressReason, directive: true,
		hit: func(v view) bool { return suppressed(langtable.KindCoverage, v) },
	}
)

// suppressionPolicies gate any code file — source or test. A suppression
// silences a quality gate regardless of which kind of file it lands in, so
// unlike the oracle smells these are not test-file-only.
var suppressionPolicies = []policy{lintSuppressPolicy, typeSuppressPolicy, coverageSuppressPolicy}
