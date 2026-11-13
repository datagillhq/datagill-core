// cmd/seed populates the DataGill database with realistic feature flag test data.
// Run this after completing the onboarding flow (sign up → create org → add integration)
// to populate the dashboard with meaningful demo data.
//
// Usage:
//
//	go run ./cmd/seed \
//	  --db-url  "postgres://user:pass@localhost:5432/datagill" \
//	  --org-id  "<uuid from organizations.key>" \
//	  --integration-id "<uuid from integrations.key>"
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

// ptr returns a pointer to v, useful for optional fields.
func ptr[T any](v T) *T { return &v }

// ago returns a UTC timestamp N full days before now, truncated to the hour.
func ago(days int) time.Time {
	return time.Now().UTC().Truncate(time.Hour).Add(-time.Duration(days) * 24 * time.Hour)
}

// ─── Actors ──────────────────────────────────────────────────────────────────

type actor struct{ email, name string }

var (
	ariel  = actor{"ariel@acme.com", "Ariel Kim"}
	sarah  = actor{"sarah@acme.com", "Sarah Jenkins"}
	marcus = actor{"marcus@acme.com", "Marcus Chen"}
	priya  = actor{"priya@acme.com", "Priya Sharma"}
	james  = actor{"james@acme.com", "James Park"}
)

// ─── Seed data types ─────────────────────────────────────────────────────────

type seedFlag struct {
	sourceKey   string
	name        string
	description string
	team        string
	maintainer  actor
	production  models.FlagEnvironment
	staging     models.FlagEnvironment
	development models.FlagEnvironment
	state       models.FlagState
	daysOld     int
	events      []seedEvent
}

type seedEvent struct {
	daysAgo     int
	eventType   models.EventType
	changeType  string
	description string
	actor       actor
	envs        []string
}

// ─── Flag definitions ────────────────────────────────────────────────────────

const projectKey = "web-app"

