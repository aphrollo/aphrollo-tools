package mutation

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The edit-time form of the commit stage: the same run, over the lines one
// edit changed against HEAD in the working tree as it stands, started by the
// edit hook detached and read by the next hook. What it reports goes to
// stderr, which the hook that started it kept in a log; how it ended goes to
// a file, written last, whose existence is the signal that the log is whole.

// RunMutantsEdit is `gate mutants edit`: measure the changed lines of file,
// and record in done how it ended: "refused" when a survivor no
// mutation-accept entry admits was found, "ok" for anything else, including a
// repo that declares nothing. The exit code is 1 for a refusal.
func RunMutantsEdit(root, file, done string, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	status, code := "ok", 0
	if editStage(root, file).Blocked {
		status, code = "refused", 1
	}
	if err := writeDoneFile(done, status); err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants edit: recording the result: %v\n", err)
		return 1
	}
	return code
}

// editStage measures one file's changed lines. It is inert in a repo that does
// not declare mutants-at-commit and for a path outside the repository.
func editStage(root, file string) GateResult {
	cfg, err := ReadMutantsConfig(root)
	if err != nil || !cfg.AtCommit {
		return mutantsResult(false, "")
	}
	rel, ok := editRelPath(root, file)
	if !ok {
		return mutantsResult(false, "")
	}
	mods := commitModules(root, []string{rel})
	if len(mods) == 0 {
		return mutantsResult(false, "")
	}
	start := commitNowFn()
	added, err := editAddedLines(root, rel)
	if err != nil {
		return commitUnmeasured("edit", root, "diff", err.Error())
	}
	return measureModules("edit", root, mods, cfg, added, nil, start)
}

// editRelPath is file as a slash path relative to root, taking a relative file
// as relative to root. ok is false for a path outside root.
func editRelPath(root, file string) (string, bool) {
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// editAddedLines answers the lines of rel, a repo-relative slash path, that
// differ from HEAD. A file no commit holds yet is entirely new.
func editAddedLines(root, rel string) (map[string]map[int]bool, error) {
	out, errText, err := gitDiffOutFn(root, "diff", "--no-color", "--no-ext-diff",
		"--src-prefix=a/", "--dst-prefix=b/", "-U0", "HEAD", "--", rel)
	if err != nil {
		return nil, fmt.Errorf("git diff -U0 HEAD: %s", gitFailureText(errText, err))
	}
	if added := parseAddedLines(out); len(added) > 0 {
		return added, nil
	}
	untracked, errText, err := gitDiffOutFn(root, "ls-files", "--others", "--exclude-standard", "--", rel)
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %s", gitFailureText(errText, err))
	}
	if strings.TrimSpace(untracked) == "" {
		return map[string]map[int]bool{}, nil
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	lines := map[int]bool{}
	for n := range bytes.Count(data, []byte("\n")) + 1 {
		lines[n+1] = true
	}
	return map[string]map[int]bool{rel: lines}, nil
}

// writeDoneFile records the outcome by rename, so a reader sees it whole or
// not at all.
func writeDoneFile(path, status string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".done-*")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(tmp, status+"\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
