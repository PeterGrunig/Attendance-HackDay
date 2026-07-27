package web

import (
	"context"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

type AttendanceExportStore interface {
	CreateIntegrationConnection(context.Context, integrations.Connection, string) (int64, error)
	LoadIntegrationConnection(context.Context, int64) (integrations.Connection, string, error)
	ListAttendanceDestinationConnections(context.Context) ([]domain.IntegrationConnectionSummary, error)
	EnableAttendanceDestination(context.Context, int64) error
	SetAttendanceDestinationStatus(context.Context, int64, string) error
	ListAttendanceExportQueue(context.Context, int) ([]domain.AttendanceExportQueueItem, error)
	AppendIntegrationAuditEvent(context.Context, domain.IntegrationAuditEvent) (int64, error)
}

type AttendanceExporter interface {
	ValidateDestination(context.Context, integrations.Connection) error
	Retry(context.Context, int64) error
}

var (
	attendanceExportStore AttendanceExportStore
	attendanceExporter    AttendanceExporter
	attendanceRegistry    *integrations.ProviderRegistry
)

func ConfigureAttendanceExports(registry *integrations.ProviderRegistry, exporter AttendanceExporter) {
	attendanceRegistry = registry
	attendanceExporter = exporter
}
