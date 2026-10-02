package postedit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The per-file half of PostBash: what an Edit of one file gets, given to each
// source file a Bash command changed. The command's writes are all in already,
// so nothing here can refuse one; it says, on the gate line, what an Edit
// would have been refused for and what the commit gate will refuse.

// liveSourceFiles is the changed paths (relative to base, slash-separated)
// that are still on disk, as absolute paths: a deleted file has no bytes to
// format, record or judge.
func liveSourceFiles(base string, changed []string) []string {
	var live []string
	for _, rel := range changed {
		abs := filepath.Join(base, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err == nil {
			live = append(live, abs)
		}
	}
	return live
}

// recordBashEdits writes one edit-ledger record per file, into the ledger of
// the project root that owns it, and answers each root's record ids joined
// for one verdict to settle them all.
func recordBashEdits(files []string) map[string]string {
	byRoot := map[string][]string{}
	for _, file := range files {
		if root := FindProjectRoot(file); root != "" {
			byRoot[root] = append(byRoot[root], file)
		}
	}
	ids := make(map[string]string, len(byRoot))
	for root, group := range byRoot {
		ids[root] = recordEdits(root, group)
	}
	return ids
}

// bashGateFinish puts the per-file notes on the gate text the roots' runs
// made, in the order an Edit's hook does: the gofmt note, the laws judged over
// every changed file in one pass on the formatted bytes (a shell write fires
// no pre-edit hook, so this is the first judge that sees it), the linter's
// findings, then a line for each smell verdict. It answers that text and the
// detached runs to start once the harvest has been read: the deferred lint of
// each Go file, and the mutation run over the files of a green root.
func bashGateFinish(session, root string, changed, live []string, formatted string, runLines, greenFiles []string) (string, func()) {
	text := strings.Join(runLines, "\n")
	text = withGateNote(text, formatted)
	written := make([]string, len(changed))
	for i, rel := range changed {
		written[i] = filepath.Join(root, filepath.FromSlash(rel))
	}
	text = withGateNote(text, lawRefusalNote(written))
	text = withGateNote(text, lintEditedFiles(live, nil))
	for _, line := range bashSmellLines(root, changed) {
		if text != "" {
			text += "\n"
		}
		text += line
	}
	return text, func() {}
}

// bashSmellLines judges each changed file that is still on disk as the
// pre-edit hook would have judged an Edit of it, over the lines that differ
// from HEAD's copy, and answers one gate line for each file with a verdict.
// A blocking verdict is what an Edit would have been denied for and what the
// commit gate refuses; an advisory one is reported as it is to an Edit.
func bashSmellLines(root string, changed []string) []string {
	var lines []string
	for _, rel := range changed {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		// absence-ok: a file HEAD does not hold has no old copy, and all of it is added.
		pre, _ := gitRead(root, "show", "HEAD:"+rel)
		d := decideImages(ClassifyFile(rel), abs, pre, string(data))
		if d.Action == Allow {
			continue
		}
		if d.Action == Block {
			AppendGateLog("postedit", root, LogToken(rel), "bash-smell:"+d.Policy, 0)
			lines = append(lines, fmt.Sprintf("gate: %s: %s — an Edit would have been denied for this; the commit gate refuses it", rel, d.Reason))
			continue
		}
		lines = append(lines, fmt.Sprintf("gate: %s: %s", rel, d.Reason))
	}
	return lines
}
