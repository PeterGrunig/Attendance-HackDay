package store

import (
	"context"
	"database/sql"
	"log"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

// CreateTeacher inserts a teacher account created from the admin tools.
func (s *SQLStore) CreateTeacher(ctx context.Context, teacher domain.User) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := requireNewUser(ctx, tx, teacher.UserID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO Users (UserID, Name, Role, Email, PasswordHash, ClassroomID)
		VALUES ($1, $2, $3, $4, $5, $6);
	`, teacher.UserID, teacher.Name, teacher.Role, teacher.Email, teacher.PasswordHash, teacher.ClassroomID); err != nil {
		return err
	}

	return tx.Commit()
}

// CreateClassroom stores a new classroom and its initial student assignments from the admin tools.
func (s *SQLStore) CreateClassroom(ctx context.Context, classroom domain.Classroom) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("Error starting transaction:", err)
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO Classrooms (ID, Name, TeacherID)
		VALUES ($1, $2, $3);
	`, classroom.ID, classroom.Name, classroom.TeacherID); err != nil {
		log.Println("Error inserting classroom:", err)
		return err
	}

	if err := replaceClassroomMemberships(ctx, tx, classroom); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateClassroom saves edits to an existing classroom and replaces its student assignments.
func (s *SQLStore) UpdateClassroom(ctx context.Context, originalID string, classroom domain.Classroom) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("Error starting transaction:", err)
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE Classrooms
		SET ID = $2, Name = $3, TeacherID = $4
		WHERE ID = $1;
	`, originalID, classroom.ID, classroom.Name, classroom.TeacherID)
	if err != nil {
		log.Println("Error updating classroom:", err)
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrClassroomNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM ClassroomStudents
		WHERE ClassroomID = $1 OR ClassroomID = $2;
	`, originalID, classroom.ID); err != nil {
		log.Println("Error clearing classroom students:", err)
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM ClassroomMemberships
		WHERE ClassroomID = $1 OR ClassroomID = $2;
	`, originalID, classroom.ID); err != nil {
		log.Println("Error clearing classroom memberships:", err)
		return err
	}

	if err := replaceClassroomMemberships(ctx, tx, classroom); err != nil {
		return err
	}

	return tx.Commit()
}

// replaceClassroomMemberships dual-writes normalized memberships and legacy
// roster rows so current pages remain compatible throughout the migration.
func replaceClassroomMemberships(ctx context.Context, tx *sql.Tx, classroom domain.Classroom) error {
	teacherIDs := classroom.TeacherIDs
	if len(teacherIDs) == 0 && classroom.TeacherID != "" {
		teacherIDs = []string{classroom.TeacherID}
	}
	for index, teacherID := range teacherIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ClassroomMemberships
				(ClassroomID, UserID, MembershipRole, IsPrimary, Active, Source, UpdatedAt)
			VALUES ($1, $2, 'teacher', $3, true, 'local', CURRENT_TIMESTAMP)
			ON CONFLICT (ClassroomID, UserID, MembershipRole) DO UPDATE SET
				IsPrimary = EXCLUDED.IsPrimary,
				Active = true,
				UpdatedAt = CURRENT_TIMESTAMP;
		`, classroom.ID, teacherID, index == 0); err != nil {
			log.Println("Error inserting classroom teacher membership:", err)
			return err
		}
	}

	if err := insertClassroomStudents(ctx, tx, classroom.ID, classroom.StudentIDs); err != nil {
		return err
	}
	for _, studentID := range classroom.StudentIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ClassroomMemberships
				(ClassroomID, UserID, MembershipRole, IsPrimary, Active, Source, UpdatedAt)
			VALUES ($1, $2, 'student', COALESCE((
				SELECT COALESCE(NULLIF(ClassroomID, ''), $1) = $1
				FROM Users WHERE UserID = $2
			), false), true, 'local', CURRENT_TIMESTAMP)
			ON CONFLICT (ClassroomID, UserID, MembershipRole) DO UPDATE SET
				IsPrimary = EXCLUDED.IsPrimary,
				Active = true,
				UpdatedAt = CURRENT_TIMESTAMP;
		`, classroom.ID, studentID); err != nil {
			log.Println("Error inserting classroom student membership:", err)
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE Users SET ClassroomID = $2
			WHERE UserID = $1 AND COALESCE(ClassroomID, '') = '';
		`, studentID, classroom.ID); err != nil {
			return err
		}
	}
	return nil
}

func insertClassroomStudents(ctx context.Context, tx *sql.Tx, classroomID string, studentIDs []string) error {
	for _, studentID := range studentIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ClassroomStudents (ClassroomID, StudentID)
			VALUES ($1, $2)
			ON CONFLICT (ClassroomID, StudentID) DO NOTHING;
		`, classroomID, studentID); err != nil {
			log.Println("Error inserting classroom student:", err)
			return err
		}
	}

	return nil
}
