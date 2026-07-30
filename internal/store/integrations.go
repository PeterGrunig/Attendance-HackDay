package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

// CreateIntegrationConnection encrypts credentials before any database call,
// ensuring provider secrets can never be persisted in plaintext.
func (s *SQLStore) CreateIntegrationConnection(ctx context.Context, connection integrations.Connection, status string) (int64, error) {
	configuration := connection.Configuration
	if len(configuration) == 0 {
		configuration = json.RawMessage(`{}`)
	}
	if !json.Valid(configuration) {
		return 0, fmt.Errorf("%w: connection configuration must be valid JSON", integrations.ErrInvalidConfiguration)
	}

	var encrypted integrations.EncryptedCredentials
	if len(connection.Credentials) > 0 {
		if s.credentialCipher == nil {
			log.Printf("integration connection %q not stored: credential encryption unavailable", connection.DisplayName)
			return 0, integrations.ErrEncryptionUnavailable
		}
		var err error
		encrypted, err = s.credentialCipher.Encrypt(connection.Credentials)
		if err != nil {
			log.Printf("integration connection %q credential encryption failed: %v", connection.DisplayName, err)
			return 0, err
		}
	}

	var connectionID int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO IntegrationConnections
			(ProviderKind, ConnectionRole, DisplayName, Status, Configuration,
			 CredentialCiphertext, CredentialNonce, EncryptionVersion)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING IntegrationConnectionID;
	`, connection.ProviderKind, connection.Role, connection.DisplayName, status, configuration,
		nullBytes(encrypted.Ciphertext), nullBytes(encrypted.Nonce), nullEncryptionVersion(encrypted.Version)).Scan(&connectionID)
	if err != nil {
		log.Printf("store integration connection %q: %v", connection.DisplayName, err)
		return 0, err
	}
	return connectionID, nil
}

// LoadIntegrationConnection decrypts credentials only for adapter runtime use;
// a missing or incorrect key leaves the stored ciphertext untouched.
func (s *SQLStore) LoadIntegrationConnection(ctx context.Context, connectionID int64) (integrations.Connection, string, error) {
	var connection integrations.Connection
	var status string
	var configuration, ciphertext, nonce []byte
	var version sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT IntegrationConnectionID, ProviderKind, ConnectionRole, DisplayName,
			Status, Configuration, CredentialCiphertext, CredentialNonce, EncryptionVersion
		FROM IntegrationConnections WHERE IntegrationConnectionID = $1;
	`, connectionID).Scan(&connection.ID, &connection.ProviderKind, &connection.Role,
		&connection.DisplayName, &status, &configuration, &ciphertext, &nonce, &version)
	if err != nil {
		return integrations.Connection{}, "", err
	}
	connection.Configuration = append(json.RawMessage(nil), configuration...)
	if len(ciphertext) == 0 {
		return connection, status, nil
	}
	if s.credentialCipher == nil {
		log.Printf("integration connection %d credentials unavailable: encryption key not configured", connectionID)
		return integrations.Connection{}, "", integrations.ErrEncryptionUnavailable
	}
	plaintext, err := s.credentialCipher.Decrypt(integrations.EncryptedCredentials{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		Version:    int(version.Int64),
	})
	if err != nil {
		log.Printf("integration connection %d credential decryption failed: %v", connectionID, err)
		return integrations.Connection{}, "", err
	}
	connection.Credentials = append(json.RawMessage(nil), plaintext...)
	return connection, status, nil
}

func (s *SQLStore) ListIntegrationConnections(ctx context.Context, providerKind string) ([]domain.IntegrationConnectionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT IntegrationConnectionID, ProviderKind, ConnectionRole, DisplayName, Status, Configuration, UpdatedAt
		FROM IntegrationConnections
		WHERE ProviderKind = $1 AND ConnectionRole = 'roster_source'
		ORDER BY IntegrationConnectionID;
	`, providerKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.IntegrationConnectionSummary{}
	for rows.Next() {
		var item domain.IntegrationConnectionSummary
		if err := rows.Scan(&item.ID, &item.ProviderKind, &item.ConnectionRole, &item.DisplayName, &item.Status, &item.Configuration, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpdateIntegrationConnection re-encrypts a complete credential document with
// a fresh nonce and never touches existing ciphertext if encryption fails.
func (s *SQLStore) UpdateIntegrationConnection(ctx context.Context, connection integrations.Connection, status string) error {
	if s.credentialCipher == nil {
		return integrations.ErrEncryptionUnavailable
	}
	encrypted, err := s.credentialCipher.Encrypt(connection.Credentials)
	if err != nil {
		return err
	}
	configuration := connection.Configuration
	if len(configuration) == 0 || !json.Valid(configuration) {
		return fmt.Errorf("%w: connection configuration must be valid JSON", integrations.ErrInvalidConfiguration)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE IntegrationConnections
		SET DisplayName = $2, Status = $3, Configuration = $4,
			CredentialCiphertext = $5, CredentialNonce = $6, EncryptionVersion = $7,
			UpdatedAt = CURRENT_TIMESTAMP
		WHERE IntegrationConnectionID = $1;
	`, connection.ID, connection.DisplayName, status, configuration,
		encrypted.Ciphertext, encrypted.Nonce, encrypted.Version)
	return err
}

