package shared

import "fmt"

// ServiceError represents a transient error that may succeed on retry.
// Examples: rate limits, network blips, server errors.
type ServiceError struct {
	Message string
	Cause   error
}

func (e *ServiceError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *ServiceError) Unwrap() error { return e.Cause }

// NonRetryableServiceError represents a permanent error where retrying will not help.
// Examples: invalid API key, bad configuration, missing permissions.
type NonRetryableServiceError struct {
	Message string
	Cause   error
}

func (e *NonRetryableServiceError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *NonRetryableServiceError) Unwrap() error { return e.Cause }
