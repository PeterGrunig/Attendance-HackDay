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
	ErrAttendanceClassForbidden = errors.New("attendance class is not assigned to this user")
	ErrAttendanceRosterChanged  = errors.New("attendance roster changed; reload before approving")
	ErrInvalidAttendanceStatus  = errors.New("attendance status must be present or absent")
)

func (s *SQLStore) ListAttendanceApprovalClasses(ctx context.Context, userID, role string) ([]domain.Classroom, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.ID, c.Name, COALESCE(c.TeacherID, ''), ''
		FROM Classrooms c
		WHERE $2 = 'admin'
		   OR EXISTS (
				SELECT 1 FROM ClassroomMemberships cm
				WHERE cm.ClassroomID = c.ID
				  AND cm.UserID = $1
				  AND cm.MembershipRole = 'teacher'
				  AND cm.Active = true
		   )
		ORDER BY c.Name, c.ID;
	`, userID, role)
	if err != nil {
		log.Printf("attendance approval class list failed: user_id=%q role=%q error=%v", userID, role, err)
		return nil, err
	}
	defer rows.Close()

	var classrooms []domain.Classroom
	for rows.Next() {
		var classroom domain.Classroom
		if err := rows.Scan(&classroom.ID, &classroom.Name, &classroom.TeacherID, &classroom.ManagedBy); err != nil {
			return nil, err
		}
		classrooms = append(classrooms, classroom)
	}
	return classrooms, rows.Err()
}

// LoadAttendanceApproval builds a review from the active roster. Raw student
// check-ins provide initial defaults, while the latest immutable batch wins
// when a teacher is reviewing a previously approved date.
func (s *SQLStore) LoadAttendanceApproval(ctx context.Context, userID, role, classroomID string, date time.Time) (domain.AttendanceApproval, error) {
	approval := domain.AttendanceApproval{ClassroomID: classroomID, Date: date}
	err := s.db.QueryRowContext(ctx, `
		SELECT c.Name
		FROM Classrooms c
		WHERE c.ID = $3
		  AND ($2 = 'admin' OR EXISTS (
				SELECT 1 FROM ClassroomMemberships cm
				WHERE cm.ClassroomID = c.ID
				  AND cm.UserID = $1
				  AND cm.MembershipRole = 'teacher'
				  AND cm.Active = true
		  ));
	`, userID, role, classroomID).Scan(&approval.ClassroomName)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AttendanceApproval{}, ErrAttendanceClassForbidden
	}
	if err != nil {
		return domain.AttendanceApproval{}, err
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT AttendanceBatchID, Version, WorkflowState, COALESCE(ApprovedBy, ''), ApprovedAt
		FROM AttendanceBatches
		WHERE ClassroomID = $1 AND AttendanceDate = $2
		ORDER BY Version DESC
		LIMIT 1;
	`, classroomID, date).Scan(
		&approval.BatchID, &approval.Version, &approval.WorkflowState,
		&approval.ApprovedBy, &approval.ApprovedAt,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.AttendanceApproval{}, err
	}

	latestStatuses := map[string]string{}
	if approval.BatchID != 0 {
		rows, queryErr := s.db.QueryContext(ctx, `
			SELECT UserID, Status
			FROM AttendanceBatchEntries
			WHERE AttendanceBatchID = $1;
		`, approval.BatchID)
		if queryErr != nil {
			return domain.AttendanceApproval{}, queryErr
		}
		for rows.Next() {
			var studentID, status string
			if scanErr := rows.Scan(&studentID, &status); scanErr != nil {
				rows.Close()
				return domain.AttendanceApproval{}, scanErr
			}
			latestStatuses[studentID] = status
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return domain.AttendanceApproval{}, rowsErr
		}
		rows.Close()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT u.UserID, u.Name, am.AttendanceMarkID,
		       CASE WHEN am.Status = 'present' THEN true ELSE false END
		FROM ClassroomMemberships cm
		JOIN Users u ON u.UserID = cm.UserID
		LEFT JOIN AttendanceMarks am
		  ON am.UserID = u.UserID
		 AND am.ClassroomID = cm.ClassroomID
		 AND am.AttendanceDate = $2
		WHERE cm.ClassroomID = $1
		  AND cm.MembershipRole = 'student'
		  AND cm.Active = true
		ORDER BY u.Name, u.UserID;
	`, classroomID, date)
	if err != nil {
		return domain.AttendanceApproval{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var student domain.AttendanceApprovalStudent
		var markID sql.NullInt64
		if err := rows.Scan(&student.UserID, &student.Name, &markID, &student.CheckedIn); err != nil {
			return domain.AttendanceApproval{}, err
		}
		if markID.Valid {
			value := markID.Int64
			student.AttendanceMarkID = &value
		}
		student.Status = "absent"
		if student.CheckedIn {
			student.Status = "present"
		}
		if latestStatus, ok := latestStatuses[student.UserID]; ok {
			student.Status = latestStatus
		}
		approval.Students = append(approval.Students, student)
	}
	return approval, rows.Err()
}

// ApproveAttendance creates a complete, versioned class snapshot in one
// serializable transaction. It never edits check-ins or reward transactions.
func (s *SQLStore) ApproveAttendance(ctx context.Context, request domain.AttendanceApprovalRequest) (domain.AttendanceBatch, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return domain.AttendanceBatch{}, err
	}
	defer tx.Rollback()

	var classroomName string
	err = tx.QueryRowContext(ctx, `
		SELECT c.Name
		FROM Classrooms c
		WHERE c.ID = $3
		  AND ($2 = 'admin' OR EXISTS (
				SELECT 1 FROM ClassroomMemberships cm
				WHERE cm.ClassroomID = c.ID
				  AND cm.UserID = $1
				  AND cm.MembershipRole = 'teacher'
				  AND cm.Active = true
		  ))
		FOR UPDATE;
	`, request.ActorUserID, request.ActorRole, request.ClassroomID).Scan(&classroomName)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AttendanceBatch{}, ErrAttendanceClassForbidden
	}
	if err != nil {
		return domain.AttendanceBatch{}, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT u.UserID, am.AttendanceMarkID
		FROM ClassroomMemberships cm
		JOIN Users u ON u.UserID = cm.UserID
		LEFT JOIN AttendanceMarks am
		  ON am.UserID = u.UserID
		 AND am.ClassroomID = cm.ClassroomID
		 AND am.AttendanceDate = $2
		WHERE cm.ClassroomID = $1
		  AND cm.MembershipRole = 'student'
		  AND cm.Active = true
		ORDER BY u.UserID;
	`, request.ClassroomID, request.Date)
	if err != nil {
		return domain.AttendanceBatch{}, err
	}

	entries := []domain.AttendanceBatchEntry{}
	roster := map[string]struct{}{}
	for rows.Next() {
		var userID string
		var markID sql.NullInt64
		if err := rows.Scan(&userID, &markID); err != nil {
			rows.Close()
			return domain.AttendanceBatch{}, err
		}
		roster[userID] = struct{}{}
		status, ok := request.Statuses[userID]
		if !ok {
			rows.Close()
			return domain.AttendanceBatch{}, ErrAttendanceRosterChanged
		}
		if status != "present" && status != "absent" {
			rows.Close()
			return domain.AttendanceBatch{}, ErrInvalidAttendanceStatus
		}
		entry := domain.AttendanceBatchEntry{UserID: userID, Status: status, DeliveryState: "pending"}
		if markID.Valid {
			value := markID.Int64
			entry.AttendanceMarkID = &value
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.AttendanceBatch{}, err
	}
	rows.Close()
	if len(roster) != len(request.Statuses) {
		return domain.AttendanceBatch{}, ErrAttendanceRosterChanged
	}
	if len(entries) == 0 {
		return domain.AttendanceBatch{}, ErrAttendanceRosterChanged
	}

	var version int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(Version), 0) + 1
		FROM AttendanceBatches
		WHERE ClassroomID = $1 AND AttendanceDate = $2;
	`, request.ClassroomID, request.Date).Scan(&version); err != nil {
		return domain.AttendanceBatch{}, err
	}
	state := "approved"
	eventType := "attendance.approved"
	if version > 1 {
		state = "correction_pending"
		eventType = "attendance.correction_created"
	}
	var destinationID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT IntegrationConnectionID
		FROM IntegrationConnections
		WHERE ConnectionRole = 'attendance_destination' AND Status = 'active'
		ORDER BY UpdatedAt DESC, IntegrationConnectionID
		LIMIT 1;
	`).Scan(&destinationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.AttendanceBatch{}, err
	}
	if destinationID.Valid && version == 1 {
		state = "export_pending"
	}

	var batchID int64
	var approvedAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO AttendanceBatches
			(ClassroomID, AttendanceDate, Version, WorkflowState, ApprovedBy, ApprovedAt,
			 DestinationConnectionID)
		VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP, $6)
		RETURNING AttendanceBatchID, ApprovedAt;
	`, request.ClassroomID, request.Date, version, state, request.ActorUserID, destinationID).Scan(&batchID, &approvedAt)
	if err != nil {
		return domain.AttendanceBatch{}, err
	}
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO AttendanceBatchEntries
				(AttendanceBatchID, UserID, Status, AttendanceMarkID, DeliveryState)
			VALUES ($1, $2, $3, $4, 'pending');
		`, batchID, entry.UserID, entry.Status, entry.AttendanceMarkID); err != nil {
			return domain.AttendanceBatch{}, err
		}
	}

	metadata, _ := json.Marshal(map[string]any{
		"classroom_name": classroomName,
		"date":           request.Date.Format("2006-01-02"),
		"version":        version,
		"student_count":  len(entries),
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO IntegrationAuditEvents
			(ActorUserID, EventType, EntityType, EntityID, Metadata, OccurredAt)
		VALUES ($1, $2, 'attendance_batch', $3, $4, CURRENT_TIMESTAMP);
	`, request.ActorUserID, eventType, fmt.Sprint(batchID), metadata); err != nil {
		return domain.AttendanceBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AttendanceBatch{}, err
	}

	log.Printf("attendance class approved: actor_user_id=%q actor_role=%q classroom_id=%q date=%s batch_id=%d version=%d state=%q students=%d",
		request.ActorUserID, request.ActorRole, request.ClassroomID, request.Date.Format("2006-01-02"),
		batchID, version, state, len(entries))
	return domain.AttendanceBatch{
		ID:             batchID,
		ClassroomID:    request.ClassroomID,
		AttendanceDate: request.Date,
		Version:        version,
		WorkflowState:  state,
		ApprovedBy:     request.ActorUserID,
		ApprovedAt:     &approvedAt,
		Entries:        entries,
	}, nil
}
