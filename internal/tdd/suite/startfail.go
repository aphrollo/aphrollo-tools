package suite

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strings"
)

// toolMissingOpen opens the Inconclusive text of a run whose command could not
// start. The memory refusal reads "SKIPPED — memory headroom: …", so the
// parenthesis is what tells the two apart.
const toolMissingOpen = "SKIPPED ("

// IsToolMissing reports whether an Inconclusive text says the run's command
// could not start, as opposed to the memory cap or the box's memory ending it.
func IsToolMissing(inconclusive string) bool {
	return strings.HasPrefix(inconclusive, toolMissingOpen)
}

// commandNotFoundRe reads the line a shell prints for a command it cannot
// find ("bash: line 1: go: command not found", dash's "sh: 1: go: not found")
// and captures the command's name.
var commandNotFoundRe = regexp.MustCompile(`(?m)([^\s:]+): (?:command )?not found\s*$`)

// shellCommandNotFound is the exit status a shell gives a command it cannot find.
const shellCommandNotFound = 127

// StartFailure says why a run's command never started, "" when it did. The
// three ways it shows: the lookup on PATH failed, the file at an explicit path
// (or its interpreter) is not there, or a shell around the command exited 127
// naming a command it could not find. Such a run tested nothing, so it is
// never a red: the text becomes the run's Inconclusive, naming the tool and,
// where it was looked up, the PATH it was looked up on. output is what the run
// printed, asked for only when an exit 127 makes it matter.
func StartFailure(err error, cmd string, output func() string, path string) string {
	var exit *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Sprintf("%s%s not on PATH: %s)", toolMissingOpen, cmd, path)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("%s%s could not start: %v)", toolMissingOpen, cmd, err)
	case errors.As(err, &exit) && exit.ExitCode() == shellCommandNotFound:
		if m := commandNotFoundRe.FindStringSubmatch(output()); m != nil {
			return fmt.Sprintf("%s%s not on PATH: %s)", toolMissingOpen, m[1], path)
		}
	}
	return ""
}
