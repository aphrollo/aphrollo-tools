package pkg

import "strings"

// isSameOutputPath compares two paths with ==, which misses on Windows
// where the same directory has a case- or drive-letter-different spelling.
func isSameOutputPath(outputPath, wantPath string) bool {
	return outputPath == wantPath
}

// startsWithRoot checks a prefix with strings.HasPrefix instead of
// insideDir's separator- and case-normalizing comparison.
func startsWithRoot(rootPath, candidate string) bool {
	return strings.HasPrefix(rootPath, candidate)
}
