package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/datagill/datagill-core/models"
)

// ErrInvalidAPIKey is returned when the provider rejects the API key.
var ErrInvalidAPIKey = errors.New("invalid API key")

// ErrProviderUnreachable is returned when the provider cannot be contacted or returns an unexpected status.
var ErrProviderUnreachable = errors.New("provider unreachable")

// IntegrationValidator checks that an API key is accepted by the given provider.
type IntegrationValidator interface {
	Validate(ctx context.Context, provider models.Source, apiKey string) error
}

type ldValidator struct {
	client *http.Client
}

// newLDValidator returns an IntegrationValidator that probes the LaunchDarkly API.
func newLDValidator() IntegrationValidator {
	return &ldValidator{client: &http.Client{Timeout: 5 * time.Second}}
}

func (v *ldValidator) Validate(ctx context.Context, _ models.Source, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://app.launchdarkly.com/api/v2/projects", nil)
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
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrInvalidAPIKey
	default:
		return ErrProviderUnreachable
	}
}
