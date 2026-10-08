package core

import (
	"reflect"
	"testing"
)

// A recorded mutation proof is read back for the same repo, in the order it
// was recorded, and never for another repo.
func TestPinProofs_ReadBackPerRepoInRecordedOrder(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a, b := t.TempDir(), t.TempDir()

	RecordPinProof(a, PinProof{Test: "TestOne", File: "x.go", Blob: "aaa"})
	RecordPinProof(a, PinProof{Test: "TestTwo", File: "y.go", Blob: "bbb"})
	RecordPinProof(b, PinProof{Test: "TestOther", File: "z.go", Blob: "ccc"})

	want := []PinProof{{Test: "TestOne", File: "x.go", Blob: "aaa"}, {Test: "TestTwo", File: "y.go", Blob: "bbb"}}
	if got := PinProofs(a); !reflect.DeepEqual(got, want) {
		t.Fatalf("PinProofs(a) = %v, want %v", got, want)
	}
}

// A repo that never recorded a proof has none.
func TestPinProofs_NoneRecordedIsEmpty(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if got := PinProofs(t.TempDir()); len(got) != 0 {
		t.Fatalf("PinProofs = %v, want none", got)
	}
}
