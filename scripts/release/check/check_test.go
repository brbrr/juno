package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePRs map[string]prVerdict // "base sha" -> verdict

func (f fakePRs) checkPR(_ context.Context, base, sha string) (prVerdict, error) {
	return f[base+" "+sha], nil
}

func TestCheck(t *testing.T) {
	r := newTestRepo(t)
	r.commit("docs", map[string]string{"docs/guide.md": "guide\n"})
	base := r.head()
	fix := r.commit("fix", map[string]string{"a.go": "package a\n\nfunc A() {}\n"})
	r.commit("feature", map[string]string{"b.go": "package b\n"})
	mainTip := r.head()

	r.run("checkout", "--quiet", "-b", "release", base)
	r.run("cherry-pick", fix)
	pick := r.head()
	docs := r.commit("docs: version", map[string]string{"docs/versioned/guide.md": "guide\n"})
	reviewed := r.commit("release fix", map[string]string{"c.go": "package c\n"})
	r.run("update-ref", "refs/remotes/origin/release/v1.0.0", reviewed)
	unreviewed := r.commit("unreviewed", map[string]string{"d.go": "package d\n"})
	retargeted := r.commit("retargeted", map[string]string{"e.go": "package e\n"})
	r.run("mv", "docs/guide.md", "guide.md")
	moved := r.commit("move docs", nil)
	empty := r.commit("empty", nil)
	r.run("checkout", "--quiet", "-b", "side")
	side := r.commit("docs: side", map[string]string{"docs/side.md": "side\n"})
	r.run("checkout", "--quiet", "release")
	r.run("merge", "--quiet", "--no-ff", "-m", "merge side", "side")
	merge := r.head()
	r.run("update-ref", "refs/remotes/origin/release/v1.0.1", merge)

	c := &checker{git: r.git, prs: fakePRs{
		"release/v1.0.0 " + reviewed:   {number: 7, approved: true},
		"release/v1.0.1 " + retargeted: {number: 8, reason: "its base branch changed"},
	}}
	verdicts, err := c.check(t.Context(), base, mainTip, merge)
	require.NoError(t, err)

	type result struct {
		accepted bool
		reason   string
	}
	got := make(map[string]result, len(verdicts))
	for _, v := range verdicts {
		got[v.sha] = result{v.accepted, v.reason}
	}
	notReviewed := "not on main, not docs only, no approved pull request"
	require.Equal(t, map[string]result{
		pick:       {true, "cherry-pick of " + fix[:shortSHA]},
		docs:       {true, "docs only"},
		reviewed:   {true, "approved pull request #7"},
		unreviewed: {false, notReviewed},
		retargeted: {false, notReviewed + ": pull request #8: its base branch changed"},
		moved:      {false, notReviewed},
		empty:      {false, notReviewed},
		side:       {true, "docs only"},
		merge:      {false, "merge commit"},
	}, got)
}

func TestCheckEmptyRange(t *testing.T) {
	r := newTestRepo(t)
	head := r.head()
	c := &checker{git: r.git, prs: fakePRs{}}

	verdicts, err := c.check(t.Context(), head, head, head)
	require.NoError(t, err)
	require.Empty(t, verdicts)
}

func TestDocsOnly(t *testing.T) {
	require.True(t, docsOnly([]string{"docs/a.md", "docs/b/c.md"}))
	require.False(t, docsOnly([]string{"docs/a.md", "Makefile"}))
	require.False(t, docsOnly([]string{"docsx/a.md"}))
	require.False(t, docsOnly(nil))
}
