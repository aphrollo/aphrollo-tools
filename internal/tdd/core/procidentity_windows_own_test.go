//go:build windows

package core

import (
	"os"
	"testing"
)

func TestProcessIdentity_ThisProcessIsItsCreationTime(t *testing.T) {
	id, ok := processIdentity(os.Getpid())
	if !ok || id == "" {
		t.Fatalf("processIdentity(self) = %q, %v; want its creation time", id, ok)
	}
	if again, _ := processIdentity(os.Getpid()); again != id {
		t.Errorf("identity changed between two reads: %q then %q", id, again)
	}
	if id, ok := processIdentity(1<<31 - 2); ok {
		t.Errorf("a pid no process holds has identity %q", id)
	}
}
