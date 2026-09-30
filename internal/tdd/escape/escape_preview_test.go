package escape

import (
	"io"
	"strings"
	"testing"
)

func TestPreviewEscape_CarriesTheKindAndThemeLabelsAndTheClosesByLine(t *testing.T) {
	title, labels, body, err := PreviewEscape(EscapeOptions{
		Reason:   "clippy passed and CI refused",
		Kind:     FalsePositiveKind,
		Labels:   []string{"netcode"},
		ClosesBy: "law no_unwrap",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "false-positive: clippy passed and CI refused"; title != want {
		t.Errorf("title = %q, want %q", title, want)
	}
	if len(labels) != 2 || labels[0] != FalsePositiveKind || labels[1] != "netcode" {
		t.Errorf("labels = %v, want [false-positive netcode]", labels)
	}
	if !strings.Contains(body, "closes-by: law no_unwrap\n") {
		t.Errorf("body lacks the closes-by line:\n%s", body)
	}
}

func TestPreviewEscape_DefaultsTheKindToEscape(t *testing.T) {
	_, labels, _, err := PreviewEscape(EscapeOptions{Reason: "a miss"})
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != EscapeKind {
		t.Errorf("labels = %v, want [%s]", labels, EscapeKind)
	}
}

func TestRecordEscape_RefusesABlankReasonBeforeWritingAnything(t *testing.T) {
	if _, err := RecordEscape(EscapeOptions{Reason: "  "}, io.Discard); err == nil {
		t.Fatal("a blank reason was recorded")
	}
}

func TestRecordEscape_RefusesAnUnknownKindBeforeWritingAnything(t *testing.T) {
	if _, err := RecordEscape(EscapeOptions{Reason: "x", Kind: "bogus"}, io.Discard); err == nil {
		t.Fatal("an unknown kind was recorded")
	}
}

func TestPreviewEscape_RefusesABlankReason(t *testing.T) {
	if _, _, _, err := PreviewEscape(EscapeOptions{Reason: "   "}); err == nil {
		t.Fatal("a blank reason was accepted")
	}
}

func TestPreviewEscape_RefusesAnUnknownKind(t *testing.T) {
	if _, _, _, err := PreviewEscape(EscapeOptions{Reason: "x", Kind: "bogus"}); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
}
