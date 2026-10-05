package gc

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// gocacheRoot is a directory that looks like a Go build cache: it holds the README
// go writes into it.
func gocacheRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "README"), "This directory holds cached build artifacts from the Go build system.\n", 90*24*time.Hour)
	return dir
}

var gocacheNameRe = regexp.MustCompile(`^([0-9a-f]+)-([ad])$`)

// gocacheFile writes a file of size bytes last used age ago. A name such as
// "0a/aaaa-d" is padded to the 64 hex digits go names an entry by; any other name
// is written as it stands.
func gocacheFile(t *testing.T, dir, rel string, size int, age time.Duration) string {
	t.Helper()
	if m := gocacheNameRe.FindStringSubmatch(filepath.Base(rel)); m != nil {
		rel = filepath.Join(filepath.Dir(rel), strings.Repeat("0", 64-len(m[1]))+m[1]+"-"+m[2])
	}
	mkFile(t, filepath.Join(dir, rel), strings.Repeat("x", size), age)
	return filepath.Join(dir, rel)
}

func TestGoCacheTrim_RemovesOldestIdleFilesDownToTheCapAndNothingElse(t *testing.T) {
	dir := gocacheRoot(t)
	oldest := gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)
	older := gocacheFile(t, dir, "0b/bbbb-d", 100, 30*time.Hour)
	idle := gocacheFile(t, dir, "0c/cccc-a", 100, 20*time.Hour)
	young := gocacheFile(t, dir, "0d/dddd-a", 100, 5*time.Hour)
	recent := gocacheFile(t, dir, "0e/eeee-a", 100, 10*time.Minute)
	readme := gocacheFile(t, dir, "README", 100, 90*24*time.Hour)
	trimTxt := gocacheFile(t, dir, "trim.txt", 100, 90*24*time.Hour)
	fuzz := gocacheFile(t, dir, "fuzz/zzzz", 100, 90*24*time.Hour)

	// 500 bytes of entries, cap 250: the three oldest idle files go (300
	// bytes), the 12 h bar spares the 5 h one, and the oldest are taken first.
	got := trimGoCache(dir, 250, 12*time.Hour, time.Now(), true)

	if got.Files != 3 || got.Freed != 300 || got.Total != 500 {
		t.Fatalf("trim = %+v, want 3 files, 300 bytes freed of 500", got)
	}
	for _, p := range []string{oldest, older, idle} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived the trim", p)
		}
	}
	for _, p := range []string{young, recent, readme, trimTxt, fuzz} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	for _, d := range []string{"0a", "0b", "0c"} {
		if info, err := os.Stat(filepath.Join(dir, d)); err != nil || !info.IsDir() {
			t.Errorf("directory %s was removed; only files may go", d)
		}
	}
}

func TestGoCacheTrim_StopsAtTheCapBeforeTouchingYoungerIdleFiles(t *testing.T) {
	dir := gocacheRoot(t)
	oldest := gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)
	next := gocacheFile(t, dir, "0b/bbbb-d", 100, 30*time.Hour)

	got := trimGoCache(dir, 100, 12*time.Hour, time.Now(), true)

	if got.Files != 1 {
		t.Fatalf("removed %d files, want 1 (under the cap after the oldest)", got.Files)
	}
	if _, err := os.Stat(oldest); err == nil {
		t.Error("the oldest file survived")
	}
	if _, err := os.Stat(next); err != nil {
		t.Errorf("a file was removed after the cap was met: %v", err)
	}
}

func TestGoCacheTrim_UnderTheCapTouchesNothing(t *testing.T) {
	dir := gocacheRoot(t)
	p := gocacheFile(t, dir, "0a/aaaa-d", 100, 400*time.Hour)

	got := trimGoCache(dir, 1000, 12*time.Hour, time.Now(), true)

	if got.Files != 0 {
		t.Errorf("removed %d files from a cache under its cap", got.Files)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file removed: %v", err)
	}
}

func TestGoCacheTrim_AgeBarNeverDropsBelowAnHour(t *testing.T) {
	dir := gocacheRoot(t)
	recent := gocacheFile(t, dir, "0a/aaaa-d", 100, 30*time.Minute)

	got := trimGoCache(dir, 0, time.Minute, time.Now(), true)

	if got.Files != 0 {
		t.Errorf("removed a file used 30 minutes ago: %+v", got)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent file removed: %v", err)
	}
}

