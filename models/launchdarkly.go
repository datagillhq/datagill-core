package models

import "time"

// LDAuditEntry represents a raw audit log entry from the LaunchDarkly API.
// See: https://apidocs.launchdarkly.com/tag/Audit-log
type LDAuditEntry struct {
	ID           string         `json:"_id"`
	Date         int64          `json:"date"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	ShortDesc    string         `json:"shortDescription"`
	Comment      string         `json:"comment"`
	Member       *LDAuditMember `json:"member,omitempty"`
	Token        *LDAuditToken  `json:"token,omitempty"`
	Target       *LDAuditTarget `json:"target,omitempty"`
	TitleVerb    string         `json:"titleVerb"`
	Title        string         `json:"title"`
	AccountID    string         `json:"accountId"`
	CurrentValue any            `json:"currentValue,omitempty"`
	PreviousVal  any            `json:"previousValue,omitempty"`
}

// LDAuditMember represents the member who performed an audited action.
type LDAuditMember struct {
	ID        string `json:"_id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// LDAuditToken represents an API token that performed an audited action.
type LDAuditToken struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
}

// LDAuditTarget represents the resource targeted by an audited action.
type LDAuditTarget struct {
	Name      string   `json:"name,omitempty"`
	Kind      string   `json:"kind"`
	Resources []string `json:"resources,omitempty"`
}

// LDFlagDetail represents a raw flag detail from the LaunchDarkly API.
// See: https://apidocs.launchdarkly.com/tag/Feature-flags
type LDFlagDetail struct {
	Key               string                  `json:"key"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	Kind              string                  `json:"kind"`
	CreationDate      int64                   `json:"creationDate"`
	Archived          bool                    `json:"archived"`
	Deprecated        bool                    `json:"deprecated"`
	Temporary         bool                    `json:"temporary"`
	IncludeInSnippet  bool                    `json:"includeInSnippet"`
	ClientSideAvail   *LDClientSideAvail      `json:"clientSideAvailability,omitempty"`
	Variations        []LDVariation           `json:"variations"`
	Tags              []string                `json:"tags"`
	Maintainer        *LDFlagMaintainer       `json:"_maintainer,omitempty"`
	MaintainerID      string                  `json:"maintainerId,omitempty"`
	MaintainerTeamKey string                  `json:"_maintainerTeam,omitempty"`
	GoalIDs           []string                `json:"goalIds,omitempty"`
	Experiments       *LDExperiments          `json:"experiments,omitempty"`
	CustomProperties  map[string]LDCustomProp `json:"customProperties,omitempty"`
	Environments      map[string]LDFlagEnv    `json:"environments"`
	Defaults          *LDFlagDefaults         `json:"defaults,omitempty"`
	Version           int                     `json:"_version"`
}

// LDClientSideAvail indicates where the flag can be evaluated client-side.
type LDClientSideAvail struct {
	UsingMobileKey     bool `json:"usingMobileKey"`
	UsingEnvironmentID bool `json:"usingEnvironmentId"`
}

// LDFlagMaintainer represents the individual maintainer of a flag.
type LDFlagMaintainer struct {
	ID        string `json:"_id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Role      string `json:"role"`
}

// LDFlagEnv represents per-environment flag configuration.
type LDFlagEnv struct {
	On                     bool              `json:"on"`
	Archived               bool              `json:"archived"`
	Salt                   string            `json:"salt"`
	Sel                    string            `json:"sel"`
	LastModified           int64             `json:"lastModified"`
	Version                int               `json:"version"`
	Targets                []LDTarget        `json:"targets,omitempty"`
	ContextTargets         []LDContextTarget `json:"contextTargets,omitempty"`
	Rules                  []LDRule          `json:"rules,omitempty"`
	Fallthrough            *LDFallthrough    `json:"fallthrough,omitempty"`
	OffVariation           *int              `json:"offVariation,omitempty"`
	Prerequisites          []LDPrerequisite  `json:"prerequisites,omitempty"`
	TrackEvents            bool              `json:"trackEvents"`
	TrackEventsFallthrough bool              `json:"trackEventsFallthrough"`
	DebugEventsUntilDate   *int64            `json:"debugEventsUntilDate,omitempty"`
	Summary                *LDFlagSummary    `json:"_summary,omitempty"`
}

