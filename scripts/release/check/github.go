package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const pageSize = 100

var errNotFound = errors.New("not found")

type account struct {
	Login string `json:"login"`
}

type pullRequest struct {
	Number         int     `json:"number"`
	MergedAt       string  `json:"merged_at"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	User           account `json:"user"`
	MergedBy       account `json:"merged_by"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type review struct {
	User     account `json:"user"`
	State    string  `json:"state"`
	CommitID string  `json:"commit_id"`
}

type prCommit struct {
	SHA       string   `json:"sha"`
	Author    *account `json:"author"`
	Committer *account `json:"committer"`
	Commit    struct {
		Verification struct {
			Verified bool `json:"verified"`
		} `json:"verification"`
	} `json:"commit"`
}

type timelineEvent struct {
	Event string `json:"event"`
}

// webFlow is the committer of commits that github.com makes for a person, such as an applied suggestion.
const webFlow = "web-flow"

const noApproval = "no approval of its last commit from a writer who did not write, open or merge it"

// prVerdict tells whether the pull request that produced a commit counts as reviewed.
// number is 0 if no merged pull request produced the commit.
type prVerdict struct {
	number   int
	approved bool
	reason   string // why the pull request does not count
}

// githubClient finds approved pull requests through the GitHub REST API.
type githubClient struct {
	baseURL string
	repo    string
	token   string
	http    *http.Client
	merged  map[string][]pullRequest // by base branch
}

func newGitHubClient(baseURL, repo, token string) *githubClient {
	return &githubClient{
		baseURL: baseURL,
		repo:    repo,
		token:   token,
		http:    http.DefaultClient,
		merged:  make(map[string][]pullRequest),
	}
}

// checkPR finds the pull request merged into base with squash commit sha, and decides if it counts as
// reviewed: a person with write access who did not write, open or merge it approved its last commit.
func (c *githubClient) checkPR(ctx context.Context, base, sha string) (prVerdict, error) {
	prs, err := c.mergedPRs(ctx, base)
	if err != nil {
		return prVerdict{}, err
	}
	for i := range prs {
		if prs[i].MergeCommitSHA == sha {
			reason, err := c.rejection(ctx, &prs[i])
			return prVerdict{number: prs[i].Number, approved: reason == "", reason: reason}, err
		}
	}
	return prVerdict{}, nil
}

// rejection returns why the pull request does not count as reviewed, or "" if it does.
func (c *githubClient) rejection(ctx context.Context, pr *pullRequest) (string, error) {
	events, err := getPages[timelineEvent](ctx, c, fmt.Sprintf("/repos/%s/issues/%d/timeline", c.repo, pr.Number))
	if err != nil {
		return "", err
	}
	for _, e := range events {
		// An approval does not record the base it was given against, so a new base can add
		// commits that the approver did not see.
		if e.Event == "base_ref_changed" || e.Event == "base_ref_force_pushed" {
			return "its base branch changed", nil
		}
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", c.repo, pr.Number)
	commits, err := getPages[prCommit](ctx, c, path+"/commits")
	if err != nil {
		return "", err
	}
	excluded, unverified := participants(pr, commits)
	if unverified != "" {
		return fmt.Sprintf("commit %s has no verified signature", unverified), nil
	}
	reviews, err := getPages[review](ctx, c, path+"/reviews")
	if err != nil {
		return "", err
	}
	for _, login := range approvers(reviews, pr.Head.SHA, excluded) {
		ok, err := c.canWrite(ctx, login)
		if err != nil || ok {
			return "", err
		}
	}
	return noApproval, nil
}

// mergedPRs returns the merged pull requests into base.
func (c *githubClient) mergedPRs(ctx context.Context, base string) ([]pullRequest, error) {
	if prs, ok := c.merged[base]; ok {
		return prs, nil
	}
	prs, err := getPages[pullRequest](ctx, c, fmt.Sprintf("/repos/%s/pulls?state=closed&base=%s", c.repo, url.QueryEscape(base)))
	if err != nil {
		return nil, err
	}
	merged := prs[:0]
	for i := range prs {
		if prs[i].MergedAt != "" {
			merged = append(merged, prs[i])
		}
	}
	c.merged[base] = merged
	return merged, nil
}

// canWrite reports whether login has write or admin access to the repository.
func (c *githubClient) canWrite(ctx context.Context, login string) (bool, error) {
	var p struct {
		Permission string `json:"permission"`
	}
	err := c.get(ctx, fmt.Sprintf("/repos/%s/collaborators/%s/permission", c.repo, url.PathEscape(login)), &p)
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	return p.Permission == "admin" || p.Permission == "write", err
}

// participants returns the logins that opened or merged the pull request or made one of its commits.
// Anyone can put any email in a commit, so a commit counts only with a verified signature, which binds
// the committer to the account that signed. github.com signs the commits it makes, with the person as
// author. unverified is the first commit without a reliable identity, or "".
func participants(pr *pullRequest, commits []prCommit) (logins map[string]bool, unverified string) {
	logins = map[string]bool{pr.User.Login: true, pr.MergedBy.Login: true}
	for _, cm := range commits {
		if !cm.Commit.Verification.Verified || cm.Committer == nil || (cm.Committer.Login == webFlow && cm.Author == nil) {
			return nil, cm.SHA
		}
		logins[cm.Committer.Login] = true
		if cm.Author != nil {
			logins[cm.Author.Login] = true
		}
	}
	return logins, ""
}

// approvers returns, sorted, the logins outside excluded whose latest decisive review approves head.
func approvers(reviews []review, head string, excluded map[string]bool) []string {
	latest := make(map[string]review, len(reviews))
	for _, r := range reviews { // the API returns reviews oldest first
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			latest[r.User.Login] = r
		}
	}
	var logins []string
	for login, r := range latest {
		if !excluded[login] && r.State == "APPROVED" && r.CommitID == head {
			logins = append(logins, login)
		}
	}
	sort.Strings(logins)
	return logins
}

// getPages returns all items of a paginated list endpoint.
func getPages[T any](ctx context.Context, c *githubClient, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	for page := 1; ; page++ {
		var items []T
		if err := c.get(ctx, fmt.Sprintf("%s%sper_page=%d&page=%d", path, sep, pageSize, page), &items); err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < pageSize {
			return all, nil
		}
	}
}

func (c *githubClient) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("requesting %s: %w", path, errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024)) //nolint:mnd // enough of the body to see the reason
		return fmt.Errorf("requesting %s: status %d: %s", path, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}
