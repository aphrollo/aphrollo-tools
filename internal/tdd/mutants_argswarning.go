package tdd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// The gate computes narrowed cargo-mutants flags into APHROLLO_MUTANTS_ARGS
// (mutantsChildEnv, mutants_run.go) -- the touched-package scoping and the
// baseline exclusion filter both live only in that argv. Nothing ever
// checked whether the producer's own script actually reads the variable
// before this, so a script that still hardcodes its own cargo-mutants
// invocation dropped every one of those flags with no signal anywhere
// (issue #423). docs/mutation-runner.md states the contract -- a producer
// MUST append $APHROLLO_MUTANTS_ARGS to its own invocation -- and this file
// is the other half: the gate noticing when a run shows no sign that
// happened.

// runProducerProcess runs cmd (the consuming repo's own mutation runner,
// already given its directory and environment), tees a capped copy of its
// combined stdout/stderr to check whether the computed argsEnvValue ever
// showed up in it, and returns the same exit-code mapping
// runMutantsProducer always has: the child's own code on a clean exit, or 1
// when the process could not even be judged. The captured output is handed
// back too — the args-unread check is not the only reader of it: a producer
// that exits non-zero because its own tool found nothing to mutate says so
// in that same text (see producerFoundNothingToMutate), and this is the one
// place that text is available before it scrolls off into the log file.
func runProducerProcess(cmd *exec.Cmd, argsEnvValue string) (int, string) {
	capture := &mutantsOutputCapture{limit: mutantsOutputCaptureLimit}
	cmd.Stdout = io.MultiWriter(os.Stdout, capture)
	cmd.Stderr = io.MultiWriter(os.Stderr, capture)
	runErr := cmd.Run()
	output := capture.buf.String()
	if msg := mutantsArgsUnreadWarning(argsEnvValue, output); msg != "" {
		logf(os.Stdout, "%s", msg)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return ee.ExitCode(), output
		}
		logf(os.Stdout, "aphrollo: %v", runErr)
		return 1, output
	}
	return 0, output
}

// mutantsOutputCaptureLimit bounds how much of the producer's combined
// stdout/stderr the unread-args check keeps: a full mutation run's output
// can run to hundreds of megabytes, and the check only ever needs a
// substring match, never the whole log (which still reaches the real
// streams unclipped through the same io.MultiWriter).
const mutantsOutputCaptureLimit = 64 * 1024

// mutantsOutputCapture tees into a size-capped buffer. Write always reports
// success for the FULL byte count, even past the cap: capping this side
// stream must never turn into a short write the producer's own process
// sees.
type mutantsOutputCapture struct {
	buf   bytes.Buffer
	limit int
}

func (c *mutantsOutputCapture) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		c.buf.Write(p[:room])
	}
	return len(p), nil
}

// mutantsEnvValue reads one KEY=value entry back out of a built environment
// slice. mutantsChildEnv (mutants_run.go) is the one place that computes
// APHROLLO_MUTANTS_ARGS; this reads its answer back rather than recomputing
// it a second time.
func mutantsEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// mutantsArgsUnreadWarning answers issue #423's second half. This is a
// heuristic, not a proof: a compliant producer that never echoes its own
// command line still trips it. That is why it returns ONE warning line,
// never a block -- the receipt stands on whatever the producer actually
// measured, and a false warning costs a reader ten seconds where a false
// "all clear" costs the whole feature going unnoticed again.
func mutantsArgsUnreadWarning(computedArgs, producerOutput string) string {
	computedArgs = strings.TrimSpace(computedArgs)
	if computedArgs == "" || strings.Contains(producerOutput, computedArgs) {
		return ""
	}
	return fmt.Sprintf(
		"aphrollo: computed %s=%q but the producer's own output shows no trace of it -- does tools/mutation_gate.sh append $%s to its cargo-mutants invocation? (docs/mutation-runner.md)",
		MutantsArgsEnv, computedArgs, MutantsArgsEnv)
}
