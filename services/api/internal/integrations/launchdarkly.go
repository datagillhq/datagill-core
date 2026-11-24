package integrations

import (
	"context"
	"fmt"
	"net/http"
)

const launchDarklyProjectsURL = "https://app.launchdarkly.com/api/v2/projects"

// LaunchDarklyValidator checks LaunchDarkly API keys with a cheap authenticated read.
type LaunchDarklyValidator struct {
	client *http.Client
	url    string
}

// NewLaunchDarklyValidator returns a validator that uses client, which must have a timeout.
func NewLaunchDarklyValidator(client *http.Client) *LaunchDarklyValidator {
	return &LaunchDarklyValidator{client: client, url: launchDarklyProjectsURL}
}

// Validate implements KeyValidator.
func (v *LaunchDarklyValidator) Validate(ctx context.Context, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", apiKey)

	resp, err := v.client.Do(req)
	if err != nil {
		return ErrProviderUnreachable
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// LaunchDarkly answers invalid keys on this endpoint with a 404
		// ("synthetic-404-with-tarpit") as well as the expected 401/403.
		return ErrInvalidAPIKey
	default:
		return ErrProviderUnreachable
	}
}
