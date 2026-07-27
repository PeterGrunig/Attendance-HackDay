package domain

import (
	"encoding/json"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

type IntegrationConnectionSummary struct {
	ID             int64
	ProviderKind   string
	ConnectionRole string
	DisplayName    string
	Status         string
	Configuration  json.RawMessage
	UpdatedAt      time.Time
}

type RosterMatchSnapshot struct {
	Mappings []ExternalEntityMapping
	Users    []User
}

type RosterImportPerson struct {
	Person         integrations.Person
	LocalID        string
	AutoMatched    bool
	SuggestedID    string
	SuggestionKind string
}

type RosterImportProposal struct {
	ConnectionID int64
	Schools      []integrations.School
	Classes      []integrations.Class
	People       []RosterImportPerson
	Memberships  []integrations.Membership
	CreatedAt    time.Time
}

type RosterImportResult struct {
	ClassesCreated      int
	UsersCreated        int
	MembershipsImported int
	MembershipsArchived int
}
