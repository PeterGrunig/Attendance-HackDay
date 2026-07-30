package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

const startingStudentCoins = 10

var (
	ErrInvalidStudent          = errors.New("invalid student")
	ErrInvalidClassroom        = errors.New("invalid classroom")
	ErrAttendanceAlreadyMarked = errors.New("attendance already marked")
	ErrShopItemNotFound        = errors.New("shop item not found")
	ErrShopItemAlreadyOwned    = errors.New("shop item already owned")
	ErrInsufficientCoins       = errors.New("insufficient coins")
)

// LoadStudentDashboardState loads attendance, rewards, avatar, and classroom
// schedule data needed by the elementary student dashboard.
func (s *SQLStore) LoadStudentDashboardState(ctx context.Context, user domain.User) (domain.StudentState, error) {
	return s.loadStudentPageState(ctx, user, true, false)
}

// LoadStudentAttendanceState loads schedule data needed to calculate an
// attendance reward.
func (s *SQLStore) LoadStudentAttendanceState(ctx context.Context, user domain.User) (domain.StudentState, error) {
	return s.loadStudentPageState(ctx, user, true, false)
}

// LoadStudentShopState loads the shared student data plus the SQL shop catalog.
func (s *SQLStore) LoadStudentShopState(ctx context.Context, user domain.User) (domain.StudentState, error) {
	return s.loadStudentPageState(ctx, user, false, true)
}

// LoadStudentAvatarState loads only the shared data used by the avatar page.
func (s *SQLStore) LoadStudentAvatarState(ctx context.Context, user domain.User) (domain.StudentState, error) {
	return s.loadStudentPageState(ctx, user, false, false)
}

// loadStudentPageState keeps page reads focused while loading balance,
// attendance, avatar, and ownership data shared by the student shell.
func (s *SQLStore) loadStudentPageState(ctx context.Context, user domain.User, includeSchedules, includeShop bool) (domain.StudentState, error) {
	if user.Role != "student" || user.ClassroomID == "" {
		return domain.StudentState{}, ErrInvalidStudent
	}

	state := domain.StudentState{
		User: user,
	}
	if err := s.loadStudentCore(ctx, &state); err != nil {
		return domain.StudentState{}, err
	}
	if includeSchedules {
		if err := s.loadSchedules(ctx, &state); err != nil {
			return domain.StudentState{}, err
		}
	}
	if includeShop {
		if err := s.loadShopItems(ctx, &state); err != nil {
			return domain.StudentState{}, err
		}
	}
	if err := s.loadOwnedShopItems(ctx, &state); err != nil {
		return domain.StudentState{}, err
	}
	return state, nil
}

// loadStudentCore combines the common balance, attendance, and avatar reads
// into one database round trip for every student page.
func (s *SQLStore) loadStudentCore(ctx context.Context, state *domain.StudentState) error {
	var (
		presentJSON string
		absentJSON  string
		hasAvatar   int
		config      domain.AvatarConfig
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			GREATEST(
				$3
				+ COALESCE((SELECT Amount FROM ManualCoinAdjustments WHERE UserID = $1), 0)
				+ COALESCE((SELECT SUM(Amount) FROM Transactions WHERE UserID = $1), 0),
				0
			),
			COALESCE(attendance.PresentDates, '[]'),
			COALESCE(attendance.AbsentDates, '[]'),
			CASE WHEN avatar.UserID IS NULL THEN 0 ELSE 1 END,
			COALESCE(avatar.Base, ''),
			COALESCE(avatar.HairStyle, ''),
			COALESCE(avatar.Clothing, ''),
			COALESCE(avatar.Accessory, ''),
			COALESCE(avatar.Effect, '')
		FROM Users AS student
		LEFT JOIN AttendanceRecords AS attendance
			ON attendance.UserID = student.UserID AND attendance.ClassroomID = $2
		LEFT JOIN AvatarConfigs AS avatar ON avatar.UserID = student.UserID
		WHERE student.UserID = $1;
	`, state.User.UserID, state.User.ClassroomID, startingStudentCoins).Scan(
		&state.CoinBalance,
		&presentJSON,
		&absentJSON,
		&hasAvatar,
		&config.Base,
		&config.HairStyle,
		&config.Clothing,
		&config.Accessory,
		&config.Effect,
	)
	if err != nil {
		return err
	}

	state.Attendance.UserID = state.User.UserID
	state.Attendance.ClassroomID = state.User.ClassroomID
	if err := json.Unmarshal([]byte(presentJSON), &state.Attendance.Present); err != nil {
		return fmt.Errorf("decode present dates: %w", err)
	}
	if err := json.Unmarshal([]byte(absentJSON), &state.Attendance.Absent); err != nil {
		return fmt.Errorf("decode absent dates: %w", err)
	}
	if hasAvatar == 1 {
		state.AvatarConfig = &config
	}
	return nil
}

func (s *SQLStore) loadSchedules(ctx context.Context, state *domain.StudentState) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ClassroomID, DayOfWeek, StartTime, EndTime, DoubleDay
		FROM Schedule WHERE ClassroomID = $1 ORDER BY ScheduleID;
	`, state.User.ClassroomID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var schedule domain.Schedule
		if err := rows.Scan(&schedule.ClassroomID, &schedule.DayOfWeek, &schedule.StartTime, &schedule.EndTime, &schedule.DoubleDay); err != nil {
			return err
		}
		state.Schedules = append(state.Schedules, schedule)
	}
	return rows.Err()
}

