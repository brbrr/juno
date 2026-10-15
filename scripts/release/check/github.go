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
	Author    *account `json:"author"`
	Committer *account `json:"committer"`
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

// approvedPR returns the number of the pull request merged into base with squash commit sha, if a
// person with write access who did not write, open or merge it approved its last commit.
// It returns 0 if there is none.
func (c *githubClient) approvedPR(ctx context.Context, base, sha string) (int, error) {
	prs, err := c.mergedPRs(ctx, base)
	if err != nil {
		return 0, err
	}
	for i := range prs {
		pr := &prs[i]
		if pr.MergeCommitSHA != sha {
			continue
		}
		path := fmt.Sprintf("/repos/%s/pulls/%d", c.repo, pr.Number)
		reviews, err := getPages[review](ctx, c, path+"/reviews")
		if err != nil {
			return 0, err
		}
		commits, err := getPages[prCommit](ctx, c, path+"/commits")
		if err != nil {
			return 0, err
		}
		for _, login := range approvers(reviews, pr.Head.SHA, participants(pr, commits)) {
			ok, err := c.canWrite(ctx, login)
			if err != nil {
				return 0, err
			}
			if ok {
				return pr.Number, nil
			}
		}
		return 0, nil
	}
	return 0, nil
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

// participants returns the logins that opened or merged the pull request or wrote one of its commits.
// Commit identities can be forged; signed commits on release branches make them reliable.
func participants(pr *pullRequest, commits []prCommit) map[string]bool {
	logins := map[string]bool{pr.User.Login: true, pr.MergedBy.Login: true}
	for _, cm := range commits {
		for _, a := range []*account{cm.Author, cm.Committer} {
			if a != nil {
				logins[a.Login] = true
			}
		}
	}
	return logins
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
