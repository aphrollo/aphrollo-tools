package tdd

import "testing"

// Issue #747: a crate's content-gated tests return early unless an
// environment switch points them at their content. The gate never sets it,
// so its run of that crate is green with every such test silently skipped,
// and a hand run that DOES set the switch was refused as redundant beside
// that green. A test that returns early is counted as passed by libtest and
// nextest alike, so the green cannot say it skipped anything; what the gate
// can know is that the repo declares the switch (fail-first-env) and that
// this run sets it, which no gate run did.

// envSwitchRoot is a cargo repo declaring one content switch for its
// fail-first proof, with a fresh whole-crate green logged for it.
func envSwitchRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_lab\"\nversion = \"0.1.0\"\n\n"+
		"[workspace]\n[workspace.metadata.aphrollo]\nfail-first-env = [\"FORGE_BEAMNG_VEHICLES=/content/vehicles\"]\n")
	write(t, root, "src/lib.rs", "pub fn roll() {}\n")
	appendGateLog("postedit", root, "cargo nextest run", "green", 0)
	return root
}

func TestDecideBashSuite_AllowsAndCountsARunThatSetsADeclaredSwitch(t *testing.T) {
	cases := []struct{ name, cmd string }{
		{"a narrowed run with a leading assignment", "FORGE_BEAMNG_VEHICLES=/content/vehicles cargo nextest run -p forge_lab -E 'test(/^suspension::roll::/)'"},
		{"a whole-crate run with a leading assignment", "FORGE_BEAMNG_VEHICLES=/content/vehicles cargo nextest run"},
		{"an export before the run", "export FORGE_BEAMNG_VEHICLES=/content/vehicles && cargo nextest run -p forge_lab"},
		{"a PowerShell assignment before the run", `$env:FORGE_BEAMNG_VEHICLES = "D:\content\vehicles"; cargo nextest run -p forge_lab`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			root := envSwitchRoot(t)

			raw := bashPayload(t, "s1", root, c.cmd)
			d, judged := DecideBashSuite(raw)
			if !judged {
				t.Fatalf("%q must be judged as a suite run", c.cmd)
			}
			if d.Action != Allow {
				t.Fatalf("a run setting a declared switch must be allowed beside the gate's green, got %v (reason %q)", d.Action, d.Reason)
			}
			LogBashSuiteDecision(raw, d)
			requireLoggedVerdict(t, cfg, "override-bash-env-switch")
		})
	}
}

// A switch the repo does not declare is not one the gate's run lacked for a
// known reason: RUST_BACKTRACE changes what a run prints, not what it tests,
// and the refusal stands.
func TestDecideBashSuite_StillRefusesARunThatSetsAnUndeclaredVariable(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := envSwitchRoot(t)

	d := decideBash(t, "s1", root, "RUST_BACKTRACE=1 cargo nextest run -p forge_lab -E 'test(/^suspension::roll::/)'")

	if d.Action != Block {
		t.Fatalf("a rerun setting only an undeclared variable must stay refused, got %v", d.Action)
	}
}
