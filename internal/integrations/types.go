package integrations

import (
	"context"
	"encoding/json"
	"time"
)

type Capability string

const (
	CapabilityRosterRead            Capability = "roster.read"
	CapabilityAttendanceWrite       Capability = "attendance.write"
	CapabilityAttendanceCorrections Capability = "attendance.correct"
	CapabilityAttendanceSafeUpsert  Capability = "attendance.upsert"
	CapabilityIncrementalSync       Capability = "sync.incremental"
)

type AuthenticationMode string

const (
	AuthenticationOAuth2            AuthenticationMode = "oauth2"
	AuthenticationClientCredentials AuthenticationMode = "client_credentials"
	AuthenticationAPIKey            AuthenticationMode = "api_key"
)

type ConnectionRole string

const (
	ConnectionRoleRosterSource          ConnectionRole = "roster_source"
	ConnectionRoleAttendanceDestination ConnectionRole = "attendance_destination"
)

type ProviderMetadata struct {
	Kind               string
	DisplayName        string
	AuthenticationMode AuthenticationMode
	Capabilities       []Capability
}

func (m ProviderMetadata) Supports(capability Capability) bool {
	for _, supported := range m.Capabilities {
		if supported == capability {
			return true
		}
	}
	return false
}

// Connection contains decrypted credentials only while an adapter call is in
// progress. Persistence code must encrypt Credentials before storing it.
type Connection struct {
	ID              int64
	ProviderKind    string
	Role            ConnectionRole
	DisplayName     string
	Configuration   json.RawMessage
	Credentials     json.RawMessage
	AttendanceCodes AttendanceCodeMapping
}

type School struct {
	ExternalID string
	SISID      string
	Name       string
	Active     bool
}

type Class struct {
	ExternalID       string
	SISID            string
	SchoolExternalID string
	Name             string
	Active           bool
}

type PersonRole string

const (
	PersonRoleStudent PersonRole = "student"
	PersonRoleTeacher PersonRole = "teacher"
)

type Person struct {
	ExternalID string
	SISID      string
	Name       string
	Email      string
	Role       PersonRole
	Active     bool
}

type Membership struct {
	ExternalID       string
	ClassExternalID  string
	PersonExternalID string
	Role             PersonRole
	Primary          bool
	Active           bool
}

type RosterRequest struct {
	Cursor string
	Limit  int
}

type RosterPage struct {
	Schools     []School
	Classes     []Class
	People      []Person
	Memberships []Membership
	NextCursor  string
}

type AttendanceStatus string

const (
	AttendancePresent AttendanceStatus = "present"
	AttendanceAbsent  AttendanceStatus = "absent"
)

type AttendanceEntry struct {
	StudentExternalID string
	Status            AttendanceStatus
	ExternalRecordID  string
	IdempotencyKey    string
}

type AttendanceCodeMapping map[AttendanceStatus]string

// AttendanceDestinationConfiguration keeps provider-neutral attendance codes
// separate from the adapter's non-secret configuration document.
type AttendanceDestinationConfiguration struct {
	Provider json.RawMessage       `json:"provider"`
	Codes    AttendanceCodeMapping `json:"attendance_codes"`
}

type AttendanceBatch struct {
	ID               int64
	Version          int
	SchoolExternalID string
	ClassExternalID  string
	SchoolDate       time.Time
	Entries          []AttendanceEntry
}

type DeliveryEntryResult struct {
	StudentExternalID string
	ExternalRecordID  string
	Accepted          bool
	Message           string
}

type DeliveryResult struct {
	Entries []DeliveryEntryResult
}

type Provider interface {
	Metadata() ProviderMetadata
}

type RosterSource interface {
	Provider
	ValidateConnection(context.Context, Connection) error
	ReadRoster(context.Context, Connection, RosterRequest) (RosterPage, error)
}

type AttendanceDestination interface {
	Provider
	ValidateConnection(context.Context, Connection) error
	ValidateAttendanceCodes(context.Context, Connection, AttendanceCodeMapping) error
	UpsertAttendanceBatch(context.Context, Connection, AttendanceBatch) (DeliveryResult, error)
}

type DestinationMappingValidator interface {
	ValidateExternalMapping(entityKind, externalID string) error
}
