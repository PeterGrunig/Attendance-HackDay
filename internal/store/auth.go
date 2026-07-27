package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

// ErrUserNotFound means no SQL user matched the requested credential lookup.
var ErrUserNotFound = errors.New("user not found")

// FindUserByEmail loads the credential and routing data needed by the login flow.
func (s *SQLStore) FindUserByEmail(ctx context.Context, email string) (domain.User, error) {
	return s.findUser(ctx, "Email", email)
}

// FindUserByID loads the current SQL user referenced by an authenticated session.
func (s *SQLStore) FindUserByID(ctx context.Context, userID string) (domain.User, error) {
	return s.findUser(ctx, "UserID", userID)
}

func (s *SQLStore) findUser(ctx context.Context, column, value string) (domain.User, error) {
	var user domain.User
	// A pending Canvas account may carry the same contact email as an existing
	// local account; usable local credentials always win that login lookup.
	query := `
		SELECT
			UserID,
			Name,
			Role,
			Email,
			PasswordHash,
			COALESCE(ClassroomID, '')
		FROM Users
		WHERE ` + column + ` = $1
		ORDER BY (PasswordHash = '!canvas-pending') ASC, UserID
		LIMIT 1;
	`
	err := s.db.QueryRowContext(ctx, query, value).Scan(
		&user.UserID,
		&user.Name,
		&user.Role,
		&user.Email,
		&user.PasswordHash,
		&user.ClassroomID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, err
	}

	return user, nil
}
