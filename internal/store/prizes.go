package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

var (
	ErrPrizeNotFound             = errors.New("prize not found")
	ErrPrizeUnavailable          = errors.New("prize unavailable")
	ErrPrizeOutOfStock           = errors.New("prize out of stock")
	ErrPrizeClassroomAccess      = errors.New("prize classroom access denied")
	ErrPrizeRedemptionNotPending = errors.New("prize redemption is not pending")
	ErrNoPrizeNotification       = errors.New("no prize notification is due")
)

func (s *SQLStore) LoadStudentPrizeState(ctx context.Context, userID string) (domain.StudentPrizeState, error) {
	state := domain.StudentPrizeState{}
	if err := s.db.QueryRowContext(ctx, `
		SELECT $2
			+ COALESCE((SELECT Amount FROM ManualCoinAdjustments WHERE UserID = $1), 0)
			+ COALESCE((SELECT SUM(Amount) FROM Transactions WHERE UserID = $1), 0);
	`, userID, startingStudentCoins).Scan(&state.CoinBalance); err != nil {
		return state, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.ID, c.Name, p.PrizeID, p.CreatedByUserID, p.Name, p.Description,
			p.Emoji, p.CoinPrice, p.AvailableStock, p.Active
		FROM ClassroomMemberships cm
		JOIN Classrooms c ON c.ID = cm.ClassroomID
		LEFT JOIN ClassroomPrizes p ON p.ClassroomID = c.ID AND p.Active = true
		WHERE cm.UserID = $1 AND cm.MembershipRole = 'student' AND cm.Active = true
		ORDER BY c.Name, c.ID, p.Name, p.PrizeID;
	`, userID)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	catalogIndexes := map[string]int{}
	for rows.Next() {
		var classroomID, classroomName string
		var prizeID sql.NullInt64
		var createdBy, name, description, emoji sql.NullString
		var price sql.NullInt64
		var stock sql.NullInt64
		var active sql.NullBool
		if err := rows.Scan(&classroomID, &classroomName, &prizeID, &createdBy, &name, &description, &emoji, &price, &stock, &active); err != nil {
			return state, err
		}
		index, ok := catalogIndexes[classroomID]
		if !ok {
			state.Catalogs = append(state.Catalogs, domain.PrizeCatalog{ClassroomID: classroomID, ClassroomName: classroomName})
			index = len(state.Catalogs) - 1
			catalogIndexes[classroomID] = index
		}
		if prizeID.Valid {
			state.Catalogs[index].Prizes = append(state.Catalogs[index].Prizes, domain.ClassroomPrize{
				ID: prizeID.Int64, ClassroomID: classroomID, ClassroomName: classroomName,
				CreatedBy: createdBy.String, Name: name.String, Description: description.String,
				Emoji: emoji.String, CoinPrice: int(price.Int64), AvailableStock: nullableStock(stock), Active: active.Bool,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	redemptions, err := s.listPrizeRedemptions(ctx, `WHERE r.StudentUserID = $1`, userID)
	state.Redemptions = redemptions
	return state, err
}

func (s *SQLStore) LoadTeacherPrizeState(ctx context.Context, teacherID string) (domain.TeacherPrizeState, error) {
	state := domain.TeacherPrizeState{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.ID, c.Name
		FROM ClassroomMemberships cm JOIN Classrooms c ON c.ID = cm.ClassroomID
		WHERE cm.UserID = $1 AND cm.MembershipRole = 'teacher' AND cm.Active = true
		ORDER BY c.Name, c.ID;
	`, teacherID)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var classroom domain.Classroom
		if err := rows.Scan(&classroom.ID, &classroom.Name); err != nil {
			rows.Close()
			return state, err
		}
		state.Classrooms = append(state.Classrooms, classroom)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}

	prizeRows, err := s.db.QueryContext(ctx, `
		SELECT p.PrizeID, p.ClassroomID, c.Name, p.CreatedByUserID, p.Name, p.Description,
			p.Emoji, p.CoinPrice, p.AvailableStock, p.Active
		FROM ClassroomPrizes p JOIN Classrooms c ON c.ID = p.ClassroomID
		WHERE EXISTS (SELECT 1 FROM ClassroomMemberships cm WHERE cm.ClassroomID = p.ClassroomID
			AND cm.UserID = $1 AND cm.MembershipRole = 'teacher' AND cm.Active = true)
		ORDER BY c.Name, p.Active DESC, p.Name, p.PrizeID;
	`, teacherID)
	if err != nil {
		return state, err
	}
	for prizeRows.Next() {
		var prize domain.ClassroomPrize
		var stock sql.NullInt64
		if err := prizeRows.Scan(&prize.ID, &prize.ClassroomID, &prize.ClassroomName, &prize.CreatedBy,
			&prize.Name, &prize.Description, &prize.Emoji, &prize.CoinPrice, &stock, &prize.Active); err != nil {
			prizeRows.Close()
			return state, err
		}
		prize.AvailableStock = nullableStock(stock)
		state.Prizes = append(state.Prizes, prize)
	}
	if err := prizeRows.Close(); err != nil {
		return state, err
	}
	state.Redemptions, err = s.listPrizeRedemptions(ctx, `WHERE EXISTS (
		SELECT 1 FROM ClassroomMemberships cm WHERE cm.ClassroomID = r.ClassroomID
		AND cm.UserID = $1 AND cm.MembershipRole = 'teacher' AND cm.Active = true)`, teacherID)
	return state, err
}

