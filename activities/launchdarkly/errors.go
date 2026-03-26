package launchdarkly

import (
	"errors"

	"github.com/datagill/datagill-core/shared"
)

// toServiceError maps client errors to shared service errors so the worker
// can convert them to the appropriate Temporal error type at registration time.
//
//   - TooManyRequestsError  → ServiceError       (retryable: rate limit is transient)
//   - UnexpectedStatusError → ServiceError       (retryable: server errors may resolve)
//   - UnauthorizedError     → NonRetryableServiceError (permanent: bad API key)
//   - NotFoundError         → NonRetryableServiceError (permanent: resource won't appear on retry)
//   - anything else         → ServiceError       (retryable: unknown errors default to retry)
func toServiceError(err error) error {
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
