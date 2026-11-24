package launchdarkly

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/shared"
)

// RateLimitedErrorType is the Temporal error type for a LaunchDarkly 429.
const RateLimitedErrorType = "RateLimited"

// toServiceError maps client errors to shared service errors so the worker
// can convert them to the appropriate Temporal error type at registration time.
//
//   - TooManyRequestsError  → retryable ApplicationError, next retry after Retry-After
//   - UnexpectedStatusError → ServiceError       (retryable: server errors may resolve)
//   - UnauthorizedError     → NonRetryableServiceError (permanent: bad API key)
//   - NotFoundError         → NonRetryableServiceError (permanent: resource won't appear on retry)
//   - anything else         → ServiceError       (retryable: unknown errors default to retry)
func toServiceError(err error) error {
	// Without NextRetryDelay, Temporal retries on its default backoff (1s, 2s, 4s...),
	// which keeps hitting the limit LaunchDarkly told us to wait out.
	var rateLimited *TooManyRequestsError
	if errors.As(err, &rateLimited) {
		delay := time.Duration(rateLimited.RetryAfter) * time.Second
		if delay <= 0 {
			delay = defaultRetryWait * time.Second
		}
		return temporal.NewApplicationErrorWithOptions(err.Error(), RateLimitedErrorType, temporal.ApplicationErrorOptions{ //nolint:wrapcheck // must be outermost for Temporal
			Cause:          err,
			NextRetryDelay: delay,
		})
	}

	var unauthorized *UnauthorizedError
	if errors.As(err, &unauthorized) {
		return &shared.NonRetryableServiceError{Message: err.Error(), Cause: err}
	}

	var notFound *NotFoundError
	if errors.As(err, &notFound) {
		return &shared.NonRetryableServiceError{Message: err.Error(), Cause: err}
	}

	return &shared.ServiceError{Message: err.Error(), Cause: err}
}
