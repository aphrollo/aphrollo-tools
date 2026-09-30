package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// ScanViewStamp is the line a baseline carries once it was written by the
// lexers that read `#` as a comment in Python, shell, TOML, Ruby and YAML. A
// baseline without it records what the lexers before them read: over a file
// with an apostrophe in a comment they blanked every line down to the next
// apostrophe, so its ceilings never counted a hit on those lines. Reading the
// same tree with the new lexers finds those hits and every one is a
// regression against a ceiling of zero, on a tree nobody touched. So a
// baseline without the stamp is judged by the lexers it was written under
// (Law.LegacyView) until a tightening `ratchet check` finds the tree at or
// below that baseline and migrates it: it records what the new lexers read
// and stamps it.
//
// A stamp carries a view number, and the language table says which view each
// row's lexing took effect at (lang.Language.View): a baseline over Java files
// is stamped 3, the view its row took effect at, because the lexers before it
// read Java as the default row. A law is judged by the lexers of its
// baseline's view until it migrates, whichever rows were added since.
const ScanViewStamp = "# scan-view: 2"

// scanViewPrefix opens a stamp line, whatever view it names.
const scanViewPrefix = "# scan-view:"

// stampedView is the view a stamp line names.
func stampedView(line string) (int, bool) {
	n, ok := strings.CutPrefix(strings.TrimSpace(line), scanViewPrefix)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(n))
	return v, err == nil
}

// ScanViewOf is the view a baseline's text was written under: the highest its
// stamp lines name, and 1, the lexers before any stamp, when it carries none.
func ScanViewOf(text string) int {
	view := 1
	for _, line := range strings.Split(text, "\n") {
		if v, ok := stampedView(line); ok {
			view = max(view, v)
		}
	}
	return view
}

// HasScanViewStamp reports whether a baseline's text carries a scan-view
// stamp at 2 or later; the staged-baseline guard reads it too.
func HasScanViewStamp(text string) bool {
	return ScanViewOf(text) >= 2
}

// languages is the language table of the repository l belongs to: the embedded
// defaults and its own `.ratchet/languages` rows. LoadLaws has already refused
// a repository whose rows do not load, so a failure here can only be the
// embedded defaults, a build defect that stops loudly.
func (l Law) languages() *lang.Table {
	if tbl, err := lang.ForRoot(l.Root); err == nil {
		return tbl
	}
	tbl, err := lang.Defaults()
	if err != nil {
		panic(fmt.Sprintf("ratchet: the embedded language table does not load: %v", err))
	}
	return tbl
}

// globNamesExt reports whether a scope glob names the file extension: the
// extension stands at the glob's end or before a character no file name
// extension holds, so `.cs` is in `**/*.cs` and `*.{cs,py}` and not in
// `**/*.css`.
func globNamesExt(glob, ext string) bool {
	rest := glob
	for range len(glob) {
		i := strings.Index(rest, ext)
		if i < 0 {
			return false
		}
		after := i + len(ext)
		if after == len(rest) || !isExtByte(rest[after]) {
			return true
		}
		rest = rest[after:]
	}
	return false
}

func isExtByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// touchedView is the view a baseline over l's files is read at by the current
// lexers: the latest view of the language rows l's scope names, for a law
// whose view of a file depends on its lexer — one that reads strings masked,
// or blanks the comments of a `//`-comment language. 1 means no lexer moved
// under it since the baselines began.
func (l Law) touchedView() int {
	slashComments := l.CodeOnly && l.commentPrefix() == "//"
	if !l.MaskStrings && !slashComments {
		return 1
	}
	touches := func(ext string) bool {
		for _, glob := range l.Scope.Include {
			if globNamesExt(strings.ToLower(glob), ext) {
				return true
			}
		}
		return false
	}
	return l.languages().ViewFor(touches, !l.MaskStrings)
}

// viewSensitive reports whether the choice of lexer can change what l sees: a
// row its scope names took effect after the first baselines.
func (l Law) viewSensitive() bool {
	return l.touchedView() > 1
}

// scanView is the view l's files are read under: 0 for the current lexers, the
// view its baseline was written under for a law still judged by those.
func (l Law) scanView() int {
	if !l.LegacyView {
		return 0
	}
	if l.ViewVersion > 0 {
		return l.ViewVersion
	}
	return 1
}

