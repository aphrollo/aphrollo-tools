package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// box31 is the box issue #1005 happened on: 31 GB, 25 GB available, swap
// nearly full.
var box31 = MemBox{RAMMB: 31763, AvailMB: 26124, SwapTotalMB: 9215, SwapFreeMB: 663}

func TestDeriveMemCap_SlotRunGetsItsShareOfThePool(t *testing.T) {
	// pool = min(31763*75/100 = 23822, avail 26124) = 23822; two slots.
	c := deriveMemCap(box31, 2, CapSlot)
	if c.MB != 11911 {
		t.Fatalf("slot cap = %dMB (%s), want 11911MB (23822/2)", c.MB, c.Why)
	}
	if c.KillLargest {
		t.Fatal("a slot run must be ended whole at its cap, not spared but for its largest process")
	}
}

func TestDeriveMemCap_BusyBoxHandsOutLessNeverMore(t *testing.T) {
	busy := MemBox{RAMMB: 31763, AvailMB: 8000}
	c := deriveMemCap(busy, 2, CapSlot)
	if c.MB != 4000 {
		t.Fatalf("cap on a box with 8000MB free = %dMB (%s), want 4000MB (free/2)", c.MB, c.Why)
	}
	if !strings.Contains(c.Why, "free 8000MB (measured)") {
		t.Fatalf("why = %q, want the measured free term named", c.Why)
	}
}

func TestDeriveMemCap_AvailAboveRAMShareDoesNotRaiseTheCap(t *testing.T) {
	idle := MemBox{RAMMB: 8192, AvailMB: 8000}
	c := deriveMemCap(idle, 1, CapSlot)
	if c.MB != 6144 {
		t.Fatalf("cap = %dMB, want 6144MB (75%% of RAM; the 8000MB free reading is above it)", c.MB)
	}
}

func TestDeriveMemCap_MutationRunGetsTheWholePool(t *testing.T) {
	c := deriveMemCap(box31, 2, CapMutation)
	if c.MB != 23822 {
		t.Fatalf("mutation cap = %dMB (%s), want the whole 23822MB pool", c.MB, c.Why)
	}
	if !c.KillLargest {
		t.Fatal("a mutation run's runaway is one mutant's test: only the largest process may be killed")
	}
}

func TestDeriveMemCap_FloorKeepsHonestWorkAlive(t *testing.T) {
	tiny := MemBox{RAMMB: 3000, AvailMB: 1000}
	c := deriveMemCap(tiny, 2, CapSlot)
	if c.MB != memCapFloorMB || !strings.Contains(c.Why, "floor") {
		t.Fatalf("cap = %dMB (%s), want the %dMB floor named", c.MB, c.Why, memCapFloorMB)
	}
}

func TestDeriveMemCap_UnknownRAMIsNoCap(t *testing.T) {
	c := deriveMemCap(MemBox{}, 2, CapSlot)
	if c.MB != 0 {
		t.Fatalf("cap = %dMB, want 0 (no cap) when the box could not be read", c.MB)
	}
}

func TestDeriveMemCap_ZeroSlotsIsOne(t *testing.T) {
	if got := deriveMemCap(box31, 0, CapSlot).MB; got != 23822 {
		t.Fatalf("cap with 0 slots = %dMB, want the whole pool 23822MB", got)
	}
}

func TestParseMemGB_PositiveWholeGBAndOffWhereAllowed(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		allowOff bool
		gb       int64
		off      bool
		bad      bool
	}{
		{"12", true, 12, false, false},
		{" 8 ", false, 8, false, false},
		{"off", true, 0, true, false},
		{"OFF", true, 0, true, false},
		{"off", false, 0, false, true},
		{"0", true, 0, false, true},
		{"-3", true, 0, false, true},
		{"12GB", true, 0, false, true},
		{"", true, 0, false, true},
	} {
		gb, off, err := parseMemGB("memory-cap", tc.raw, tc.allowOff)
		if (err != nil) != tc.bad || gb != tc.gb || off != tc.off {
			t.Errorf("parseMemGB(%q, allowOff=%v) = (%d, %v, %v), want (%d, %v, bad=%v)",
				tc.raw, tc.allowOff, gb, off, err, tc.gb, tc.off, tc.bad)
		}
	}
}

// repoWithConfig makes a directory with an aphrollo.toml and a .git marker,
// and returns a subdirectory of it, the shape a run started in a package dir
// has.
func repoWithConfig(t *testing.T, toml string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "inner")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func withBox(t *testing.T, box MemBox) {
	t.Helper()
	prev := memBoxFn
	memBoxFn = func() MemBox { return box }
	t.Cleanup(func() { memBoxFn = prev })
}

func TestMemCapFor_ConfigOverridesTheDerivedCapFromASubdirectory(t *testing.T) {
	withBox(t, box31)
	dir := repoWithConfig(t, "[aphrollo]\nmemory-cap = \"6\"\n")
	c := MemCapFor(dir, CapSlot)
	if c.MB != 6144 {
		t.Fatalf("cap = %dMB (%s), want 6144MB from memory-cap = 6", c.MB, c.Why)
	}
}

