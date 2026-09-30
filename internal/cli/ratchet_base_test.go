package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// TestRatchetCheckCLI_BaseFlagJudgesTheTreeAgainstItsBase: `--base <ref>` means
// only what the tree added since the ref counts, and no --base leaves the run
// judged by the baselines alone.
func TestRatchetCheckCLI_BaseFlagJudgesTheTreeAgainstItsBase(t *testing.T) {
	root := lawRepo(t)
	original := ratchetCheckFn
	t.Cleanup(func() { ratchetCheckFn = original })
	var got ratchet.Options
	ratchetCheckFn = func(opts ratchet.Options) (ratchet.Result, error) {
		got = opts
		return ratchet.Result{}, nil
	}
	for args, want := range map[string]bool{"--base HEAD~1": true, "": false} {
		argv := append([]string{"ratchet", "check", "--repo", root}, strings.Fields(args)...)
		var out, errb bytes.Buffer
		if code := Run(argv, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%q: exit = %d (stderr: %s)", args, code, errb.String())
		}
		if got.BaseRelative != want {
			t.Errorf("%q: Options.BaseRelative = %v, want %v", args, got.BaseRelative, want)
		}
	}
}
