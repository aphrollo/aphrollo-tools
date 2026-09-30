package cli

import "github.com/aphrollo/aphrollo-tools/internal/tdd"

// The mutation runners fingerprint the git state a leaked test process would
// change, and refuse a run that changed it. Recording the escape is the escape
// package's job, which the runners' package sits below and so cannot import:
// the binary wires the two together here.
func init() {
	tdd.SetGitWorldRecorder(tdd.NoteGitWorldEscape)
}
