package main

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// SearchRepository is the data access interface for the search service.
// The in-memory implementation supports local dev and frontend work before
// Postgres is wired up (Phase 2B).
type SearchRepository interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
	ListFlags(ctx context.Context, orgID string, filters FlagFilters) ([]FlagSummary, error)
	GetFlag(ctx context.Context, orgID, flagKey string) (FlagDetail, error)
	GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) ([]ChangelogEntry, error)
	GetTimeline(ctx context.Context, orgID string, p Pagination) ([]ChangelogEntry, error)
}

// SearchRequest is the input for a full-text or structured search query.
type SearchRequest struct {
	OrgID string
	Query string
	Limit int
}

// SearchResult is a single result returned from Search.
type SearchResult struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"orgId"`
	FlagKey     string    `json:"flagKey"`
	Source      string    `json:"source"`
	Environment string    `json:"environment"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Score       float64   `json:"score"`
	LastEventAt time.Time `json:"lastEventAt"`
}

// FlagFilters are optional filters applied when listing flags.
type FlagFilters struct {
	Source      string
	Environment string
}

// FlagSummary is a lightweight flag representation used in list responses.
type FlagSummary struct {
	FlagKey     string    `json:"flagKey"`
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	Environment string    `json:"environment"`
	LastEventAt time.Time `json:"lastEventAt"`
}

// FlagDetail is the full flag representation including description and tags.
type FlagDetail struct {
	FlagKey     string    `json:"flagKey"`
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	Environment string    `json:"environment"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ChangelogEntry is a single flag change event, used in both history and
// timeline responses.
type ChangelogEntry struct {
	ID          string    `json:"id"`
	FlagKey     string    `json:"flagKey"`
	OrgID       string    `json:"orgId"`
	Source      string    `json:"source"`
	Environment string    `json:"environment"`
	EventType   string    `json:"eventType"`
	ActorEmail  string    `json:"actorEmail"`
	OccurredAt  time.Time `json:"occurredAt"`
	IsAutomated bool      `json:"isAutomated"`
	IsRollback  bool      `json:"isRollback"`
}

// Pagination controls page-based result slicing.
type Pagination struct {
	Page  int
	Limit int
}
