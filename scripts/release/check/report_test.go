package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteReport(t *testing.T) {
	v := releaseVersion{tag: "v1.0.0", core: "v1.0.0"}
	sha := strings.Repeat("a", 40)
	verdicts := []verdict{
		{commit: commit{sha: strings.Repeat("b", 40), subject: "fix | pipe"}, accepted: true, reason: "docs only"},
		{commit: commit{sha: strings.Repeat("c", 40), subject: "bad"}, reason: "merge commit"},
	}

	var got strings.Builder
	writeReport(&got, v, "release/v1.0.0", sha, verdicts)
	require.Equal(t, "## Release v1.0.0 from release/v1.0.0\n\n"+
		"Commit: `"+sha+"`\n\n"+
		"| Commit | Subject | Result |\n|---|---|---|\n"+
		"| `bbbbbbbbbbbb` | fix \\| pipe | accepted: docs only |\n"+
		"| `cccccccccccc` | bad | **rejected**: merge commit |\n", got.String())

	got.Reset()
	writeReport(&got, v, "main", sha, nil)
	require.Equal(t, "## Release v1.0.0 from main\n\nCommit: `"+sha+"`\n\n"+
		"All commits are on main.\n", got.String())
}

func TestRejected(t *testing.T) {
	require.Equal(t, 1, rejected([]verdict{{accepted: true}, {accepted: false}}))
	require.Zero(t, rejected(nil))
}
