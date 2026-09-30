package lang

import (
	"regexp"
	"testing"
	"testing/fstest"
)

func TestEmbeddedDigest_IsStableSixteenHexDigits(t *testing.T) {
	a, b := EmbeddedDigest(), EmbeddedDigest()
	if a != b {
		t.Errorf("two calls differ: %q and %q", a, b)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(a) {
		t.Errorf("digest = %q, want 16 hex digits", a)
	}
}

func TestDigestOf_FollowsTheNamesAndTextOfTheFiles(t *testing.T) {
	base := fstest.MapFS{"d/a.toml": {Data: []byte("one")}, "d/b.toml": {Data: []byte("two")}}
	want := digestOf(base, "d")
	if want == "" || want != digestOf(base, "d") {
		t.Fatalf("digest = %q, want a stable non-empty one", want)
	}
	edited := fstest.MapFS{"d/a.toml": {Data: []byte("one!")}, "d/b.toml": {Data: []byte("two")}}
	if digestOf(edited, "d") == want {
		t.Error("edited text kept the digest")
	}
	renamed := fstest.MapFS{"d/c.toml": {Data: []byte("one")}, "d/b.toml": {Data: []byte("two")}}
	if digestOf(renamed, "d") == want {
		t.Error("a renamed file kept the digest")
	}
	added := fstest.MapFS{"d/a.toml": {Data: []byte("one")}, "d/b.toml": {Data: []byte("two")}, "d/c.toml": {Data: []byte("")}}
	if digestOf(added, "d") == want {
		t.Error("an added empty file kept the digest")
	}
}

func TestDigestOf_ANameIsNotJoinedToItsText(t *testing.T) {
	a := fstest.MapFS{"d/ab": {Data: []byte("c")}}
	b := fstest.MapFS{"d/a": {Data: []byte("bc")}}
	if digestOf(a, "d") == digestOf(b, "d") {
		t.Error("file ab holding c and file a holding bc share a digest")
	}
}

func TestDigestOf_SkipsAnEntryThatIsNotAFileAndAMissingDirectoryIsEmpty(t *testing.T) {
	plain := fstest.MapFS{"d/a.toml": {Data: []byte("one")}}
	withDir := fstest.MapFS{"d/a.toml": {Data: []byte("one")}, "d/sub/x.toml": {Data: []byte("nested")}}
	if digestOf(withDir, "d") != digestOf(plain, "d") {
		t.Error("a subdirectory entry moved the digest: it cannot be read as a file and is skipped")
	}
	if got := digestOf(plain, "missing"); got != "" {
		t.Errorf("a missing directory digests to %q, want empty", got)
	}
}
