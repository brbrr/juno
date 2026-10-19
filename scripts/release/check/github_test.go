package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func approval(login, state, commitID string) review {
	r := review{State: state, CommitID: commitID}
	r.User.Login = login
	return r
}

func TestApprovers(t *testing.T) {
	excluded := map[string]bool{"author": true, "merger": true}
	tests := []struct {
		name    string
		reviews []review
		want    []string
	}{
		{"approved on head", []review{approval("bob", "APPROVED", "head")}, []string{"bob"}},
		{"comment after approval keeps it", []review{approval("bob", "APPROVED", "head"), approval("bob", "COMMENTED", "head")}, []string{"bob"}},
		{"approved by an excluded login", []review{approval("author", "APPROVED", "head"), approval("merger", "APPROVED", "head")}, nil},
		{"approved older commit", []review{approval("bob", "APPROVED", "old")}, nil},
		{"changes requested after approval", []review{approval("bob", "APPROVED", "head"), approval("bob", "CHANGES_REQUESTED", "head")}, nil},
		{"dismissed after approval", []review{approval("bob", "APPROVED", "head"), approval("bob", "DISMISSED", "head")}, nil},
		{"two approvers sorted", []review{approval("eve", "APPROVED", "head"), approval("bob", "APPROVED", "head")}, []string{"bob", "eve"}},
		{"no reviews", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, approvers(tt.reviews, "head", excluded))
		})
	}
}

// fakeGitHub serves one merged pull request #7 into release/v1.0.0 with squash commit "squash-sha",
// head "head", author "author" and merger "merger".
type fakeGitHub struct {
	reviews     [][]review // pages
	commits     string     // JSON of the pull request commits
	timeline    string     // JSON of the pull request timeline; empty means no events
	permissions map[string]string
	requests    int
}

