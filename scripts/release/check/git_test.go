package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type testRepo struct {
	t   *testing.T
	git *gitRepo
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &testRepo{t: t, git: &gitRepo{dir: t.TempDir()}}
	r.run("init", "--quiet", "--initial-branch=main")
	r.run("config", "user.name", "test")
	r.run("config", "user.email", "test@example.com")
	r.commit("init", map[string]string{"README.md": "juno\n"})
	return r
}

func (r *testRepo) run(args ...string) string {
	r.t.Helper()
	out, err := r.git.run(r.t.Context(), args...)
	require.NoError(r.t, err)
	return out
}

func (r *testRepo) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for name, content := range files {
		path := filepath.Join(r.git.dir, name)
		require.NoError(r.t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(r.t, os.WriteFile(path, []byte(content), 0o600))
	}
	r.run("add", "--all")
	r.run("commit", "--quiet", "--allow-empty", "-m", msg)
	return r.head()
}

func (r *testRepo) head() string {
	r.t.Helper()
	return r.run("rev-parse", "HEAD")
}

func TestCommits(t *testing.T) {
	r := newTestRepo(t)
	base := r.head()
	first := r.commit("first", map[string]string{"a.go": "package a\n"})
	r.run("checkout", "--quiet", "-b", "side")
	side := r.commit("side | pipe", map[string]string{"b.go": "package b\n"})
	r.run("checkout", "--quiet", "main")
	second := r.commit("second", map[string]string{"c.go": "package c\n"})
	r.run("merge", "--quiet", "--no-ff", "-m", "merge side", "side")
	merge := r.head()

	got, err := r.git.commits(t.Context(), base, merge)
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Equal(t, commit{sha: merge, subject: "merge side", parents: 2}, got[3])
	require.ElementsMatch(t, []commit{
		{sha: first, subject: "first", parents: 1},
		{sha: side, subject: "side | pipe", parents: 1},
		{sha: second, subject: "second", parents: 1},
	}, got[:3])

	none, err := r.git.commits(t.Context(), merge, merge)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestPatchIDs(t *testing.T) {
	r := newTestRepo(t)
	base := r.head()
	fix := r.commit("fix", map[string]string{"a.go": "package a\n\nfunc A() {}\n", "blob.bin": "\x00\x01A"})
	onMain, err := r.git.patchIDs(t.Context(), base, "main")
	require.NoError(t, err)
	require.Len(t, onMain, 1)
	require.NotEmpty(t, onMain[fix])

	branch := func(name string, files map[string]string) (string, map[string]string) {
		r.run("checkout", "--quiet", "-b", name, base)
		sha := r.commit("fix", files)
		ids, err := r.git.patchIDs(t.Context(), base, name)
		require.NoError(t, err)
		return sha, ids
	}

	r.run("checkout", "--quiet", "-b", "pick", base)
	r.run("cherry-pick", fix)
	pick := r.head()
	picked, err := r.git.patchIDs(t.Context(), base, "pick")
	require.NoError(t, err)
	require.Equal(t, onMain[fix], picked[pick])

	ws, wsIDs := branch("whitespace", map[string]string{"a.go": "package a\n\nfunc A()  {}\n", "blob.bin": "\x00\x01A"})
	require.NotEmpty(t, wsIDs[ws])
	require.NotEqual(t, onMain[fix], wsIDs[ws])

	bin, binIDs := branch("binary", map[string]string{"a.go": "package a\n\nfunc A() {}\n", "blob.bin": "\x00\x01B"})
	require.NotEmpty(t, binIDs[bin])
	require.NotEqual(t, onMain[fix], binIDs[bin])

	empty, emptyIDs := branch("empty", nil)
	require.NotContains(t, emptyIDs, empty)
}

func TestChangedFiles(t *testing.T) {
	r := newTestRepo(t)
	root := r.run("rev-list", "--max-parents=0", "HEAD")
	got, err := r.git.changedFiles(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, got)

	r.commit("docs", map[string]string{"docs/guide.md": "guide\n"})
	r.run("mv", "docs/guide.md", "guide.md")
	moved := r.commit("move", nil)
	got, err = r.git.changedFiles(t.Context(), moved)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"docs/guide.md", "guide.md"}, got)

	empty := r.commit("empty", nil)
	got, err = r.git.changedFiles(t.Context(), empty)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTagExists(t *testing.T) {
	r := newTestRepo(t)
	r.run("tag", "v1.0.0")

	got, err := r.git.tagExists(t.Context(), "v1.0.0")
	require.NoError(t, err)
	require.True(t, got)

	got, err = r.git.tagExists(t.Context(), "v1.0.1")
	require.NoError(t, err)
	require.False(t, got)
}

func TestRevParseAndMergeBase(t *testing.T) {
	r := newTestRepo(t)
	base := r.head()
	r.commit("main", map[string]string{"a.go": "package a\n"})
	r.run("checkout", "--quiet", "-b", "release", base)
	r.commit("release", map[string]string{"b.go": "package b\n"})

	got, err := r.git.mergeBase(t.Context(), "main", "release")
	require.NoError(t, err)
	require.Equal(t, base, got)

	_, err = r.git.revParse(t.Context(), "origin/missing")
	require.ErrorContains(t, err, "running git rev-parse")
}

func TestCommitsWithEmptyMessages(t *testing.T) {
	r := newTestRepo(t)
	base := r.head()
	r.run("commit", "--quiet", "--allow-empty", "--allow-empty-message", "-m", "")
	first := r.head()
	r.commit("commit "+first+" "+base, map[string]string{"main.go": "package main\n"})
	evil := r.head()
	r.run("commit", "--quiet", "--allow-empty", "--allow-empty-message", "-m", "")
	last := r.head()

	got, err := r.git.commits(t.Context(), base, last)
	require.NoError(t, err)
	require.Equal(t, []commit{
		{sha: first, subject: "", parents: 1},
		{sha: evil, subject: "commit " + first + " " + base, parents: 1},
		{sha: last, subject: "", parents: 1},
	}, got)
}
