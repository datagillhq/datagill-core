package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const defaultOrgID = "local-dev"

type SearchRepository interface {
	Search(context.Context, SearchRequest) ([]SearchResult, error)
	ListFlags(context.Context, string) ([]FlagSummary, error)
}

type SearchRequest struct {
	OrgID string
	Query string
	Limit int
}

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

type FlagSummary struct {
	FlagKey     string    `json:"flagKey"`
	Source      string    `json:"source"`
	Environment string    `json:"environment"`
	LastEventAt time.Time `json:"lastEventAt"`
}

type memorySearchRepository struct {
	results []SearchResult
}

func newMemorySearchRepository() SearchRepository {
	now := time.Date(2026, time.March, 17, 9, 0, 0, 0, time.UTC)

	return &memorySearchRepository{
		results: []SearchResult{
			{
				ID:          "flag-checkout-redesign",
				OrgID:       defaultOrgID,
				FlagKey:     "checkout-redesign",
				Source:      "launchdarkly",
				Environment: "production",
				Title:       "Checkout redesign rollout",
				Description: "Progressive rollout for the new checkout experience in production.",
				Score:       0.96,
				LastEventAt: now.Add(-2 * time.Hour),
			},
			{
				ID:          "flag-search-relevance",
				OrgID:       defaultOrgID,
				FlagKey:     "search-relevance-tuning",
				Source:      "launchdarkly",
				Environment: "staging",
				Title:       "Search relevance tuning",
				Description: "Staging flag for semantic ranking experiments and search UI validation.",
				Score:       0.88,
				LastEventAt: now.Add(-26 * time.Hour),
			},
			{
				ID:          "flag-onboarding-flow",
				OrgID:       defaultOrgID,
				FlagKey:     "new-user-onboarding",
				Source:      "unleash",
				Environment: "production",
				Title:       "New user onboarding flow",
				Description: "Controls the onboarding flow shown to first-time users after signup.",
				Score:       0.82,
				LastEventAt: now.Add(-72 * time.Hour),
			},
		},
	}
}

func (m *memorySearchRepository) Search(_ context.Context, req SearchRequest) ([]SearchResult, error) {
	needle := strings.TrimSpace(strings.ToLower(req.Query))
	if req.Limit <= 0 {
		req.Limit = 10
	}

	var filtered []SearchResult
	for _, item := range m.results {
		if item.OrgID != req.OrgID {
			continue
		}

		if needle == "" || strings.Contains(strings.ToLower(item.FlagKey), needle) || strings.Contains(strings.ToLower(item.Title), needle) || strings.Contains(strings.ToLower(item.Description), needle) {
			filtered = append(filtered, item)
		}
	}

	slices.SortFunc(filtered, func(a, b SearchResult) int {
		if a.Score == b.Score {
			if a.LastEventAt.Equal(b.LastEventAt) {
				return strings.Compare(a.ID, b.ID)
			}
			if a.LastEventAt.After(b.LastEventAt) {
				return -1
			}
			return 1
		}
		if a.Score > b.Score {
			return -1
		}
		return 1
	})

	if len(filtered) > req.Limit {
		filtered = filtered[:req.Limit]
	}

	return filtered, nil
}

func (m *memorySearchRepository) ListFlags(ctx context.Context, orgID string) ([]FlagSummary, error) {
	results, err := m.Search(ctx, SearchRequest{OrgID: orgID, Limit: len(m.results)})
	if err != nil {
		return nil, err
	}

	summaries := make([]FlagSummary, 0, len(results))
	for _, item := range results {
		summaries = append(summaries, FlagSummary{
			FlagKey:     item.FlagKey,
			Source:      item.Source,
			Environment: item.Environment,
			LastEventAt: item.LastEventAt,
		})
	}

	return summaries, nil
}

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

func orgIDFromRequest(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return defaultOrgID
	}

	return raw
}

func parseLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 10, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parseLimit: %w", err)
	}
	if limit <= 0 || limit > 50 {
		return 0, strconv.ErrSyntax
	}

	return limit, nil
}
