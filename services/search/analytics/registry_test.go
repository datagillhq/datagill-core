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
	r.Register(&stubPlugin{"launched_wins"})

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

func TestRegistry_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate metric registration")
		}
	}()
	r := analytics.NewRegistry()
	r.Register(&stubPlugin{"launched_wins"})
	r.Register(&stubPlugin{"launched_wins"}) // should panic
}
