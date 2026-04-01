package launchdarkly

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// NormalizeFlagDetailInput is the input for the NormalizeFlagDetail activity.
type NormalizeFlagDetailInput struct {
	Flag            models.LDFlagDetail
	SourceRecordKey uuid.UUID
	IntegrationKey  uuid.UUID
	OrgID           uuid.UUID
	ProjectKey      string
}

// NormalizeFlagDetail transforms a LaunchDarkly flag into a canonical feature flag.
func (a *Activities) NormalizeFlagDetail(_ context.Context, input NormalizeFlagDetailInput) (models.FeatureFlag, error) {
	flag := input.Flag

	environments := make(map[string]models.FlagEnvironment, len(flag.Environments))
	for key, env := range flag.Environments {
		environments[key] = models.FlagEnvironment{
			Enabled:           env.On,
			RolloutPercentage: extractRolloutPercentage(flag, env),
		}
	}

	return models.FeatureFlag{
		Key:             models.ComputeFlagKey(input.OrgID, models.SourceLaunchDarkly, input.ProjectKey, flag.Key),
		SourceRecordKey: input.SourceRecordKey,
		IntegrationID:   input.IntegrationKey,
		OrgID:           input.OrgID,
		State:           computeFlagState(flag),
		Data: models.FeatureFlagData{
			Name:        flag.Name,
			Description: flag.Description,
			Source: models.FlagSource{
				Key:       flag.Key,
				Provider:  models.SourceLaunchDarkly,
				Project:   models.FlagProject{ID: input.ProjectKey},
				CreatedAt: creationDate(flag),
			},
			Maintainer:   extractMaintainer(flag),
			Lifecycle:    extractLifecycle(flag),
			Environments: environments,
		},
	}, nil
}

func computeFlagState(flag models.LDFlagDetail) models.FlagState {
	if len(flag.Environments) == 0 {
		return models.FlagStateOff
	}

	allOn, allOff := true, true
	for _, env := range flag.Environments {
		if env.On {
			allOff = false
		} else {
			allOn = false
		}
	}

	switch {
	case allOn:
		return models.FlagStateOn
	case allOff:
		return models.FlagStateOff
	default:
		return models.FlagStatePartial
	}
}

func extractMaintainer(flag models.LDFlagDetail) *models.FlagMaintainer {
	if flag.Maintainer != nil && flag.Maintainer.Email != "" {
		return &models.FlagMaintainer{
			Person: &models.FlagMaintainerPerson{Email: flag.Maintainer.Email},
		}
	}
	if flag.MaintainerTeam != nil && flag.MaintainerTeam.Name != "" {
		return &models.FlagMaintainer{
			Team: &models.FlagMaintainerTeam{Name: flag.MaintainerTeam.Name},
		}
	}
	return nil
}

func extractLifecycle(flag models.LDFlagDetail) models.FlagLifecycle {
	purpose := models.LifecyclePurposePermanent
	if flag.Temporary {
		purpose = models.LifecyclePurposeTemporary
	}

	state := models.LifecycleStateActive
	switch {
	case flag.Archived:
		state = models.LifecycleStateArchived
	case flag.Deprecated:
		state = models.LifecycleStateDeprecated
	}

	return models.FlagLifecycle{Purpose: purpose, State: state}
}

// extractRolloutPercentage derives what percentage of traffic is being served the
// "on" variation for this environment.
//
//   - Off (env.On = false)                         → 0%
//   - On, fallthrough points to a specific variation → 100%
//   - On, fallthrough is a percentage rollout        → weight of the on-variation / 1000
//     (LD weights are out of 100 000; requires flag.Defaults to identify the on-variation)
//   - Can't determine                               → nil
func extractRolloutPercentage(flag models.LDFlagDetail, env models.LDFlagEnv) *float64 {
	pct := func(v float64) *float64 { return &v }

	if !env.On {
		return pct(0)
	}
	if env.Fallthrough == nil || env.Fallthrough.Rollout == nil {
		return pct(100)
	}
	if flag.Defaults == nil {
		return nil
	}
	onVariation := flag.Defaults.OnVariation
	for _, v := range env.Fallthrough.Rollout.Variations {
		if v.Variation == onVariation {
			return pct(float64(v.Weight) / 1000.0)
		}
	}
	return nil
}

func creationDate(flag models.LDFlagDetail) *time.Time {
	if flag.CreationDate <= 0 {
		return nil
	}
	t := time.UnixMilli(flag.CreationDate)
	return &t
}
