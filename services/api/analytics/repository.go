package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/repository"
)

// AnalyticsRepository is the data access interface required by metric plugins.
// repository.PostgresRepository satisfies this interface.
type AnalyticsRepository interface {
	QueryLaunchedWins(ctx context.Context, orgID uuid.UUID, from, to time.Time, team, actor string) (repository.LaunchedWinsResult, error)
	QueryTopTeams(ctx context.Context, orgID uuid.UUID, from, to time.Time) (repository.TopTeamsResult, error)
	QueryVelocity(ctx context.Context, orgID uuid.UUID, from, to time.Time) (repository.VelocityResult, error)
	QueryAuditLog(ctx context.Context, orgID uuid.UUID, from, to time.Time, team, actor string, page, limit int) (repository.AuditLogResult, error)
	QueryPullRequests(ctx context.Context, orgID uuid.UUID, from, to time.Time, page, limit int) (repository.PullRequestsResult, error)
}
