package main

import "time"

// --- Repository input types ---

// SearchRequest is the input for a full-text or structured search query.
type SearchRequest struct {
	OrgID string
	Query string
	Limit int
}

// FlagFilters are optional filters applied when listing flags.
type FlagFilters struct {
	Source      string
	Environment string
}

// Pagination controls page-based result slicing.
type Pagination struct {
	Page  int
	Limit int
}

// --- Domain model types ---

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

// --- HTTP response types ---

type SearchResponse struct {
	Query   string         `json:"query"`
	OrgID   string         `json:"orgId"`
	Count   int            `json:"count"`
	Results []SearchResult `json:"results"`
}

type FlagsResponse struct {
	OrgID string        `json:"orgId"`
	Count int           `json:"count"`
	Flags []FlagSummary `json:"flags"`
}

type FlagDetailResponse struct {
	OrgID string     `json:"orgId"`
	Flag  FlagDetail `json:"flag"`
}

type HistoryResponse struct {
	OrgID   string           `json:"orgId"`
	FlagKey string           `json:"flagKey"`
	Page    int              `json:"page"`
	Count   int              `json:"count"`
	Entries []ChangelogEntry `json:"entries"`
}

type TimelineResponse struct {
	OrgID   string           `json:"orgId"`
	Page    int              `json:"page"`
	Count   int              `json:"count"`
	Entries []ChangelogEntry `json:"entries"`
}
