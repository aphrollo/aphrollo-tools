package core

import (
	"time"
)

// bashSnapshot is the tree as it stood before one shell command ran.
type bashSnapshot struct {
	Root string `json:"root"`
	// StatusHash fingerprints `git status --porcelain -uall -z`: unchanged
	// means no file entered or left the dirty set, which is the cheap
	// no-op answer for the commands that read rather than write.
	StatusHash string `json:"status_hash"`
	// Dirty stamps each dirty SOURCE path "<size>:<mtime-unix-nanos>". The
	// hash alone cannot see a second edit to a file that was already
	// modified — the status line is identical before and after — and that is
	// the common case, so the stamps carry it.
	// bound: one entry per dirty source path, not per tracked file.
	Dirty map[string]string `json:"dirty"`
	// MergeHead is the commit MERGE_HEAD named when the snapshot was taken,
	// "" when no merge was in progress. It is what lets the harvest tell a
	// command that CONCLUDED a merge from one that edited the merged paths.
	MergeHead string `json:"merge_head,omitempty"`
	// Head is the commit HEAD named when the snapshot was taken: an
	// unchanged HEAD with MERGE_HEAD gone is an aborted merge.
	Head string    `json:"head,omitempty"`
	At   time.Time `json:"at"`
}
