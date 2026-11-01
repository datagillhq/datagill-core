package github

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/shared"
)

const defaultRateLimitRetryAfter = 60 * time.Second

// NotFoundErrorType is the Temporal error type for a GitHub 404 on a single resource,
// such as a pull request or repository that was deleted or is no longer visible to the
// app. Workflows can skip that one item. An installation that is gone is reported by
// toInstallationError instead, since it affects every request.
const NotFoundErrorType = "GitHubNotFound"

// TooManyRequestsError is returned when the GitHub API rate limit is exceeded.
type TooManyRequestsError struct {
	RetryAfter int
}

func (e *TooManyRequestsError) Error() string {
	return fmt.Sprintf("rate limited, retry after %ds", e.RetryAfter)
}

// UnauthorizedError is returned when the GitHub App auth is invalid or lacks permissions.
type UnauthorizedError struct {
	StatusCode int
	Message    string
}

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf("unauthorized (%d): %s", e.StatusCode, e.Message)
}

// NotFoundError is returned when the installation no longer exists or is inaccessible to this app.
type NotFoundError struct {
	StatusCode int
	Message    string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("not found (%d): %s", e.StatusCode, e.Message)
}

// UnexpectedStatusError is returned for any other non-2xx response.
type UnexpectedStatusError struct {
	StatusCode int
	Message    string
}

func (e *UnexpectedStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Message)
}

// toServiceError maps client errors from requests for a single resource (search, PR
// detail, diff) to Temporal application errors:
//   - TooManyRequestsError  → retryable, next retry delayed by Retry-After header
//   - UnauthorizedError     → non-retryable (bad credentials won't fix on retry)
//   - NotFoundError         → non-retryable NotFoundErrorType (that resource is gone)
//   - anything else         → retryable (transient server/network errors)
func toServiceError(err error) error {
	if rateLimited := toRateLimitedError(err); rateLimited != nil {
		return rateLimited
	}

	var unauthorized *UnauthorizedError
	if errors.As(err, &unauthorized) {
		return &shared.NonRetryableServiceError{Message: err.Error(), Cause: err}
	}

	var notFound *NotFoundError
	if errors.As(err, &notFound) {
		return temporal.NewNonRetryableApplicationError(err.Error(), NotFoundErrorType, err) //nolint:wrapcheck // must be outermost for Temporal
	}

	return &shared.ServiceError{Message: err.Error(), Cause: err}
}

// toInstallationError maps errors from creating an installation token. A 401, 403 or 404
// here means the integration itself is unusable (bad credentials, app uninstalled), so it
// is never reported as NotFoundErrorType: callers must not skip it as a single missing item.
func toInstallationError(err error) error {
	if rateLimited := toRateLimitedError(err); rateLimited != nil {
		return rateLimited
	}

	var unauthorized *UnauthorizedError
	var notFound *NotFoundError
	if errors.As(err, &unauthorized) || errors.As(err, &notFound) {
		return &shared.NonRetryableServiceError{Message: err.Error(), Cause: err}
	}

	return &shared.ServiceError{Message: err.Error(), Cause: err}
}

// toRateLimitedError returns a retryable error that waits for Retry-After, or nil if err
// is not a rate limit.
func toRateLimitedError(err error) error {
	var rateLimited *TooManyRequestsError
	if !errors.As(err, &rateLimited) {
		return nil
	}
	delay := time.Duration(rateLimited.RetryAfter) * time.Second
	if delay <= 0 {
		delay = defaultRateLimitRetryAfter
	}
	return temporal.NewApplicationErrorWithOptions(err.Error(), "RateLimited", temporal.ApplicationErrorOptions{ //nolint:wrapcheck // must be outermost for Temporal
		Cause:          err,
		NextRetryDelay: delay,
	})
}

// IsNotFound reports whether an activity error seen by a workflow is a GitHub 404 for a
// single resource, which the workflow can skip.
func IsNotFound(err error) bool {
	var appErr *temporal.ApplicationError
	return errors.As(err, &appErr) && appErr.Type() == NotFoundErrorType
}
