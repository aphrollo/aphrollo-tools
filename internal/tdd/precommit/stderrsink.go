package precommit

import (
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

// stderrFor is where a gate line about root (or a directory under it) goes:
// the gate speaks on stderr because stdout belongs to git. A test that asserts
// on those lines registers a writer for its own root (rootseam.SetStderr)
// instead of swapping the process-wide os.Stderr, so it runs beside the rest.
func stderrFor(root string) io.Writer { return rootseam.Stderr(root) }