func (s *SQLStore) listPrizeRedemptions(ctx context.Context, where string, arg any) ([]domain.PrizeRedemption, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.RedemptionID, r.PrizeID, r.ClassroomID, c.Name, r.StudentUserID, u.Name,
			r.PrizeName, r.PrizeEmoji, r.CoinPrice, r.Status, r.PurchasedAt,
			r.FulfilledAt, r.CanceledAt,
			CASE WHEN COUNT(n.NotificationID) = 0 THEN 'no recipient'
				WHEN COUNT(*) FILTER (WHERE n.State = 'failed') > 0 THEN 'failed'
				WHEN COUNT(*) FILTER (WHERE n.State IN ('pending', 'sending')) > 0 THEN 'pending'
				ELSE 'sent' END
		FROM PrizeRedemptions r JOIN Classrooms c ON c.ID = r.ClassroomID
		JOIN Users u ON u.UserID = r.StudentUserID
		LEFT JOIN PrizeNotificationOutbox n ON n.RedemptionID = r.RedemptionID
		`+where+`
		GROUP BY r.RedemptionID, r.PrizeID, r.ClassroomID, c.Name, r.StudentUserID, u.Name,
			r.PrizeName, r.PrizeEmoji, r.CoinPrice, r.Status, r.PurchasedAt, r.FulfilledAt, r.CanceledAt
		ORDER BY (r.Status = 'pending') DESC, r.PurchasedAt DESC, r.RedemptionID DESC;
	`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.PrizeRedemption{}
	for rows.Next() {
		var item domain.PrizeRedemption
		var fulfilled, canceled sql.NullTime
		if err := rows.Scan(&item.ID, &item.PrizeID, &item.ClassroomID, &item.ClassroomName,
			&item.StudentUserID, &item.StudentName, &item.PrizeName, &item.PrizeEmoji,
			&item.CoinPrice, &item.Status, &item.PurchasedAt, &fulfilled, &canceled,
			&item.NotificationStatus); err != nil {
			return nil, err
		}
		if fulfilled.Valid {
			value := fulfilled.Time
			item.FulfilledAt = &value
		}
		if canceled.Valid {
			value := canceled.Time
			item.CanceledAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PurchaseClassroomPrize serializes purchases per student and prize so the
// coin debit, finite-stock decrement, redemption, and email enqueue cannot diverge.
func (s *SQLStore) PurchaseClassroomPrize(ctx context.Context, studentID string, prizeID int64, occurredAt time.Time) (domain.PrizeRedemption, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return domain.PrizeRedemption{}, err
	}
	defer tx.Rollback()
	var studentName string
	if err := tx.QueryRowContext(ctx, `SELECT Name FROM Users WHERE UserID = $1 AND Role = 'student' FOR UPDATE`, studentID).Scan(&studentName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PrizeRedemption{}, ErrPrizeClassroomAccess
		}
		return domain.PrizeRedemption{}, err
	}
	var prize domain.ClassroomPrize
	var stock sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT p.ClassroomID, c.Name, p.Name, p.Emoji, p.CoinPrice, p.AvailableStock, p.Active
		FROM ClassroomPrizes p JOIN Classrooms c ON c.ID = p.ClassroomID
		WHERE p.PrizeID = $1 FOR UPDATE OF p`, prizeID).Scan(&prize.ClassroomID, &prize.ClassroomName,
		&prize.Name, &prize.Emoji, &prize.CoinPrice, &stock, &prize.Active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PrizeRedemption{}, ErrPrizeNotFound
		}
		return domain.PrizeRedemption{}, err
	}
	if !prize.Active {
		return domain.PrizeRedemption{}, ErrPrizeUnavailable
	}
	if stock.Valid && stock.Int64 == 0 {
		return domain.PrizeRedemption{}, ErrPrizeOutOfStock
	}
	var membership int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM ClassroomMemberships WHERE ClassroomID = $1 AND UserID = $2
		AND MembershipRole = 'student' AND Active = true`, prize.ClassroomID, studentID).Scan(&membership); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PrizeRedemption{}, ErrPrizeClassroomAccess
		}
		return domain.PrizeRedemption{}, err
	}
	var balance int
	if err := tx.QueryRowContext(ctx, `SELECT $2 + COALESCE((SELECT Amount FROM ManualCoinAdjustments WHERE UserID = $1), 0)
		+ COALESCE((SELECT SUM(Amount) FROM Transactions WHERE UserID = $1), 0)`, studentID, startingStudentCoins).Scan(&balance); err != nil {
		return domain.PrizeRedemption{}, err
	}
	if balance < prize.CoinPrice {
		return domain.PrizeRedemption{}, ErrInsufficientCoins
	}
	var transactionID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO Transactions (UserID, Amount, Timestamp, Description)
		VALUES ($1, $2, $3, $4) RETURNING TransactionID`, studentID, -prize.CoinPrice, occurredAt,
		fmt.Sprintf("Redeemed classroom prize: %s", prize.Name)).Scan(&transactionID); err != nil {
		return domain.PrizeRedemption{}, err
	}
	if stock.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE ClassroomPrizes SET AvailableStock = AvailableStock - 1, UpdatedAt = $2 WHERE PrizeID = $1`, prizeID, occurredAt); err != nil {
			return domain.PrizeRedemption{}, err
		}
	}
	redemption := domain.PrizeRedemption{PrizeID: prizeID, ClassroomID: prize.ClassroomID, ClassroomName: prize.ClassroomName,
		StudentUserID: studentID, StudentName: studentName, PrizeName: prize.Name, PrizeEmoji: prize.Emoji,
		CoinPrice: prize.CoinPrice, Status: "pending", PurchasedAt: occurredAt}
	if err := tx.QueryRowContext(ctx, `INSERT INTO PrizeRedemptions
		(PrizeID, ClassroomID, StudentUserID, PrizeName, PrizeEmoji, CoinPrice, PurchaseTransactionID, PurchasedAt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING RedemptionID`, prizeID, prize.ClassroomID, studentID,
		prize.Name, prize.Emoji, prize.CoinPrice, transactionID, occurredAt).Scan(&redemption.ID); err != nil {
		return domain.PrizeRedemption{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO PrizeNotificationOutbox (RedemptionID, RecipientEmail, NextAttemptAt)
		SELECT $1, lower(btrim(u.Email)), $2 FROM ClassroomMemberships cm JOIN Users u ON u.UserID = cm.UserID
		WHERE cm.ClassroomID = $3 AND cm.MembershipRole = 'teacher' AND cm.Active = true AND btrim(u.Email) <> ''
		ON CONFLICT (RedemptionID, RecipientEmail) DO NOTHING`, redemption.ID, occurredAt, prize.ClassroomID)
	if err != nil {
		return domain.PrizeRedemption{}, err
	}
	recipients, err := result.RowsAffected()
	if err != nil {
		return domain.PrizeRedemption{}, err
	}
	if recipients == 0 {
		redemption.NotificationStatus = "no recipient"
		log.Printf("prize purchase has no teacher email recipient: redemption_id=%d classroom_id=%q", redemption.ID, prize.ClassroomID)
	} else {
		redemption.NotificationStatus = "pending"
	}
	if err := tx.Commit(); err != nil {
		return domain.PrizeRedemption{}, err
	}
	return redemption, nil
}

