package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

var (
	ErrAttendanceExportNotDue    = errors.New("attendance export is not due")
	ErrAttendanceExportExhausted = errors.New("attendance export retry limit reached")
	ErrAttendanceExportMapping   = errors.New("attendance export mapping is incomplete")
)

func (s *SQLStore) ListAttendanceDestinationConnections(ctx context.Context) ([]domain.IntegrationConnectionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT IntegrationConnectionID, ProviderKind, ConnectionRole, DisplayName,
			Status, Configuration, UpdatedAt
		FROM IntegrationConnections
		WHERE ConnectionRole = 'attendance_destination'
		ORDER BY DisplayName, IntegrationConnectionID;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var connections []domain.IntegrationConnectionSummary
	for rows.Next() {
		var connection domain.IntegrationConnectionSummary
		if err := rows.Scan(&connection.ID, &connection.ProviderKind, &connection.ConnectionRole,
			&connection.DisplayName, &connection.Status, &connection.Configuration,
			&connection.UpdatedAt); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

// EnableAttendanceDestination activates one outbound destination without
// changing roster-source connections, then queues locally approved batches.
func (s *SQLStore) EnableAttendanceDestination(ctx context.Context, connectionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var targetID int64
	err = tx.QueryRowContext(ctx, `
		SELECT IntegrationConnectionID
		FROM IntegrationConnections
		WHERE IntegrationConnectionID = $1
		  AND ConnectionRole = 'attendance_destination'
		FOR UPDATE;
	`, connectionID).Scan(&targetID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE IntegrationConnections
		SET Status = CASE WHEN IntegrationConnectionID = $1 THEN 'active' ELSE 'disabled' END,
			UpdatedAt = CURRENT_TIMESTAMP
		WHERE ConnectionRole = 'attendance_destination'
		  AND (IntegrationConnectionID = $1 OR Status = 'active');
	`, connectionID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE AttendanceBatches
		SET DestinationConnectionID = $1,
			WorkflowState = CASE
				WHEN WorkflowState = 'approved' THEN 'export_pending'
				ELSE WorkflowState
			END,
			UpdatedAt = CURRENT_TIMESTAMP
		WHERE DestinationConnectionID IS NULL
		  AND WorkflowState IN ('approved', 'correction_pending');
	`, connectionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) SetAttendanceDestinationStatus(ctx context.Context, connectionID int64, status string) error {
	if status != "active" && status != "disabled" && status != "error" {
		return fmt.Errorf("invalid connection status %q", status)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE IntegrationConnections
		SET Status = $2, UpdatedAt = CURRENT_TIMESTAMP
		WHERE IntegrationConnectionID = $1
		  AND ConnectionRole = 'attendance_destination';
	`, connectionID, status)
	return err
}

func (s *SQLStore) ListAttendanceExportQueue(ctx context.Context, limit int) ([]domain.AttendanceExportQueueItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.AttendanceBatchID, b.ClassroomID, c.Name, b.AttendanceDate,
			b.Version, b.WorkflowState, COALESCE(b.DestinationConnectionID, 0),
			COALESCE(ic.DisplayName, 'No destination'), COUNT(a.AttendanceDeliveryAttemptID),
			COALESCE((
				SELECT NULLIF(abe.LastError, '')
				FROM AttendanceBatchEntries abe
				WHERE abe.AttendanceBatchID = b.AttendanceBatchID
				  AND COALESCE(abe.LastError, '') <> ''
				ORDER BY abe.AttendanceBatchEntryID
				LIMIT 1
			), ''), b.UpdatedAt
		FROM AttendanceBatches b
		JOIN Classrooms c ON c.ID = b.ClassroomID
		LEFT JOIN IntegrationConnections ic
			ON ic.IntegrationConnectionID = b.DestinationConnectionID
		LEFT JOIN AttendanceDeliveryAttempts a
			ON a.AttendanceBatchID = b.AttendanceBatchID
		WHERE b.WorkflowState IN ('export_pending', 'export_failed', 'correction_pending')
		GROUP BY b.AttendanceBatchID, c.Name, ic.DisplayName
		ORDER BY b.UpdatedAt, b.AttendanceBatchID
		LIMIT $1;
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.AttendanceExportQueueItem
	for rows.Next() {
		var item domain.AttendanceExportQueueItem
		if err := rows.Scan(&item.BatchID, &item.ClassroomID, &item.ClassroomName,
			&item.AttendanceDate, &item.Version, &item.WorkflowState,
			&item.DestinationID, &item.Destination, &item.AttemptCount,
			&item.LastError, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLStore) ListDueAttendanceExportIDs(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.AttendanceBatchID
		FROM AttendanceBatches b
		JOIN IntegrationConnections ic
		  ON ic.IntegrationConnectionID = b.DestinationConnectionID
		 AND ic.Status = 'active'
		 AND ic.ConnectionRole = 'attendance_destination'
		WHERE b.WorkflowState IN ('export_pending', 'correction_pending')
		ORDER BY b.UpdatedAt, b.AttendanceBatchID
		LIMIT $1;
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// StartAttendanceExportAttempt atomically claims one batch. Recent in-flight
// attempts and exponential backoff prevent multiple workers from hot-looping.
func (s *SQLStore) StartAttendanceExportAttempt(ctx context.Context, batchID int64, maxAttempts int, now time.Time, force bool) (domain.AttendanceDeliveryAttempt, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return domain.AttendanceDeliveryAttempt{}, err
	}
	defer tx.Rollback()

	var connectionID int64
	var workflowState string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(DestinationConnectionID, 0), WorkflowState
		FROM AttendanceBatches
		WHERE AttendanceBatchID = $1
		FOR UPDATE;
	`, batchID).Scan(&connectionID, &workflowState)
	if err != nil {
		return domain.AttendanceDeliveryAttempt{}, err
	}
	if connectionID == 0 || (!force && workflowState != "export_pending" && workflowState != "correction_pending") {
		return domain.AttendanceDeliveryAttempt{}, ErrAttendanceExportNotDue
	}

	var attemptCount int
	var latestState sql.NullString
	var latestStarted sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*),
			(SELECT State FROM AttendanceDeliveryAttempts WHERE AttendanceBatchID = $1 ORDER BY AttemptNumber DESC LIMIT 1),
			(SELECT StartedAt FROM AttendanceDeliveryAttempts WHERE AttendanceBatchID = $1 ORDER BY AttemptNumber DESC LIMIT 1)
		FROM AttendanceDeliveryAttempts
		WHERE AttendanceBatchID = $1;
	`, batchID).Scan(&attemptCount, &latestState, &latestStarted)
	if err != nil {
		return domain.AttendanceDeliveryAttempt{}, err
	}
	if !force && attemptCount >= maxAttempts {
		return domain.AttendanceDeliveryAttempt{}, ErrAttendanceExportExhausted
	}
	if !force && latestStarted.Valid {
		wait := retryDelay(attemptCount)
		if latestState.String == "started" {
			wait = 5 * time.Minute
		}
		if now.Before(latestStarted.Time.Add(wait)) {
			return domain.AttendanceDeliveryAttempt{}, ErrAttendanceExportNotDue
		}
	}

	attempt := domain.AttendanceDeliveryAttempt{
		BatchID: batchID, ConnectionID: connectionID, AttemptNumber: attemptCount + 1,
		State: "started", StartedAt: now,
		IdempotencyKey: fmt.Sprintf("attendance-batch:%d:attempt:%d", batchID, attemptCount+1),
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO AttendanceDeliveryAttempts
			(AttendanceBatchID, IntegrationConnectionID, IdempotencyKey,
			 AttemptNumber, State, StartedAt)
		VALUES ($1, $2, $3, $4, 'started', $5)
		RETURNING AttendanceDeliveryAttemptID;
	`, attempt.BatchID, attempt.ConnectionID, attempt.IdempotencyKey,
		attempt.AttemptNumber, attempt.StartedAt).Scan(&attempt.ID)
	if err != nil {
		return domain.AttendanceDeliveryAttempt{}, err
	}
	startedMetadata, _ := json.Marshal(map[string]any{
		"attempt": attempt.AttemptNumber, "connection_id": attempt.ConnectionID,
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO IntegrationAuditEvents
			(EventType, EntityType, EntityID, Metadata, OccurredAt)
		VALUES ('attendance.export_attempt_started', 'attendance_batch', $1, $2, CURRENT_TIMESTAMP);
	`, fmt.Sprint(batchID), startedMetadata); err != nil {
		return domain.AttendanceDeliveryAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AttendanceDeliveryAttempt{}, err
	}
	return attempt, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	delay := 30 * time.Second * time.Duration(1<<min(attempt-1, 6))
	if delay > 30*time.Minute {
		return 30 * time.Minute
	}
	return delay
}

func (s *SQLStore) LoadAttendanceExportWork(ctx context.Context, batchID, connectionID int64) (domain.AttendanceExportWork, error) {
	var work domain.AttendanceExportWork
	work.ConnectionID = connectionID
	err := s.db.QueryRowContext(ctx, `
		SELECT b.AttendanceBatchID, b.ClassroomID, b.AttendanceDate, b.Version,
			b.WorkflowState, ic.ProviderKind, COALESCE(sm.ExternalID, ''),
			COALESCE(cm.ExternalID, '')
		FROM AttendanceBatches b
		JOIN Classrooms c ON c.ID = b.ClassroomID
		JOIN IntegrationConnections ic
		  ON ic.IntegrationConnectionID = $2
		 AND ic.ConnectionRole = 'attendance_destination'
		LEFT JOIN ExternalEntityMappings sm
		  ON sm.IntegrationConnectionID = ic.IntegrationConnectionID
		 AND sm.EntityKind = 'school' AND sm.LocalID = c.SchoolID AND sm.Active = true
		LEFT JOIN ExternalEntityMappings cm
		  ON cm.IntegrationConnectionID = ic.IntegrationConnectionID
		 AND cm.EntityKind = 'classroom' AND cm.LocalID = b.ClassroomID AND cm.Active = true
		WHERE b.AttendanceBatchID = $1
		  AND b.DestinationConnectionID = $2;
	`, batchID, connectionID).Scan(
		&work.Batch.ID, &work.Batch.ClassroomID, &work.Batch.AttendanceDate,
		&work.Batch.Version, &work.Batch.WorkflowState, &work.ProviderKind,
		&work.SchoolExternalID, &work.ClassExternalID,
	)
	if err != nil {
		return domain.AttendanceExportWork{}, err
	}
	if work.SchoolExternalID == "" || work.ClassExternalID == "" {
		return domain.AttendanceExportWork{}, ErrAttendanceExportMapping
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT abe.UserID, COALESCE(um.ExternalID, ''), abe.Status,
			COALESCE(NULLIF(abe.ExternalRecordID, ''), (
				SELECT prior.ExternalRecordID
				FROM AttendanceBatchEntries prior
				JOIN AttendanceBatches pb ON pb.AttendanceBatchID = prior.AttendanceBatchID
				WHERE pb.ClassroomID = b.ClassroomID
				  AND pb.AttendanceDate = b.AttendanceDate
				  AND pb.Version < b.Version
				  AND prior.UserID = abe.UserID
				  AND COALESCE(prior.ExternalRecordID, '') <> ''
				ORDER BY pb.Version DESC LIMIT 1
			), '')
		FROM AttendanceBatchEntries abe
		JOIN AttendanceBatches b ON b.AttendanceBatchID = abe.AttendanceBatchID
		LEFT JOIN ExternalEntityMappings um
		  ON um.IntegrationConnectionID = $2
		 AND um.EntityKind = 'user' AND um.LocalID = abe.UserID AND um.Active = true
		WHERE abe.AttendanceBatchID = $1
		ORDER BY abe.UserID;
	`, batchID, connectionID)
	if err != nil {
		return domain.AttendanceExportWork{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry domain.AttendanceExportWorkEntry
		if err := rows.Scan(&entry.LocalUserID, &entry.StudentExternalID,
			&entry.Status, &entry.ExternalRecordID); err != nil {
			return domain.AttendanceExportWork{}, err
		}
		if entry.StudentExternalID == "" {
			return domain.AttendanceExportWork{}, ErrAttendanceExportMapping
		}
		work.Entries = append(work.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return domain.AttendanceExportWork{}, err
	}
	if len(work.Entries) == 0 {
		return domain.AttendanceExportWork{}, ErrAttendanceExportMapping
	}
	return work, nil
}

// FinishAttendanceExport persists every student response and the batch state
// with the attempt audit event, so failed delivery is never shown as official.
func (s *SQLStore) FinishAttendanceExport(ctx context.Context, attempt domain.AttendanceDeliveryAttempt, results []domain.AttendanceExportEntryResult, providerError string, retryable, exhausted bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()

	attemptState := "accepted"
	batchState := "exported"
	eventType := "attendance.export_succeeded"
	if providerError != "" {
		attemptState = "rejected"
		batchState = "export_failed"
		eventType = "attendance.export_failed"
		if retryable && !exhausted {
			attemptState = "retry_pending"
			batchState = "export_pending"
			eventType = "attendance.export_retry_scheduled"
		}
		deliveryState := "rejected"
		if attemptState == "retry_pending" {
			deliveryState = "retry_pending"
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE AttendanceBatchEntries
			SET DeliveryState = $2, LastError = $3, UpdatedAt = CURRENT_TIMESTAMP
			WHERE AttendanceBatchID = $1;
		`, attempt.BatchID, deliveryState, providerError); err != nil {
			return err
		}
	} else {
		for _, result := range results {
			state := "accepted"
			if !result.Accepted {
				state = "rejected"
				attemptState = "rejected"
				batchState = "export_failed"
				eventType = "attendance.export_rejected"
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE AttendanceBatchEntries
				SET DeliveryState = $3, ExternalRecordID = NULLIF($4, ''),
					LastError = NULLIF($5, ''), UpdatedAt = CURRENT_TIMESTAMP
				WHERE AttendanceBatchID = $1 AND UserID = $2;
			`, attempt.BatchID, result.LocalUserID, state,
				result.ExternalRecordID, result.Message); err != nil {
				return err
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE AttendanceDeliveryAttempts
		SET State = $2, ErrorMessage = NULLIF($3, ''), CompletedAt = $4
		WHERE AttendanceDeliveryAttemptID = $1;
	`, attempt.ID, attemptState, providerError, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE AttendanceBatches
		SET WorkflowState = $2, UpdatedAt = CURRENT_TIMESTAMP
		WHERE AttendanceBatchID = $1;
	`, attempt.BatchID, batchState); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{
		"attempt": attempt.AttemptNumber, "state": attemptState,
		"batch_state": batchState, "error": providerError, "results": results,
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO IntegrationAuditEvents
			(EventType, EntityType, EntityID, Metadata, OccurredAt)
		VALUES ($1, 'attendance_batch', $2, $3, CURRENT_TIMESTAMP);
	`, eventType, fmt.Sprint(attempt.BatchID), metadata); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("attendance export completed: batch_id=%d attempt=%d state=%q batch_state=%q",
		attempt.BatchID, attempt.AttemptNumber, attemptState, batchState)
	return nil
}
