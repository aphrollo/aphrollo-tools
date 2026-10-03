package gitx

import (
	"path/filepath"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
)

// HeadCopy is the text HEAD holds for the file at path (absolute), read from
// the object store's files with no spawn, the blob the hook's one status names.
// inHead is false for a file HEAD does not hold (an untracked or newly added
// one, or a rename's destination). ok is false when only git can say: the tree
// is in no repository or its status cannot be read, the file is not dirty (so
// it may be tracked and unchanged, or ignored), it is in conflict, or its blob
// cannot be read.
func HeadCopy(path string) (text string, inHead, ok bool) {
	c, st := HookStatus(filepath.Dir(path))
	if c == nil || st == nil {
		return "", false, false
	}
	rel, err := filepath.Rel(c.Root(), path)
	if err != nil {
		return "", false, false
	}
	e, found := st.Entry(filepath.ToSlash(rel))
	if !found {
		return "", false, false
	}
	switch e.Kind {
	case igit.Untracked:
		return "", false, true
	case igit.Renamed:
		return "", false, true
	case igit.Ordinary:
		if e.HeadOID == "" {
			return "", false, true
		}
		blob, err := c.ReadBlob(e.HeadOID)
		if err != nil {
			return "", false, false
		}
		return string(blob), true, true
	}
	return "", false, false
}
