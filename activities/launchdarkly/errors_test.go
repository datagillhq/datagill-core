package launchdarkly

import (
	"errors"
	"testing"

	"github.com/datagill/datagill-core/shared"
)

func TestToServiceError(t *testing.T) {
	tests := []struct {
		name         string
		input        error
		nonRetryable bool
	}{
		{
			name:         "TooManyRequestsError is retryable",
			input:        &TooManyRequestsError{RetryAfter: 30},
			nonRetryable: false,
		},
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
