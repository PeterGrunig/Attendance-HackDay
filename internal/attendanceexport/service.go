package attendanceexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
	"github.com/PeterGrunig/Attendance-HackDay/internal/store"
)

const (
	maxAutomaticAttempts = 5
	maxBatchesPerRun     = 20
	exportPollInterval   = 15 * time.Second
)

type Store interface {
	LoadIntegrationConnection(context.Context, int64) (integrations.Connection, string, error)
	ListDueAttendanceExportIDs(context.Context, int) ([]int64, error)
	StartAttendanceExportAttempt(context.Context, int64, int, time.Time, bool) (domain.AttendanceDeliveryAttempt, error)
	LoadAttendanceExportWork(context.Context, int64, int64) (domain.AttendanceExportWork, error)
	FinishAttendanceExport(context.Context, domain.AttendanceDeliveryAttempt, []domain.AttendanceExportEntryResult, string, bool, bool) error
	SetAttendanceDestinationStatus(context.Context, int64, string) error
}

type Service struct {
	store    Store
	registry *integrations.ProviderRegistry
}

func New(serviceStore Store, registry *integrations.ProviderRegistry) *Service {
	return &Service{store: serviceStore, registry: registry}
}

// ValidateDestination ensures a provider can safely repeat attendance writes
// before a connection may become an active outbound destination.
func (s *Service) ValidateDestination(ctx context.Context, connection integrations.Connection) error {
	destination, prepared, err := s.destination(connection)
	if err != nil {
		return err
	}
	if err := destination.ValidateConnection(ctx, prepared); err != nil {
		return err
	}
	return destination.ValidateAttendanceCodes(ctx, prepared, prepared.AttendanceCodes)
}