func TestGoCacheTrim_DryReportsAndRemovesNothing(t *testing.T) {
	dir := gocacheRoot(t)
	p := gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)

	got := trimGoCache(dir, 0, 12*time.Hour, time.Now(), false)

	if got.Files != 1 || got.Freed != 100 {
		t.Errorf("dry plan = %+v, want 1 file and 100 bytes", got)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("a dry trim removed %s: %v", p, err)
	}
}

func TestParseGoCacheCap_SizeUnits(t *testing.T) {
	for in, want := range map[string]int64{
		"20GB":  20 << 30,
		"20 GB": 20 << 30,
		"20g":   20 << 30,
		"512MB": 512 << 20,
		"1TB":   1 << 40,
	} {
		got, err := ParseGoCacheCap(in)
		if err != nil || got != want {
			t.Errorf("ParseGoCacheCap(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "GB", "-1GB", "0GB", "twenty", "20XB"} {
		if _, err := ParseGoCacheCap(in); err == nil {
			t.Errorf("ParseGoCacheCap(%q) accepted", in)
		}
	}
}

func TestReadGoCacheSettings_DefaultsAndKeys(t *testing.T) {
	root := t.TempDir()
	got, err := ReadGoCacheSettings(root)
	if err != nil || got.Cap != 20<<30 || got.Age != 12*time.Hour {
		t.Fatalf("defaults = %+v, %v; want 20 GB and 12h", got, err)
	}
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"),
		[]byte("[aphrollo]\ngocache-cap = \"5GB\"\ngocache-age = \"6h\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadGoCacheSettings(root)
	if err != nil || got.Cap != 5<<30 || got.Age != 6*time.Hour {
		t.Fatalf("declared = %+v, %v; want 5 GB and 6h", got, err)
	}
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"),
		[]byte("[aphrollo]\ngocache-cap = \"lots\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGoCacheSettings(root); err == nil || !strings.Contains(err.Error(), "gocache-cap") {
		t.Errorf("a malformed cap is refused naming the key, got %v", err)
	}
}

func TestGoCacheTrimDue_OncePerSixHours(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	now := time.Now()
	if !gocacheTrimDue(now) {
		t.Fatal("no stamp means due")
	}
	stampGoCacheTrim(now)
	if gocacheTrimDue(now.Add(5 * time.Hour)) {
		t.Error("due again after 5h")
	}
	if !gocacheTrimDue(now.Add(6*time.Hour + time.Minute)) {
		t.Error("not due after 6h")
	}
}

func TestTrimGoCacheOf_ReadsTheCachePathFromGoEnvOnce(t *testing.T) {
	dir := gocacheRoot(t)
	p := gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)
	calls := 0
	prev := goCacheDirFn
	goCacheDirFn = func() string { calls++; return dir }
	t.Cleanup(func() { goCacheDirFn = prev })

	got := TrimGoCache(GoCacheSettings{Cap: 1, Age: 12 * time.Hour}, true)

	if calls != 1 {
		t.Errorf("go env GOCACHE asked %d times, want 1", calls)
	}
	if got.Files != 1 {
		t.Errorf("trim = %+v", got)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("file survived")
	}
}

func TestGoEnvGoCache_NamesTheDirectoryGoEnvReports(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOCACHE", dir)

	got := goEnvGoCache()

	if filepath.Clean(got) != filepath.Clean(dir) {
		t.Errorf("goEnvGoCache() = %q, want %q", got, dir)
	}
}

// ratchet: test_removed TestGoEnvGoCache_ADisabledCacheIsNoDirectory: goEnvGoCache now answers what go printed, and the guard refuses "off" with a message: TestGoCacheTrim_RefusesADirectoryThatIsNotAGoCache

func TestGoEnvGoCache_ReportsADisabledCacheAsGoPrintsIt(t *testing.T) {
	t.Setenv("GOCACHE", "off")

	if got := goEnvGoCache(); got != "off" {
		t.Errorf("goEnvGoCache() = %q with the cache off, want off", got)
	}
}

func TestGoCacheTrim_OnlyEntriesNamedLikeGoNamesThemAreRemoved(t *testing.T) {
	dir := gocacheRoot(t)
	entry := gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)
	var kept []string
	for _, rel := range []string{
		"0a/notes.txt",                         // not an entry at all
		"0a/" + strings.Repeat("a", 64),        // no -a or -d tag
		"0a/" + strings.Repeat("a", 63) + "-a", // 63 digits
		"0a/" + strings.Repeat("A", 64) + "-a", // upper case
		"0a/" + strings.Repeat("a", 64) + "-x", // another tag
		"0a/" + strings.Repeat("a", 64) + "-a.bak",
	} {
		mkFile(t, filepath.Join(dir, rel), strings.Repeat("x", 100), 40*time.Hour)
		kept = append(kept, filepath.Join(dir, rel))
	}

	got := trimGoCache(dir, 0, 12*time.Hour, time.Now(), true)

	if got.Files != 1 {
		t.Errorf("removed %d files, want only the one entry: %+v", got.Files, got)
	}
	if _, err := os.Stat(entry); err == nil {
		t.Error("the entry survived")
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is not one of go's entries and was removed: %v", p, err)
		}
	}
}

