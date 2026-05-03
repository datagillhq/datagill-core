package models

import "time"

// GitHubSearchResponse is the raw response from GitHub's issue search API.
type GitHubSearchResponse struct {
	TotalCount        int                      `json:"total_count"`
	IncompleteResults bool                     `json:"incomplete_results"`
	Items             []GitHubSearchResultItem `json:"items"`
}

// GitHubSearchResultItem is one pull request search hit from GitHub's issue search API.
type GitHubSearchResultItem struct {
	ID            int64                   `json:"id"`
	Number        int64                   `json:"number"`
	Title         string                  `json:"title"`
	Body          string                  `json:"body,omitempty"`
	State         string                  `json:"state,omitempty"`
	HTMLURL       string                  `json:"html_url,omitempty"`
	RepositoryURL string                  `json:"repository_url"`
	User          GitHubUserSummary       `json:"user"`
	Labels        []GitHubLabelSummary    `json:"labels,omitempty"`
	Assignees     []GitHubUserSummary     `json:"assignees,omitempty"`
	PullRequest   *GitHubPullRequestLinks `json:"pull_request,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
	ClosedAt      *time.Time              `json:"closed_at,omitempty"`
}

// GitHubPullRequestLinks contains the PR-specific URLs included in search results.
type GitHubPullRequestLinks struct {
	URL      string `json:"url,omitempty"`
	HTMLURL  string `json:"html_url,omitempty"`
	DiffURL  string `json:"diff_url,omitempty"`
	PatchURL string `json:"patch_url,omitempty"`
}

// GitHubUserSummary is a lightweight GitHub user shape used in search results.
type GitHubUserSummary struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// GitHubLabelSummary is a lightweight label shape used in search results.
type GitHubLabelSummary struct {
	Name string `json:"name"`
}

// GitHubPullRequestDetail is the raw response from GitHub's pull request detail API.
type GitHubPullRequestDetail struct {
	ID             int64                   `json:"id"`
	Number         int64                   `json:"number"`
	Title          string                  `json:"title"`
	Body           string                  `json:"body,omitempty"`
	State          string                  `json:"state,omitempty"`
	HTMLURL        string                  `json:"html_url,omitempty"`
	DiffURL        string                  `json:"diff_url,omitempty"`
	PatchURL       string                  `json:"patch_url,omitempty"`
	MergeCommitSHA string                  `json:"merge_commit_sha,omitempty"`
	User           GitHubUserSummary       `json:"user"`
	Head           GitHubPullRequestBranch `json:"head"`
	Base           GitHubPullRequestBranch `json:"base"`
	Additions      int                     `json:"additions"`
	Deletions      int                     `json:"deletions"`
	ChangedFiles   int                     `json:"changed_files"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
	ClosedAt       *time.Time              `json:"closed_at,omitempty"`
	MergedAt       *time.Time              `json:"merged_at,omitempty"`
}

// GitHubPullRequestBranch contains the branch metadata embedded in a PR detail response.
type GitHubPullRequestBranch struct {
	Ref  string                  `json:"ref"`
	SHA  string                  `json:"sha"`
	Repo GitHubRepositorySummary `json:"repo"`
}

// GitHubRepositorySummary contains the repository metadata embedded in a PR detail response.
type GitHubRepositorySummary struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	FullName string            `json:"full_name"`
	Owner    GitHubUserSummary `json:"owner"`
}