func (s *SQLStore) DisableIntegrationConnection(ctx context.Context, connectionID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE IntegrationConnections
		SET Status = 'disabled', CredentialCiphertext = NULL, CredentialNonce = NULL,
			EncryptionVersion = NULL, UpdatedAt = CURRENT_TIMESTAMP
		WHERE IntegrationConnectionID = $1;
	`, connectionID)
	return err
}

func (s *SQLStore) LoadRosterMatchSnapshot(ctx context.Context, connectionID int64) (domain.RosterMatchSnapshot, error) {
	var snapshot domain.RosterMatchSnapshot
	rows, err := s.db.QueryContext(ctx, `
		SELECT ExternalEntityMappingID, IntegrationConnectionID, EntityKind, ExternalID,
			LocalID, COALESCE(SISID, ''), Active, LastSeenAt
		FROM ExternalEntityMappings
		WHERE IntegrationConnectionID = $1 OR COALESCE(SISID, '') <> '';
	`, connectionID)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var mapping domain.ExternalEntityMapping
		if err := rows.Scan(&mapping.ID, &mapping.ConnectionID, &mapping.EntityKind,
			&mapping.ExternalID, &mapping.LocalID, &mapping.SISID, &mapping.Active,
			&mapping.LastSeenAt); err != nil {
			rows.Close()
			return snapshot, err
		}
		snapshot.Mappings = append(snapshot.Mappings, mapping)
	}
	if err := rows.Close(); err != nil {
		return snapshot, err
	}

	userRows, err := s.db.QueryContext(ctx, `
		SELECT UserID, Name, Role, Email, COALESCE(ClassroomID, '')
		FROM Users ORDER BY UserID;
	`)
	if err != nil {
		return snapshot, err
	}
	defer userRows.Close()
	for userRows.Next() {
		var user domain.User
		if err := userRows.Scan(&user.UserID, &user.Name, &user.Role, &user.Email, &user.ClassroomID); err != nil {
			return snapshot, err
		}
		snapshot.Users = append(snapshot.Users, user)
	}
	return snapshot, userRows.Err()
}

// ApplyRosterImport atomically materializes a confirmed normalized roster,
// dual-writes compatibility rows, and archives provider-managed memberships
// missing from the newly confirmed Canvas snapshot.
func (s *SQLStore) ApplyRosterImport(ctx context.Context, proposal domain.RosterImportProposal, decisions map[string]string, actorUserID string) (domain.RosterImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RosterImportResult{}, err
	}
	defer tx.Rollback()

	result := domain.RosterImportResult{}
	now := time.Now()
	schoolIDs := map[string]string{}
	for _, school := range proposal.Schools {
		localID := stableImportedID("canvas-school", proposal.ConnectionID, school.ExternalID)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO Schools (ID, Name, Active, UpdatedAt)
			VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
			ON CONFLICT (ID) DO UPDATE SET Name = EXCLUDED.Name, Active = EXCLUDED.Active, UpdatedAt = CURRENT_TIMESTAMP;
		`, localID, school.Name, school.Active); err != nil {
			return result, err
		}
		schoolIDs[school.ExternalID] = localID
		if err := upsertMappingTx(ctx, tx, proposal.ConnectionID, "school", school.ExternalID, localID, school.SISID, school.Active, now); err != nil {
			return result, err
		}
	}

	classIDs := map[string]string{}
	for _, class := range proposal.Classes {
		localID, mapped, err := mappedLocalIDTx(ctx, tx, proposal.ConnectionID, "classroom", class.ExternalID)
		if err != nil {
			return result, err
		}
		if !mapped && class.SISID != "" {
			localID, mapped, err = mappedLocalIDBySISTx(ctx, tx, "classroom", class.SISID)
			if err != nil {
				return result, err
			}
		}
		if !mapped {
			localID = stableImportedID("canvas-class", proposal.ConnectionID, class.ExternalID)
			result.ClassesCreated++
		}
		schoolID := schoolIDs[class.SchoolExternalID]
		if schoolID == "" {
			schoolID = "local-default"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO Classrooms (ID, Name, TeacherID, SchoolID)
			VALUES ($1, $2, '', $3)
			ON CONFLICT (ID) DO UPDATE SET Name = EXCLUDED.Name, SchoolID = EXCLUDED.SchoolID;
		`, localID, class.Name, schoolID); err != nil {
			return result, err
		}
		classIDs[class.ExternalID] = localID
		if err := upsertMappingTx(ctx, tx, proposal.ConnectionID, "classroom", class.ExternalID, localID, class.SISID, class.Active, now); err != nil {
			return result, err
		}
	}

	personIDs := map[string]string{}
	for _, item := range proposal.People {
		localID := item.LocalID
		if !item.AutoMatched {
			if decision := strings.TrimSpace(decisions[item.Person.ExternalID]); decision != "" && decision != "new" {
				localID = decision
			}
		}
		if localID == "" {
			localID = stableImportedID("canvas-user", proposal.ConnectionID, item.Person.ExternalID)
			role := string(item.Person.Role)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO Users (UserID, Name, Role, Email, PasswordHash, ClassroomID)
				VALUES ($1, $2, $3, $4, '!canvas-pending', '')
				ON CONFLICT (UserID) DO NOTHING;
			`, localID, item.Person.Name, role, item.Person.Email); err != nil {
				return result, err
			}
			result.UsersCreated++
		}
		personIDs[item.Person.ExternalID] = localID
		if err := upsertMappingTx(ctx, tx, proposal.ConnectionID, "user", item.Person.ExternalID, localID, item.Person.SISID, item.Person.Active, now); err != nil {
			return result, err
		}
	}

	for _, localClassID := range classIDs {
		tag, err := tx.ExecContext(ctx, `
			UPDATE ClassroomMemberships
			SET Active = false, UpdatedAt = CURRENT_TIMESTAMP
			WHERE ClassroomID = $1 AND Source = 'imported' AND Active = true;
		`, localClassID)
		if err != nil {
			return result, err
		}
		count, _ := tag.RowsAffected()
		result.MembershipsArchived += int(count)
	}

	for _, membership := range proposal.Memberships {
		if !membership.Active {
			continue
		}
		classID, userID := classIDs[membership.ClassExternalID], personIDs[membership.PersonExternalID]
		if classID == "" || userID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ClassroomMemberships
				(ClassroomID, UserID, MembershipRole, IsPrimary, Active, Source, UpdatedAt)
			VALUES ($1, $2, $3, $4, true, 'imported', CURRENT_TIMESTAMP)
			ON CONFLICT (ClassroomID, UserID, MembershipRole) DO UPDATE SET
				IsPrimary = EXCLUDED.IsPrimary, Active = true, Source = 'imported', UpdatedAt = CURRENT_TIMESTAMP;
		`, classID, userID, membership.Role, membership.Primary); err != nil {
			return result, err
		}
		result.MembershipsImported++
		if membership.Role == integrations.PersonRoleStudent {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ClassroomStudents (ClassroomID, StudentID) VALUES ($1, $2)
				ON CONFLICT (ClassroomID, StudentID) DO NOTHING;
				UPDATE Users SET ClassroomID = $1
				WHERE UserID = $2 AND COALESCE(ClassroomID, '') = '';
			`, classID, userID); err != nil {
				return result, err
			}
		} else if membership.Role == integrations.PersonRoleTeacher {
			if _, err := tx.ExecContext(ctx, `
				UPDATE Classrooms SET TeacherID = $2
				WHERE ID = $1 AND COALESCE(TeacherID, '') = '';
			`, classID, userID); err != nil {
				return result, err
			}
		}
	}
	result.MembershipsArchived -= result.MembershipsImported
	if result.MembershipsArchived < 0 {
		result.MembershipsArchived = 0
	}

	metadata, _ := json.Marshal(result)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO IntegrationAuditEvents (ActorUserID, EventType, EntityType, EntityID, Metadata, OccurredAt)
		VALUES ($1, 'roster.imported', 'integration_connection', $2, $3, CURRENT_TIMESTAMP);
	`, actorUserID, strconv.FormatInt(proposal.ConnectionID, 10), metadata); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func stableImportedID(prefix string, connectionID int64, externalID string) string {
	sum := sha256.Sum256([]byte(externalID))
	return fmt.Sprintf("%s-%d-%x", prefix, connectionID, sum[:6])
}

func mappedLocalIDTx(ctx context.Context, tx *sql.Tx, connectionID int64, kind, externalID string) (string, bool, error) {
	var localID string
	err := tx.QueryRowContext(ctx, `
		SELECT LocalID FROM ExternalEntityMappings
		WHERE IntegrationConnectionID = $1 AND EntityKind = $2 AND ExternalID = $3;
	`, connectionID, kind, externalID).Scan(&localID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return localID, err == nil, err
}

func mappedLocalIDBySISTx(ctx context.Context, tx *sql.Tx, kind, sisID string) (string, bool, error) {
	var localID string
	err := tx.QueryRowContext(ctx, `
		SELECT LocalID FROM ExternalEntityMappings
		WHERE EntityKind = $1 AND LOWER(SISID) = LOWER($2) AND Active = true
		ORDER BY UpdatedAt DESC LIMIT 1;
	`, kind, sisID).Scan(&localID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return localID, err == nil, err
}

func upsertMappingTx(ctx context.Context, tx *sql.Tx, connectionID int64, kind, externalID, localID, sisID string, active bool, seenAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO ExternalEntityMappings
			(IntegrationConnectionID, EntityKind, ExternalID, LocalID, SISID, Active, LastSeenAt, UpdatedAt)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, CURRENT_TIMESTAMP)
		ON CONFLICT (IntegrationConnectionID, EntityKind, ExternalID) DO UPDATE SET
			LocalID = EXCLUDED.LocalID, SISID = EXCLUDED.SISID, Active = EXCLUDED.Active,
			LastSeenAt = EXCLUDED.LastSeenAt, UpdatedAt = CURRENT_TIMESTAMP;
	`, connectionID, kind, externalID, localID, sisID, active, seenAt)
	return err
}

