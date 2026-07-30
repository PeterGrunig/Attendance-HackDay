package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

// UpsertStudentIntegrationConnection persists one owner-scoped provider login,
// replacing its encrypted credential document with a fresh nonce on reconnect.
func (s *SQLStore) UpsertStudentIntegrationConnection(ctx context.Context, userID string, connection integrations.Connection) (int64, error) {
	if connection.Role != integrations.ConnectionRoleStudentLink {
		return 0, fmt.Errorf("%w: student connection role is required", integrations.ErrInvalidConfiguration)
	}
	if s.credentialCipher == nil {
		return 0, integrations.ErrEncryptionUnavailable
	}
	if len(connection.Configuration) == 0 || !json.Valid(connection.Configuration) {
		return 0, fmt.Errorf("%w: connection configuration must be valid JSON", integrations.ErrInvalidConfiguration)
	}
	if len(connection.Credentials) == 0 {
		return 0, fmt.Errorf("%w: student provider credentials are required", integrations.ErrInvalidConfiguration)
	}
	encrypted, err := s.credentialCipher.Encrypt(connection.Credentials)
	if err != nil {
		return 0, err
	}

	var connectionID int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO IntegrationConnections
			(ProviderKind, ConnectionRole, DisplayName, Status, Configuration,
			 CredentialCiphertext, CredentialNonce, EncryptionVersion, OwnerUserID)
		VALUES ($1, 'student_link', $2, 'active', $3, $4, $5, $6, $7)
		ON CONFLICT (OwnerUserID, ProviderKind) WHERE ConnectionRole = 'student_link'
		DO UPDATE SET
			DisplayName = EXCLUDED.DisplayName,
			Status = 'active',
			Configuration = EXCLUDED.Configuration,
			CredentialCiphertext = EXCLUDED.CredentialCiphertext,
			CredentialNonce = EXCLUDED.CredentialNonce,
			EncryptionVersion = EXCLUDED.EncryptionVersion,
			UpdatedAt = CURRENT_TIMESTAMP
		RETURNING IntegrationConnectionID;
	`, connection.ProviderKind, connection.DisplayName, connection.Configuration,
		encrypted.Ciphertext, encrypted.Nonce, encrypted.Version, userID).Scan(&connectionID)
	if err != nil {
		log.Printf("student provider connection persistence failed: provider=%q user_id=%q error=%v",
			connection.ProviderKind, userID, err)
		return 0, err
	}
	return connectionID, nil
}

func (s *SQLStore) LoadStudentIntegrationConnection(ctx context.Context, userID, providerKind string) (integrations.Connection, string, error) {
	var connectionID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT IntegrationConnectionID
		FROM IntegrationConnections
		WHERE OwnerUserID = $1
		  AND ProviderKind = $2
		  AND ConnectionRole = 'student_link';
	`, userID, providerKind).Scan(&connectionID)
	if err != nil {
		return integrations.Connection{}, "", err
	}
	return s.LoadIntegrationConnection(ctx, connectionID)
}

func (s *SQLStore) LoadStudentIntegrationSummary(ctx context.Context, userID, providerKind string) (domain.IntegrationConnectionSummary, error) {
	var summary domain.IntegrationConnectionSummary
	err := s.db.QueryRowContext(ctx, `
		SELECT IntegrationConnectionID, ProviderKind, ConnectionRole, DisplayName,
			Status, Configuration, UpdatedAt
		FROM IntegrationConnections
		WHERE OwnerUserID = $1
		  AND ProviderKind = $2
		  AND ConnectionRole = 'student_link';
	`, userID, providerKind).Scan(
		&summary.ID, &summary.ProviderKind, &summary.ConnectionRole,
		&summary.DisplayName, &summary.Status, &summary.Configuration, &summary.UpdatedAt,
	)
	return summary, err
}

func (s *SQLStore) DisableStudentIntegrationConnection(ctx context.Context, userID, providerKind string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE IntegrationConnections
		SET Status = 'disabled',
			CredentialCiphertext = NULL,
			CredentialNonce = NULL,
			EncryptionVersion = NULL,
			UpdatedAt = CURRENT_TIMESTAMP
		WHERE OwnerUserID = $1
		  AND ProviderKind = $2
		  AND ConnectionRole = 'student_link';
	`, userID, providerKind)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	return nil
}
