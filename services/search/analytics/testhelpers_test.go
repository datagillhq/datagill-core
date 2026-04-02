package analytics_test

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/repository"
)

// fakeAnalyticsRepo is a shared test double for AnalyticsRepository.
// Set only the fields relevant to the plugin under test; everything else
// returns a zero value. When err is non-nil it is returned by all methods.
type fakeAnalyticsRepo struct {
	launchedWins repository.LaunchedWinsResult
	topTeams     repository.TopTeamsResult
	velocity     repository.VelocityResult
	auditLog     repository.AuditLogResult
	err          error
}

func (f *fakeAnalyticsRepo) QueryLaunchedWins(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string) (repository.LaunchedWinsResult, error) {
	return f.launchedWins, f.err
}
func (f *fakeAnalyticsRepo) QueryTopTeams(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.TopTeamsResult, error) {
	return f.topTeams, f.err
}
func (f *fakeAnalyticsRepo) QueryVelocity(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.VelocityResult, error) {
	return f.velocity, f.err
}
func (f *fakeAnalyticsRepo) QueryAuditLog(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string, _, _ int) (repository.AuditLogResult, error) {
	return f.auditLog, f.err
}
