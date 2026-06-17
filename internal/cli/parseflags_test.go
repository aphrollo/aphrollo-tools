package cli

import (
	"flag"
	"io"
	"slices"
	"testing"
)

func newFS() (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	apply := fs.Bool("apply", false, "")
	return fs, apply
}

// Flags may appear before, between, or after positionals.
func TestParseFlagsAnywhere_InterspersedFlags(t *testing.T) {
	fs, apply := newFS()
	pos, err := parseFlagsAnywhere(fs, []string{"repo", "--apply", "branch"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*apply {
		t.Error("--apply not parsed")
	}
	if want := []string{"repo", "branch"}; !slices.Equal(pos, want) {
		t.Errorf("pos = %v, want %v", pos, want)
	}
}

// After a standalone "--", every remaining token is a positional — even ones
// that start with a dash — instead of being rejected as unknown flags.
func TestParseFlagsAnywhere_DoubleDashTerminator(t *testing.T) {
	fs, apply := newFS()
	pos, err := parseFlagsAnywhere(fs, []string{"--apply", "--", "-weird", "-name"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*apply {
		t.Error("--apply before -- not parsed")
	}
	if want := []string{"-weird", "-name"}; !slices.Equal(pos, want) {
		t.Errorf("pos = %v, want %v (everything after -- is positional)", pos, want)
	}
}
