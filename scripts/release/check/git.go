package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// gitRepo runs git commands in a local repository.
type gitRepo struct {
	dir string
}

// commit is one commit in a release range.
type commit struct {
	sha     string
	subject string
	parents int
}

func (g *gitRepo) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // arguments come from the release tool, not from users
	cmd.Dir = g.dir
	return cmd
}

// run runs git with args and returns its trimmed standard output.
func (g *gitRepo) run(ctx context.Context, args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := g.command(ctx, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("running git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// revParse returns the commit SHA of rev.
func (g *gitRepo) revParse(ctx context.Context, rev string) (string, error) {
	return g.run(ctx, "rev-parse", "--verify", rev+"^{commit}")
}

// mergeBase returns the best common ancestor of a and b.
func (g *gitRepo) mergeBase(ctx context.Context, a, b string) (string, error) {
	return g.run(ctx, "merge-base", a, b)
}

// tagExists reports whether the tag exists in the repository.
func (g *gitRepo) tagExists(ctx context.Context, tag string) (bool, error) {
	err := g.command(ctx, "show-ref", "--verify", "--quiet", "refs/tags/"+tag).Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("looking up tag %s: %w", tag, err)
	}
}

// commits returns the commits in base..tip, oldest first.
func (g *gitRepo) commits(ctx context.Context, base, tip string) ([]commit, error) {
	// Commit text can hold anything, so the list comes only from SHAs and the subjects only label it.
	out, err := g.run(ctx, "rev-list", "--reverse", "--topo-order", "--parents", base+".."+tip)
	if err != nil || out == "" {
		return nil, err
	}
	subjects, err := g.subjects(ctx, base, tip)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(out, "\n")
	commits := make([]commit, len(lines))
	for i, line := range lines {
		fields := strings.Fields(line)
		commits[i] = commit{sha: fields[0], subject: subjects[fields[0]], parents: len(fields) - 1}
	}
	return commits, nil
}

// subjects returns the subject line of each commit in base..tip, by commit SHA.
func (g *gitRepo) subjects(ctx context.Context, base, tip string) (map[string]string, error) {
	out, err := g.run(ctx, "log", "--format=%H%x00%s", base+".."+tip)
	if err != nil {
		return nil, err
	}
	subjects := make(map[string]string)
	for line := range strings.Lines(out) {
		sha, subject, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "\x00")
		subjects[sha] = subject
	}
	return subjects, nil
}

// patchIDs returns the patch-id of each non-merge, non-empty commit in base..tip, by commit SHA.
// Whitespace and binary content count, so only an exact copy of a change gets the same id.
func (g *gitRepo) patchIDs(ctx context.Context, base, tip string) (map[string]string, error) {
	logCmd := g.command(ctx, "log", "--no-merges", "--no-color", "--no-ext-diff", "-p", "--binary",
		"--pretty=format:commit %H", base+".."+tip)
	idCmd := g.command(ctx, "patch-id", "--verbatim")
	pipe, err := logCmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	idCmd.Stdin = pipe
	var out bytes.Buffer
	idCmd.Stdout = &out
	if err := logCmd.Start(); err != nil {
		return nil, fmt.Errorf("running git log: %w", err)
	}
	if err := idCmd.Run(); err != nil {
		return nil, fmt.Errorf("running git patch-id: %w", err)
	}
	if err := logCmd.Wait(); err != nil {
		return nil, fmt.Errorf("running git log: %w", err)
	}

	ids := make(map[string]string)
	for line := range strings.Lines(out.String()) {
		id, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			return nil, fmt.Errorf("parsing git patch-id output %q", line)
		}
		ids[sha] = id
	}
	return ids, nil
}

// changedFiles returns the paths that commit sha changes. A rename gives both paths.
func (g *gitRepo) changedFiles(ctx context.Context, sha string) ([]string, error) {
	out, err := g.run(ctx, "diff-tree", "--no-commit-id", "--name-only", "-r", "--no-renames", "--root", sha)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