func (f *fakeGitHub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/pulls", func(w http.ResponseWriter, req *http.Request) {
		f.requests++
		assert.Equal(t, "Bearer token", req.Header.Get("Authorization"))
		assert.Equal(t, "closed", req.URL.Query().Get("state"))
		assert.Equal(t, "release/v1.0.0", req.URL.Query().Get("base"))
		var page []map[string]any
		switch req.URL.Query().Get("page") {
		case "1":
			for i := range pageSize {
				page = append(page, map[string]any{"number": 100 + i, "merged_at": "2026-10-01T00:00:00Z", "merge_commit_sha": "other"})
			}
		case "2":
			page = []map[string]any{
				{"number": 6, "merged_at": nil, "merge_commit_sha": "closed-sha"},
				{
					"number": 7, "merged_at": "2026-10-01T00:00:00Z", "merge_commit_sha": "squash-sha",
					"user": map[string]any{"login": "author"}, "head": map[string]any{"sha": "head"},
					"merged_by": map[string]any{"login": "merger"},
				},
			}
		}
		assert.NoError(t, json.NewEncoder(w).Encode(page))
	})
	mux.HandleFunc("GET /repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, req *http.Request) {
		var page []review
		if n := req.URL.Query().Get("page"); n == "1" || n == "2" {
			if i := int(n[0] - '1'); i < len(f.reviews) {
				page = f.reviews[i]
			}
		}
		assert.NoError(t, json.NewEncoder(w).Encode(page))
	})
	mux.HandleFunc("GET /repos/o/r/pulls/7/commits", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page") == "1" {
			fmt.Fprint(w, f.commits)
			return
		}
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("GET /repos/o/r/issues/7/timeline", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page") == "1" && f.timeline != "" {
			fmt.Fprint(w, f.timeline)
			return
		}
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("GET /repos/o/r/collaborators/{login}/permission", func(w http.ResponseWriter, req *http.Request) {
		permission, ok := f.permissions[req.PathValue("login")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		fmt.Fprintf(w, `{"permission":%q}`, permission)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// authorCommits are signed commits by the author: one pushed, one made on github.com.
const authorCommits = `[
	{"sha":"c1","author":{"login":"author"},"committer":{"login":"author"},"commit":{"verification":{"verified":true}}},
	{"sha":"c2","author":{"login":"author"},"committer":{"login":"web-flow"},"commit":{"verification":{"verified":true}}}
]`

func TestCheckPR(t *testing.T) {
	f := &fakeGitHub{
		reviews:     [][]review{{approval("bob", "APPROVED", "head")}},
		commits:     authorCommits,
		timeline:    `[{"event":"reviewed"},{"event":"merged"}]`,
		permissions: map[string]string{"bob": "write"},
	}
	c := newGitHubClient(f.server(t).URL, "o/r", "token")

	got, err := c.checkPR(t.Context(), "release/v1.0.0", "squash-sha")
	require.NoError(t, err)
	require.Equal(t, prVerdict{number: 7, approved: true}, got)
	require.Equal(t, 2, f.requests)

	got, err = c.checkPR(t.Context(), "release/v1.0.0", "closed-sha")
	require.NoError(t, err)
	require.Zero(t, got)

	got, err = c.checkPR(t.Context(), "release/v1.0.0", "unknown")
	require.NoError(t, err)
	require.Zero(t, got)
	require.Equal(t, 2, f.requests, "merged pull requests of a branch are fetched once")
}

func TestCheckPRRejects(t *testing.T) {
	comments := make([]review, pageSize-1)
	for i := range comments {
		comments[i] = approval("bob", "COMMENTED", "head")
	}
	bobApproves := [][]review{{approval("bob", "APPROVED", "head")}}
	bobWrites := map[string]string{"bob": "write"}
	tests := []struct {
		name        string
		reviews     [][]review
		commits     string
		timeline    string
		permissions map[string]string
		reason      string
	}{
		{
			name:        "approver without write access",
			reviews:     [][]review{{approval("sockpuppet", "APPROVED", "head")}},
			commits:     authorCommits,
			permissions: map[string]string{"sockpuppet": "read"},
			reason:      noApproval,
		},
		{
			name:    "approver who is not a collaborator",
			reviews: [][]review{{approval("stranger", "APPROVED", "head")}},
			commits: authorCommits,
			reason:  noApproval,
		},
		{
			name:        "approver who merged the pull request",
			reviews:     [][]review{{approval("merger", "APPROVED", "head")}},
			commits:     authorCommits,
			permissions: map[string]string{"merger": "write"},
			reason:      noApproval,
		},
		{
			name:        "approver who pushed a commit",
			reviews:     bobApproves,
			commits:     `[{"sha":"c1","author":{"login":"author"},"committer":{"login":"bob"},"commit":{"verification":{"verified":true}}}]`,
			permissions: bobWrites,
			reason:      noApproval,
		},
		{
			name:        "approver who made a commit on github.com",
			reviews:     bobApproves,
			commits:     `[{"sha":"c1","author":{"login":"bob"},"committer":{"login":"web-flow"},"commit":{"verification":{"verified":true}}}]`,
			permissions: bobWrites,
			reason:      noApproval,
		},
		{
			name:        "commit without a verified signature",
			reviews:     bobApproves,
			commits:     `[{"sha":"c1","author":{"login":"author"},"committer":{"login":"author"},"commit":{"verification":{"verified":false}}}]`,
			permissions: bobWrites,
			reason:      "commit c1 has no verified signature",
		},
		{
			name:        "commit with an email of no account",
			reviews:     bobApproves,
			commits:     `[{"sha":"c1","author":null,"committer":null,"commit":{"verification":{"verified":true}}}]`,
			permissions: bobWrites,
			reason:      "commit c1 has no verified signature",
		},
		{
			name:        "base branch changed",
			reviews:     bobApproves,
			commits:     authorCommits,
			timeline:    `[{"event":"reviewed"},{"event":"base_ref_changed"}]`,
			permissions: bobWrites,
			reason:      "its base branch changed",
		},
		{
			name:        "base branch force-pushed",
			reviews:     bobApproves,
			commits:     authorCommits,
			timeline:    `[{"event":"base_ref_force_pushed"}]`,
			permissions: bobWrites,
			reason:      "its base branch changed",
		},
		{
			name:        "changes requested on the second page",
			reviews:     [][]review{append([]review{approval("bob", "APPROVED", "head")}, comments...), {approval("bob", "CHANGES_REQUESTED", "head")}},
			commits:     authorCommits,
			permissions: map[string]string{"bob": "admin"},
			reason:      noApproval,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{reviews: tt.reviews, commits: tt.commits, timeline: tt.timeline, permissions: tt.permissions}
			got, err := newGitHubClient(f.server(t).URL, "o/r", "token").checkPR(t.Context(), "release/v1.0.0", "squash-sha")
			require.NoError(t, err)
			require.Equal(t, prVerdict{number: 7, reason: tt.reason}, got)
		})
	}
}

func TestCheckPRReportsHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := newGitHubClient(srv.URL, "o/r", "token").checkPR(t.Context(), "release/v1.0.0", "sha")
	require.ErrorContains(t, err, "status 500")
}
