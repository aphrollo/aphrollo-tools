package shell

import "testing"

// bareWords wraps plain strings as unquoted shellWords, for a test that
// exercises psFileTarget's flag matching directly and does not care about
// quoting.
func bareWords(texts ...string) []shellWord {
	words := make([]shellWord, len(texts))
	for i, s := range texts {
		words[i] = shellWord{text: s, raw: s}
	}
	return words
}

// A path flag with nothing after it (`Set-Content -Path` alone, the operand
// dropped or wrapped to a next line the tokenizer never sees) must not be
// read past the end of argv — it names no file, so psFileTarget falls
// through to the positional scan rather than indexing one past the last arg.
func TestPsFileTarget_ATrailingFlagWithNoValueNamesNoFile(t *testing.T) {
	t.Parallel()
	if got := psFileTarget(bareWords("-Path"), "-Path"); got != nil {
		t.Fatalf("psFileTarget = %v, want nil — the flag names no file with nothing after it", got)
	}
}

// The normal case: the flag's own next argument is the file.
func TestPsFileTarget_ReadsTheValueAfterAMatchingFlag(t *testing.T) {
	t.Parallel()
	got := psFileTarget(bareWords("-Value", "hi", "-Path", "notes.txt"), "-Path")
	if len(got) != 1 || got[0].text != "notes.txt" {
		t.Fatalf("psFileTarget = %v, want [notes.txt]", got)
	}
}
