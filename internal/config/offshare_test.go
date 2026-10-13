package config_test

import (
	"testing"
)

// ab-off-share is read from the [aphrollo] table of aphrollo.toml like the other
// aliased keys, defaults to 0 (no off arm) and refuses a share outside 0..100.
func TestAbOffShare_DefaultsToNoOffArmAndReadsFromAphrolloToml(t *testing.T) {
	repo := repoWith(t, map[string]string{})
	if got := loadRepo(repo).Get("ab-off-share"); got.Value.N != 0 {
		t.Errorf("default ab-off-share = %d, want 0", got.Value.N)
	}
	repo = repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nab-off-share = 25\n"})
	got := loadRepo(repo).Get("ab-off-share")
	if got.Value.N != 25 || got.Alias == "" {
		t.Errorf("ab-off-share = %+v, want 25 read through aphrollo.toml", got)
	}
}

func TestAbOffShare_ARepoCannotDeclareMoreThanAHundredPercent(t *testing.T) {
	repo := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nab-off-share = 101\n"})
	cfg := loadRepo(repo)
	if got := cfg.Get("ab-off-share"); got.Value.N != 0 {
		t.Errorf("ab-off-share = %d after an invalid 101, want the built-in 0", got.Value.N)
	}
	if len(cfg.Diagnostics()) == 0 {
		t.Errorf("an out-of-range ab-off-share must be named in the diagnostics")
	}
}
