// Command check verifies that a release ref can be released: the version and ref match, the tag
// does not exist, and two people reviewed every commit that is not on main.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

type options struct {
	ref, version, repo, dir, summary, output, apiURL string
}

func main() {
	var o options
	flag.StringVar(&o.ref, "ref", "", "branch to release: main or release/vX.Y.Z")
	flag.StringVar(&o.version, "version", "", "release tag, for example v0.16.9 or v0.16.9-rc.1")
	flag.StringVar(&o.repo, "repo", "", "GitHub repository as owner/name")
	flag.StringVar(&o.dir, "dir", ".", "git repository with all branches and tags of origin fetched")
	flag.StringVar(&o.summary, "summary", "", "file to append the Markdown report to")
	flag.StringVar(&o.output, "output", "", "file to append the step outputs to")
	flag.StringVar(&o.apiURL, "api-url", "https://api.github.com", "GitHub API URL")
	flag.Parse()

	if err := run(context.Background(), &o, os.Getenv("GITHUB_TOKEN")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o *options, token string) error {
	v, err := parseVersion(o.version)
	if err != nil {
		return err
	}
	if err := checkRef(o.ref, v); err != nil {
		return err
	}
	g := &gitRepo{dir: o.dir}
	exists, err := g.tagExists(ctx, v.tag)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("tag %s exists", v.tag)
	}
	sha, err := g.revParse(ctx, "refs/remotes/origin/"+o.ref)
	if err != nil {
		return err
	}
	mainTip, err := g.revParse(ctx, "refs/remotes/origin/main")
	if err != nil {
		return err
	}
	base, err := g.mergeBase(ctx, mainTip, sha)
	if err != nil {
		return err
	}

	c := &checker{git: g, prs: newGitHubClient(o.apiURL, o.repo, token)}
	verdicts, err := c.check(ctx, base, mainTip, sha)
	if err != nil {
		return err
	}
	if err := appendTo(o.summary, func(w io.Writer) { writeReport(w, v, o.ref, sha, verdicts) }); err != nil {
		return err
	}
	writeReport(os.Stdout, v, o.ref, sha, verdicts)
	if n := rejected(verdicts); n > 0 {
		return fmt.Errorf("%d commits on %s are not reviewed", n, o.ref)
	}
	return appendTo(o.output, func(w io.Writer) {
		fmt.Fprintf(w, "sha=%s\nprerelease=%s\n", sha, strconv.FormatBool(v.prerelease))
	})
}

// appendTo appends what write writes to the file at path. An empty path writes nothing.
func appendTo(path string, write func(io.Writer)) (err error) {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:mnd // owner read and write
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	write(f)
	return nil
}
