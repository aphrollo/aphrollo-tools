//go:build !windows

package cli

import (
	"errors"
	"os/exec"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// openers are the commands that open a file in the default application, the
// first on PATH wins: xdg-open on a desktop unix, open on macOS.
var openers = []string{"xdg-open", "open"}

// openBrowserTimeout bounds the opener, which returns as soon as it has handed
// the file over.
const openBrowserTimeout = 15 * time.Second

// openInBrowser opens the file with the platform's opener, as a guarded light
// child that ends with its tree.
func openInBrowser(path string) error {
	for _, name := range openers {
		if _, err := exec.LookPath(name); err == nil {
			return run.LightRun(run.Spec{Name: name, Args: []string{path}, Timeout: openBrowserTimeout})
		}
	}
	return errors.New("no xdg-open or open on PATH")
}
