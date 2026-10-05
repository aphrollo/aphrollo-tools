package shadow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddsSymbol_ReadsTheGoEditsOwnTextForANewFuncOrExportedName(t *testing.T) {
	dir := t.TempDir()
	onDisk := filepath.Join(dir, "a.go")
	if err := os.WriteFile(onDisk, []byte("package a\n\nfunc Old() {}\n\nvar hidden = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := func(old, now string) Payload {
		var p Payload
		p.ToolName = "Edit"
		p.ToolInput.OldString, p.ToolInput.NewString = old, now
		return p
	}
	write := func(content string) Payload {
		var p Payload
		p.ToolName = "Write"
		p.ToolInput.Content = content
		return p
	}
	multi := func(old, now string) Payload {
		var p Payload
		p.ToolName = "MultiEdit"
		p.ToolInput.Edits = append(p.ToolInput.Edits, struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}{old, now})
		return p
	}
	cases := []struct {
		name string
		p    Payload
		file string
		want bool
	}{
		{"a new func in an edit", edit("x := 1", "x := 1\n}\n\nfunc New() {"), onDisk, true},
		{"a new unexported func counts", edit("", "func helper() {}"), onDisk, true},
		{"a new method", edit("", "func (t *T) Run() {}"), onDisk, true},
		{"the same func with a new body", edit("func Old() {}", "func Old() { _ = 1 }"), onDisk, false},
		{"a body edit", edit("return 1", "return 2"), onDisk, false},
		{"a new exported type", edit("", "type Config struct{}"), onDisk, true},
		{"a new unexported var does not count", edit("", "var cache = 1"), onDisk, false},
		{"a new exported const in a block", edit("", "const (\n\tA = 1\n\tB = 2\n)"), onDisk, true},
		{"a write adding a func to the file on disk", write("package a\n\nfunc Old() {}\n\nfunc Added() {}\n"), onDisk, true},
		{"a write that keeps the file's names", write("package a\n\nfunc Old() { _ = 1 }\n\nvar hidden = 2\n"), onDisk, false},
		{"a write of a file not there declares its names new", write("package a\n\nfunc F() {}\n"), filepath.Join(dir, "new.go"), true},
		{"a multi edit adding a func", multi("", "func M() {}"), onDisk, true},
		{"another language is never read", edit("", "func New() {}"), filepath.Join(dir, "a.rs"), false},
		{"a shell call writes nothing the payload shows", Payload{ToolName: "Bash"}, onDisk, false},
	}
	for _, c := range cases {
		if got := AddsSymbol(c.p, c.file); got != c.want {
			t.Errorf("%s: AddsSymbol = %v, want %v", c.name, got, c.want)
		}
	}
}
