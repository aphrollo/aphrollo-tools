package tdd

import (
	"strings"
	"testing"
)

// The README's configuration table and the table install prints are one
// source (issue #877): its opt-in rows are rendered from the same Feature
// table, in order and verbatim, and no other row of the README names one of
// those keys, so a key cannot be documented twice, telling two stories.
func TestReadme_ConfigurationRowsAreTheFeatureTable(t *testing.T) {
	t.Parallel()
	readme := strings.ReplaceAll(repoFile(t, "README.md"), "\r\n", "\n")
	rows := featureReadmeRows()
	if !strings.Contains(readme, strings.Join(rows, "\n")+"\n") {
		t.Errorf("README.md does not carry the opt-in rows verbatim and in order; paste them from featureReadmeRows():\n%s",
			strings.Join(rows, "\n"))
	}
	for _, line := range strings.Split(readme, "\n") {
		if !strings.HasPrefix(line, "|") || strings.Contains(strings.Join(rows, "\n"), line) {
			continue
		}
		for _, f := range features {
			if strings.Contains(line, "`"+f.Key+"`") {
				t.Errorf("README.md documents %s outside its feature row: %s", f.Key, line)
			}
		}
	}
}
