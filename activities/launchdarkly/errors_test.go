package launchdarkly

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/activities"
	"github.com/datagill/datagill-core/shared"
)

func TestToServiceError(t *testing.T) {
	tests := []struct {
		name         string
		input        error
		nonRetryable bool
	}{
		{
			name:         "UnexpectedStatusError is retryable",
			input:        &UnexpectedStatusError{StatusCode: 500, Message: "internal error"},
			nonRetryable: false,
		},
		{
			name:         "UnauthorizedError is non-retryable",
			input:        &UnauthorizedError{StatusCode: 401, Message: "bad key"},
			nonRetryable: true,
		},
		{
			name:         "NotFoundError is non-retryable",
			input:        &NotFoundError{URL: "https://example.com"},
			nonRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toServiceError(tt.input)

			var nonRetryable *shared.NonRetryableServiceError
			isNonRetryable := errors.As(result, &nonRetryable)

			if tt.nonRetryable && !isNonRetryable {
				t.Errorf("expected NonRetryableServiceError, got %T", result)
			}
			if !tt.nonRetryable && isNonRetryable {
				t.Errorf("expected ServiceError (retryable), got NonRetryableServiceError")
			}
		})
	}
}

func TestToServiceError_RateLimitedWaitsForRetryAfter(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter int
		want       time.Duration
	}{
		{"uses Retry-After", 30, 30 * time.Second},
		{"falls back to default", 0, defaultRetryWait * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := toServiceError(&TooManyRequestsError{RetryAfter: tt.retryAfter})

			var appErr *temporal.ApplicationError
			if !errors.As(err, &appErr) {
				t.Fatalf("expected *temporal.ApplicationError, got %T", err)
			}
			if appErr.NonRetryable() {
				t.Error("rate limit must stay retryable")
			}
			if appErr.Type() != RateLimitedErrorType {
				t.Errorf("Type = %q, want %q", appErr.Type(), RateLimitedErrorType)
			}
			if appErr.NextRetryDelay() != tt.want {
				t.Errorf("NextRetryDelay = %s, want %s", appErr.NextRetryDelay(), tt.want)
			}
		})
	}
}

// ScrollAuditLogs wraps page errors, so the delay must survive the worker's error
// classification interceptor.
func TestToServiceError_RateLimitedSurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("scroll audit logs: %w", toServiceError(&TooManyRequestsError{RetryAfter: 45}))

	var appErr *temporal.ApplicationError
	if !errors.As(activities.ClassifyActivityError(wrapped), &appErr) {
		t.Fatal("expected an ApplicationError after classification")
	}
	if appErr.NextRetryDelay() != 45*time.Second {
		t.Errorf("NextRetryDelay = %s, want 45s", appErr.NextRetryDelay())
	}
}