func (s *SQLStore) UpsertExternalEntityMapping(ctx context.Context, mapping domain.ExternalEntityMapping) (int64, error) {
	var mappingID int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO ExternalEntityMappings
			(IntegrationConnectionID, EntityKind, ExternalID, LocalID, SISID, Active, LastSeenAt, UpdatedAt)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, CURRENT_TIMESTAMP)
		ON CONFLICT (IntegrationConnectionID, EntityKind, ExternalID) DO UPDATE SET
			LocalID = EXCLUDED.LocalID, SISID = EXCLUDED.SISID,
			Active = EXCLUDED.Active, LastSeenAt = EXCLUDED.LastSeenAt,
			UpdatedAt = CURRENT_TIMESTAMP
		RETURNING ExternalEntityMappingID;
	`, mapping.ConnectionID, mapping.EntityKind, mapping.ExternalID, mapping.LocalID,
		mapping.SISID, mapping.Active, mapping.LastSeenAt).Scan(&mappingID)
	return mappingID, err
}

// CreateAttendanceBatch stores an approved roster snapshot atomically so an
// exporter can never observe a batch without all of its student entries.
func (s *SQLStore) CreateAttendanceBatch(ctx context.Context, batch domain.AttendanceBatch) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var batchID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO AttendanceBatches
			(ClassroomID, AttendanceDate, Version, WorkflowState, ApprovedBy,
			 ApprovedAt, DestinationConnectionID)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
		RETURNING AttendanceBatchID;
	`, batch.ClassroomID, batch.AttendanceDate, batch.Version, batch.WorkflowState,
		batch.ApprovedBy, batch.ApprovedAt, batch.DestinationConnectionID).Scan(&batchID)
	if err != nil {
		return 0, err
	}
	for _, entry := range batch.Entries {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO AttendanceBatchEntries
				(AttendanceBatchID, UserID, Status, AttendanceMarkID, DeliveryState,
				 ExternalRecordID, LastError)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''));
		`, batchID, entry.UserID, entry.Status, entry.AttendanceMarkID,
			entry.DeliveryState, entry.ExternalRecordID, entry.LastError); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return batchID, nil
}

func (s *SQLStore) RecordAttendanceDeliveryAttempt(ctx context.Context, attempt domain.AttendanceDeliveryAttempt) (int64, error) {
	var attemptID int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO AttendanceDeliveryAttempts
			(AttendanceBatchID, IntegrationConnectionID, IdempotencyKey, AttemptNumber,
			 State, ResponseCode, ErrorMessage, StartedAt, CompletedAt)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9)
		RETURNING AttendanceDeliveryAttemptID;
	`, attempt.BatchID, attempt.ConnectionID, attempt.IdempotencyKey,
		attempt.AttemptNumber, attempt.State, attempt.ResponseCode,
		attempt.ErrorMessage, attempt.StartedAt, attempt.CompletedAt).Scan(&attemptID)
	return attemptID, err
}

func (s *SQLStore) AppendIntegrationAuditEvent(ctx context.Context, event domain.IntegrationAuditEvent) (int64, error) {
	metadata := event.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return 0, fmt.Errorf("%w: audit metadata must be valid JSON", integrations.ErrInvalidConfiguration)
	}
	var eventID int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO IntegrationAuditEvents
			(ActorUserID, EventType, EntityType, EntityID, Metadata, OccurredAt)
		VALUES (NULLIF($1, ''), $2, $3, $4, $5, $6)
		RETURNING IntegrationAuditEventID;
	`, event.ActorUserID, event.EventType, event.EntityType, event.EntityID,
		metadata, event.OccurredAt).Scan(&eventID)
	return eventID, err
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullEncryptionVersion(version int) any {
	if version == 0 {
		return nil
	}
	return version
}
