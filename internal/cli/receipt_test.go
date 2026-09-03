package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The signer is the one writer of a receipt's MAC, so the command has to work
// from a script: a path in, a signed file out, and a non-zero exit with a
// reason when it cannot.
func TestReceiptSign_SignsTheFileItIsGiven(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "mutation-receipt.json")
	if err := os.WriteFile(path, []byte(`{"tip_tree":"abc","verdict":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "receipt", "sign", path}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var signed map[string]any
	if err := json.Unmarshal(data, &signed); err != nil {
		t.Fatal(err)
	}
	if signed["mac"] == nil || signed["mac"] == "" {
		t.Fatalf("receipt is unsigned after signing: %s", data)
	}
	if signed["tip_tree"] != "abc" {
		t.Fatalf("the signer changed the body: %s", data)
	}
}

// A receipt that is not there, or is not JSON, is a script error worth an
// exit code — signing it silently would leave a run believing it was proven.
func TestReceiptSign_FailsLoudlyOnAFileItCannotSign(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "receipt", "sign", filepath.Join(t.TempDir(), "nope.json")},
		strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatal("signing a missing receipt must not exit 0")
	}
	if errb.Len() == 0 {
		t.Fatal("a failure with no reason on stderr is a silent failure")
	}
}
