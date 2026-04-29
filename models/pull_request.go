package models

import (
	"time"

	"github.com/google/uuid"
)

// PullRequestState is the operational state stored for a pull request row.
type PullRequestState string

const (
	PullRequestStateMerged PullRequestState = "merged"
	PullRequestStateClosed PullRequestState = "closed"
)

// PullRequestData is the normalized payload stored in pull_requests.data JSONB.
type PullRequestData struct {
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Source      PullRequestSource  `json:"source"`
	Actor       PullRequestActor   `json:"actor"`
	Details     PullRequestDetails `json:"details"`
}

// PullRequestSource holds provider-origin metadata for a pull request.
type PullRequestSource struct {
	Key          string     `json:"key"`
	Provider     Source     `json:"provider"`
	RepositoryID string     `json:"repository_id"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	MergedAt     *time.Time `json:"merged_at,omitempty"`
}

// PullRequestActor identifies the PR opener.
type PullRequestActor struct {
	Author PullRequestAuthor `json:"author"`
}

// PullRequestAuthor holds author identity fields from GitHub.
type PullRequestAuthor struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Email string `json:"email,omitempty"`
}

// PullRequestDetails captures display and diff metadata for a pull request.
type PullRequestDetails struct {
	URL        string                `json:"url"`
	Number     int64                 `json:"number"`
	Repository PullRequestRepository `json:"repository"`
	Labels     []string              `json:"labels,omitempty"`
	Assignees  []PullRequestAssignee `json:"assignees,omitempty"`
	Diff       PullRequestDiff       `json:"diff"`
}

// PullRequestRepository identifies the repository that owns the pull request.
type PullRequestRepository struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// PullRequestAssignee holds lightweight assignee identity fields.
type PullRequestAssignee struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// PullRequestDiff contains merge and file-change metadata.
type PullRequestDiff struct {
	URL      string                  `json:"url"`
	SHA      string                  `json:"sha"`
	Branches PullRequestDiffBranches `json:"branches"`
	Stats    PullRequestDiffStats    `json:"stats"`
}

// PullRequestDiffBranches stores the source and target branch names.
type PullRequestDiffBranches struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// PullRequestDiffStats stores summarized file-change counts.
type PullRequestDiffStats struct {
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changed_files"`
}

// PullRequest maps to the pull_requests table.
type PullRequest struct {
	ID              int64
	Key             uuid.UUID
	SourceRecordKey uuid.UUID
	IntegrationID   uuid.UUID
	OrgID           uuid.UUID
	State           PullRequestState
	Data            PullRequestData
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