func (s *SQLStore) CreateClassroomPrize(ctx context.Context, teacherID string, input domain.PrizeInput) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO ClassroomPrizes
		(ClassroomID, CreatedByUserID, Name, Description, Emoji, CoinPrice, AvailableStock, Active)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8 WHERE EXISTS (SELECT 1 FROM ClassroomMemberships
			WHERE ClassroomID=$1 AND UserID=$2 AND MembershipRole='teacher' AND Active=true)
		RETURNING PrizeID`, input.ClassroomID, teacherID, input.Name, input.Description, input.Emoji,
		input.CoinPrice, stockValue(input.AvailableStock), input.Active).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPrizeClassroomAccess
	}
	return id, err
}

func (s *SQLStore) UpdateClassroomPrize(ctx context.Context, teacherID string, input domain.PrizeInput) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ClassroomPrizes SET Name=$3, Description=$4, Emoji=$5,
		CoinPrice=$6, AvailableStock=$7, Active=$8, UpdatedAt=CURRENT_TIMESTAMP
		WHERE PrizeID=$1 AND ClassroomID=$2 AND EXISTS (SELECT 1 FROM ClassroomMemberships
			WHERE ClassroomID=$2 AND UserID=$9 AND MembershipRole='teacher' AND Active=true)`, input.ID,
		input.ClassroomID, input.Name, input.Description, input.Emoji, input.CoinPrice,
		stockValue(input.AvailableStock), input.Active, teacherID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrPrizeNotFound
	}
	return nil
}

