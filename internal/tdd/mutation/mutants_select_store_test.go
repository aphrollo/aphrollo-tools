package mutation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// selStoreTree is a repository whose packages p and q are the fixture.
func selStoreTree(t *testing.T) string {
	t.Helper()
	root := covermapRepo(t)
	mustWrite(t, filepath.Join(root, "p", "p.go"), "package p\n\nfunc F() int { return 1 }\n")
	mustWrite(t, filepath.Join(root, "p", "p_test.go"), "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = F() }\n")
	mustWrite(t, filepath.Join(root, "q", "q_test.go"), "package q\n")
	mustWrite(t, filepath.Join(root, "other", "o.go"), "package other\n")
	return root
}

func selStoreKey(t *testing.T, root string, tags []string) string {
	t.Helper()
	key, err := selKey(context.Background(), root, MutantsConfig{}, tags, []string{"p"}, []string{"p", "q"})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSelKey_MovesWhenSourceTestsTagsOrToolchainChange(t *testing.T) {
	root := selStoreTree(t)
	base := selStoreKey(t, root, nil)
	if again := selStoreKey(t, root, nil); again != base {
		t.Fatalf("the same tree keyed twice gave %s and %s", base, again)
	}
	mustWrite(t, filepath.Join(root, "other", "o.go"), "package other\n\nvar X = 1\n")
	if got := selStoreKey(t, root, nil); got != base {
		t.Error("a file of a package outside the measured ones moved the key")
	}
	if got := selStoreKey(t, root, []string{"integration"}); got == base {
		t.Error("the key did not move with the tags")
	}
	defer setGoEnvForTest(func(context.Context, string) (string, error) { return "go9.9\n", nil })()
	if got := selStoreKey(t, root, nil); got == base {
		t.Error("the key did not move with the Go version")
	}
}

func TestSelKey_SourceAndTestEditsMoveIt(t *testing.T) {
	for name, edit := range map[string]struct{ file, body string }{
		"source":       {"p/p.go", "package p\n\nfunc F() int { return 2 }\n"},
		"test":         {"p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {}\n"},
		"a test of q":  {"q/q_test.go", "package q\n\nvar y = 1\n"},
		"testdata":     {"p/testdata/in.txt", "x"},
		"new file":     {"p/extra.go", "package p\n"},
		"module graph": {"go.sum", "x v1 h1:y\n"},
	} {
		t.Run(name, func(t *testing.T) {
			root := selStoreTree(t)
			base := selStoreKey(t, root, nil)
			mustWrite(t, filepath.Join(root, filepath.FromSlash(edit.file)), edit.body)
			if selStoreKey(t, root, nil) == base {
				t.Errorf("an edit to %s left the key where it was", edit.file)
			}
		})
	}
}

func TestSelStore_RoundTripAndStaleShape(t *testing.T) {
	root := selStoreTree(t)
	idx := &selIndex{Schema: selSchema, Key: "k1k1k1k1k1k1k1k1k1k1", Tests: []selTest{{Pkg: "p", Name: "TestF"}},
		Files:    map[string][]selBlock{"p/p.go": {{From: 3, To: 3, Tests: []int{0}}}},
		FileHash: map[string]string{"p/p.go": selFileHash(root, "p/p.go")}}
	if err := idx.save(root); err != nil {
		t.Fatal(err)
	}
	got := loadSelIndex(root, idx.Key)
	if got == nil || len(got.Tests) != 1 || got.Files["p/p.go"][0].Tests[0] != 0 {
		t.Fatalf("loaded %+v", got)
	}
	if loadSelIndex(root, "0000000000000000ffff") != nil {
		t.Error("an index was found for a key none was saved under")
	}
	if why := got.stale(root, "p/p.go"); why != "" {
		t.Errorf("a file as measured is stale: %s", why)
	}
	if err := os.WriteFile(filepath.Join(root, "p", "p.go"), []byte("package p\n\n\nfunc F() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if why := got.stale(root, "p/p.go"); why == "" {
		t.Error("a file edited since the measurement is not stale: its lines moved")
	}
	if why := got.stale(root, "p/unknown.go"); why == "" {
		t.Error("a file the index never saw has a shape that cannot be checked")
	}
}

func TestSelStore_AnIndexOfAnotherKeyInsideTheFileIsNotTrusted(t *testing.T) {
	root := selStoreTree(t)
	idx := &selIndex{Schema: selSchema, Key: "abababababababab0001", Files: map[string][]selBlock{}}
	if err := idx.save(root); err != nil {
		t.Fatal(err)
	}
	other := *idx
	other.Key = "abababababababab0002" // the file name carries only the first 16 characters
	if err := other.save(root); err != nil {
		t.Fatal(err)
	}
	if got := loadSelIndex(root, "abababababababab0001"); got != nil && got.Key != "abababababababab0001" {
		t.Errorf("loaded an index of key %s for 0001", got.Key)
	}
}

func TestSelKey_AFileInASubdirectoryTheTestsMayEmbedMovesIt(t *testing.T) {
	root := selStoreTree(t)
	mustWrite(t, filepath.Join(root, "p", "assets", "a.txt"), "one")
	base := selStoreKey(t, root, nil)
	mustWrite(t, filepath.Join(root, "p", "assets", "a.txt"), "two")
	if selStoreKey(t, root, nil) == base {
		t.Error("an edit to p/assets/a.txt, which //go:embed may read, left the key where it was")
	}
	mustWrite(t, filepath.Join(root, "p", "sub", "s.go"), "package sub\n")
	withSub := selStoreKey(t, root, nil)
	mustWrite(t, filepath.Join(root, "p", "sub", "s.go"), "package sub\n\nvar X = 1\n")
	if selStoreKey(t, root, nil) != withSub {
		t.Error("an edit to another package nested in p moved p's key")
	}
}

func TestSelKeyFrom_AKeyFromTheSharedHashIsTheKeyOfTheSameDirs(t *testing.T) {
	root := selStoreTree(t)
	ctx := context.Background()
	want, _ := selKey(ctx, root, MutantsConfig{}, nil, []string{"p"}, []string{"p", "q"})
	got, _ := selKeyFrom(ctx, root, MutantsConfig{}, nil, []string{"p"}, selContentHash(root, []string{"p", "q"}))
	if got != want {
		t.Errorf("selKeyFrom = %s, selKey = %s", got, want)
	}
}
