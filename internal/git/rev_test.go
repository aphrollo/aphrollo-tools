package git

import "testing"

func TestRev_ResolvesAShortNameLikeRevParseWithoutSpawning(t *testing.T) {
	dir := repoWithCommit(t)
	want := gitT(t, dir, "rev-parse", "HEAD")
	gitT(t, dir, "branch", "topic")
	gitT(t, dir, "tag", "v1")
	c := mustNew(t, dir)

	for _, name := range []string{"topic", "v1", "refs/heads/topic"} {
		if got := c.Rev(name); got != want {
			t.Errorf("Rev(%q) = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"nope", "topic..topic", ""} {
		if got := c.Rev(name); got != "" {
			t.Errorf("Rev(%q) = %q, want nothing", name, got)
		}
	}
	if c.Spawns() != 0 {
		t.Errorf("spawns = %d, want 0", c.Spawns())
	}
}
