package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunIgnoresTagsNamedLikeBranches(t *testing.T) {
	r := newTestRepo(t)
	reviewed := r.head()
	r.run("update-ref", "refs/remotes/origin/main", reviewed)
	evil := r.commit("evil", map[string]string{"evil.go": "package evil\n"})
	r.run("tag", "origin/main", evil)

	output := filepath.Join(t.TempDir(), "output")
	o := &options{ref: "main", version: "v1.0.0-rc.1", repo: "o/r", dir: r.git.dir, output: output}
	require.NoError(t, run(t.Context(), o, "token"))

	got, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "sha="+reviewed+"\nprerelease=true\n", string(got))
}
