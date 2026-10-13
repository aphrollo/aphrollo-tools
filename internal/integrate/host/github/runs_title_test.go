package github

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// A run listing carries the title of the run's commit, which names the PR a squash merge
// made ("Add a thing (#12)"): what ties a red run on main to the merge that produced it.
func TestRunsOn_AskForAndDecodeTheCommitTitleOfEachRun(t *testing.T) {
	if !strings.Contains(listFields, "displayTitle") {
		t.Errorf("listFields = %q, want displayTitle among them", listFields)
	}
	var runs []host.WorkflowRun
	if err := json.Unmarshal([]byte(`[{"databaseId":7,"headSha":"abc","displayTitle":"Add a thing (#12)"}]`), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Title != "Add a thing (#12)" {
		t.Errorf("runs = %+v, want the title decoded", runs)
	}
}