// Run polls the durable batch outbox at a fixed interval. Each pass is bounded
// so provider or database trouble cannot create an unbounded retry loop.
func (s *Service) Run(ctx context.Context) {
	if s == nil || s.store == nil || s.registry == nil {
		return
	}
	ticker := time.NewTicker(exportPollInterval)
	defer ticker.Stop()
	for {
		if err := s.ProcessDue(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("attendance export outbox pass failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) ProcessDue(ctx context.Context) error {
	ids, err := s.store.ListDueAttendanceExportIDs(ctx, maxBatchesPerRun)
	if err != nil {
		return err
	}
	for _, batchID := range ids {
		if err := s.process(ctx, batchID, false); err != nil &&
			!errors.Is(err, store.ErrAttendanceExportNotDue) &&
			!errors.Is(err, store.ErrAttendanceExportExhausted) {
			log.Printf("attendance export batch failed: batch_id=%d error=%v", batchID, err)
		}
	}
	return nil
}

func (s *Service) Retry(ctx context.Context, batchID int64) error {
	return s.process(ctx, batchID, true)
}

// process claims a durable attempt, builds provider-neutral external
// identities, calls one adapter, and persists every student-level response.
func (s *Service) process(ctx context.Context, batchID int64, force bool) error {
	attempt, err := s.store.StartAttendanceExportAttempt(ctx, batchID, maxAutomaticAttempts, time.Now(), force)
	if err != nil {
		return err
	}
	work, err := s.store.LoadAttendanceExportWork(ctx, batchID, attempt.ConnectionID)
	if err != nil {
		return s.finishError(ctx, attempt, err, false)
	}
	connection, status, err := s.store.LoadIntegrationConnection(ctx, attempt.ConnectionID)
	if err != nil {
		return s.finishError(ctx, attempt, err, retryable(err))
	}
	if status != "active" {
		return s.finishError(ctx, attempt, fmt.Errorf("attendance destination is %s", status), false)
	}
	destination, prepared, err := s.destination(connection)
	if err != nil {
		return s.finishError(ctx, attempt, err, false)
	}

	batch := integrations.AttendanceBatch{
		ID: work.Batch.ID, Version: work.Batch.Version,
		SchoolExternalID: work.SchoolExternalID, ClassExternalID: work.ClassExternalID,
		SchoolDate: work.Batch.AttendanceDate,
	}
	localByExternal := make(map[string]string, len(work.Entries))
	for _, entry := range work.Entries {
		localByExternal[entry.StudentExternalID] = entry.LocalUserID
		batch.Entries = append(batch.Entries, integrations.AttendanceEntry{
			StudentExternalID: entry.StudentExternalID,
			Status:            integrations.AttendanceStatus(entry.Status),
			ExternalRecordID:  entry.ExternalRecordID,
			IdempotencyKey: idempotencyIdentity(
				attempt.ConnectionID, work.SchoolExternalID, work.ClassExternalID,
				work.Batch.AttendanceDate, entry.StudentExternalID, work.Batch.Version,
			),
		})
	}

	response, err := destination.UpsertAttendanceBatch(ctx, prepared, batch)
	if err != nil {
		return s.finishError(ctx, attempt, err, retryable(err))
	}
	byExternal := make(map[string]integrations.DeliveryEntryResult, len(response.Entries))
	for _, result := range response.Entries {
		if _, expected := localByExternal[result.StudentExternalID]; expected {
			byExternal[result.StudentExternalID] = result
		}
	}
	results := make([]domain.AttendanceExportEntryResult, 0, len(work.Entries))
	for _, entry := range work.Entries {
		result, ok := byExternal[entry.StudentExternalID]
		if !ok {
			results = append(results, domain.AttendanceExportEntryResult{
				LocalUserID: entry.LocalUserID,
				Message:     "destination returned no result for this student",
			})
			continue
		}
		if result.Accepted && result.ExternalRecordID == "" {
			result.Accepted = false
			result.Message = "destination accepted attendance without an external record identifier"
		}
		results = append(results, domain.AttendanceExportEntryResult{
			LocalUserID: entry.LocalUserID, Accepted: result.Accepted,
			ExternalRecordID: result.ExternalRecordID, Message: result.Message,
		})
	}
	return s.store.FinishAttendanceExport(ctx, attempt, results, "", false, false)
}

func (s *Service) destination(connection integrations.Connection) (integrations.AttendanceDestination, integrations.Connection, error) {
	if s == nil || s.registry == nil {
		return nil, integrations.Connection{}, integrations.ErrProviderNotFound
	}
	if connection.Role != integrations.ConnectionRoleAttendanceDestination {
		return nil, integrations.Connection{}, fmt.Errorf("%w: connection is not an attendance destination", integrations.ErrInvalidConfiguration)
	}
	provider, err := s.registry.Get(connection.ProviderKind)
	if err != nil {
		return nil, integrations.Connection{}, err
	}
	metadata := provider.Metadata()
	if !metadata.Supports(integrations.CapabilityAttendanceWrite) ||
		(!metadata.Supports(integrations.CapabilityAttendanceSafeUpsert) &&
			!metadata.Supports(integrations.CapabilityAttendanceCorrections)) {
		return nil, integrations.Connection{}, fmt.Errorf("%w: provider must support safe attendance upsert or correction", integrations.ErrCapabilityUnsupported)
	}
	destination, ok := provider.(integrations.AttendanceDestination)
	if !ok {
		return nil, integrations.Connection{}, integrations.ErrCapabilityUnsupported
	}
	var config integrations.AttendanceDestinationConfiguration
	if err := json.Unmarshal(connection.Configuration, &config); err != nil {
		return nil, integrations.Connection{}, fmt.Errorf("%w: attendance destination configuration", integrations.ErrInvalidConfiguration)
	}
	if len(config.Provider) == 0 {
		config.Provider = json.RawMessage(`{}`)
	}
	if config.Codes[integrations.AttendancePresent] == "" ||
		config.Codes[integrations.AttendanceAbsent] == "" {
		return nil, integrations.Connection{}, fmt.Errorf("%w: present and absent code mappings are required", integrations.ErrInvalidConfiguration)
	}
	connection.Configuration = config.Provider
	connection.AttendanceCodes = config.Codes
	return destination, connection, nil
}

func (s *Service) finishError(ctx context.Context, attempt domain.AttendanceDeliveryAttempt, err error, canRetry bool) error {
	message := err.Error()
	exhausted := attempt.AttemptNumber >= maxAutomaticAttempts
	if finishErr := s.store.FinishAttendanceExport(ctx, attempt, nil, message, canRetry, exhausted); finishErr != nil {
		return fmt.Errorf("finish export after %v: %w", err, finishErr)
	}
	if errors.Is(err, integrations.ErrAuthentication) || errors.Is(err, integrations.ErrPermission) {
		if statusErr := s.store.SetAttendanceDestinationStatus(ctx, attempt.ConnectionID, "error"); statusErr != nil {
			log.Printf("attendance destination health update failed: connection_id=%d error=%v", attempt.ConnectionID, statusErr)
		}
	}
	return err
}

func retryable(err error) bool {
	return errors.Is(err, integrations.ErrRateLimited) ||
		errors.Is(err, integrations.ErrTemporaryFailure) ||
		errors.Is(err, context.DeadlineExceeded)
}

func idempotencyIdentity(destinationID int64, schoolID, classID string, date time.Time, studentID string, version int) string {
	value := fmt.Sprintf("%d|%s|%s|%s|%s|%d", destinationID, schoolID, classID,
		date.Format("2006-01-02"), studentID, version)
	sum := sha256.Sum256([]byte(value))
	return "aq-attendance-" + hex.EncodeToString(sum[:])
}
