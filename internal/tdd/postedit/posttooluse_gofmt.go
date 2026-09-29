package postedit

import (
	"bytes"
	"go/format"
	"os"
	"strings"
)

// gofmtEdited formats target in place when it is a Go source gofmt would
// change, and returns the note the edit's gate line carries; "" when the file
// is left as it is. Only the edited file is touched. gofmt is deterministic
// and never changes meaning, so an agent's edit reaches the commit gate's
// gofmt stage already clean; that stage keeps judging edits made outside the
// hooks. A file that does not parse stays as it is: its compile error belongs
// to the test run, not to this. A CRLF file is judged on its LF form and keeps
// its CRLF endings, so a checkout that carries them is formatted, never
// converted.
func gofmtEdited(target string) string {
	if !strings.HasSuffix(target, ".go") {
		return ""
	}
	// A file that cannot be read yields no bytes, which do not parse either.
	src, _ := os.ReadFile(target)
	lf := bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	formatted, err := format.Source(lf)
	if err != nil || bytes.Equal(formatted, lf) {
		return ""
	}
	if !bytes.Equal(lf, src) {
		formatted = bytes.ReplaceAll(formatted, []byte("\n"), []byte("\r\n"))
	}
	// An existing file keeps its own mode; the perm only applies on create.
	if os.WriteFile(target, formatted, 0o644) != nil {
		return ""
	}
	return "gofmt formatted " + target
}

// withGateNote puts note on the edit's gate line, the first line of text,
// ahead of any red summary body and after any note already there. With no
// gate line (nothing was tested) the note is the line. The gofmt note and the
// law note (lawRefusalNote) both ride on it.
func withGateNote(text, note string) string {
	if note == "" {
		return text
	}
	if text == "" {
		return "gate: " + note
	}
	end := strings.IndexByte(text, '\n')
	if end < 0 {
		end = len(text)
	}
	return text[:end] + " (" + note + ")" + text[end:]
}