// LDTarget represents a targeting rule for specific user keys.
type LDTarget struct {
	Values      []string `json:"values"`
	Variation   int      `json:"variation"`
	ContextKind string   `json:"contextKind,omitempty"`
}

// LDContextTarget represents targeting rules for context kinds.
type LDContextTarget struct {
	Values      []string `json:"values"`
	Variation   int      `json:"variation"`
	ContextKind string   `json:"contextKind"`
}

// LDRule represents a targeting rule with clauses.
type LDRule struct {
	ID          string     `json:"_id"`
	Variation   *int       `json:"variation,omitempty"`
	Rollout     *LDRollout `json:"rollout,omitempty"`
	Clauses     []LDClause `json:"clauses"`
	TrackEvents bool       `json:"trackEvents"`
	Description string     `json:"description,omitempty"`
	Ref         string     `json:"ref,omitempty"`
}

// LDClause represents a single clause in a targeting rule.
type LDClause struct {
	ID          string `json:"_id,omitempty"`
	Attribute   string `json:"attribute"`
	Op          string `json:"op"`
	Values      []any  `json:"values"`
	ContextKind string `json:"contextKind,omitempty"`
	Negate      bool   `json:"negate"`
}

// LDRollout represents a percentage rollout configuration.
type LDRollout struct {
	Variations      []LDWeightedVar `json:"variations"`
	ContextKind     string          `json:"contextKind,omitempty"`
	BucketBy        string          `json:"bucketBy,omitempty"`
	ExperimentAlloc *LDExpAlloc     `json:"experimentAllocation,omitempty"`
	Seed            *int            `json:"seed,omitempty"`
}

// LDWeightedVar represents a variation with a percentage weight.
type LDWeightedVar struct {
	Variation int  `json:"variation"`
	Weight    int  `json:"weight"`
	Untracked bool `json:"untracked,omitempty"`
}

// LDExpAlloc represents experiment allocation settings.
type LDExpAlloc struct {
	DefaultVariation int  `json:"defaultVariation"`
	CanReshuffle     bool `json:"canReshuffle"`
}

// LDFallthrough represents the default rule when no other rules match.
type LDFallthrough struct {
	Variation *int       `json:"variation,omitempty"`
	Rollout   *LDRollout `json:"rollout,omitempty"`
}

// LDPrerequisite represents a flag that must be on before this flag can be on.
type LDPrerequisite struct {
	Key       string `json:"key"`
	Variation int    `json:"variation"`
}

// LDFlagSummary represents summary statistics for a flag environment.
type LDFlagSummary struct {
	Variations map[string]int `json:"variations,omitempty"`
}

// LDVariation represents a single variation value for a flag.
type LDVariation struct {
	ID          string `json:"_id,omitempty"`
	Value       any    `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// LDExperiments holds experiment metadata for a flag.
type LDExperiments struct {
	BaselineIdx int         `json:"baselineIdx"`
	Items       []LDExpItem `json:"items,omitempty"`
}

// LDExpItem represents a single experiment item.
type LDExpItem struct {
	MetricKey   string     `json:"metricKey"`
	Iteration   int        `json:"_id"`
	Environment string     `json:"environmentId"`
	StartDate   *time.Time `json:"startDate,omitempty"`
	StopDate    *time.Time `json:"stopDate,omitempty"`
}

// LDCustomProp represents a custom property on a flag.
type LDCustomProp struct {
	Name  string   `json:"name"`
	Value []string `json:"value"`
}

// LDFlagDefaults represents the default on/off variations.
type LDFlagDefaults struct {
	OnVariation  int `json:"onVariation"`
	OffVariation int `json:"offVariation"`
}