var flags = []seedFlag{
	{
		sourceKey: "enable-checkout-v2", name: "Enable Checkout v2",
		description: "New checkout flow for premium users",
		team:        "Platform Eng", maintainer: ariel,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(100.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOn, daysOld: 60,
		events: []seedEvent{
			{59, models.EventTypeCreated, "flag_created", "Created feature flag", ariel, []string{"production", "staging", "development"}},
			{44, models.EventTypeUpdated, "flag_updated", "Updated targeting rules in staging", ariel, []string{"staging"}},
			{30, models.EventTypeToggled, "flag_enabled", "Enabled for beta users in production", sarah, []string{"production"}},
			{20, models.EventTypeUpdated, "flag_targeting_updated", "Expanded rollout to 50%", ariel, []string{"production"}},
			{10, models.EventTypeUpdated, "flag_targeting_updated", "Expanded rollout to 100%", ariel, []string{"production"}},
			{4, models.EventTypeUpdated, "flag_updated", "Performance optimizations applied", sarah, []string{"production", "staging"}},
			{1, models.EventTypeUpdated, "flag_updated", "Updated targeting rules for premium users", ariel, []string{"production"}},
		},
	},
	{
		sourceKey: "apple-pay-integration", name: "Apple Pay Integration",
		description: "Apple Pay as a payment method at checkout",
		team:        "Revenue Systems", maintainer: marcus,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(100.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: false},
		state:       models.FlagStateOn, daysOld: 45,
		events: []seedEvent{
			{44, models.EventTypeCreated, "flag_created", "Created Apple Pay integration flag", marcus, []string{"production", "staging", "development"}},
			{35, models.EventTypeUpdated, "flag_updated", "Updated payment provider config in staging", marcus, []string{"staging"}},
			{22, models.EventTypeToggled, "flag_enabled", "Enabled for production traffic", priya, []string{"production"}},
			{12, models.EventTypeUpdated, "flag_targeting_updated", "Rolled out to 100% of users", marcus, []string{"production"}},
			{5, models.EventTypeUpdated, "flag_updated", "Updated error handling for declined cards", priya, []string{"production", "staging"}},
			{2, models.EventTypeUpdated, "flag_updated", "Minor payment config adjustment", marcus, []string{"production"}},
		},
	},
	{
		sourceKey: "sso-integration", name: "SSO Integration",
		description: "SAML-based single sign-on for enterprise customers",
		team:        "Platform Eng", maintainer: ariel,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(100.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOn, daysOld: 35,
		events: []seedEvent{
			{34, models.EventTypeCreated, "flag_created", "Created SSO integration flag", ariel, []string{"production", "staging", "development"}},
			{28, models.EventTypeUpdated, "flag_updated", "Configured SAML provider in staging", ariel, []string{"staging"}},
			{18, models.EventTypeToggled, "flag_enabled", "Enabled for enterprise tier customers", sarah, []string{"production", "staging"}},
			{12, models.EventTypeUpdated, "flag_updated", "Updated SAML certificate rotation policy", ariel, []string{"production"}},
			{6, models.EventTypeUpdated, "flag_updated", "Extended session timeout to 24h", ariel, []string{"production", "staging"}},
			{2, models.EventTypeUpdated, "flag_updated", "Patched session handling edge case", sarah, []string{"production"}},
		},
	},
	{
		sourceKey: "feature-flag-audit-ui", name: "Feature Flag Audit UI",
		description: "Enhanced audit trail view in the management console",
		team:        "Platform Eng", maintainer: sarah,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(100.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOn, daysOld: 55,
		events: []seedEvent{
			{54, models.EventTypeCreated, "flag_created", "Created audit UI flag", sarah, []string{"production", "staging", "development"}},
			{40, models.EventTypeUpdated, "flag_updated", "Updated table layout in staging", sarah, []string{"staging"}},
			{25, models.EventTypeToggled, "flag_enabled", "Rolled out to all internal users", ariel, []string{"production"}},
			{15, models.EventTypeUpdated, "flag_updated", "Added CSV export feature", sarah, []string{"production"}},
			{8, models.EventTypeUpdated, "flag_updated", "Improved filter query performance", ariel, []string{"production"}},
			{3, models.EventTypeUpdated, "flag_updated", "Fixed pagination edge case", sarah, []string{"production", "staging"}},
			{1, models.EventTypeUpdated, "flag_updated", "Refactored audit trail display component", sarah, []string{"production"}},
		},
	},
	{
		sourceKey: "localization-v2", name: "Localization v2",
		description: "Redesigned i18n system with dynamic locale loading",
		team:        "Mobile", maintainer: james,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(100.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOn, daysOld: 40,
		events: []seedEvent{
			{39, models.EventTypeCreated, "flag_created", "Created localization v2 flag", james, []string{"production", "staging", "development"}},
			{28, models.EventTypeUpdated, "flag_updated", "Added Spanish and French locales", james, []string{"staging"}},
			{18, models.EventTypeToggled, "flag_enabled", "Enabled in production for English users", james, []string{"production"}},
			{10, models.EventTypeUpdated, "flag_updated", "Added German and Japanese locales", james, []string{"production", "staging"}},
			{5, models.EventTypeUpdated, "flag_targeting_updated", "Rolled out to 100% of users", james, []string{"production"}},
			{3, models.EventTypeToggled, "flag_updated", "Fully launched in all supported locales", james, []string{"production"}},
			{1, models.EventTypeUpdated, "flag_updated", "Added Portuguese locale support", james, []string{"production", "staging"}},
		},
	},
	{
		sourceKey: "new-onboarding-flow", name: "New Onboarding Flow",
		description: "Redesigned user onboarding with inline guidance and progress tracking",
		team:        "Mobile", maintainer: james,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(50.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStatePartial, daysOld: 20,
		events: []seedEvent{
			{19, models.EventTypeCreated, "flag_created", "Created onboarding flow flag", james, []string{"production", "staging", "development"}},
			{14, models.EventTypeUpdated, "flag_updated", "Updated step 2 UI copy and layout", james, []string{"staging", "development"}},
			{8, models.EventTypeToggled, "flag_enabled", "Enabled for 50% of new signups", james, []string{"production"}},
			{4, models.EventTypeUpdated, "flag_updated", "Adjusted targeting to exclude enterprise tier", james, []string{"production"}},
			{1, models.EventTypeUpdated, "flag_updated", "Adjusted rollout rules", james, []string{"production"}},
		},
	},
	{
		sourceKey: "mobile-push-notifications", name: "Mobile Push Notifications",
		description: "Rich push notifications with deep-links for mobile app users",
		team:        "Mobile", maintainer: james,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(75.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: false},
		state:       models.FlagStatePartial, daysOld: 18,
		events: []seedEvent{
			{17, models.EventTypeCreated, "flag_created", "Created push notifications flag", james, []string{"production", "staging"}},
			{12, models.EventTypeUpdated, "flag_updated", "Configured notification templates and TTL", james, []string{"staging"}},
			{7, models.EventTypeToggled, "flag_enabled", "Enabled for 25% of mobile users", james, []string{"production"}},
			{4, models.EventTypeUpdated, "flag_targeting_updated", "Increased rollout to 75%", james, []string{"production"}},
			{2, models.EventTypeUpdated, "flag_updated", "Increased rollout to 75%", james, []string{"production", "staging"}},
		},
	},
	{
		sourceKey: "beta-dashboard", name: "Beta Dashboard",
		description: "New analytics dashboard with real-time metrics and custom date ranges",
		team:        "Platform Eng", maintainer: sarah,
		production:  models.FlagEnvironment{Enabled: true, RolloutPercentage: ptr(25.0)},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStatePartial, daysOld: 14,
		events: []seedEvent{
			{13, models.EventTypeCreated, "flag_created", "Created beta dashboard flag", sarah, []string{"production", "staging", "development"}},
			{10, models.EventTypeUpdated, "flag_updated", "Updated chart layout and color scheme", sarah, []string{"staging"}},
			{7, models.EventTypeToggled, "flag_enabled", "Enabled for internal engineering team", ariel, []string{"production"}},
			{4, models.EventTypeUpdated, "flag_targeting_updated", "Increased rollout to 25% of users", sarah, []string{"production"}},
			{1, models.EventTypeUpdated, "flag_updated", "Increased rollout to 25%", sarah, []string{"production"}},
		},
	},
	{
		sourceKey: "dark-mode", name: "Dark Mode",
		description: "System-wide dark mode theme toggle",
		team:        "Platform Eng", maintainer: sarah,
		production:  models.FlagEnvironment{Enabled: false},
		staging:     models.FlagEnvironment{Enabled: false},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOff, daysOld: 85,
		events: []seedEvent{
			{84, models.EventTypeCreated, "flag_created", "Created dark mode flag", sarah, []string{"development"}},
			{60, models.EventTypeUpdated, "flag_updated", "Prototype ready for staging review", sarah, []string{"staging"}},
			{35, models.EventTypeRollback, "flag_rollback", "Rolled back due to CSS rendering issues in Safari", ariel, []string{"staging"}},
			{7, models.EventTypeUpdated, "flag_updated", "Re-enabled in development with fixed styles", sarah, []string{"development"}},
		},
	},
	{
		sourceKey: "ai-recommendations", name: "AI Recommendations",
		description: "ML-powered product recommendations at checkout and homepage",
		team:        "Revenue Systems", maintainer: priya,
		production:  models.FlagEnvironment{Enabled: false},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOff, daysOld: 30,
		events: []seedEvent{
			{29, models.EventTypeCreated, "flag_created", "Created AI recommendations flag", priya, []string{"staging", "development"}},
			{20, models.EventTypeUpdated, "flag_updated", "Updated model inference configuration", priya, []string{"staging"}},
			{12, models.EventTypeUpdated, "flag_updated", "Switched to updated recommendation model v2", marcus, []string{"staging"}},
			{5, models.EventTypeUpdated, "flag_updated", "Updated model weights", priya, []string{"staging"}},
		},
	},
	{
		sourceKey: "payment-gateway-v3", name: "Payment Gateway v3",
		description: "Next-gen payment processing with improved fraud detection",
		team:        "Revenue Systems", maintainer: marcus,
		production:  models.FlagEnvironment{Enabled: false},
		staging:     models.FlagEnvironment{Enabled: true},
		development: models.FlagEnvironment{Enabled: true},
		state:       models.FlagStateOff, daysOld: 25,
		events: []seedEvent{
			{24, models.EventTypeCreated, "flag_created", "Created payment gateway v3 flag", marcus, []string{"staging", "development"}},
			{16, models.EventTypeUpdated, "flag_updated", "Updated Stripe SDK and fraud scoring config", marcus, []string{"staging", "development"}},
			{8, models.EventTypeUpdated, "flag_updated", "Added velocity-based fraud detection", priya, []string{"staging"}},
			{3, models.EventTypeUpdated, "flag_updated", "Enabled in staging for QA testing", marcus, []string{"staging"}},
		},
	},
	{
		sourceKey: "data-export-api", name: "Data Export API",
		description: "Self-service CSV and Parquet data export for enterprise analytics",
		team:        "Revenue Systems", maintainer: priya,
		production:  models.FlagEnvironment{Enabled: false},
		staging:     models.FlagEnvironment{Enabled: false},
		development: models.FlagEnvironment{Enabled: false},
		state:       models.FlagStateOff, daysOld: 50,
		events: []seedEvent{
			{49, models.EventTypeCreated, "flag_created", "Created data export API flag", priya, []string{"development"}},
			{40, models.EventTypeUpdated, "flag_updated", "Schema design complete, enabled in staging", priya, []string{"staging"}},
			{25, models.EventTypeRollback, "flag_rollback", "Paused pending security review", marcus, []string{"staging"}},
		},
	},
}

// ─── Insert helpers ───────────────────────────────────────────────────────────

const insertFlag = `
	INSERT INTO feature_flags
		(key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (key) DO UPDATE SET
		source_record_key = EXCLUDED.source_record_key,
		state             = EXCLUDED.state,
		data              = EXCLUDED.data,
		updated_at        = EXCLUDED.updated_at`

const insertChangelog = `
	INSERT INTO feature_flag_changelog
		(key, source_record_key, feature_flag_key, integration_id, org_id, event_type, state, occurred_at, data, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (key) DO UPDATE SET
		state      = EXCLUDED.state,
		data       = EXCLUDED.data,
		updated_at = EXCLUDED.updated_at`

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	dbURL := flag.String("db-url", "", "PostgreSQL connection string (required)")
	orgIDStr := flag.String("org-id", "", "Organization UUID — organizations.key (required)")
	integrationIDStr := flag.String("integration-id", "", "Integration UUID — integrations.key (required)")
	flag.Parse()

	if *dbURL == "" || *orgIDStr == "" || *integrationIDStr == "" {
		flag.Usage()
		log.Fatal("all three flags are required")
	}

	orgID, err := uuid.Parse(*orgIDStr)
	if err != nil {
		log.Fatalf("invalid --org-id: %v", err)
	}
	integrationID, err := uuid.Parse(*integrationIDStr)
	if err != nil {
		log.Fatalf("invalid --integration-id: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping: %v", err)
	}

	now := time.Now().UTC()
	flagCount, eventCount := 0, 0

	for _, f := range flags {
		flagKey := models.ComputeFlagKey(orgID, models.SourceLaunchDarkly, projectKey, f.sourceKey)
		createdAt := ago(f.daysOld)

		createdAtPtr := createdAt
		data := models.FeatureFlagData{
			Name:        f.name,
			Description: f.description,
			Source: models.FlagSource{
				Key:       f.sourceKey,
				Provider:  models.SourceLaunchDarkly,
				Project:   models.FlagProject{ID: projectKey},
				CreatedAt: &createdAtPtr,
			},
			Maintainer: &models.FlagMaintainer{
				Person: &models.FlagMaintainerPerson{Email: f.maintainer.email},
				Team:   &models.FlagMaintainerTeam{Name: f.team},
			},
			Lifecycle: models.FlagLifecycle{
				Purpose: models.LifecyclePurposeTemporary,
				State:   models.LifecycleStateActive,
			},
			Environments: map[string]models.FlagEnvironment{
				"production":  f.production,
				"staging":     f.staging,
				"development": f.development,
			},
		}

		dataJSON, err := json.Marshal(data)
		if err != nil {
			log.Fatalf("marshal flag %s: %v", f.sourceKey, err)
		}

		srcKey := models.ComputeSourceRecordKey(orgID, models.SourceLaunchDarkly, models.SourceTypeFlagDetail, f.sourceKey)

		_, err = pool.Exec(ctx, insertFlag,
			flagKey, srcKey, integrationID, orgID,
			string(f.state), dataJSON,
			createdAt, now,
		)
		if err != nil {
			log.Fatalf("insert flag %s: %v", f.sourceKey, err)
		}
		flagCount++

		for i, ev := range f.events {
			occurredAt := ago(ev.daysAgo)

			eventID := fmt.Sprintf("%s-evt-%d", f.sourceKey, i)
			changelogKey := models.ComputeChangelogKey(orgID, models.SourceLaunchDarkly, projectKey, eventID)
			evtSrcKey := models.ComputeSourceRecordKey(orgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, eventID)

			evtData := models.ChangelogData{
				Description: ev.description,
				Actor: models.ChangelogActor{
					Email: ev.actor.email,
					Name:  ev.actor.name,
				},
				Source: models.FlagSource{
					Key:      eventID,
					Provider: models.SourceLaunchDarkly,
					Project:  models.FlagProject{ID: projectKey},
				},
				Environments: ev.envs,
				ChangeType:   ev.changeType,
			}

			evtDataJSON, err := json.Marshal(evtData)
			if err != nil {
				log.Fatalf("marshal event %s: %v", eventID, err)
			}

			_, err = pool.Exec(ctx, insertChangelog,
				changelogKey, evtSrcKey, flagKey,
				integrationID, orgID,
				string(ev.eventType),
				string(models.RecordStateActive),
				occurredAt,
				evtDataJSON,
				occurredAt, now,
			)
			if err != nil {
				log.Fatalf("insert changelog %s: %v", eventID, err)
			}
			eventCount++
		}
	}

	fmt.Printf("✓ Seeded %d feature flags and %d changelog events\n", flagCount, eventCount)
	fmt.Printf("  org_id:          %s\n", orgID)
	fmt.Printf("  integration_id:  %s\n", integrationID)
}