func TestMemCapFor_OffDisablesTheCap(t *testing.T) {
	withBox(t, box31)
	dir := repoWithConfig(t, "[aphrollo]\nmemory-cap = \"off\"\n")
	if c := MemCapFor(dir, CapSlot); c.MB != 0 {
		t.Fatalf("cap = %dMB, want none for memory-cap = off", c.MB)
	}
}

func TestMemCapFor_UnparsableValueFallsBackToDerivedAndSaysSo(t *testing.T) {
	withBox(t, box31)
	t.Setenv(buildSlotsEnv, "2")
	dir := repoWithConfig(t, "[aphrollo]\nmemory-cap = \"lots\"\n")
	c := MemCapFor(dir, CapSlot)
	if c.MB != 11911 {
		t.Fatalf("cap = %dMB, want the derived 11911MB, never no cap, for a typo", c.MB)
	}
	if !strings.Contains(c.Why, `memory-cap must be`) {
		t.Fatalf("why = %q, want the refusal named", c.Why)
	}
}

func TestMemCapFor_NoConfigIsDerived(t *testing.T) {
	withBox(t, box31)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(buildSlotsEnv, "2")
	if c := MemCapFor(dir, CapSlot); c.MB != 11911 {
		t.Fatalf("cap = %dMB, want the derived 11911MB", c.MB)
	}
}

func TestDeriveHeadroomMB_AnEighthOfRAMClamped(t *testing.T) {
	for _, tc := range []struct {
		ram, want int64
	}{
		{0, 0},
		{8192, 2048},   // 1024 raised to the floor
		{31763, 3970},  // an eighth
		{262144, 8192}, // 32768 lowered to the ceiling
	} {
		if got := deriveHeadroomMB(MemBox{RAMMB: tc.ram}); got != tc.want {
			t.Errorf("deriveHeadroomMB(ram=%d) = %d, want %d", tc.ram, got, tc.want)
		}
	}
}

func TestSwapPressured_NinetyPercentIsTheLine(t *testing.T) {
	at := MemBox{SwapTotalMB: 1000, SwapFreeMB: 100}
	below := MemBox{SwapTotalMB: 1000, SwapFreeMB: 101}
	if !swapPressured(at) {
		t.Error("swap 90% used must be pressure")
	}
	if swapPressured(below) {
		t.Error("swap 89.9% used must not be pressure")
	}
	if swapPressured(MemBox{}) {
		t.Error("no swap is not pressure")
	}
}

func TestHeadroomVerdict_StartsOrRefusesWithTheNumbers(t *testing.T) {
	t.Run("enough is a start", func(t *testing.T) {
		if ok, why := headroomVerdict(MemBox{RAMMB: 32768, AvailMB: 4096}, 0); !ok || why != "" {
			t.Fatalf("got (%v, %q), want a start with 4096MB free and 4096MB needed", ok, why)
		}
	})
	t.Run("one megabyte short is a refusal that says the numbers", func(t *testing.T) {
		ok, why := headroomVerdict(MemBox{RAMMB: 32768, AvailMB: 4095}, 0)
		if ok || !strings.Contains(why, "4.0 GB available") || !strings.Contains(why, "needs 4.0 GB") {
			t.Fatalf("got (%v, %q), want a refusal naming 4.0 GB available against 4.0 GB", ok, why)
		}
	})
	t.Run("full swap doubles the requirement", func(t *testing.T) {
		box := MemBox{RAMMB: 32768, AvailMB: 6000, SwapTotalMB: 1000, SwapFreeMB: 50}
		ok, why := headroomVerdict(box, 0)
		if ok || !strings.Contains(why, "swap 95% full") {
			t.Fatalf("got (%v, %q), want a refusal naming the swap doubling: 6000MB < 2*4096MB", ok, why)
		}
	})
	t.Run("an unreadable box is not scarce", func(t *testing.T) {
		if ok, _ := headroomVerdict(MemBox{RAMMB: 32768}, 0); !ok {
			t.Fatal("unknown available memory must start, never refuse")
		}
	})
	t.Run("a configured requirement replaces the derived one", func(t *testing.T) {
		if ok, _ := headroomVerdict(MemBox{RAMMB: 32768, AvailMB: 9000}, 10*1024); ok {
			t.Fatal("9000MB free against a configured 10GB must refuse")
		}
	})
}

// fakeClock replaces the wait's sleep and clock: sleeping advances time, so
// a wait is proven without waiting.
func fakeClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	prevSleep, prevNow := headroomSleepFn, headroomNowFn
	headroomSleepFn = func(d time.Duration) { now = now.Add(d) }
	headroomNowFn = func() time.Time { return now }
	t.Cleanup(func() { headroomSleepFn, headroomNowFn = prevSleep, prevNow })
	return &now
}

