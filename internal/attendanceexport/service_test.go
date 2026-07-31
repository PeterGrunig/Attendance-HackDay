package attendanceexport

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

type exportTestStore struct {
	attempt       domain.AttendanceDeliveryAttempt
	work          domain.AttendanceExportWork
	connection    integrations.Connection
	status        string
	finished      bool
	finishResults []domain.AttendanceExportEntryResult
	finishMessage string
	finishRetry   bool
	finishExhaust bool
	statusValue   string
}

func (s *exportTestStore) LoadIntegrationConnection(context.Context, int64) (integrations.Connection, string, error) {
	return s.connection, s.status, nil
}
func (s *exportTestStore) ListDueAttendanceExportIDs(context.Context, int) ([]int64, error) {
	return []int64{s.attempt.BatchID}, nil
}
func (s *exportTestStore) StartAttendanceExportAttempt(context.Context, int64, int, time.Time, bool) (domain.AttendanceDeliveryAttempt, error) {
	return s.attempt, nil
}
func (s *exportTestStore) LoadAttendanceExportWork(context.Context, int64, int64) (domain.AttendanceExportWork, error) {
	return s.work, nil
}
func (s *exportTestStore) FinishAttendanceExport(_ context.Context, _ domain.AttendanceDeliveryAttempt, results []domain.AttendanceExportEntryResult, message string, retryable, exhausted bool) error {
	s.finished = true
	s.finishResults = results
	s.finishMessage = message
	s.finishRetry = retryable
	s.finishExhaust = exhausted
	return nil
}
func (s *exportTestStore) SetAttendanceDestinationStatus(_ context.Context, _ int64, status string) error {
	s.statusValue = status
	return nil
}

type exportTestDestination struct {
	metadata       integrations.ProviderMetadata
	validateCalled bool
	received       integrations.Connection
	batch          integrations.AttendanceBatch
	result         integrations.DeliveryResult
	err            error
}

func (d *exportTestDestination) Metadata() integrations.ProviderMetadata { return d.metadata }
func (d *exportTestDestination) ValidateConnection(_ context.Context, connection integrations.Connection) error {
	d.validateCalled = true
	d.received = connection
	return d.err
}
func (d *exportTestDestination) ValidateAttendanceCodes(_ context.Context, connection integrations.Connection, _ integrations.AttendanceCodeMapping) error {
	d.received = connection
	return d.err
}
func (d *exportTestDestination) UpsertAttendanceBatch(_ context.Context, connection integrations.Connection, batch integrations.AttendanceBatch) (integrations.DeliveryResult, error) {
	d.received = connection
	d.batch = batch
	return d.result, d.err
}

func newExportTestService(t *testing.T, destination *exportTestDestination, store *exportTestStore) *Service {
	t.Helper()
	registry := integrations.NewProviderRegistry()
	if err := registry.Register(destination); err != nil {
		t.Fatalf("register destination: %v", err)
	}
	return New(store, registry)
}

func exportTestConnection(t *testing.T) integrations.Connection {
	t.Helper()
	configuration, err := json.Marshal(integrations.AttendanceDestinationConfiguration{
		Provider: json.RawMessage(`{"tenant":"district"}`),
		Codes: integrations.AttendanceCodeMapping{
			integrations.AttendancePresent: "P",
			integrations.AttendanceAbsent:  "A",
		},
	})
	if err != nil {
		t.Fatalf("marshal connection: %v", err)
	}
	return integrations.Connection{
		ID: 7, ProviderKind: "test-destination",
		Role:          integrations.ConnectionRoleAttendanceDestination,
		Configuration: configuration,
	}
}

func exportTestMetadata() integrations.ProviderMetadata {
	return integrations.ProviderMetadata{
		Kind: "test-destination",
		Capabilities: []integrations.Capability{
			integrations.CapabilityAttendanceWrite,
			integrations.CapabilityAttendanceSafeUpsert,
		},
	}
}

func TestValidateDestinationUnwrapsProviderConfigurationAndCodes(t *testing.T) {
	destination := &exportTestDestination{metadata: exportTestMetadata()}
	service := newExportTestService(t, destination, &exportTestStore{})

	if err := service.ValidateDestination(context.Background(), exportTestConnection(t)); err != nil {
		t.Fatalf("ValidateDestination: %v", err)
	}
	if !destination.validateCalled || string(destination.received.Configuration) != `{"tenant":"district"}` {
		t.Fatalf("destination received %#v", destination.received)
	}
	if destination.received.AttendanceCodes[integrations.AttendanceAbsent] != "A" {
		t.Fatalf("attendance codes = %#v", destination.received.AttendanceCodes)
	}
}

