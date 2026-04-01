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

// ValidatorRegistry dispatches validation to the provider-specific implementation.
// Register a new provider by adding it to newValidatorRegistry.
type ValidatorRegistry struct {
	validators map[models.Source]IntegrationValidator
}

// newValidatorRegistry returns a registry pre-loaded with all supported providers.
func newValidatorRegistry() *ValidatorRegistry {
	return &ValidatorRegistry{
		validators: map[models.Source]IntegrationValidator{
			models.SourceLaunchDarkly: newLDValidator(),
		},
	}
}

// Validate dispatches to the provider-specific validator.
// Returns ErrProviderUnreachable if the provider has no registered validator.
func (r *ValidatorRegistry) Validate(ctx context.Context, provider models.Source, apiKey string) error {
	v, ok := r.validators[provider]
	if !ok {
		return fmt.Errorf("%w: unknown provider %q", ErrProviderUnreachable, provider)
	}
	return v.Validate(ctx, provider, apiKey)
}

// ldValidator validates LaunchDarkly API keys.
type ldValidator struct {
	client *http.Client
}

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
