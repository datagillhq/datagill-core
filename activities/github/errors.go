package github

import (
	"errors"
	"fmt"

	"github.com/datagill/datagill-core/shared"
)

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

// toServiceError maps client errors to shared service errors so the worker can
// classify permanent GitHub auth/config failures as non-retryable.
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
