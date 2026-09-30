package mask

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// The fixture tests in this package were written against one function per
// language. They run unchanged against the generic lexer through these
// wrappers, each of which reads the named row of the embedded table.

func defaults(t testing.TB) *lang.Table {
	tbl, err := lang.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func rowLexer(name string) func(src string, blankStrings, blankComments bool) string {
	tbl, err := lang.Defaults()
	if err != nil {
		panic(err)
	}
	row, ok := tbl.Named(name)
	if !ok {
		panic("no language row " + name)
	}
	lexer := NewLexer(row)
	return lexer.Lex
}

var (
	RustTokens   = rowLexer("rust")
	PythonTokens = rowLexer("python")
	ShellTokens  = rowLexer("shell")
	TOMLTokens   = rowLexer("toml")
	RubyTokens   = rowLexer("ruby")
	YAMLTokens   = rowLexer("yaml")
)
