package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/mask"
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
const ScanViewStamp = "# scan-view: 2"

// scanViewExts are the file kinds whose lexer changed: a law over none of
// them reads the same bytes under either view.
var scanViewExts = []string{".py", ".sh", ".toml", ".rb", ".yaml", ".yml"}

// HasScanViewStamp reports whether a baseline's text carries a scan-view
// stamp at 2 or later; the staged-baseline guard reads it too.
func HasScanViewStamp(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if n, ok := strings.CutPrefix(strings.TrimSpace(line), "# scan-view:"); ok {
			if v, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && v >= 2 {
				return true
			}
		}
	}
	return false
}

// viewSensitive reports whether the choice of lexer can change what l sees: it
// reads a string-masked view of a file kind whose lexer changed.
func (l Law) viewSensitive() bool {
	if !l.MaskStrings {
		return false
	}
	for _, glob := range l.Scope.Include {
		glob = strings.ToLower(glob)
		for _, ext := range scanViewExts {
			if strings.Contains(glob, ext) {
				return true
			}
		}
	}
	return false
}

// legacyBaseline reports whether l is judged by the lexers its baseline was
// written under: it can tell the lexers apart, and its baseline file exists
// without a stamp. A law with no baseline file yet has no history to keep. The
// baseline is read from proposed when it holds it, as loadLawBaseline does.
func legacyBaseline(root string, proposed map[string]string, l Law) bool {
	if l.Baseline == "" || !l.viewSensitive() {
		return false
	}
	if text, ok := proposed[l.Baseline]; ok {
		return !HasScanViewStamp(text)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(l.Baseline)))
	return err == nil && !HasScanViewStamp(string(data))
}

// legacyNote names the way out for a law that is still judged by the old
// lexers, so the view never lingers unnoticed.
func legacyNote(l Law) string {
	return fmt.Sprintf("%s: its baseline carries no `%s` and is judged by the lexers it was written under, "+
		"which read a quote inside a `#` comment as opening a string; a tightening `aphrollo ratchet check` "+
		"migrates it to the current lexers once the tree is at or below that baseline", l.Name, ScanViewStamp)
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
// baselineRel is stamped already, declared by no law, or belongs to a law a
// lexer change cannot move: there is no migration to recompute.
func MigratedBaselineText(opts Options, baselineRel, legacyText string) (text string, ok bool, err error) {
	if HasScanViewStamp(legacyText) {
		return "", false, nil
	}
	laws, err := LoadLaws(opts.Root)
	if err != nil {
		return "", false, err
	}
	for _, law := range laws {
		if law.Baseline != baselineRel || law.UnknownKind != "" || !law.viewSensitive() {
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

// Stamp puts the scan-view stamp at the head of the baseline when it has none.
func (b *Baseline) Stamp() {
	for _, l := range b.lines {
		if !l.data && strings.TrimSpace(l.verbatim) == ScanViewStamp {
			return
		}
	}
	b.lines = append([]baselineLine{{verbatim: ScanViewStamp}}, b.lines...)
}

// maskerFor picks the string masker a file's extension calls for. Rust reads
// a lifetime's `'` as code, not as a quote that blanks every line up to the
// next apostrophe. Python, shell, TOML, Ruby and YAML read `#` as a comment, so an
// apostrophe or a quote inside one opens no string; read as code, it blanks
// the lines below it just the same, and a law reports nothing over code it
// never saw. Every other file keeps the language-neutral lexer.
func maskerFor(file string) func(string) string {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".rs":
		return func(src string) string { return mask.RustTokens(src, true, false) }
	case ".py":
		return func(src string) string { return mask.PythonTokens(src, true, false) }
	case ".sh":
		return func(src string) string { return mask.ShellTokens(src, true, false) }
	case ".toml":
		return func(src string) string { return mask.TOMLTokens(src, true, false) }
	case ".rb":
		return func(src string) string { return mask.RubyTokens(src, true, false) }
	case ".yaml", ".yml":
		return func(src string) string { return mask.YAMLTokens(src, true, false) }
	}
	return func(src string) string { return mask.Tokens(src, true, false, false) }
}

// legacyMaskerFor is maskerFor as it stood before Python, shell and TOML (and
// Ruby and YAML after them) read `#` as a comment: only Rust has a lexer of
// its own, and every other file is read by the language-neutral one.
func legacyMaskerFor(file string) func(string) string {
	if strings.EqualFold(filepath.Ext(file), ".rs") {
		return func(src string) string { return mask.RustTokens(src, true, false) }
	}
	return func(src string) string { return mask.Tokens(src, true, false, false) }
}
