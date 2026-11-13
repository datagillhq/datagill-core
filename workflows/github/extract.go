package github

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/activities"
	ghactivities "github.com/datagill/datagill-core/activities/github"
	"github.com/datagill/datagill-core/models"
)

const (
	searchPullRequestsTimeout = 2 * time.Minute
	getIntegrationTimeout     = 10 * time.Second
	pullRequestDetailTimeout  = 30 * time.Second
	normalizeTimeout          = 30 * time.Second
	savePullRequestTimeout    = 30 * time.Second

	syncCadence         = 30 * time.Minute
	searchOverlap       = 5 * time.Minute
	initialSearchWindow = syncCadence + searchOverlap
	minSearchWindow     = 5 * time.Minute
	searchPageSize      = 100
	searchSplitLimit    = 900
	detailConcurrency   = 10
)

// ExtractInput is the input for GitHubExtractWorkflow.
type ExtractInput struct {
	Integration         models.Integration
	LastSearchedThrough *time.Time
	SearchFrom          *time.Time
	RunOnce             bool
}

// GitHubExtractWorkflow ingests merged pull requests for one GitHub integration.
func GitHubExtractWorkflow(ctx workflow.Context, input ExtractInput) error {
	integration, err := currentIntegration(ctx, input.Integration)
	if err != nil {
		// Deleted mid-run: stop quietly, like a paused integration.
		if activities.IsIntegrationNotFound(err) {
			return nil
		}
		return fmt.Errorf("GitHubExtractWorkflow: get integration: %w", err)
	}
	if integration.State != models.IntegrationStateActive {
		return nil
	}

	to := workflow.Now(ctx)
	from := searchFrom(input, to)

	refs, err := searchPullRequests(ctx, integration, from, to)
	if err != nil {
		return fmt.Errorf("GitHubExtractWorkflow: search pull requests: %w", err)
	}
	if err := processPullRequests(ctx, integration, refs); err != nil {
		return fmt.Errorf("GitHubExtractWorkflow: process pull requests: %w", err)
	}
	if input.RunOnce {
		return nil
	}
	if err := workflow.Sleep(ctx, syncCadence); err != nil {
		return err
	}
	return workflow.NewContinueAsNewError(ctx, GitHubExtractWorkflow, ExtractInput{
		Integration:         integration,
		LastSearchedThrough: &to,
	})
}

func searchFrom(input ExtractInput, to time.Time) time.Time {
	if input.LastSearchedThrough != nil {
		return input.LastSearchedThrough.Add(-searchOverlap)
	}
	if input.SearchFrom != nil {
		return *input.SearchFrom
	}
	return to.Add(-initialSearchWindow)
}

func currentIntegration(ctx workflow.Context, integration models.Integration) (models.Integration, error) {
	getCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: getIntegrationTimeout,
	})
	var current models.Integration
	if err := workflow.ExecuteActivity(getCtx, (*activities.SharedActivities).GetIntegration, integration.OrgID, integration.Key).Get(ctx, &current); err != nil {
		return models.Integration{}, err
	}
	return current, nil
}

func searchPullRequests(ctx workflow.Context, integration models.Integration, from, to time.Time) ([]ghactivities.PullRequestRef, error) {
	searchCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: searchPullRequestsTimeout,
	})

	var refs []ghactivities.PullRequestRef
	page := 1
	for {
		var out ghactivities.SearchPullRequestsOutput
		err := workflow.ExecuteActivity(searchCtx, (*ghactivities.Activities).SearchPullRequests, ghactivities.SearchPullRequestsInput{
			IntegrationKey: integration.Key,
			OrgID:          integration.OrgID,
			From:           from,
			To:             to,
			Page:           page,
			PerPage:        searchPageSize,
		}).Get(ctx, &out)
		if err != nil {
			return nil, err
		}

		needsSplit := out.TotalCount > searchSplitLimit || out.IncompleteResults
		if needsSplit {
			if to.Sub(from) <= minSearchWindow {
				return nil, fmt.Errorf("github search window %s..%s exceeds reliable search limits: total_count=%d incomplete_results=%t", from.Format(time.RFC3339), to.Format(time.RFC3339), out.TotalCount, out.IncompleteResults)
			}
			mid := from.Add(to.Sub(from) / 2)
			left, err := searchPullRequests(ctx, integration, from, mid)
			if err != nil {
				return nil, err
			}
			right, err := searchPullRequests(ctx, integration, mid, to)
			if err != nil {
				return nil, err
			}
			return append(left, right...), nil
		}

		refs = append(refs, out.Refs...)
		if out.NextPage == 0 {
			break
		}
		page = out.NextPage
	}

	return refs, nil
}

func processPullRequests(ctx workflow.Context, integration models.Integration, refs []ghactivities.PullRequestRef) error {
	logger := workflow.GetLogger(ctx)
	detailCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: pullRequestDetailTimeout,
	})
	normalizeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: normalizeTimeout,
	})
	saveCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: savePullRequestTimeout,
	})

	for start := 0; start < len(refs); start += detailConcurrency {
		end := start + detailConcurrency
		if end > len(refs) {
			end = len(refs)
		}

		errs := make([]error, end-start)
		wg := workflow.NewWaitGroup(ctx)
		for i, ref := range refs[start:end] {
			i := i
			ref := ref
			wg.Add(1)
			workflow.Go(ctx, func(gCtx workflow.Context) {
				defer wg.Done()

				var detail ghactivities.GetPullRequestDetailOutput
				if err := workflow.ExecuteActivity(detailCtx, (*ghactivities.Activities).GetPullRequestDetail, ghactivities.GetPullRequestDetailInput{
					IntegrationKey: integration.Key,
					OrgID:          integration.OrgID,
					Ref:            ref,
				}).Get(gCtx, &detail); err != nil {
					// A PR or repo deleted (or hidden from the app) since the search is skipped,
					// so one missing PR does not fail every run. Anything else, such as bad
					// credentials, affects every PR and fails the run.
					if ghactivities.IsNotFound(err) {
						logger.Warn("skipping pull request: not found", "owner", ref.Owner, "repo", ref.Repo, "number", ref.Number, "error", err)
						return
					}
					logger.Warn("pull request detail fetch failed", "owner", ref.Owner, "repo", ref.Repo, "number", ref.Number, "error", err)
					errs[i] = err
					return
				}

				var pr models.PullRequest
				if err := workflow.ExecuteActivity(normalizeCtx, (*ghactivities.Activities).NormalizePullRequest, ghactivities.NormalizePullRequestInput{
					IntegrationKey:  integration.Key,
					OrgID:           integration.OrgID,
					SourceRecordKey: detail.SourceRecordKey,
					Ref:             detail.Ref,
					Detail:          detail.Detail,
				}).Get(gCtx, &pr); err != nil {
					logger.Warn("skipping pull request: normalize failed", "owner", ref.Owner, "repo", ref.Repo, "number", ref.Number, "error", err)
					errs[i] = err
					return
				}

				if err := workflow.ExecuteActivity(saveCtx, (*ghactivities.Activities).SavePullRequest, pr).Get(gCtx, nil); err != nil {
					logger.Warn("skipping pull request: save failed", "owner", ref.Owner, "repo", ref.Repo, "number", ref.Number, "error", err)
					errs[i] = err
				}
			})
		}
		wg.Wait(ctx)
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
	}
	return nil
}