func TestWaitForHeadroom_StartsAtOnceWhenThereIsRoom(t *testing.T) {
	withBox(t, MemBox{RAMMB: 32768, AvailMB: 20000})
	fakeClock(t)
	if why := WaitForHeadroom(t.TempDir(), time.Minute); why != "" {
		t.Fatalf("refused with %q on a box with room", why)
	}
}

func TestWaitForHeadroom_StartsOnceMemoryFreesUp(t *testing.T) {
	calls := 0
	prev := memBoxFn
	memBoxFn = func() MemBox {
		calls++
		if calls < 4 {
			return MemBox{RAMMB: 32768, AvailMB: 1000}
		}
		return MemBox{RAMMB: 32768, AvailMB: 20000}
	}
	t.Cleanup(func() { memBoxFn = prev })
	clock := fakeClock(t)
	start := *clock
	if why := WaitForHeadroom(t.TempDir(), time.Minute); why != "" {
		t.Fatalf("refused with %q though memory freed up on the 4th look", why)
	}
	if got := clock.Sub(start); got != 3*memHeadroomPollEvery {
		t.Fatalf("waited %s, want 3 polls (%s)", got, 3*memHeadroomPollEvery)
	}
}

func TestWaitForHeadroom_RefusesAfterTheBoundWithOneLine(t *testing.T) {
	withBox(t, MemBox{RAMMB: 32768, AvailMB: 1000})
	clock := fakeClock(t)
	start := *clock
	why := WaitForHeadroom(t.TempDir(), 20*time.Second)
	if !strings.Contains(why, "memory headroom: 1.0 GB available") || !strings.Contains(why, "(waited 20s)") || strings.Contains(why, "\n") {
		t.Fatalf("refusal = %q, want one line naming the numbers and the 20s wait", why)
	}
	if got := clock.Sub(start); got > 20*time.Second {
		t.Fatalf("waited %s past the 20s bound", got)
	}
}

func TestWaitForHeadroom_ConfiguredRequirementApplies(t *testing.T) {
	withBox(t, MemBox{RAMMB: 32768, AvailMB: 9000})
	fakeClock(t)
	dir := repoWithConfig(t, "[aphrollo]\nmemory-headroom = \"10\"\n")
	why := WaitForHeadroom(dir, 0)
	if !strings.Contains(why, "needs 10.0 GB") {
		t.Fatalf("refusal = %q, want the configured 10 GB requirement", why)
	}
}

func TestParseMeminfoMB_ReadsTheFourKeysAndLeavesTheRestUnknown(t *testing.T) {
	data := "MemTotal:       32526536 kB\nMemFree:         1000 kB\nMemAvailable:   26750000 kB\nSwapTotal:       9437180 kB\nSwapFree:         679000 kB\nBogus: x kB\n"
	got := parseMeminfoMB(data)
	want := MemBox{RAMMB: 31764, AvailMB: 26123, SwapTotalMB: 9215, SwapFreeMB: 663}
	if got != want {
		t.Fatalf("parseMeminfoMB = %+v, want %+v", got, want)
	}
	if empty := parseMeminfoMB("MemTotal: junk kB"); empty != (MemBox{}) {
		t.Fatalf("an unparsable key must stay unknown, got %+v", empty)
	}
}

func TestMemCapSplitAmong_ShardsShareADerivedPoolNotADeclaredCap(t *testing.T) {
	pool := deriveMemCap(box31, 2, CapMutation) // 23822MB
	if got := pool.splitAmong(4); got.MB != 5955 || !strings.Contains(got.Why, "one of 4 shards") {
		t.Fatalf("derived pool split four ways = %dMB (%s), want 5955MB naming the shards", got.MB, got.Why)
	}
	if got := pool.splitAmong(1); got.MB != pool.MB {
		t.Fatalf("one shard must keep the whole pool, got %dMB", got.MB)
	}
	if got := pool.splitAmong(20); got.MB != memCapFloorMB {
		t.Fatalf("twenty shards = %dMB, want the %dMB floor", got.MB, memCapFloorMB)
	}
	declared := MemCap{MB: 6144, Why: "memory-cap = 6 in aphrollo.toml"}
	if got := declared.splitAmong(4); got.MB != 6144 {
		t.Fatalf("a declared cap is the repo's word for one run and must not be divided, got %dMB", got.MB)
	}
}

func TestMemCapFor_ADeclaredCapIsNotMarkedDerived(t *testing.T) {
	withBox(t, box31)
	dir := repoWithConfig(t, "[aphrollo]\nmemory-cap = \"6\"\n")
	if MemCapFor(dir, CapMutation).Derived {
		t.Fatal("a cap read from aphrollo.toml is declared, not derived")
	}
	if !deriveMemCap(box31, 2, CapSlot).Derived {
		t.Fatal("a cap worked out from the box is derived")
	}
}