func (s *SQLStore) FulfillPrizeRedemption(ctx context.Context, teacherID string, redemptionID int64, occurredAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE PrizeRedemptions r SET Status='fulfilled', FulfilledAt=$3, FulfilledByUserID=$2
		WHERE r.RedemptionID=$1 AND r.Status='pending' AND EXISTS (SELECT 1 FROM ClassroomMemberships cm
			WHERE cm.ClassroomID=r.ClassroomID AND cm.UserID=$2 AND cm.MembershipRole='teacher' AND cm.Active=true)`, redemptionID, teacherID, occurredAt)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrPrizeRedemptionNotPending
	}
	return nil
}

// CancelPrizeRedemption makes the pending-to-canceled transition exactly once,
// using the price snapshot for the refund and restoring only finite inventory.
func (s *SQLStore) CancelPrizeRedemption(ctx context.Context, teacherID string, redemptionID int64, occurredAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var studentID, prizeName, classroomID, status string
	var prizeID int64
	var price int
	if err := tx.QueryRowContext(ctx, `SELECT r.StudentUserID, r.PrizeID, r.PrizeName, r.CoinPrice, r.ClassroomID, r.Status
		FROM PrizeRedemptions r WHERE r.RedemptionID=$1 FOR UPDATE`, redemptionID).Scan(&studentID, &prizeID, &prizeName, &price, &classroomID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPrizeRedemptionNotPending
		}
		return err
	}
	if status != "pending" {
		return ErrPrizeRedemptionNotPending
	}
	var access int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM ClassroomMemberships WHERE ClassroomID=$1 AND UserID=$2
		AND MembershipRole='teacher' AND Active=true`, classroomID, teacherID).Scan(&access); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPrizeClassroomAccess
		}
		return err
	}
	var refundID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO Transactions (UserID, Amount, Timestamp, Description)
		VALUES ($1,$2,$3,$4) RETURNING TransactionID`, studentID, price, occurredAt,
		fmt.Sprintf("Refunded classroom prize: %s", prizeName)).Scan(&refundID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ClassroomPrizes SET AvailableStock=AvailableStock+1, UpdatedAt=$2
		WHERE PrizeID=$1 AND AvailableStock IS NOT NULL`, prizeID, occurredAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE PrizeRedemptions SET Status='canceled', CanceledAt=$2,
		CanceledByUserID=$3, RefundTransactionID=$4 WHERE RedemptionID=$1`, redemptionID, occurredAt, teacherID, refundID); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableStock(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	stock := int(value.Int64)
	return &stock
}

func stockValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *SQLStore) ClaimPrizeNotification(ctx context.Context, now, staleBefore time.Time, maxAttempts int) (domain.PrizeNotification, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PrizeNotification{}, err
	}
	defer tx.Rollback()
	var notification domain.PrizeNotification
	err = tx.QueryRowContext(ctx, `
		SELECT NotificationID FROM PrizeNotificationOutbox
		WHERE (State = 'pending' AND AttemptCount < $1 AND NextAttemptAt <= $2)
			OR (State = 'sending' AND LockedAt < $3)
		ORDER BY NextAttemptAt, NotificationID LIMIT 1 FOR UPDATE SKIP LOCKED`, maxAttempts, now, staleBefore).Scan(&notification.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return notification, ErrNoPrizeNotification
	}
	if err != nil {
		return notification, err
	}
	err = tx.QueryRowContext(ctx, `
		UPDATE PrizeNotificationOutbox SET AttemptCount=CASE WHEN State='pending' THEN AttemptCount+1 ELSE AttemptCount END,
			State='sending',
			LockedAt=$2, LastError='' WHERE NotificationID=$1 RETURNING RedemptionID, RecipientEmail, AttemptCount`,
		notification.ID, now).Scan(&notification.RedemptionID, &notification.Recipient, &notification.AttemptNumber)
	if err != nil {
		return notification, err
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT u.Name, c.Name, r.PrizeName, r.PrizeEmoji, r.CoinPrice, r.PurchasedAt
		FROM PrizeRedemptions r JOIN Users u ON u.UserID=r.StudentUserID
		JOIN Classrooms c ON c.ID=r.ClassroomID WHERE r.RedemptionID=$1`, notification.RedemptionID).Scan(
		&notification.StudentName, &notification.ClassroomName, &notification.PrizeName,
		&notification.PrizeEmoji, &notification.CoinPrice, &notification.PurchasedAt); err != nil {
		return notification, err
	}
	if err := tx.Commit(); err != nil {
		return notification, err
	}
	return notification, nil
}

func (s *SQLStore) FinishPrizeNotification(ctx context.Context, notificationID int64, sent bool, nextAttempt time.Time, message string, exhausted bool) error {
	state := "pending"
	var sentAt any
	if sent {
		state, sentAt = "sent", time.Now()
	} else if exhausted {
		state = "failed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE PrizeNotificationOutbox SET State=$2, NextAttemptAt=$3,
		LockedAt=NULL, LastError=$4, SentAt=$5 WHERE NotificationID=$1`, notificationID, state, nextAttempt, message, sentAt)
	return err
}