// baselineView is the view l's baseline was written under, and whether the
// current lexers read l's files differently from it: the law can tell the
// lexers apart and its baseline file exists stamped below the view they reach.
// A law with no baseline file yet has no history to keep. The baseline is read
// from proposed when it holds it, as loadLawBaseline does.
func baselineView(root string, proposed map[string]string, l Law) (view int, stale bool) {
	if l.Baseline == "" {
		return 0, false
	}
	touched := l.touchedView()
	if touched <= 1 {
		return 0, false
	}
	text, ok := proposed[l.Baseline]
	if !ok {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(l.Baseline)))
		if err != nil {
			return 0, false
		}
		text = string(data)
	}
	view = ScanViewOf(text)
	return view, touched > view
}

// legacyBaseline reports whether l is judged by the lexers its baseline was
// written under.
func legacyBaseline(root string, proposed map[string]string, l Law) bool {
	_, stale := baselineView(root, proposed, l)
	return stale
}

// legacyNote names the way out for a law that is still judged by the old
// lexers, so the view never lingers unnoticed.
func legacyNote(l Law) string {
	return fmt.Sprintf("%s: its baseline is stamped below `%s` and is judged by the lexers it was written under, "+
		"which read a quote inside a `#` comment as opening a string and know none of the newer language rows; "+
		"a tightening `aphrollo ratchet check` migrates it to the current lexers once the tree is at or below that baseline", l.Name, stampFor(l.touchedView()))
}

// stampFor is the stamp line of a view.
func stampFor(view int) string {
	return fmt.Sprintf("%s %d", scanViewPrefix, view)
}

// migratedNote is the one line a migration reports.
func migratedNote(l Law, rows int) string {
	return fmt.Sprintf("migrated %s to the current lexers (%d rows)", l.Name, rows)
}

// pendingMigration is a legacy law whose tree is at or below its baseline as
// the old lexers read it, so the baseline may move onto the current ones.
type pendingMigration struct {
	law      Law
	baseline *Baseline
	path     string
}

// commitMigrations rewrites each pending baseline under the current lexers
// with the stamp, and returns the baseline files written and one note per law.
// Called only once the whole run is regression-free, like commitTightened.
func commitMigrations(opts Options, pending []pendingMigration) (written, notes []string, err error) {
	for _, m := range pending {
		law := m.law
		law.LegacyView = false
		hits, err := adoptHitsIn(Options{Root: opts.Root, Tracked: opts.Tracked, TrackedIgnored: opts.TrackedIgnored}, law)
		if err != nil {
			return nil, nil, err
		}
		rows := adoptOnto(m.baseline, law, hits)
		wrote, err := m.baseline.WriteIfChanged(m.path)
		if err != nil {
			return nil, nil, err
		}
		if wrote {
			written = append(written, m.law.Baseline)
		}
		notes = append(notes, migratedNote(m.law, rows))
	}
	return written, notes, nil
}

// MigratedBaselineText is the baseline a legacy one becomes under the current
// lexers, recomputed from the tree opts describes (the staged tree, for the
// commit guard): exactly what a tightening check writes. ok is false when
// baselineRel is declared by no law, or belongs to a law a lexer change cannot
// move or that its stamp already reads under the current lexers: there is no
// migration to recompute.
func MigratedBaselineText(opts Options, baselineRel, legacyText string) (text string, ok bool, err error) {
	laws, err := LoadLaws(opts.Root)
	if err != nil {
		return "", false, err
	}
	for _, law := range laws {
		if law.Baseline != baselineRel || law.UnknownKind != "" || law.touchedView() <= ScanViewOf(legacyText) {
			continue
		}
		baseline, err := ParseBaseline(legacyText, baselineForm(law))
		if err != nil {
			return "", false, err
		}
		hits, err := adoptHitsIn(opts, law)
		if err != nil {
			return "", false, err
		}
		adoptOnto(baseline, law, hits)
		return baseline.Render(), true, nil
	}
	return "", false, nil
}

// Stamp puts the first scan-view stamp, view 2, at the head of the baseline
// when it has none.
func (b *Baseline) Stamp() {
	b.StampAt(2)
}

// StampAt puts the stamp of view at the head of the baseline, or raises the
// stamp it carries to view; a stamp already at or above view stays.
func (b *Baseline) StampAt(view int) {
	for i, l := range b.lines {
		if l.data {
			continue
		}
		if v, ok := stampedView(l.verbatim); ok {
			if v < view {
				b.lines[i].verbatim = stampFor(view)
			}
			return
		}
	}
	b.lines = append([]baselineLine{{verbatim: stampFor(view)}}, b.lines...)
}
