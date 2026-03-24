package main

import (
	"context"
	"testing"
)

// TestInstrumentedRepositoryDelegates verifies that every method delegates
// to the inner repository and returns its result unmodified.
func TestInstrumentedRepositoryDelegates(t *testing.T) {
	ctx := context.Background()

	t.Run("Search delegates result", func(t *testing.T) {
		inner := &stubRepo{searchResults: []SearchResult{{ID: "flag-1"}}}
		repo := newInstrumentedRepository(inner)
		got, err := repo.Search(ctx, SearchRequest{OrgID: "org-1", Query: "test", Limit: 10})
		if err != nil || len(got) != 1 || got[0].ID != "flag-1" {
			t.Errorf("Search: got %+v, err %v", got, err)
		}
	})

	t.Run("Search propagates error", func(t *testing.T) {
		inner := &stubRepo{searchErr: repoErr}
		repo := newInstrumentedRepository(inner)
		_, err := repo.Search(ctx, SearchRequest{OrgID: "org-1", Query: "test", Limit: 10})
		if err != repoErr {
			t.Errorf("Search: expected repoErr, got %v", err)
		}
	})

	t.Run("ListFlags delegates result", func(t *testing.T) {
		inner := &stubRepo{flags: []FlagSummary{{FlagKey: "flag-1"}}}
		repo := newInstrumentedRepository(inner)
		got, err := repo.ListFlags(ctx, "org-1", FlagFilters{})
		if err != nil || len(got) != 1 || got[0].FlagKey != "flag-1" {
			t.Errorf("ListFlags: got %+v, err %v", got, err)
		}
	})

	t.Run("GetFlag delegates result", func(t *testing.T) {
		inner := &stubRepo{flagDetail: FlagDetail{FlagKey: "flag-1", Name: "My Flag"}}
		repo := newInstrumentedRepository(inner)
		got, err := repo.GetFlag(ctx, "org-1", "flag-1")
		if err != nil || got.Name != "My Flag" {
			t.Errorf("GetFlag: got %+v, err %v", got, err)
		}
	})

	t.Run("GetFlag propagates ErrNotFound", func(t *testing.T) {
		inner := &stubRepo{flagDetailErr: ErrNotFound}
		repo := newInstrumentedRepository(inner)
		_, err := repo.GetFlag(ctx, "org-1", "missing")
		if err != ErrNotFound {
			t.Errorf("GetFlag: expected ErrNotFound, got %v", err)
		}
	})

	t.Run("GetFlagHistory delegates result", func(t *testing.T) {
		inner := &stubRepo{history: []ChangelogEntry{{ID: "entry-1"}}}
		repo := newInstrumentedRepository(inner)
		got, err := repo.GetFlagHistory(ctx, "org-1", "flag-1", Pagination{Page: 1, Limit: 10})
		if err != nil || len(got) != 1 || got[0].ID != "entry-1" {
			t.Errorf("GetFlagHistory: got %+v, err %v", got, err)
		}
	})

	t.Run("GetTimeline delegates result", func(t *testing.T) {
		inner := &stubRepo{timeline: []ChangelogEntry{{ID: "entry-1"}}}
		repo := newInstrumentedRepository(inner)
		got, err := repo.GetTimeline(ctx, "org-1", Pagination{Page: 1, Limit: 10})
		if err != nil || len(got) != 1 || got[0].ID != "entry-1" {
			t.Errorf("GetTimeline: got %+v, err %v", got, err)
		}
	})
}
