package failfirst

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// headSHAFor is the current commit of root's repo, "" outside a repo — the
// first half of "does this result describe the code on disk now".
func headSHAFor(root string) string {
	out, err := run.LightOutput(run.Spec{Name: gitBinary(), Args: []string{"-C", root, "rev-parse", "HEAD"}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
