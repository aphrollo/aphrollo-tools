package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const configUsage = `usage: aphrollo config [features] [--repo <dir>]
       aphrollo config show [--dir <dir>]
       aphrollo config set <key> <value> [--user|--repo] [--dir <dir>] [--dry]

features (the default) prints the opt-in feature table: each key, its value in
--repo (default the working directory's repo), what turning it on costs, and
how to turn it on. Read-only; the first install in a repo prints the same
table once.

show prints every setting of the schema with its effective value and where it
came from: built-in, the user's config.toml, or the repo's trellis.toml, naming
the aphrollo.toml key it was read through when it is an alias. A key or value
a file got wrong is named on stderr with its file, layer and line, and its
layer's value is the built-in one.

set writes one key to the repo's trellis.toml (--repo, the default where the
key may be a repo key) or the user's config.toml (--user), and records a
config.set event naming the key and the value. --dry prints what it would write
and stops.
`

// runConfig dispatches: show and set are the settings, anything else is the
// feature table the verb always printed.
func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, configUsage)
		return 0
	}
	if len(args) > 0 {
		switch args[0] {
		case "show":
			return runConfigShow(args[1:], stdout, stderr)
		case "set":
			return runConfigSet(args[1:], stdout, stderr)
		case "features":
			args = args[1:]
		}
	}
	return runConfigFeatures(args, stdout, stderr)
}

// runConfigFeatures prints the feature table with the repo's current values. A
// directory outside any repo is judged as itself, so the defaults still show.
func runConfigFeatures(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repo whose declared values the table shows")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := tdd.RepoRoot(*repo)
	if root == "" {
		root = *repo
	}
	fmt.Fprint(stdout, tdd.RenderFeatures(root))
	return 0
}

// loadConfig reads every layer for the repo dir stands in, and says which repo
// that is ("" outside any).
func loadConfig(dir string) (cfg *config.Config, repo string) {
	repo = tdd.RepoRoot(dir)
	return config.Load(config.Options{Repo: repo, RepoID: config.RepoID(repo)}), repo
}

func runConfigShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "directory inside the repo to read")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if refuseArgs("config show", pos, stderr) {
		return 2
	}
	cfg, repo := loadConfig(*dir)
	if repo != "" {
		fmt.Fprintf(stdout, "repo        %s (id %s)\n", repo, config.RepoID(repo))
	} else {
		fmt.Fprintln(stdout, "repo        none: only the built-in and user layers are read")
	}
	fmt.Fprintf(stdout, "user config %s\n\n", filepath.Join(config.ConfigRoot(), "config.toml"))
	for _, s := range cfg.Settings() {
		fmt.Fprintf(stdout, "%-24s %-12s %s\n", s.Key, config.Display(s.Value), sourceOf(s))
	}
	if legacy := cfg.Legacy(); len(legacy) > 0 {
		fmt.Fprintln(stdout, "\naphrollo.toml keys with no schema equivalent, read under their own names:")
		for _, l := range legacy {
			fmt.Fprintf(stdout, "%-24s %s\n", l.Name, config.Display(l.Value))
		}
	}
	for _, d := range cfg.Diagnostics() {
		fmt.Fprintf(stderr, "config: %s\n", d)
	}
	return 0
}

// sourceOf is the layer column of a row: the layer, and the file and line or
// the alias that declared it.
func sourceOf(s config.Setting) string {
	if s.Layer == config.BuiltIn {
		if s.Fallback {
			return "built-in (the declared value was rejected, see stderr)"
		}
		return "built-in"
	}
	if s.Layer == config.Flag {
		return "flag"
	}
	if s.Layer == config.Env {
		return "env " + s.Alias + " (deprecated)"
	}
	if s.Alias != "" {
		return fmt.Sprintf("%s alias %s", s.Layer, s.Alias)
	}
	out := fmt.Sprintf("%s %s:%d", s.Layer, filepath.Base(s.File), s.Line)
	if len(s.Overrides) > 0 {
		out += " (overrides alias " + strings.Join(s.Overrides, ", ") + ")"
	}
	return out
}

func runConfigSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mf := addMutFlags(fs)
	user := fs.Bool("user", false, "write the user's config.toml")
	repoLayer := fs.Bool("repo", false, "write the repo's trellis.toml")
	dir := fs.String("dir", ".", "directory inside the repo")
	pos, err := mf.parse(fs, "config set", args, stderr)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "usage: aphrollo config set <key> <value> [--user|--repo] [--dir <dir>] [--dry]")
		return 2
	}
	if *user && *repoLayer {
		fmt.Fprintln(stderr, "aphrollo config set: --user and --repo name different files; pass one")
		return 2
	}
	k, ok := config.Lookup(pos[0])
	if !ok {
		fmt.Fprintf(stderr, "aphrollo config set: unknown key %q; `aphrollo config show` lists them\n", pos[0])
		return 1
	}
	layer := config.Repo
	switch {
	case *user:
		layer = config.User
	case !*repoLayer && !k.AllowedIn(config.Repo):
		layer = config.User
	}
	if !k.AllowedIn(layer) {
		fmt.Fprintf(stderr, "aphrollo config set: %s may not be set in the %s layer\n", k.Name, layer)
		return 1
	}
	v, err := k.Parse(pos[1])
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo config set: %s: %v\n", k.Name, err)
		return 1
	}
	repo := tdd.RepoRoot(*dir)
	path := filepath.Join(repo, "trellis.toml")
	if layer == config.User {
		path = filepath.Join(config.ConfigRoot(), "config.toml")
	} else if repo == "" {
		fmt.Fprintln(stderr, "aphrollo config set: not inside a git repository; pass --dir or --user")
		return 1
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "aphrollo config set: %v\n", err)
		return 1
	}
	text, err := config.SetText(string(old), k, v)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo config set: %s: %v\n", path, err)
		return 1
	}
	entry := k.Field + " = " + config.TOML(v)
	if !mf.execute() {
		fmt.Fprintf(stdout, "would write %s: %s\n", path, entry)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "aphrollo config set: %v\n", err)
		return 1
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		fmt.Fprintf(stderr, "aphrollo config set: %v\n", err)
		return 1
	}
	tdd.AppendEvent(tdd.Event{Kind: "config.set", Root: repo, Detail: map[string]string{
		"key": k.Name, "value": config.Display(v), "layer": layer.String(),
	}})
	fmt.Fprintf(stdout, "wrote %s: %s\n", path, entry)
	return 0
}
