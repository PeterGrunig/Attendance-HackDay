package domain

import (
	"encoding/json"
	"time"
)

type ClassroomMembership struct {
	ClassroomID string
	UserID      string
	Role        string
	Primary     bool
	Active      bool
	Source      string
}

type ExternalEntityMapping struct {
	ID           int64
	ConnectionID int64
	EntityKind   string
	ExternalID   string
	LocalID      string
	SISID        string
	Active       bool
	LastSeenAt   *time.Time
}

type AttendanceMark struct {
	ID             int64
	UserID         string
	ClassroomID    string
	AttendanceDate time.Time
	Status         string
	Source         string
	CheckInAt      *time.Time
}

type AttendanceBatch struct {
	ID                      int64
	ClassroomID             string
	AttendanceDate          time.Time
	Version                 int
	WorkflowState           string
	ApprovedBy              string
	ApprovedAt              *time.Time
	DestinationConnectionID *int64
	Entries                 []AttendanceBatchEntry
}

type AttendanceBatchEntry struct {
	ID               int64
	BatchID          int64
	UserID           string
	Status           string
	AttendanceMarkID *int64
	DeliveryState    string
	ExternalRecordID string
	LastError        string
}

// AttendanceApprovalStudent is one active roster member as displayed in the
// daily class review, including the raw check-in signal and reviewed status.
type AttendanceApprovalStudent struct {
	UserID           string
	Name             string
	CheckedIn        bool
	Status           string
	AttendanceMarkID *int64
}

// AttendanceApproval captures the latest immutable class snapshot alongside
// the active roster teachers use to create the next version.
type AttendanceApproval struct {
	ClassroomID   string
	ClassroomName string
	Date          time.Time
	BatchID       int64
	Version       int
	WorkflowState string
	ApprovedBy    string
	ApprovedAt    *time.Time
	Students      []AttendanceApprovalStudent
}

type AttendanceApprovalRequest struct {
	ClassroomID string
	Date        time.Time
	ActorUserID string
	ActorRole   string
	Statuses    map[string]string
}

type AttendanceDeliveryAttempt struct {
	ID             int64
	BatchID        int64
	ConnectionID   int64
	IdempotencyKey string
	AttemptNumber  int
	State          string
	ResponseCode   *int
	ErrorMessage   string
	StartedAt      time.Time
	CompletedAt    *time.Time
}

type AttendanceExportQueueItem struct {
	BatchID        int64
	ClassroomID    string
	ClassroomName  string
	AttendanceDate time.Time
	Version        int
	WorkflowState  string
	DestinationID  int64
	Destination    string
	AttemptCount   int
	LastError      string
	UpdatedAt      time.Time
}

type AttendanceExportWork struct {
	Batch            AttendanceBatch
	ConnectionID     int64
	ProviderKind     string
	SchoolExternalID string
	ClassExternalID  string
	Entries          []AttendanceExportWorkEntry
}

type AttendanceExportWorkEntry struct {
	LocalUserID       string
	StudentExternalID string
	Status            string
	ExternalRecordID  string
}

type AttendanceExportEntryResult struct {
	LocalUserID      string
	Accepted         bool
	ExternalRecordID string
	Message          string
}

type IntegrationAuditEvent struct {
	ID          int64
	ActorUserID string
	EventType   string
	EntityType  string
	EntityID    string
	Metadata    json.RawMessage
	OccurredAt  time.Time
}
