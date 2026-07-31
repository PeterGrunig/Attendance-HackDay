//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	_ "github.com/lib/pq"
)

func TestPostgresUserAndClassroomLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	createPostgresTestSchema(t, db)
	store := NewSQLStore(db)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO Classrooms (ID, Name, TeacherID) VALUES ('seed-class', 'Seed Class', '')`); err != nil {
		t.Fatalf("seed classroom: %v", err)
	}

	student := domain.User{
		UserID: "integration-student", Name: "Integration Student", Role: "student",
		Email: "integration.student@example.com", PasswordHash: "hash", ClassroomID: "seed-class",
	}
	if err := store.CreateStudent(ctx, student); err != nil {
		t.Fatalf("CreateStudent: %v", err)
	}
	loaded, err := store.FindUserByEmail(ctx, student.Email)
	if err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if loaded.UserID != student.UserID || loaded.ClassroomID != "seed-class" {
		t.Fatalf("loaded student = %#v", loaded)
	}
	if err := store.CreateStudent(ctx, student); !errors.Is(err, ErrUserAlreadyExists) {
		t.Fatalf("duplicate CreateStudent error = %v, want ErrUserAlreadyExists", err)
	}

	missing := student
	missing.UserID = "rolled-back-student"
	missing.Email = "rolled.back@example.com"
	missing.ClassroomID = "missing-class"
	if err := store.CreateStudent(ctx, missing); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("missing classroom error = %v, want ErrClassroomNotFound", err)
	}
	var rolledBackCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM Users WHERE UserID = $1`, missing.UserID).Scan(&rolledBackCount); err != nil {
		t.Fatalf("check rollback: %v", err)
	}
	if rolledBackCount != 0 {
		t.Fatal("failed CreateStudent persisted a user")
	}

	teacher := domain.User{UserID: "integration-teacher", Name: "Integration Teacher", Role: "teacher", Email: "teacher@example.com", PasswordHash: "hash"}
	if err := store.CreateTeacher(ctx, teacher); err != nil {
		t.Fatalf("CreateTeacher: %v", err)
	}
	classroom := domain.Classroom{
		ID: "integration-class", Name: "Integration Class", TeacherID: teacher.UserID,
		StudentIDs: []string{student.UserID},
	}
	if err := store.CreateClassroom(ctx, classroom); err != nil {
		t.Fatalf("CreateClassroom: %v", err)
	}
	classrooms, err := store.ListClassrooms(ctx)
	if err != nil {
		t.Fatalf("ListClassrooms: %v", err)
	}
	found := false
	for _, item := range classrooms {
		if item.ID == classroom.ID {
			found = item.TeacherID == teacher.UserID && len(item.StudentIDs) == 1 && item.StudentIDs[0] == student.UserID
		}
	}
	if !found {
		t.Fatalf("ListClassrooms did not return normalized roster: %#v", classrooms)
	}
}

func createPostgresTestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	const schema = `
		CREATE TEMP TABLE Users (
			UserID text PRIMARY KEY, Name text NOT NULL, Role text NOT NULL,
			Email text NOT NULL, PasswordHash text NOT NULL, ClassroomID text
		);
		CREATE TEMP TABLE Classrooms (
			ID text PRIMARY KEY, Name text NOT NULL, TeacherID text
		);
		CREATE TEMP TABLE ClassroomStudents (
			ClassroomID text NOT NULL, StudentID text NOT NULL,
			PRIMARY KEY (ClassroomID, StudentID)
		);
		CREATE TEMP TABLE ClassroomMemberships (
			ClassroomID text NOT NULL, UserID text NOT NULL, MembershipRole text NOT NULL,
			IsPrimary boolean NOT NULL DEFAULT false, Active boolean NOT NULL DEFAULT true,
			Source text NOT NULL DEFAULT 'local', UpdatedAt timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (ClassroomID, UserID, MembershipRole)
		);
		CREATE TEMP TABLE IntegrationConnections (
			IntegrationConnectionID bigint PRIMARY KEY, ProviderKind text NOT NULL
		);
		CREATE TEMP TABLE ExternalEntityMappings (
			IntegrationConnectionID bigint NOT NULL, EntityKind text NOT NULL,
			LocalID text NOT NULL, Active boolean NOT NULL DEFAULT true,
			UpdatedAt timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create temporary PostgreSQL schema: %v", err)
	}
}
