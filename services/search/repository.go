package main

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// SearchRepository is the data access interface for the search service.
// The concrete implementation (PostgresSearchRepository) is wired up in
// Phase 2B.
type SearchRepository interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
	ListFlags(ctx context.Context, orgID string, filters FlagFilters) ([]FlagSummary, error)
	GetFlag(ctx context.Context, orgID, flagKey string) (FlagDetail, error)
	GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) ([]ChangelogEntry, error)
	GetTimeline(ctx context.Context, orgID string, p Pagination) ([]ChangelogEntry, error)
}
