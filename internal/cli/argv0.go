package cli

import (
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// shimVerbs maps a shim's file name (extension stripped, lower-cased) to the
// `gate` subcommand a binary invoked under that name runs. The queue dir holds
// COPIES of the aphrollo binary named cargo.exe and git.exe, so the shim is a
// real executable rather than a batch file: cmd.exe strips `^` out of a .cmd
// argument (which broke `MERGE_HEAD^{tree}` and nextest's `-E test(/^mod::/)`)
// and re-splits quoted arguments, and neither is recoverable from inside the
// script.
var shimVerbs = map[string]string{
	"cargo": "cargo",
	"git":   "git",
}

// DispatchArgs maps the process's own argv into the arguments Run parses. A
// binary invoked as cargo/cargo.exe or git/git.exe is a queue shim and forwards
// everything to the matching `gate` subcommand; under any other name the
// arguments are the command, unchanged. The caller's slice is never written
// through — os.Args has spare capacity and appending in place would rewrite it.
func DispatchArgs(argv0 string, args []string) []string {
	name := filepath.Base(argv0)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	verb, ok := shimVerbs[strings.ToLower(name)]
	if !ok {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, tdd.CmdName, verb)
	return append(out, args...)
}
