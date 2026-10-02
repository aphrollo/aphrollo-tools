package undercover

import "testing"

func TestRefName_AcceptsOnlyTheExactAssignedBranchOfARemoteSession(t *testing.T) {
	const assigned = "claude/fix-login-8f3a"
	l := New(nil)
	cases := []struct {
		name       string
		remote     string
		assignedTo string
		ref        string
		wantHit    bool
	}{
		{"exact name in a remote session passes", "true", assigned, assigned, false},
		{"a prefix of it is still refused", "true", assigned, "claude/fix-login", true},
		{"a longer name is still refused", "true", assigned, assigned + "-2", true},
		{"another claude branch is still refused", "true", assigned, "claude/other-1", true},
		{"a different case is still refused", "true", assigned, "Claude/fix-login-8f3a", true},
		{"outside a remote session the name is refused", "", assigned, assigned, true},
		{"no assigned name refuses everything", "true", "", assigned, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(RemoteEnv, c.remote)
			t.Setenv(AssignedBranchEnv, c.assignedTo)
			if _, hit := l.RefName(c.ref); hit != c.wantHit {
				t.Fatalf("RefName(%q) hit = %v, want %v", c.ref, hit, c.wantHit)
			}
		})
	}
}
