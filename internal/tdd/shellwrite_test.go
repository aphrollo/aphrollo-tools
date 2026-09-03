package tdd

import "testing"

// A path flag with nothing after it (`Set-Content -Path` alone, the operand
// dropped or wrapped to a next line the tokenizer never sees) must not be
// read past the end of argv — it names no file, so psFileTarget falls
// through to the positional scan rather than indexing one past the last arg.
func TestPsFileTarget_ATrailingFlagWithNoValueNamesNoFile(t *testing.T) {
	if got := psFileTarget([]string{"-Path"}, "-Path"); got != nil {
		t.Fatalf("psFileTarget = %v, want nil — the flag names no file with nothing after it", got)
	}
}

// The normal case: the flag's own next argument is the file.
func TestPsFileTarget_ReadsTheValueAfterAMatchingFlag(t *testing.T) {
	got := psFileTarget([]string{"-Value", "hi", "-Path", "notes.txt"}, "-Path")
	if len(got) != 1 || got[0] != "notes.txt" {
		t.Fatalf("psFileTarget = %v, want [notes.txt]", got)
	}
}
