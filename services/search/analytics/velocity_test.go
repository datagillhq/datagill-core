package analytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/repository"
	"github.com/datagill/datagill-core/services/search/analytics"
)

func TestVelocityPlugin(t *testing.T) {
	q := analytics.Query{OrgID: uuid.New(), From: time.Now().Add(-24 * time.Hour), To: time.Now()}

	t.Run("returns average minutes and sample size", func(t *testing.T) {
		p := analytics.NewVelocityPlugin(&fakeAnalyticsRepo{
			velocity: repository.VelocityResult{AverageMinutes: 90.5, SampleSize: 4},
		})

		if p.Metric() != "velocity" {
			t.Fatalf("unexpected metric name: %s", p.Metric())
		}
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := out.(analytics.VelocityResponse)
		if resp.AverageTimeToFullRolloutMinutes != 90.5 {
			t.Errorf("expected 90.5 minutes, got %v", resp.AverageTimeToFullRolloutMinutes)
		}
		if resp.SampleSize != 4 {
			t.Errorf("expected SampleSize 4, got %d", resp.SampleSize)
		}
	})

	t.Run("sample size below threshold passes through unchanged", func(t *testing.T) {
		// Frontend owns the disclaimer for SampleSize < 3 — backend always returns it.
		p := analytics.NewVelocityPlugin(&fakeAnalyticsRepo{
			velocity: repository.VelocityResult{AverageMinutes: 60, SampleSize: 2},
		})
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.(analytics.VelocityResponse).SampleSize != 2 {
			t.Error("expected SampleSize 2 to pass through unmodified")
		}
	})

	t.Run("empty window returns zero values", func(t *testing.T) {
		p := analytics.NewVelocityPlugin(&fakeAnalyticsRepo{})
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := out.(analytics.VelocityResponse)
		if resp.AverageTimeToFullRolloutMinutes != 0 || resp.SampleSize != 0 {
			t.Errorf("expected zero values, got %+v", resp)
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		p := analytics.NewVelocityPlugin(&fakeAnalyticsRepo{velocityErr: errors.New("db error")})
		if _, err := p.Execute(context.Background(), q); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("ResponseType returns VelocityResponse", func(t *testing.T) {
		p := analytics.NewVelocityPlugin(&fakeAnalyticsRepo{})
		if _, ok := p.ResponseType().(analytics.VelocityResponse); !ok {
			t.Errorf("expected VelocityResponse, got %T", p.ResponseType())
		}
	})
}
