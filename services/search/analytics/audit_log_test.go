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

func TestAuditLogPlugin(t *testing.T) {
	flagKey := uuid.New()
	now := time.Now()
	q := analytics.Query{OrgID: uuid.New(), From: now.Add(-24 * time.Hour), To: now, Page: 2, Limit: 10}

	t.Run("returns paginated response with correct envelope", func(t *testing.T) {
		p := analytics.NewAuditLogPlugin(&fakeAnalyticsRepo{
			auditLog: repository.AuditLogResult{
				Total: 42,
				Entries: []repository.AuditEntry{
					{OccurredAt: now, FlagName: "my-flag", FlagKey: flagKey, ActorEmail: "a@b.com", ActorName: "Alice", EventType: "updated", Environments: []string{"prod"}},
				},
			},
		})

		if p.Metric() != "audit_log" {
			t.Fatalf("unexpected metric name: %s", p.Metric())
		}
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := out.(analytics.AuditLogResponse)
		if resp.Total != 42 {
			t.Errorf("expected Total 42, got %d", resp.Total)
		}
		if resp.Page != 2 {
			t.Errorf("expected Page 2, got %d", resp.Page)
		}
		if resp.Count != 1 {
			t.Errorf("expected Count 1, got %d", resp.Count)
		}
		if resp.Data[0].FlagKey != flagKey {
			t.Errorf("unexpected flag key: %v", resp.Data[0].FlagKey)
		}
		if len(resp.Data[0].Environments) != 1 || resp.Data[0].Environments[0] != "prod" {
			t.Errorf("unexpected environments: %v", resp.Data[0].Environments)
		}
	})

	t.Run("nil environments coerced to empty slice", func(t *testing.T) {
		p := analytics.NewAuditLogPlugin(&fakeAnalyticsRepo{
			auditLog: repository.AuditLogResult{
				Total:   1,
				Entries: []repository.AuditEntry{{FlagKey: flagKey, Environments: nil}},
			},
		})
		out, _ := p.Execute(context.Background(), q)
		if out.(analytics.AuditLogResponse).Data[0].Environments == nil {
			t.Error("expected non-nil environments slice")
		}
	})

	t.Run("empty result returns empty data slice", func(t *testing.T) {
		p := analytics.NewAuditLogPlugin(&fakeAnalyticsRepo{})
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := out.(analytics.AuditLogResponse)
		if resp.Total != 0 || resp.Count != 0 || len(resp.Data) != 0 {
			t.Errorf("expected empty response, got %+v", resp)
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		p := analytics.NewAuditLogPlugin(&fakeAnalyticsRepo{auditLogErr: errors.New("db error")})
		if _, err := p.Execute(context.Background(), q); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("ResponseType returns AuditLogResponse", func(t *testing.T) {
		p := analytics.NewAuditLogPlugin(&fakeAnalyticsRepo{})
		if _, ok := p.ResponseType().(analytics.AuditLogResponse); !ok {
			t.Errorf("expected AuditLogResponse, got %T", p.ResponseType())
		}
	})
}
