package analytics_test

import (
	"context"
	"errors"
	"testing"

	"github.com/datagill/datagill-core/services/search/analytics"
)

type stubPlugin struct{ name string }

func (s *stubPlugin) Metric() string                                            { return s.name }
func (s *stubPlugin) Execute(_ context.Context, _ analytics.Query) (any, error) { return nil, nil }

func TestRegistry_GetRegistered(t *testing.T) {
	r := analytics.NewRegistry()
	if err := r.Register(&stubPlugin{"launched_wins"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p, err := r.Get("launched_wins")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Metric() != "launched_wins" {
		t.Fatalf("got metric %q, want %q", p.Metric(), "launched_wins")
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	r := analytics.NewRegistry()

	_, err := r.Get("no_such_metric")
	if !errors.Is(err, analytics.ErrUnknownMetric) {
		t.Fatalf("got %v, want ErrUnknownMetric", err)
	}
}

func TestRegistry_DuplicateReturnsError(t *testing.T) {
	r := analytics.NewRegistry()
	if err := r.Register(&stubPlugin{"launched_wins"}); err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}
	err := r.Register(&stubPlugin{"launched_wins"})
	if !errors.Is(err, analytics.ErrDuplicateMetric) {
		t.Fatalf("got %v, want ErrDuplicateMetric", err)
	}
}
