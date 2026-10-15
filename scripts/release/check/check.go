package main

import (
	"context"
	"fmt"
	"strings"
)

const shortSHA = 12

// prFinder finds the approved pull request that produced a commit on a release branch.
type prFinder interface {
	approvedPR(ctx context.Context, base, sha string) (int, error)
}

// verdict tells why a commit is accepted, or why it is rejected.
type verdict struct {
	commit
	accepted bool
	reason   string
}

// checker decides whether two people reviewed each commit of a release ref that is not on main.
type checker struct {
	git *gitRepo
	prs prFinder
}

// check returns a verdict for each commit in base..tip, oldest first.
func (c *checker) check(ctx context.Context, base, mainTip, tip string) ([]verdict, error) {
	commits, err := c.git.commits(ctx, base, tip)
	if err != nil || len(commits) == 0 {
		return nil, err
	}
	mainIDs, err := c.git.patchIDs(ctx, base, mainTip)
	if err != nil {
		return nil, err
	}
	onMain := make(map[string]string, len(mainIDs))
	for sha, id := range mainIDs {
		onMain[id] = sha
	}
	ids, err := c.git.patchIDs(ctx, base, tip)
	if err != nil {
		return nil, err
	}

	verdicts := make([]verdict, len(commits))
	for i, cm := range commits {
		if verdicts[i], err = c.checkCommit(ctx, cm, onMain[ids[cm.sha]]); err != nil {
			return nil, err
		}
	}
	return verdicts, nil
}

// checkCommit decides about one commit. mainSHA is the main commit with the same patch, or empty.
func (c *checker) checkCommit(ctx context.Context, cm commit, mainSHA string) (verdict, error) {
	if cm.parents > 1 {
		return verdict{commit: cm, reason: "merge commit"}, nil
	}
	if mainSHA != "" {
		return verdict{commit: cm, accepted: true, reason: "cherry-pick of " + mainSHA[:shortSHA]}, nil
	}
	files, err := c.git.changedFiles(ctx, cm.sha)
	if err != nil {
		return verdict{}, err
	}
	if docsOnly(files) {
		return verdict{commit: cm, accepted: true, reason: "docs only"}, nil
	}
	branches, err := c.git.releaseBranchesContaining(ctx, cm.sha)
	if err != nil {
		return verdict{}, err
	}
	for _, branch := range branches {
		n, err := c.prs.approvedPR(ctx, branch, cm.sha)
		if err != nil {
			return verdict{}, err
		}
		if n != 0 {
			return verdict{commit: cm, accepted: true, reason: fmt.Sprintf("approved pull request #%d", n)}, nil
		}
	}
	return verdict{commit: cm, reason: "not on main, not docs only, no approved pull request"}, nil
}

// docsOnly reports whether files is not empty and every file is under docs/.
// The release image and binaries do not contain docs, and the docs site deploys from main.
func docsOnly(files []string) bool {
	for _, f := range files {
		if !strings.HasPrefix(f, "docs/") {
			return false
		}
	}
	return len(files) > 0
}
