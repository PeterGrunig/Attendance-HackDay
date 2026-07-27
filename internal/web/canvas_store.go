package web

import (
	"context"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

type CanvasIntegrationStore interface {
	CreateIntegrationConnection(context.Context, integrations.Connection, string) (int64, error)
	LoadIntegrationConnection(context.Context, int64) (integrations.Connection, string, error)
	ListIntegrationConnections(context.Context, string) ([]domain.IntegrationConnectionSummary, error)
	UpdateIntegrationConnection(context.Context, integrations.Connection, string) error
	DisableIntegrationConnection(context.Context, int64) error
	LoadRosterMatchSnapshot(context.Context, int64) (domain.RosterMatchSnapshot, error)
	ApplyRosterImport(context.Context, domain.RosterImportProposal, map[string]string, string) (domain.RosterImportResult, error)
	AppendIntegrationAuditEvent(context.Context, domain.IntegrationAuditEvent) (int64, error)
}
