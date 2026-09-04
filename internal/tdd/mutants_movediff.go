package tdd

import (
	"regexp"
	"strconv"
	"strings"
)

// A crate-topology lane MOVES code: a module leaves one crate and arrives in
// another, byte for byte, with no behaviour change at all. To `--in-diff`
// every one of those lines is a changed line, so a split lane would mutate
// thousands of lines nobody touched — hours of measurement for a diff that
// says nothing about behaviour.
//
// git already knows which lines only moved. The lane diff is therefore taken
// with move detection ON, and every line git marks as moved is dropped before
// the diff reaches the runner:
//
//	--color-moved=plain                       — mark moved lines
//	--color-moved-ws=allow-indentation-change — a re-indented move is a move
//	-M                                        — a whole renamed file is a move
//
// The colours are pinned on the command line rather than read from the box's
// git config, because this parses them: a user who recoloured their diffs
// would otherwise silently turn the filter off.

// diffMovedColours are the escape sequences the pinned config produces for a
// line git considers moved: cyan for an added one, magenta for a removed one.
var diffMovedColours = map[string]bool{"36": true, "35": true, "1;36": true, "1;35": true}

// ansiPrefix captures the escape sequences a diff line starts with.
var ansiPrefix = regexp.MustCompile(`^(?:\x1b\[([0-9;]*)m)+`)

// ansiAny matches every escape sequence, for stripping.
var ansiAny = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// moveAwareDiff is the lane's diff with the moved lines taken out, and how
// many lines that was.
func moveAwareDiff(repoRoot, base, tip string, files []string) (string, int) {
	args := []string{
		"-c", "color.diff.new=green", "-c", "color.diff.old=red",
		"-c", "color.diff.newMoved=cyan", "-c", "color.diff.oldMoved=magenta",
		"-c", "color.diff.newMovedAlternative=cyan", "-c", "color.diff.oldMovedAlternative=magenta",
		"diff", "--color=always", "-M",
		"--color-moved=plain", "--color-moved-ws=allow-indentation-change",
		base, tip, "--",
	}
	out, err := git(repoRoot, append(args, files...)...)
	if err != nil {
		return "", 0
	}
	return filterMovedLines(out)
}

// filterMovedLines rewrites a coloured diff into a plain one with every moved
// line removed, and reports how many were removed.
//
// A moved ADDED line becomes context: it exists in the new file and is not
// worth mutating, and keeping it as context leaves the file's line numbering —
// which is what a mutant's identity is built on — exactly where it was. A
// moved REMOVED line is dropped outright; it is not in the new file at all.
// A hunk left with no real addition is dropped, and a file left with no hunk
// goes with it.
func filterMovedLines(coloured string) (string, int) {
	var out []string
	var fileHeader, hunkHeader []string
	var hunkLines []string
	hunkChanged, moved := false, 0
	oldStart, newStart := 0, 0

	flushHunk := func() {
		if hunkChanged && len(hunkLines) > 0 {
			oldCount, newCount := 0, 0
			for _, l := range hunkLines {
				switch {
				case strings.HasPrefix(l, "+"):
					newCount++
				case strings.HasPrefix(l, "-"):
					oldCount++
				default:
					oldCount++
					newCount++
				}
			}
			if len(fileHeader) > 0 {
				out = append(out, fileHeader...)
				fileHeader = nil
			}
			out = append(out, "@@ -"+hunkRange(oldStart, oldCount)+" +"+hunkRange(newStart, newCount)+" @@")
			out = append(out, hunkLines...)
		}
		hunkLines, hunkChanged = nil, false
	}

	for _, raw := range strings.Split(strings.ReplaceAll(coloured, "\r\n", "\n"), "\n") {
		colour := ""
		if m := ansiPrefix.FindStringSubmatch(raw); m != nil {
			colour = m[1]
		}
		line := ansiAny.ReplaceAllString(raw, "")
		switch {
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			oldStart, newStart = hunkStarts(line)
		case strings.HasPrefix(line, "diff --git"):
			flushHunk()
			fileHeader = []string{line}
			hunkHeader = nil
		case fileHeader != nil && len(hunkHeader) == 0 &&
			(strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "--- ") ||
				strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "similarity index") ||
				strings.HasPrefix(line, "rename ") || strings.HasPrefix(line, "new file") ||
				strings.HasPrefix(line, "deleted file")):
			fileHeader = append(fileHeader, line)
		case strings.HasPrefix(line, "+"):
			if diffMovedColours[colour] {
				moved++
				hunkLines = append(hunkLines, " "+line[1:])
				continue
			}
			hunkLines = append(hunkLines, line)
			hunkChanged = true
		case strings.HasPrefix(line, "-"):
			if diffMovedColours[colour] {
				moved++
				continue
			}
			hunkLines = append(hunkLines, line)
			hunkChanged = true
		case strings.HasPrefix(line, " ") || line == "":
			if len(hunkLines) > 0 || oldStart > 0 {
				hunkLines = append(hunkLines, line)
			}
		}
	}
	flushHunk()
	if len(out) == 0 {
		return "", moved
	}
	return strings.Join(out, "\n") + "\n", moved
}

// hunkRange renders one side of a hunk header.
func hunkRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// hunkStarts reads the two starting line numbers out of a hunk header.
func hunkStarts(header string) (oldStart, newStart int) {
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return 0, 0
	}
	return firstNumber(strings.TrimPrefix(fields[1], "-")), firstNumber(strings.TrimPrefix(fields[2], "+"))
}

func firstNumber(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// diffHasHunks reports whether a diff still asks for anything to be measured.
func diffHasHunks(diff string) bool {
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			return true
		}
	}
	return false
}

// diffFiles is every path a move-aware diff still carries a hunk for —
// filterMovedLines drops a file's header entirely once every one of its
// hunks was moved away, so a path missing here is a path with nothing left
// to mutate.
func diffFiles(diff string) map[string]bool {
	out := map[string]bool{}
	for line := range strings.SplitSeq(diff, "\n") {
		if p, ok := diffHeaderPath(line); ok {
			out[p] = true
		}
	}
	return out
}

// movedOnlyFiles is the file-level reading of moveAwareDiff: which of the
// given files git's move detection emptied entirely, and how many lines that
// was. A tool that scopes itself by REF rather than by a diff this package
// controls (gremlins' own --diff) cannot be handed the filtered text
// directly, so its walk is narrowed the other way — every file this reports
// belongs in the run's own --exclude-files, because nothing on it changed in
// any sense a mutant could constrain.
func movedOnlyFiles(repoRoot, base, tip string, files []string) (moved []string, movedLines int) {
	if len(files) == 0 {
		return nil, 0
	}
	laneDiff, n := moveAwareDiff(repoRoot, base, tip, files)
	survived := diffFiles(laneDiff)
	for _, f := range files {
		if !survived[f] {
			moved = append(moved, f)
		}
	}
	return moved, n
}
