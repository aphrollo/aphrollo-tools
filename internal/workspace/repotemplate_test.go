package workspace

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// repoTemplate is the committed repo initRepoAt hands each test a copy of,
// built on first use: six git spawns per test were most of what a throwaway
// repo cost. The directory is the package run's, removed by TestMain with the
// stub dirs.
var repoTemplate = sync.OnceValue(func() string {
	root, err := os.MkdirTemp("", "aphrollo-ws-repo-template-")
	if err != nil {
		panic(err)
	}
	registerStubDir(root)
	repo := filepath.Join(root, "repo")
	if err := gitiso.BuildRepo(repo, map[string]string{"go.mod": "module x\n\ngo 1.26\n"}); err != nil {
		panic(err)
	}
	return repo
})
