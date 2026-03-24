package main

import (
	"context"
	"time"
)

// instrumentedRepository wraps a SearchRepository and records
// datagill_db_query_duration_seconds for each method call.
type instrumentedRepository struct {
	inner SearchRepository
}

func newInstrumentedRepository(inner SearchRepository) SearchRepository {
	return &instrumentedRepository{inner: inner}
}

func (r *instrumentedRepository) record(query string, start time.Time) {
	dbQueryDuration.WithLabelValues(query).Observe(time.Since(start).Seconds())
}

func (r *instrumentedRepository) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	defer r.record("search", time.Now())
	return r.inner.Search(ctx, req)
}

func (r *instrumentedRepository) ListFlags(ctx context.Context, orgID string, filters FlagFilters) ([]FlagSummary, error) {
	defer r.record("list_flags", time.Now())
	return r.inner.ListFlags(ctx, orgID, filters)
}

func (r *instrumentedRepository) GetFlag(ctx context.Context, orgID, flagKey string) (FlagDetail, error) {
	defer r.record("get_flag", time.Now())
	return r.inner.GetFlag(ctx, orgID, flagKey)
}

func (r *instrumentedRepository) GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) ([]ChangelogEntry, error) {
	defer r.record("get_flag_history", time.Now())
	return r.inner.GetFlagHistory(ctx, orgID, flagKey, p)
}

func (r *instrumentedRepository) GetTimeline(ctx context.Context, orgID string, p Pagination) ([]ChangelogEntry, error) {
	defer r.record("get_timeline", time.Now())
	return r.inner.GetTimeline(ctx, orgID, p)
}
