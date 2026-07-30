package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

// SeedAttendanceDestinationMappings reuses durable SIS IDs from any roster
// source while keeping the destination's mapping namespace independent.
func (s *SQLStore) SeedAttendanceDestinationMappings(ctx context.Context, connectionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	if err := tx.QueryRowContext(ctx, `
		SELECT ConnectionRole FROM IntegrationConnections
		WHERE IntegrationConnectionID = $1 FOR UPDATE;
	`, connectionID).Scan(&role); err != nil {
		return err
	}
	if role != "attendance_destination" {
		return fmt.Errorf("connection %d is not an attendance destination", connectionID)
	}
	for _, entityKind := range []string{"school", "classroom", "user"} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ExternalEntityMappings
				(IntegrationConnectionID, EntityKind, ExternalID, LocalID, SISID,
				 Active, LastSeenAt, UpdatedAt)
			SELECT $1, source.EntityKind, source.SISID, source.LocalID, source.SISID,
				true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
			FROM (
				SELECT DISTINCT ON (EntityKind, LocalID)
					EntityKind, LocalID, SISID
				FROM ExternalEntityMappings
				WHERE EntityKind = $2
				  AND IntegrationConnectionID <> $1
				  AND Active = true
				  AND COALESCE(SISID, '') <> ''
				ORDER BY EntityKind, LocalID, UpdatedAt DESC
			) source
			ON CONFLICT (IntegrationConnectionID, EntityKind, LocalID) DO NOTHING;
		`, connectionID, entityKind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) ListAttendanceDestinationMappings(ctx context.Context, connectionID int64) ([]domain.AttendanceDestinationMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH local_entities AS (
			SELECT 'school'::text AS EntityKind, s.ID AS LocalID, s.Name AS LocalName
			FROM Schools s WHERE s.Active = true
			UNION ALL
			SELECT 'classroom', c.ID, c.Name
			FROM Classrooms c
			UNION ALL
			SELECT DISTINCT 'user', u.UserID, u.Name
			FROM Users u
			JOIN ClassroomMemberships cm ON cm.UserID = u.UserID
			WHERE cm.MembershipRole = 'student' AND cm.Active = true
		)
		SELECT le.EntityKind, le.LocalID, le.LocalName,
			COALESCE(destination.SISID, suggestion.SISID, ''),
			COALESCE(destination.ExternalID, '')
		FROM local_entities le
		LEFT JOIN ExternalEntityMappings destination
		  ON destination.IntegrationConnectionID = $1
		 AND destination.EntityKind = le.EntityKind
		 AND destination.LocalID = le.LocalID
		 AND destination.Active = true
		LEFT JOIN LATERAL (
			SELECT source.SISID
			FROM ExternalEntityMappings source
			WHERE source.IntegrationConnectionID <> $1
			  AND source.EntityKind = le.EntityKind
			  AND source.LocalID = le.LocalID
			  AND source.Active = true
			  AND COALESCE(source.SISID, '') <> ''
			ORDER BY source.UpdatedAt DESC LIMIT 1
		) suggestion ON true
		ORDER BY CASE le.EntityKind WHEN 'school' THEN 1 WHEN 'classroom' THEN 2 ELSE 3 END,
			le.LocalName, le.LocalID;
	`, connectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mappings []domain.AttendanceDestinationMapping
	for rows.Next() {
		var mapping domain.AttendanceDestinationMapping
		if err := rows.Scan(&mapping.EntityKind, &mapping.LocalID, &mapping.LocalName,
			&mapping.SISID, &mapping.ExternalID); err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}
	return mappings, rows.Err()
}

func (s *SQLStore) SetAttendanceDestinationMapping(ctx context.Context, connectionID int64, mapping domain.AttendanceDestinationMapping) error {
	if mapping.EntityKind != "school" && mapping.EntityKind != "classroom" && mapping.EntityKind != "user" {
		return fmt.Errorf("invalid mapping entity kind %q", mapping.EntityKind)
	}
	if mapping.LocalID == "" || mapping.ExternalID == "" {
		return fmt.Errorf("local and external identifiers are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	switch mapping.EntityKind {
	case "school":
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM Schools WHERE ID = $1 AND Active = true);`, mapping.LocalID).Scan(&valid)
	case "classroom":
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM Classrooms WHERE ID = $1);`, mapping.LocalID).Scan(&valid)
	case "user":
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM ClassroomMemberships
				WHERE UserID = $1 AND MembershipRole = 'student' AND Active = true
			);
		`, mapping.LocalID).Scan(&valid)
	}
	if err != nil {
		return err
	}
	if !valid {
		return sql.ErrNoRows
	}
	var destination bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM IntegrationConnections
			WHERE IntegrationConnectionID = $1
			  AND ConnectionRole = 'attendance_destination'
		);
	`, connectionID).Scan(&destination); err != nil {
		return err
	}
	if !destination {
		return sql.ErrNoRows
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO ExternalEntityMappings
			(IntegrationConnectionID, EntityKind, ExternalID, LocalID, SISID,
			 Active, LastSeenAt, UpdatedAt)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (IntegrationConnectionID, EntityKind, LocalID) DO UPDATE SET
			ExternalID = EXCLUDED.ExternalID,
			SISID = EXCLUDED.SISID,
			Active = true,
			LastSeenAt = CURRENT_TIMESTAMP,
			UpdatedAt = CURRENT_TIMESTAMP;
	`, connectionID, mapping.EntityKind, mapping.ExternalID, mapping.LocalID, mapping.SISID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) CountMissingAttendanceDestinationMappings(ctx context.Context, connectionID int64) (int, error) {
	mappings, err := s.ListAttendanceDestinationMappings(ctx, connectionID)
	if err != nil {
		return 0, err
	}
	missing := 0
	for _, mapping := range mappings {
		if mapping.ExternalID == "" {
			missing++
		}
	}
	return missing, nil
}