func TestRetryMapsProviderResultsAndGeneratesIdempotencyKeys(t *testing.T) {
	date := time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC)
	store := &exportTestStore{
		attempt:    domain.AttendanceDeliveryAttempt{ID: 3, BatchID: 11, ConnectionID: 7, AttemptNumber: 1},
		connection: exportTestConnection(t),
		status:     "active",
		work: domain.AttendanceExportWork{
			Batch:            domain.AttendanceBatch{ID: 11, Version: 2, AttendanceDate: date},
			SchoolExternalID: "school-1", ClassExternalID: "class-1",
			Entries: []domain.AttendanceExportWorkEntry{
				{LocalUserID: "local-1", StudentExternalID: "external-1", Status: "present"},
				{LocalUserID: "local-2", StudentExternalID: "external-2", Status: "absent"},
				{LocalUserID: "local-3", StudentExternalID: "external-3", Status: "absent"},
			},
		},
	}
	destination := &exportTestDestination{
		metadata: exportTestMetadata(),
		result: integrations.DeliveryResult{Entries: []integrations.DeliveryEntryResult{
			{StudentExternalID: "external-1", Accepted: true, ExternalRecordID: "record-1"},
			{StudentExternalID: "external-2", Accepted: true},
			{StudentExternalID: "unexpected", Accepted: true, ExternalRecordID: "ignored"},
		}},
	}
	service := newExportTestService(t, destination, store)

	if err := service.Retry(context.Background(), 11); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if !store.finished || len(store.finishResults) != 3 {
		t.Fatalf("finished=%t results=%#v", store.finished, store.finishResults)
	}
	if !store.finishResults[0].Accepted || store.finishResults[0].ExternalRecordID != "record-1" {
		t.Fatalf("accepted result = %#v", store.finishResults[0])
	}
	if store.finishResults[1].Accepted || !strings.Contains(store.finishResults[1].Message, "without an external record") {
		t.Fatalf("missing record result = %#v", store.finishResults[1])
	}
	if store.finishResults[2].Accepted || !strings.Contains(store.finishResults[2].Message, "no result") {
		t.Fatalf("missing student result = %#v", store.finishResults[2])
	}
	keys := map[string]bool{}
	for _, entry := range destination.batch.Entries {
		if !strings.HasPrefix(entry.IdempotencyKey, "aq-attendance-") || keys[entry.IdempotencyKey] {
			t.Fatalf("invalid or duplicate idempotency key %q", entry.IdempotencyKey)
		}
		keys[entry.IdempotencyKey] = true
	}
}

func TestRetryPersistsAuthenticationFailureAndDisablesDestination(t *testing.T) {
	store := &exportTestStore{
		attempt:    domain.AttendanceDeliveryAttempt{BatchID: 11, ConnectionID: 7, AttemptNumber: 5},
		connection: exportTestConnection(t), status: "active",
		work: domain.AttendanceExportWork{Batch: domain.AttendanceBatch{ID: 11}},
	}
	destination := &exportTestDestination{metadata: exportTestMetadata(), err: integrations.ErrAuthentication}
	service := newExportTestService(t, destination, store)

	err := service.Retry(context.Background(), 11)
	if !errors.Is(err, integrations.ErrAuthentication) {
		t.Fatalf("Retry error = %v, want ErrAuthentication", err)
	}
	if !store.finished || store.finishRetry || !store.finishExhaust || store.statusValue != "error" {
		t.Fatalf("failure state: finished=%t retry=%t exhausted=%t status=%q", store.finished, store.finishRetry, store.finishExhaust, store.statusValue)
	}
}

func TestRetryableClassifiesOnlyTransientFailures(t *testing.T) {
	for _, err := range []error{integrations.ErrRateLimited, integrations.ErrTemporaryFailure, context.DeadlineExceeded} {
		if !retryable(err) {
			t.Fatalf("retryable(%v) = false", err)
		}
	}
	for _, err := range []error{integrations.ErrAuthentication, integrations.ErrPermission, integrations.ErrPermanentRejection} {
		if retryable(err) {
			t.Fatalf("retryable(%v) = true", err)
		}
	}
}
