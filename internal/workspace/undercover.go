package workspace

import (
	"errors"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// The workspace verbs' half of the undercover walls: the lane name `create`
// and `claim` are asked for, and the head ref, title and body `pr`, `ship` and
// `submit` hand to gh, are refused at plan time, before a worktree, a push or
// a gh call exists to undo. Inert unless the repo sets `undercover = true`.

// undercoverText is one piece of text about to be posted, and what it is.
type undercoverText struct {
	kind, text string
}

// undercoverCheck refuses a ref name or a text carrying a tell. root is the
// checkout whose manifest decides whether the check applies at all.
func undercoverCheck(root, branch string, texts ...undercoverText) error {
	tells, on := undercover.Load(root)
	if !on {
		return nil
	}
	if branch != "" {
		if tell, hit := tells.RefName(branch); hit {
			return errors.New(undercover.RefRefusal("branch", branch, tell))
		}
	}
	for _, t := range texts {
		if h, hit := tells.Text(t.text); hit {
			return errors.New(undercover.TextRefusal(t.kind, h))
		}
	}
	return nil
}
