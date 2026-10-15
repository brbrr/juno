package main

import (
	"fmt"
	"io"
	"strings"
)

// writeReport writes the Markdown summary that the release approver reads.
func writeReport(w io.Writer, v releaseVersion, ref, sha string, verdicts []verdict) {
	fmt.Fprintf(w, "## Release %s from %s\n\nCommit: `%s`\n\n", v.tag, ref, sha)
	if len(verdicts) == 0 {
		fmt.Fprint(w, "All commits are on main.\n")
		return
	}
	fmt.Fprint(w, "| Commit | Subject | Result |\n|---|---|---|\n")
	for _, v := range verdicts {
		result := "accepted: " + v.reason
		if !v.accepted {
			result = "**rejected**: " + v.reason
		}
		fmt.Fprintf(w, "| `%s` | %s | %s |\n", v.sha[:shortSHA], strings.ReplaceAll(v.subject, "|", `\|`), result)
	}
}

// rejected returns the number of rejected commits.
func rejected(verdicts []verdict) int {
	n := 0
	for _, v := range verdicts {
		if !v.accepted {
			n++
		}
	}
	return n
}
