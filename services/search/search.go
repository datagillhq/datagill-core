package main

import (
	"fmt"
	"strconv"
	"strings"
)

const defaultOrgID = "local-dev"

// Response types returned by handlers.

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

// parseLimit parses and validates the ?limit= query parameter.
func parseLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 10, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parseLimit: %w", err)
	}
	if limit <= 0 || limit > 100 {
		return 0, fmt.Errorf("limit must be between 1 and 100")
	}
	return limit, nil
}

// parsePage parses and validates the ?page= query parameter.
func parsePage(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parsePage: %w", err)
	}
	if page <= 0 {
		return 0, fmt.Errorf("page must be >= 1")
	}
	return page, nil
}
