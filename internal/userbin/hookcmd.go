package userbin

import (
	"fmt"
	"path/filepath"
	"strings"
)

// shQuote single-quotes s for POSIX sh, slash-normalized: a raw Windows path's
// backslashes are escapes to the shell a hook runs in.
func shQuote(s string) string {
	s = filepath.ToSlash(s)
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// HookCommand is the one-line sh command a settings.json hook runs: resolve
// the binary (user-space current, then fallback), then run it with args. A
// binary found nowhere, or one that outruns budgetSecs (timeout(1), where the
// box has it), is a no-op that exits 0 with one stderr line, never a failed
// hook. A binary's own exit code, a deny included, passes through. Nothing
// here downloads.
func HookCommand(root, fallback string, budgetSecs int, args string) string {
	var b strings.Builder
	b.WriteString("aphrollo_root=" + shQuote(root) + "; aphrollo_fallback=" + shQuote(fallback) + "; ")
	b.WriteString(`x="$aphrollo_root/$(cat "$aphrollo_root/` + pointerName + `" 2>/dev/null)/` + BinName + ExeSuffix + `"; `)
	b.WriteString(`[ -x "$x" ] || x="$aphrollo_fallback"; `)
	b.WriteString(`[ -x "$x" ] || { echo "aphrollo: no binary found (run: aphrollo update), hook skipped" >&2; exit 0; }; `)
	if budgetSecs > 0 {
		fmt.Fprintf(&b, `if command -v timeout >/dev/null 2>&1; then timeout %d "$x" %s; r=$?; `+
			`[ "$r" -eq 124 ] && { echo "aphrollo: hook over its %ds budget, skipped" >&2; exit 0; }; exit "$r"; fi; `, budgetSecs, args, budgetSecs)
	}
	b.WriteString(`exec "$x" ` + args)
	return b.String()
}