func (s *SQLStore) loadShopItems(ctx context.Context, state *domain.StudentState) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ID, Name, Price, Description, COALESCE(ImagePath, ''), COALESCE(Slot, '')
		FROM ShopItems ORDER BY ID;
	`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item domain.ShopItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Price, &item.Description, &item.ImagePath, &item.Slot); err != nil {
			rows.Close()
			return err
		}
		state.ShopItems = append(state.ShopItems, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return nil
}

func (s *SQLStore) loadOwnedShopItems(ctx context.Context, state *domain.StudentState) error {
	ownedRows, err := s.db.QueryContext(ctx, `
		SELECT ShopItemID FROM OwnedShopItems WHERE UserID = $1 ORDER BY ShopItemID;
	`, state.User.UserID)
	if err != nil {
		return err
	}
	defer ownedRows.Close()
	for ownedRows.Next() {
		var itemID string
		if err := ownedRows.Scan(&itemID); err != nil {
			return err
		}
		state.OwnedShopItemIDs = append(state.OwnedShopItemIDs, itemID)
	}
	return ownedRows.Err()
}

// MarkAttendanceAndAwardCoins locks a student's attendance row so one date and
// its matching reward transaction are committed together at most once.
func (s *SQLStore) MarkAttendanceAndAwardCoins(ctx context.Context, userID, classroomID, date string, reward int, occurredAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var storedClassroomID, role string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(ClassroomID, ''), Role FROM Users WHERE UserID = $1 FOR UPDATE;
	`, userID).Scan(&storedClassroomID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidStudent
	}
	if err != nil {
		return err
	}
	if role != "student" {
		return ErrInvalidStudent
	}
	if storedClassroomID == "" || storedClassroomID != classroomID {
		return ErrInvalidClassroom
	}

	var presentJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(PresentDates, '[]') FROM AttendanceRecords
		WHERE UserID = $1 AND ClassroomID = $2
		FOR UPDATE;
	`, userID, classroomID).Scan(&presentJSON)
	recordExists := err == nil
	present := []string{}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(presentJSON), &present); err != nil {
			return fmt.Errorf("decode present dates: %w", err)
		}
	}
	for _, presentDate := range present {
		if presentDate == date {
			return ErrAttendanceAlreadyMarked
		}
	}
	present = append(present, date)
	encodedPresent, err := json.Marshal(present)
	if err != nil {
		return err
	}

	if !recordExists {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO AttendanceRecords (UserID, ClassroomID, PresentDates, AbsentDates)
			VALUES ($1, $2, $3, '[]');
		`, userID, classroomID, string(encodedPresent))
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE AttendanceRecords SET PresentDates = $3 WHERE UserID = $1 AND ClassroomID = $2;
		`, userID, classroomID, string(encodedPresent))
	}
	if err != nil {
		return err
	}
	// AttendanceMarks is the student's pending indication for teacher review,
	// not an official decision. Keep it in the same transaction as the legacy
	// JSON record and reward so partial check-ins cannot be observed.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO AttendanceMarks
			(UserID, ClassroomID, AttendanceDate, Status, Source, CheckInAt, UpdatedAt)
		VALUES ($1, $2, $3, 'present', 'student_checkin', $4, CURRENT_TIMESTAMP)
		ON CONFLICT (UserID, ClassroomID, AttendanceDate) DO UPDATE SET
			Status = 'present',
			Source = 'student_checkin',
			CheckInAt = EXCLUDED.CheckInAt,
			UpdatedAt = CURRENT_TIMESTAMP;
	`, userID, classroomID, date, occurredAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO Transactions (UserID, Amount, Timestamp, Description)
		VALUES ($1, $2, $3, $4);
	`, userID, reward, occurredAt, fmt.Sprintf("Attendance reward for %s", date))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// PurchaseShopItem checks ownership and balance under one serializable SQL
// transaction so the debit and ownership record cannot diverge.
func (s *SQLStore) PurchaseShopItem(ctx context.Context, userID, itemID string, occurredAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var itemName string
	var price int
	err = tx.QueryRowContext(ctx, `SELECT Name, Price FROM ShopItems WHERE ID = $1 FOR UPDATE;`, itemID).Scan(&itemName, &price)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrShopItemNotFound
	}
	if err != nil {
		return err
	}
	var owned int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM OwnedShopItems WHERE UserID = $1 AND ShopItemID = $2 FOR UPDATE;
	`, userID, itemID).Scan(&owned)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		return ErrShopItemAlreadyOwned
	}

	var balance int
	err = tx.QueryRowContext(ctx, `
		SELECT $2
			+ COALESCE((SELECT Amount FROM ManualCoinAdjustments WHERE UserID = $1), 0)
			+ COALESCE((SELECT SUM(Amount) FROM Transactions WHERE UserID = $1), 0);
	`, userID, startingStudentCoins).Scan(&balance)
	if err != nil {
		return err
	}
	if balance < price {
		return ErrInsufficientCoins
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO Transactions (UserID, Amount, Timestamp, Description)
		VALUES ($1, $2, $3, $4);
	`, userID, -price, occurredAt, fmt.Sprintf("Purchased %s", itemName)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO OwnedShopItems (UserID, ShopItemID) VALUES ($1, $2);
	`, userID, itemID); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveAvatarConfig replaces the student's saved avatar while preserving one row per user.
func (s *SQLStore) SaveAvatarConfig(ctx context.Context, userID string, config domain.AvatarConfig) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO AvatarConfigs (UserID, Base, HairStyle, Clothing, Accessory, Effect)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (UserID) DO UPDATE SET
			Base = EXCLUDED.Base,
			HairStyle = EXCLUDED.HairStyle,
			Clothing = EXCLUDED.Clothing,
			Accessory = EXCLUDED.Accessory,
			Effect = EXCLUDED.Effect;
	`, userID, config.Base, config.HairStyle, config.Clothing, config.Accessory, config.Effect)
	return err
}
