package github

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// FetchPRDiffInput is the input for the FetchPRDiff activity.
type FetchPRDiffInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	Owner          string
	Repo           string
	PRNumber       int64
	BaseRef        string
	HeadSHA        string
}

// FetchPRDiff fetches the raw unified diff for a pull request using an installation token.
func (a *Activities) FetchPRDiff(ctx context.Context, input FetchPRDiffInput) (string, error) {
	token, err := a.getInstallationToken(ctx, input.OrgID, input.IntegrationKey)
	if err != nil {
		return "", fmt.Errorf("FetchPRDiff get token: %w", err)
	}

	diff, err := a.client.FetchRawDiff(ctx, token.Token, input.Owner, input.Repo, input.PRNumber, input.BaseRef, input.HeadSHA)
	if err != nil {
		return "", toServiceError(fmt.Errorf("FetchPRDiff: %w", err))
	}
	return diff, nil
}
