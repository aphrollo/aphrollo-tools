package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_RefusesWhatItDoesNotKnow(t *testing.T) {
	for _, args := range [][]string{{"-nope"}, {"stray"}, {"-only", "elsewhere"}, {"-files", "0"}, {"-lines", "10"}} {
		var out, errw bytes.Buffer
		if code := run(args, &out, &errw); code != 2 {
			t.Errorf("run(%v) = %d, want 2; stderr %q", args, code, errw.String())
		}
	}
}

func TestRun_ASkippedTreeIsNamed(t *testing.T) {
	var out, errw bytes.Buffer
	run([]string{"-only", "elsewhere"}, &out, &errw)
	if !strings.Contains(errw.String(), `"elsewhere"`) {
		t.Errorf("stderr = %q, want it to name the tree it does not know", errw.String())
	}
}
