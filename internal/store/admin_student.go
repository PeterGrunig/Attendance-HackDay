package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

// ErrClassroomNotFound means a write referenced a classroom ID that is not in PostgreSQL.
var ErrClassroomNotFound = errors.New("classroom not found")

// ErrUserAlreadyExists means a requested user ID is already stored in PostgreSQL.
var ErrUserAlreadyExists = errors.New("user already exists")

// SQLStore owns PostgreSQL data access for the application's persistent flows.
type SQLStore struct {
	db               *sql.DB
	credentialCipher integrations.CredentialCipher
}

type SQLStoreOption func(*SQLStore)

func WithCredentialCipher(cipher integrations.CredentialCipher) SQLStoreOption {
	return func(store *SQLStore) { store.credentialCipher = cipher }
}

// NewSQLStore wraps an existing database handle without taking ownership of
// closing it and accepts optional integration security configuration.
func NewSQLStore(db *sql.DB, options ...SQLStoreOption) *SQLStore {
	store := &SQLStore{db: db}
	for _, option := range options {
		option(store)
	}
	return store
}

// ListClassrooms rebuilds classroom rosters from normalized active memberships
// while retaining one primary teacher for compatibility with existing pages.
func (s *SQLStore) ListClassrooms(ctx context.Context) ([]domain.Classroom, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.ID, c.Name, COALESCE(cm.UserID, ''), COALESCE(cm.MembershipRole, ''),
			COALESCE(cm.IsPrimary, false), COALESCE((
				SELECT ic.ProviderKind
				FROM ExternalEntityMappings eem
				JOIN IntegrationConnections ic
					ON ic.IntegrationConnectionID = eem.IntegrationConnectionID
				WHERE eem.EntityKind = 'classroom' AND eem.LocalID = c.ID AND eem.Active = true
				ORDER BY eem.UpdatedAt DESC LIMIT 1
			), '')
		FROM Classrooms AS c
		LEFT JOIN ClassroomMemberships AS cm
			ON cm.ClassroomID = c.ID AND cm.Active = true
		ORDER BY c.ID, cm.MembershipRole, cm.IsPrimary DESC, cm.UserID;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	classrooms := []domain.Classroom{}
	classroomIndexes := map[string]int{}
	for rows.Next() {
		var (
			classroomID string
			name        string
			userID      string
			role        string
			isPrimary   bool
			managedBy   string
		)
		if err := rows.Scan(&classroomID, &name, &userID, &role, &isPrimary, &managedBy); err != nil {
			return nil, err
		}

		index, ok := classroomIndexes[classroomID]
		if !ok {
			classrooms = append(classrooms, domain.Classroom{
				ID:        classroomID,
				Name:      name,
				ManagedBy: managedBy,
			})
			index = len(classrooms) - 1
			classroomIndexes[classroomID] = index
		}

		switch role {
		case "student":
			if userID != "" {
				classrooms[index].StudentIDs = append(classrooms[index].StudentIDs, userID)
			}
		case "teacher":
			if userID != "" {
				classrooms[index].TeacherIDs = append(classrooms[index].TeacherIDs, userID)
				if classrooms[index].TeacherID == "" || isPrimary {
					classrooms[index].TeacherID = userID
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return classrooms, nil
}

// ListClassroomUsers returns users that can be displayed in classroom roster views.
func (s *SQLStore) ListClassroomUsers(ctx context.Context) (map[string]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			UserID,
			Name,
			Role,
			Email,
			COALESCE(ClassroomID, '')
		FROM Users
		ORDER BY UserID;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := map[string]domain.User{}
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.UserID, &user.Name, &user.Role, &user.Email, &user.ClassroomID); err != nil {
			return nil, err
		}
		users[user.UserID] = user
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

// CreateStudent inserts a student and classroom assignment in one SQL transaction.
func (s *SQLStore) CreateStudent(ctx context.Context, student domain.User) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := requireClassroom(ctx, tx, student.ClassroomID); err != nil {
		return err
	}
	if err := requireNewUser(ctx, tx, student.UserID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO Users (UserID, Name, Role, Email, PasswordHash, ClassroomID)
		VALUES ($1, $2, $3, $4, $5, $6);
	`, student.UserID, student.Name, student.Role, student.Email, student.PasswordHash, student.ClassroomID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ClassroomStudents (ClassroomID, StudentID)
		VALUES ($1, $2)
		ON CONFLICT (ClassroomID, StudentID) DO NOTHING;
	`, student.ClassroomID, student.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ClassroomMemberships
			(ClassroomID, UserID, MembershipRole, IsPrimary, Active, Source, UpdatedAt)
		VALUES ($1, $2, 'student', true, true, 'local', CURRENT_TIMESTAMP)
		ON CONFLICT (ClassroomID, UserID, MembershipRole) DO UPDATE SET
			IsPrimary = EXCLUDED.IsPrimary,
			Active = true,
			UpdatedAt = CURRENT_TIMESTAMP;
	`, student.ClassroomID, student.UserID); err != nil {
		return err
	}

	return tx.Commit()
}

func requireClassroom(ctx context.Context, tx *sql.Tx, classroomID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM Classrooms
		WHERE ID = $1;
	`, classroomID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrClassroomNotFound
	}
	return err
}

func requireNewUser(ctx context.Context, tx *sql.Tx, userID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM Users
		WHERE UserID = $1;
	`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrUserAlreadyExists
}
