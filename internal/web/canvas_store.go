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
	UpsertStudentIntegrationConnection(context.Context, string, integrations.Connection) (int64, error)
	LoadStudentIntegrationConnection(context.Context, string, string) (integrations.Connection, string, error)
	LoadStudentIntegrationSummary(context.Context, string, string) (domain.IntegrationConnectionSummary, error)
	DisableStudentIntegrationConnection(context.Context, string, string) error
	LoadRosterMatchSnapshot(context.Context, int64) (domain.RosterMatchSnapshot, error)
	ApplyRosterImport(context.Context, domain.RosterImportProposal, map[string]string, string) (domain.RosterImportResult, error)
	AppendIntegrationAuditEvent(context.Context, domain.IntegrationAuditEvent) (int64, error)
}
