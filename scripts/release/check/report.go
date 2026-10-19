package main

import (
	"fmt"
	"io"
	"strings"
)

var resultWords = map[result]string{accepted: "accepted", needsReview: "**review**", rejected: "**rejected**"}

// writeReport writes the Markdown summary that the release approver reads.
func writeReport(w io.Writer, repo string, v releaseVersion, ref, sha string, verdicts []verdict) {
	fmt.Fprintf(w, "## Release %s from %s\n\nCommit: `%s`\n\n", v.tag, ref, sha)
	if len(verdicts) == 0 {
		fmt.Fprint(w, "All commits are on main.\n")
		return
	}
	if n := len(withResult(verdicts, needsReview)); n > 0 {
		fmt.Fprintf(w, "**%d commits are not on main.** Read them before you approve. "+
			"Do not approve if you are the author or committer of one of them.\n\n", n)
	}
	fmt.Fprint(w, "| Commit | Subject | Result |\n|---|---|---|\n")
	for _, v := range verdicts {
		fmt.Fprintf(w, "| [`%s`](https://github.com/%s/commit/%s) | %s | %s: %s |\n", v.sha[:shortSHA], repo, v.sha,
			strings.ReplaceAll(v.subject, "|", `\|`), resultWords[v.result], v.reason)
	}
}

// withResult returns the SHAs of the commits with result r.
func withResult(verdicts []verdict, r result) []string {
	var shas []string
	for _, v := range verdicts {
		if v.result == r {
			shas = append(shas, v.sha)
		}
	}
	return shas
}
