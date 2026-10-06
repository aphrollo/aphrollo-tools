package shadow

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func TestLangOf_NamesTheLanguageOfAUnitAndOfARunnersCommand(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"internal/lane", "go"},
		{".", "go"},
		{"tools/x/pkg", "go"},
		{"python:backend", "python"},
		{"typescript:frontend", "ts"},
		{"javascript:web", "ts"},
		{"rust:crates/engine", "rust"},
		{"other:.", "other"},
		{"", ""},
	} {
		if got := LangOfUnit(c.in); got != c.want {
			t.Errorf("LangOfUnit(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct{ in, want string }{
		{"go", "go"},
		{"python", "python"}, {"python3", "python"}, {"pytest", "python"}, {"py", "python"},
		{"npx", "ts"}, {"npm", "ts"}, {"node", "ts"}, {"vitest", "ts"}, {"jest", "ts"}, {"pnpm", "ts"}, {"yarn", "ts"}, {"bun", "ts"},
		{"cargo", "rust"},
		{"make", ""},
		{"", ""},
	} {
		if got := LangOfCommand(c.in); got != c.want {
			t.Errorf("LangOfCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedGreen_ARecordNamesItsLanguageInTheEvent(t *testing.T) {
	b := pjBox(t)
	for _, c := range []struct{ file, want string }{
		{"backend/app/service.py", "python"},
		{"frontend/src/api.ts", "ts"},
		{"backend-go/pkg/p.go", "go"},
	} {
		r := b.askRedGreen(c.file)
		if r.Lang != c.want || r.event(Source{Root: b.root}, b.lane).Detail["lang"] != c.want {
			t.Errorf("%s: lang %q, want %q (record and event)", c.file, r.Lang, c.want)
		}
	}
}

func TestFlush_ARunsRecordNamesTheLanguageOfItsRunner(t *testing.T) {
	got := capture(t)
	QueueRun(Source{Root: t.TempDir(), Lang: "python"}, func() (RunFact, bool) {
		return RunFact{Word: "green (3 passed)", Verdict: kernel.VerdictGreen}, true
	})
	QueueRun(Source{Root: t.TempDir(), Lang: "ts"}, func() (RunFact, bool) { return RunFact{Word: "no-verdict-word"}, false })
	Flush()
	if len(*got) != 2 || (*got)[0].Detail["lang"] != "python" || (*got)[1].Detail["lang"] != "ts" {
		t.Errorf("events = %+v, want a judged and an unjudged run each naming its language", *got)
	}
}
