package sqlc

import (
	"strings"
	"testing"
)

func TestNameOf(t *testing.T) {
	cases := map[string]string{
		"func (q *Queries) GetWidget(ctx context.Context) error {": "GetWidget",
		"func GetWidget(ctx context.Context) error {":              "GetWidget",
		"type GetWidgetParams struct {":                            "GetWidgetParams",
		"type Widget struct {":                                     "Widget",
		"const getWidget = `SELECT 1`":                             "getWidget",
		"var foo = 1":                                              "foo",
		"\tnot a top-level decl":                                   "",
		"import \"context\"":                                       "",
		"package gen":                                              "",
	}
	for line, want := range cases {
		if got := nameOf(line); got != want {
			t.Errorf("nameOf(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestInScopeSet(t *testing.T) {
	set := inScopeSet([]string{"AppendStreamEvent"})
	for _, want := range []string{"AppendStreamEvent", "appendStreamEvent", "AppendStreamEventParams", "AppendStreamEventRow"} {
		if !set[want] {
			t.Errorf("in-scope set missing %q", want)
		}
	}
	if set["CrmTicket"] {
		t.Errorf("CrmTicket must not be in scope for an AppendStreamEvent change")
	}
}

func TestParseQueryBlocks(t *testing.T) {
	sql := `-- name: GetWidget :one
SELECT * FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT * FROM widget;
`
	blocks := parseQueryBlocks(sql)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if !strings.Contains(blocks["GetWidget"], "WHERE id = $1") {
		t.Errorf("GetWidget block wrong: %q", blocks["GetWidget"])
	}
}

func TestChangedQueryNamesFromContent(t *testing.T) {
	base := `-- name: GetWidget :one
SELECT id, name FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT id, name FROM widget;
`
	work := `-- name: GetWidget :one
SELECT id, name, color FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT id, name FROM widget;
`
	changed := changedNamesBetween(base, work)
	if len(changed) != 1 || changed[0] != "GetWidget" {
		t.Errorf("want [GetWidget], got %v", changed)
	}
}

// scopedMerge keeps committed content, swapping in regen only for in-scope
// symbols (the changed query's func/const/Params/Row) and dropping drift.
func TestScopedMergeAppliesOnlyInScope(t *testing.T) {
	committed := `package gen

import "context"

const getWidget = ` + "`SELECT id, name FROM widget WHERE id = $1`" + `

func (q *Queries) GetWidget(ctx context.Context, id int64) (Widget, error) {
	return Widget{}, nil
}

func (q *Queries) ListWidgets(ctx context.Context) ([]Widget, error) {
	return nil, nil
}
`
	// Regen: GetWidget gained a column (in-scope), ListWidgets body churned
	// (drift — ListWidgets wasn't a changed query), and a brand-new drift type
	// CrmTicketType appeared (unrelated migration).
	regen := `package gen

import "context"

const getWidget = ` + "`SELECT id, name, color FROM widget WHERE id = $1`" + `

func (q *Queries) GetWidget(ctx context.Context, id int64) (Widget, error) {
	return Widget{Color: ""}, nil
}

func (q *Queries) ListWidgets(ctx context.Context) ([]Widget, error) {
	return []Widget{}, nil
}

type CrmTicketType struct {
	ID int64
}
`
	inScope := inScopeSet([]string{"GetWidget"})
	merged := scopedMerge(committed, regen, func(n string) bool { return inScope[n] })

	// In-scope GetWidget hunks applied.
	if !strings.Contains(merged, "name, color FROM widget") {
		t.Errorf("in-scope GetWidget const not applied:\n%s", merged)
	}
	if !strings.Contains(merged, "Widget{Color: \"\"}") {
		t.Errorf("in-scope GetWidget func body not applied:\n%s", merged)
	}
	// Drift backed out: ListWidgets stays at the committed body.
	if !strings.Contains(merged, "return nil, nil") {
		t.Errorf("ListWidgets drift should be backed out (kept committed):\n%s", merged)
	}
	if strings.Contains(merged, "return []Widget{}, nil") {
		t.Errorf("ListWidgets drift body leaked into merged output:\n%s", merged)
	}
	// Drift addition omitted entirely.
	if strings.Contains(merged, "CrmTicketType") {
		t.Errorf("drift-added type CrmTicketType must not be applied:\n%s", merged)
	}
}

func TestScopedMergeAppliesInScopeAddition(t *testing.T) {
	committed := `package gen

func (q *Queries) GetWidget() {}
`
	regen := `package gen

func (q *Queries) GetWidget() {}

func (q *Queries) CountWidgets() int { return 0 }
`
	inScope := inScopeSet([]string{"CountWidgets"})
	merged := scopedMerge(committed, regen, func(n string) bool { return inScope[n] })
	if !strings.Contains(merged, "CountWidgets") {
		t.Errorf("in-scope new query CountWidgets should be added:\n%s", merged)
	}
}
