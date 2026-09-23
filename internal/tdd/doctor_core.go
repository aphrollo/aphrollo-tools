package tdd

import (
	"path/filepath"
	"runtime"
	"strings"
)

// samePath compares two directory paths the way the OS resolves them:
// case-insensitively on Windows, and with separators and trailing slashes
// normalized everywhere.
func samePath(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(filepath.FromSlash(strings.Trim(p, `"`)))
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	return clean(a) == clean(b)
}

// sortStrings sorts in place; the report has to read the same way twice.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
