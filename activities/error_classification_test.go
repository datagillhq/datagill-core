package activities

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/shared"
)

func TestClassifyActivityError(t *testing.T) {
	rateLimited := temporal.NewApplicationErrorWithOptions("rate limited", "RateLimited", temporal.ApplicationErrorOptions{
		NextRetryDelay: 42 * time.Second,
	})

	tests := []struct {
		name             string
		err              error
		wantType         string
		wantMessage      string
		wantNonRetryable bool
		wantDelay        time.Duration
	}{
		{
			name:             "non-retryable service error",
			err:              &shared.NonRetryableServiceError{Message: "unauthorized (401)"},
			wantType:         NonRetryableServiceErrorType,
			wantMessage:      "unauthorized (401)",
			wantNonRetryable: true,
		},
		{
			name:             "wrapped non-retryable service error",
			err:              fmt.Errorf("EnrichPullRequest get token: %w", &shared.NonRetryableServiceError{Message: "not found (404)"}),
			wantType:         NonRetryableServiceErrorType,
			wantMessage:      "EnrichPullRequest get token: not found (404)",
			wantNonRetryable: true,
		},
		{
			name:        "wrapped rate limit keeps its retry delay",
			err:         fmt.Errorf("EnrichPullRequest get token: %w", rateLimited),
			wantType:    "RateLimited",
			wantMessage: "EnrichPullRequest get token: rate limited",
			wantDelay:   42 * time.Second,
		},
		{
			name:             "wrapped non-retryable application error stays non-retryable",
			err:              fmt.Errorf("lookup: %w", temporal.NewNonRetryableApplicationError("gone", IntegrationNotFoundErrorType, nil)),
			wantType:         IntegrationNotFoundErrorType,
			wantMessage:      "lookup: gone",
			wantNonRetryable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyActivityError(tt.err)

			appErr, ok := got.(*temporal.ApplicationError) //nolint:errorlint // Temporal reads the outermost error
			if !ok {
				t.Fatalf("expected outermost *temporal.ApplicationError, got %T: %v", got, got)
			}
			if appErr.Type() != tt.wantType {
				t.Errorf("type = %q, want %q", appErr.Type(), tt.wantType)
			}
			if appErr.NonRetryable() != tt.wantNonRetryable {
				t.Errorf("non-retryable = %v, want %v", appErr.NonRetryable(), tt.wantNonRetryable)
			}
			if appErr.NextRetryDelay() != tt.wantDelay {
				t.Errorf("next retry delay = %v, want %v", appErr.NextRetryDelay(), tt.wantDelay)
			}
			if appErr.Message() != tt.wantMessage {
				t.Errorf("message = %q, want %q", appErr.Message(), tt.wantMessage)
			}
		})
	}
}

func TestClassifyActivityError_LeavesOtherErrorsUnchanged(t *testing.T) {
	transient := fmt.Errorf("fetch diff: %w", &shared.ServiceError{Message: "unexpected status 502"})
	topLevel := temporal.NewApplicationError("already visible", "Custom")

	if got := ClassifyActivityError(nil); got != nil {
		t.Errorf("nil error: got %v", got)
	}
	if got := ClassifyActivityError(transient); got != transient { //nolint:errorlint // identity check
		t.Errorf("transient error should be unchanged, got %v", got)
	}
	if got := ClassifyActivityError(topLevel); got != topLevel { //nolint:errorlint // identity check
		t.Errorf("top-level ApplicationError should be unchanged, got %v", got)
	}
}

// With the interceptor installed, a non-retryable service error must stop after one
// attempt even though the retry policy allows more.
func TestErrorClassificationInterceptor_StopsRetryingNonRetryableErrors(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{&ErrorClassificationInterceptor{}},
	})

	attempts := 0
	failingActivity := func(context.Context) error {
		attempts++
		return fmt.Errorf("get token: %w", &shared.NonRetryableServiceError{Message: "unauthorized (401)"})
	}
	env.RegisterActivityWithOptions(failingActivity, activity.RegisterOptions{Name: "FailingActivity"})

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
		})
		return workflow.ExecuteActivity(ctx, "FailingActivity").Get(ctx, nil)
	})

	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected workflow to fail")
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != NonRetryableServiceErrorType {
		t.Fatalf("expected %s, got %v", NonRetryableServiceErrorType, err)
	}
	if attempts != 1 {
		t.Errorf("activity ran %d times, want 1", attempts)
	}
}
