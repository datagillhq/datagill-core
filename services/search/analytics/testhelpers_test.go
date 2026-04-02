package analytics_test

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/repository"
)

// fakeAnalyticsRepo is a shared test double for AnalyticsRepository.
// Set only the fields relevant to the plugin under test; everything else
// returns a zero value. Each method has its own error field so tests can
// express independent failure scenarios (e.g. one method fails, others succeed).
type fakeAnalyticsRepo struct {
	launchedWins    repository.LaunchedWinsResult
	launchedWinsErr error
	topTeams        repository.TopTeamsResult
	topTeamsErr     error
	velocity        repository.VelocityResult
	velocityErr     error
	auditLog        repository.AuditLogResult
	auditLogErr     error
}

func (f *fakeAnalyticsRepo) QueryLaunchedWins(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string) (repository.LaunchedWinsResult, error) {
	return f.launchedWins, f.launchedWinsErr
}
func (f *fakeAnalyticsRepo) QueryTopTeams(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.TopTeamsResult, error) {
	return f.topTeams, f.topTeamsErr
}
func (f *fakeAnalyticsRepo) QueryVelocity(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.VelocityResult, error) {
	return f.velocity, f.velocityErr
}
func (f *fakeAnalyticsRepo) QueryAuditLog(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string, _, _ int) (repository.AuditLogResult, error) {
	return f.auditLog, f.auditLogErr
}
