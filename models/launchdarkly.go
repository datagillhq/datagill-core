package models

import "time"

// LDAuditEntry represents a raw audit log entry from the LaunchDarkly API.
// See: https://launchdarkly.com/docs/api/audit-log/get-audit-log-entry
type LDAuditEntry struct {
	ID              string          `json:"_id"`
	AccountID       string          `json:"_accountId"`
	Date            int64           `json:"date"`
	Accesses        []LDAccess      `json:"accesses,omitempty"`
	Kind            string          `json:"kind"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	ShortDesc       string          `json:"shortDescription"`
	Comment         string          `json:"comment,omitempty"`
	Subject         *LDAuditSubject `json:"subject,omitempty"`
	Member          *LDAuditMember  `json:"member,omitempty"`
	Token           *LDAuditToken   `json:"token,omitempty"`
	App             *LDAuditApp     `json:"app,omitempty"`
	TitleVerb       string          `json:"titleVerb,omitempty"`
	Title           string          `json:"title,omitempty"`
	Target          *LDAuditTarget  `json:"target,omitempty"`
	Parent          *LDAuditParent  `json:"parent,omitempty"`
	Delta           any             `json:"delta,omitempty"`
	TriggerBody     any             `json:"triggerBody,omitempty"`
	Merge           any             `json:"merge,omitempty"`
	PreviousVersion any             `json:"previousVersion,omitempty"`
	CurrentVersion  any             `json:"currentVersion,omitempty"`
	Subentries      []LDAuditEntry  `json:"subentries,omitempty"`
}

// LDAccess represents a resource access in an audit entry.
type LDAccess struct {
	Action   string `json:"action,omitempty"`
	Resource string `json:"resource,omitempty"`
}

// LDAuditSubject represents the subject who performed an audited action.
type LDAuditSubject struct {
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// LDAuditApp represents an authorized app that performed an audited action.
type LDAuditApp struct {
	ID             string `json:"_id,omitempty"`
	IsScim         bool   `json:"isScim,omitempty"`
	Name           string `json:"name,omitempty"`
	MaintainerName string `json:"maintainerName,omitempty"`
}

// LDAuditParent represents the parent resource of an audited action.
type LDAuditParent struct {
	Name     string `json:"name,omitempty"`
	Resource string `json:"resource,omitempty"`
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
	ID           string `json:"_id,omitempty"`
	Name         string `json:"name,omitempty"`
	Ending       string `json:"ending,omitempty"`
	ServiceToken bool   `json:"serviceToken,omitempty"`
}

// LDAuditTarget represents the resource targeted by an audited action.
type LDAuditTarget struct {
	Name      string   `json:"name,omitempty"`
	Resources []string `json:"resources,omitempty"`
}

// LDFlagDetail represents a raw flag detail from the LaunchDarkly API.
// See: https://launchdarkly.com/docs/api/feature-flags/get-feature-flag
type LDFlagDetail struct {
	Key               string                  `json:"key"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description,omitempty"`
	Kind              string                  `json:"kind"`
	Version           int                     `json:"_version"`
	CreationDate      int64                   `json:"creationDate"`
	IncludeInSnippet  bool                    `json:"includeInSnippet,omitempty"`
	ClientSideAvail   *LDClientSideAvail      `json:"clientSideAvailability,omitempty"`
	Variations        []LDVariation           `json:"variations"`
	Temporary         bool                    `json:"temporary"`
	Tags              []string                `json:"tags"`
	MaintainerID      string                  `json:"maintainerId,omitempty"`
	Maintainer        *LDFlagMaintainer       `json:"_maintainer,omitempty"`
	MaintainerTeamKey string                  `json:"maintainerTeamKey,omitempty"`
	MaintainerTeam    *LDMaintainerTeam       `json:"_maintainerTeam,omitempty"`
	GoalIDs           []string                `json:"goalIds,omitempty"`
	Experiments       *LDExperiments          `json:"experiments,omitempty"`
	CustomProperties  map[string]LDCustomProp `json:"customProperties,omitempty"`
	Archived          bool                    `json:"archived"`
	ArchivedDate      *int64                  `json:"archivedDate,omitempty"`
	Deprecated        bool                    `json:"deprecated,omitempty"`
	DeprecatedDate    *int64                  `json:"deprecatedDate,omitempty"`
	Defaults          *LDFlagDefaults         `json:"defaults,omitempty"`
	Purpose           string                  `json:"_purpose,omitempty"`
	MigrationSettings *LDMigrationSettings    `json:"migrationSettings,omitempty"`
	Environments      map[string]LDFlagEnv    `json:"environments,omitempty"`
}

// LDMaintainerTeam represents the team that maintains a flag.
type LDMaintainerTeam struct {
	Key  string `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
}

// LDMigrationSettings represents migration-specific flag configuration.
type LDMigrationSettings struct {
	ContextKind string `json:"contextKind,omitempty"`
	StageCount  int    `json:"stageCount,omitempty"`
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
	On                     bool                    `json:"on"`
	Archived               bool                    `json:"archived"`
	Salt                   string                  `json:"salt"`
	Sel                    string                  `json:"sel"`
	LastModified           int64                   `json:"lastModified"`
	Version                int                     `json:"version"`
	Targets                []LDTarget              `json:"targets,omitempty"`
	ContextTargets         []LDContextTarget       `json:"contextTargets,omitempty"`
	Rules                  []LDRule                `json:"rules,omitempty"`
	Fallthrough            *LDFallthrough          `json:"fallthrough,omitempty"`
	OffVariation           *int                    `json:"offVariation,omitempty"`
	Prerequisites          []LDPrerequisite        `json:"prerequisites,omitempty"`
	Site                   *LDLink                 `json:"_site,omitempty"`
	Access                 *LDAccess               `json:"_access,omitempty"`
	EnvironmentName        string                  `json:"_environmentName,omitempty"`
	TrackEvents            bool                    `json:"trackEvents"`
	TrackEventsFallthrough bool                    `json:"trackEventsFallthrough"`
	DebugEventsUntilDate   *int64                  `json:"_debugEventsUntilDate,omitempty"`
	Summary                *LDFlagSummary          `json:"_summary,omitempty"`
	Evaluation             *LDEvaluation           `json:"evaluation,omitempty"`
	MigrationSettings      *LDEnvMigrationSettings `json:"migrationSettings,omitempty"`
}

// LDLink represents a link to a resource.
type LDLink struct {
	Href string `json:"href,omitempty"`
	Type string `json:"type,omitempty"`
}

// LDEvaluation represents evaluation context kinds.
type LDEvaluation struct {
	ContextKinds []string `json:"contextKinds,omitempty"`
}

// LDEnvMigrationSettings represents environment-level migration settings.
type LDEnvMigrationSettings struct {
	CheckRatio int `json:"checkRatio,omitempty"`
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
	ID          string     `json:"_id,omitempty"`
	Disabled    bool       `json:"disabled,omitempty"`
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
	Untracked bool `json:"_untracked,omitempty"`
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
	Variations    map[string]int `json:"variations,omitempty"`
	Prerequisites int            `json:"prerequisites,omitempty"`
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
