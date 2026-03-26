// Package launchdarkly provides Temporal activities for the LaunchDarkly ETL pipeline.
package launchdarkly

import "github.com/datagill/datagill-core/repository"

// Activities contains Temporal activities for LaunchDarkly ETL.
type Activities struct {
	client *Client
	repo   repository.Repository
}

// New creates a new Activities instance.
func New(client *Client, repo repository.Repository) *Activities {
	return &Activities{client: client, repo: repo}
}
