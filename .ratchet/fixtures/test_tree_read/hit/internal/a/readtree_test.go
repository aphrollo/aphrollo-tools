package a

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadsTwoLevelsUpTheSourceTree(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_ = data
}
