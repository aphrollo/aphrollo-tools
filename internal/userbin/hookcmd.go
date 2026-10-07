package userbin

import (
	"fmt"
	"strings"
)

// shQuote single-quotes s for POSIX sh, slash-normalized: a raw Windows path's
// backslashes are escapes to the shell a hook runs in. The replacement is
// literal rather than filepath.ToSlash, which is a no-op off Windows, so a
// Windows path is normalized whichever OS writes the command.
func shQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// HookCommand is the one-line sh command a settings.json hook runs: resolve
// the binary (user-space current, then fallback), then run it with args. A
// binary found nowhere, or one that outruns budgetSecs (GNU timeout(1), where the
// box has it), is a no-op that exits 0 with one stderr line, never a failed
// hook. A binary's own exit code, a deny included, passes through. Nothing
// here downloads.
func HookCommand(root, fallback string, budgetSecs int, args string) string {
	var b strings.Builder
	b.WriteString("aphrollo_root=" + shQuote(root) + "; aphrollo_fallback=" + shQuote(fallback) + "; ")
	b.WriteString(`x="$aphrollo_root/$(cat "$aphrollo_root/` + pointerName + `" 2>/dev/null)/` + BinName + ExeSuffix + `"; `)
	b.WriteString(`[ -x "$x" ] || x="$aphrollo_fallback"; `)
	b.WriteString(`[ -x "$x" ] || { echo "aphrollo: no binary found (run: aphrollo update), hook skipped" >&2; exit 0; }; `)
	if budgetSecs <= 0 {
		b.WriteString(`exec "$x" ` + args)
		return b.String()
	}
	fmt.Fprintf(&b, `t=""; timeout --version >/dev/null 2>&1 && t="timeout %d"; $t "$x" %s; r=$?; `+
		`[ "$r" -eq 124 ] && { echo "aphrollo: hook over its %ds budget, skipped" >&2; exit 0; }; exit "$r"`, budgetSecs, args, budgetSecs)
	return b.String()
}

// Prelude is the sh text a shim starts with: it sets $x to the binary to run,
// the user-space current under root when its file is there, else fallback.
// The two assignment lines are what doctor reads back.
func Prelude(root, fallback string) string {
	return "aphrollo_root=" + shQuote(root) + "\n" +
		"aphrollo_fallback=" + shQuote(fallback) + "\n" +
		`x="$aphrollo_root/$(cat "$aphrollo_root/` + pointerName + `" 2>/dev/null)/` + BinName + ExeSuffix + `"` + "\n" +
		`[ -x "$x" ] || x="$aphrollo_fallback"` + "\n"
}

// Launch is the root and fallback a hook command or shim carries, read back
// from its text; ok is false for text this package did not write.
func Launch(text string) (root, fallback string, ok bool) {
	var haveRoot, haveFallback bool
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, ";")
		for _, field := range strings.Split(line, "; ") {
			if v, found := strings.CutPrefix(field, "aphrollo_root="); found {
				root, haveRoot = shUnquote(v), true
			}
			if v, found := strings.CutPrefix(field, "aphrollo_fallback="); found {
				fallback, haveFallback = shUnquote(v), true
			}
		}
	}
	return root, fallback, haveRoot && haveFallback
}

func shUnquote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "'")
	s = strings.TrimSuffix(s, "'")
	return strings.ReplaceAll(s, `'\''`, "'")
}