func TestGoCacheTrim_RefusesADirectoryThatIsNotAGoCache(t *testing.T) {
	dir := t.TempDir() // no README, no trim.txt
	p := gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)

	got := trimGoCache(dir, 0, 12*time.Hour, time.Now(), true)

	if got.Refused == "" || !strings.Contains(got.Refused, "README") {
		t.Errorf("Refused = %q, want a message naming the missing README", got.Refused)
	}
	if got.Files != 0 {
		t.Errorf("removed %d files from a directory that is no Go cache", got.Files)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file removed: %v", err)
	}
}

func TestGoCacheTrim_ATrimTxtAloneMakesItAGoCache(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "trim.txt"), "1", 90*24*time.Hour)
	gocacheFile(t, dir, "0a/aaaa-d", 100, 40*time.Hour)

	if got := trimGoCache(dir, 0, 12*time.Hour, time.Now(), true); got.Refused != "" || got.Files != 1 {
		t.Errorf("trim = %+v, want the entry removed", got)
	}
}

func TestGoCacheRefusal_NamesWhyADirectoryIsNotOneToTrim(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.VolumeName(home) + string(filepath.Separator)
	for name, dir := range map[string]string{
		"empty":    "",
		"off":      "off",
		"relative": filepath.Join("some", "cache"),
		"home":     home,
		"temp":     os.TempDir(),
		"root":     root,
	} {
		if why := goCacheRefusal(dir); why == "" {
			t.Errorf("%s (%q) was accepted as a cache to trim", name, dir)
		}
	}
	if why := goCacheRefusal(gocacheRoot(t)); why != "" {
		t.Errorf("a cache directory was refused: %s", why)
	}
}

func TestTrimGoCache_APathGoEnvGaveThatIsNoCacheIsRefusedAndReported(t *testing.T) {
	prev := goCacheDirFn
	goCacheDirFn = func() string { return "off" }
	t.Cleanup(func() { goCacheDirFn = prev })

	got := TrimGoCache(GoCacheSettings{Cap: 1, Age: DefaultGoCacheAge}, true)

	if got.Refused == "" || !strings.Contains(RenderGoCacheTrim(got, true), "not trimmed") {
		t.Errorf("trim = %+v, rendered %q, want a refusal that the line says", got, RenderGoCacheTrim(got, true))
	}
}

func TestGoCacheTrim_NeverUsesAFileFromTheLastTwoHoursWhateverAgeIsAsked(t *testing.T) {
	dir := gocacheRoot(t)
	p := gocacheFile(t, dir, "0a/aaaa-d", 100, 90*time.Minute)

	got := trimGoCache(dir, 0, time.Minute, time.Now(), true)

	if got.Files != 0 {
		t.Errorf("removed a file used 90 minutes ago: %+v", got)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file removed: %v", err)
	}
}

func TestReadGoCacheSettings_AnAgeUnderTwoHoursIsClampedWithAWarningNamingTheKey(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"90m", "soon"} {
		if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte("[aphrollo]\ngocache-age = \""+bad+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := ReadGoCacheSettings(root)

		if err != nil || got.Age != 2*time.Hour {
			t.Errorf("%s: got %+v, %v; want the age clamped to 2h and no error, so the rest of gc still runs", bad, got, err)
		}
		if !strings.Contains(got.Warning, "gocache-age") || !strings.Contains(got.Warning, "2h") {
			t.Errorf("%s: warning = %q, want the key and the 2h named", bad, got.Warning)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte("[aphrollo]\ngocache-age = \"2h\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadGoCacheSettings(root); err != nil || got.Age != 2*time.Hour || got.Warning != "" {
		t.Errorf("2h itself is the minimum and must read clean, got %+v, %v", got, err)
	}
}

// ratchet: test_removed TestReadGoCacheSettings_AnAgeUnderTwoHoursIsRefusedNamingTheKey: an age under the minimum no longer aborts the sweep; TestReadGoCacheSettings_AnAgeUnderTwoHoursIsClampedWithAWarningNamingTheKey holds it.
