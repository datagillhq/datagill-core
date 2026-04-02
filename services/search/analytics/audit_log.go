package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuditLogEntry is a single changelog row in the audit_log response.
type AuditLogEntry struct {
	OccurredAt   time.Time `json:"occurredAt"`
	FlagName     string    `json:"flagName"`
	FlagKey      uuid.UUID `json:"flagKey"`
	ActorEmail   string    `json:"actorEmail"`
	ActorName    string    `json:"actorName"`
	EventType    string    `json:"eventType"`
	Environments []string  `json:"environments"`
}

// AuditLogResponse is the audit_log plugin response envelope.
type AuditLogResponse struct {
	Total int             `json:"total"`
	Page  int             `json:"page"`
	Count int             `json:"count"`
	Data  []AuditLogEntry `json:"data"`
}

type auditLogPlugin struct{ repo AnalyticsRepository }

// NewAuditLogPlugin returns a plugin for the "audit_log" metric.
func NewAuditLogPlugin(repo AnalyticsRepository) Plugin {
	return &auditLogPlugin{repo: repo}
}

func (p *auditLogPlugin) Metric() string    { return "audit_log" }
func (p *auditLogPlugin) ResponseType() any { return AuditLogResponse{} }

func (p *auditLogPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryAuditLog(ctx, q.OrgID, q.From, q.To, q.Team, q.Actor, q.Page, q.Limit)
	if err != nil {
		return nil, err
	}
	entries := make([]AuditLogEntry, 0, len(res.Entries))
	for _, e := range res.Entries {
		envs := e.Environments
		if envs == nil {
			envs = []string{}
		}
		entries = append(entries, AuditLogEntry{
			OccurredAt:   e.OccurredAt,
			FlagName:     e.FlagName,
			FlagKey:      e.FlagKey,
			ActorEmail:   e.ActorEmail,
			ActorName:    e.ActorName,
			EventType:    e.EventType,
			Environments: envs,
		})
	}
	return AuditLogResponse{
		Total: res.Total,
		Page:  q.Page,
		Count: len(entries),
		Data:  entries,
	}, nil
}
