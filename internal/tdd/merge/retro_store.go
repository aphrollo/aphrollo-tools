package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A retro waits in the gate's state dir until a hook prints it. One that a
// session's merge produced waits under that session and is printed by the
// session's next UserPromptSubmit or PostToolUse hook; one produced outside
// any session waits under its repo and is printed by the next SessionStart
// there. Printing claims the file by renaming it from .pending to
// .delivered, so two hooks racing for it print it once between them.

const (
	retroPending   = ".pending"
	retroDelivered = ".delivered"
)

// retroDir is the retro records' root inside the state dir.
func retroDir() string {
	return filepath.Join(StateDir(), "retro")
}

// retroSlug makes a path or id safe as one file name.
func retroSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// retroRepoKey names dir's repo by its primary checkout, so a lane and its
// primary share one key. "" outside a repository.
func retroRepoKey(dir string) string {
	root := RepoRoot(dir)
	if root == "" {
		return ""
	}
	if primary := primaryCheckoutRoot(root); primary != "" {
		root = primary
	}
	return retroSlug(filepath.Clean(root))
}

// recordRetro writes text as pending for session, or for mainRepo when the
// merge ran in no session.
func recordRetro(session, mainRepo string, pr int, text string) error {
	repo := retroRepoKey(mainRepo)
	var dir, name string
	switch {
	case session != "":
		dir = filepath.Join(retroDir(), "session-"+retroSlug(session))
		name = fmt.Sprintf("%s-pr%d", repo, pr)
	case repo != "":
		dir = filepath.Join(retroDir(), "repo-"+repo)
		name = fmt.Sprintf("pr%d", pr)
	default:
		return fmt.Errorf("no session and no repository to keep it for")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, name+retroPending), []byte(text))
}

// TakeSessionRetros returns and marks delivered every retro pending for
// session; "" when there is none.
func TakeSessionRetros(session string) string {
	if session == "" {
		return ""
	}
	return takeRetros(filepath.Join(retroDir(), "session-"+retroSlug(session)))
}

// TakeRepoRetros returns and marks delivered every retro a session-less merge
// left for dir's repository; "" when there is none.
func TakeRepoRetros(dir string) string {
	repo := retroRepoKey(dir)
	if repo == "" {
		return ""
	}
	return takeRetros(filepath.Join(retroDir(), "repo-"+repo))
}

// takeRetros claims every pending record in dir, in name order.
func takeRetros(dir string) string {
	names, err := filepath.Glob(filepath.Join(dir, "*"+retroPending))
	if err != nil {
		return ""
	}
	sort.Strings(names)
	var out []string
	for _, pending := range names {
		delivered := strings.TrimSuffix(pending, retroPending) + retroDelivered
		if os.Rename(pending, delivered) != nil {
			continue // another hook claimed it
		}
		data, err := os.ReadFile(delivered)
		if err != nil {
			continue
		}
		out = append(out, strings.TrimRight(string(data), "\n"))
	}
	return strings.Join(out, "\n\n")
}
