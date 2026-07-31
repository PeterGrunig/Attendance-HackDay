package prizeemail

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	datastore "github.com/PeterGrunig/Attendance-HackDay/internal/store"
)

const (
	maxAttempts       = 6 // one initial send plus five scheduled retries
	maxMessagesPerRun = 20
	pollInterval      = 15 * time.Second
	staleLockAge      = 5 * time.Minute
)

var retryDelays = [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}

type Store interface {
	ClaimPrizeNotification(context.Context, time.Time, time.Time, int) (domain.PrizeNotification, error)
	FinishPrizeNotification(context.Context, int64, bool, time.Time, string, bool) error
}

type Message struct {
	To      string
	Subject string
	Body    string
}

type MailSender interface {
	Send(context.Context, Message) error
}

type Service struct {
	store  Store
	sender MailSender
}

func New(store Store, sender MailSender) *Service {
	return &Service{store: store, sender: sender}
}

// Run drains the durable prize email outbox in bounded passes. A stale sending
// lock is reclaimable after restart, while exhausted messages remain visible to teachers.
func (s *Service) Run(ctx context.Context) {
	if s == nil || s.store == nil || s.sender == nil {
		return
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if err := s.ProcessDue(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("prize notification pass failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) ProcessDue(ctx context.Context) error {
	if s == nil || s.store == nil || s.sender == nil {
		return nil
	}
	for range maxMessagesPerRun {
		now := time.Now()
		notification, err := s.store.ClaimPrizeNotification(ctx, now, now.Add(-staleLockAge), maxAttempts)
		if errors.Is(err, datastore.ErrNoPrizeNotification) {
			return nil
		}
		if err != nil {
			return err
		}
		message := notificationMessage(notification)
		sendErr := s.sender.Send(ctx, message)
		if sendErr == nil {
			if err := s.store.FinishPrizeNotification(ctx, notification.ID, true, now, "", false); err != nil {
				return err
			}
			log.Printf("prize notification sent: notification_id=%d redemption_id=%d", notification.ID, notification.RedemptionID)
			continue
		}
		exhausted := notification.AttemptNumber >= maxAttempts
		next := now.Add(retryDelays[min(notification.AttemptNumber-1, len(retryDelays)-1)])
		if err := s.store.FinishPrizeNotification(ctx, notification.ID, false, next, sendErr.Error(), exhausted); err != nil {
			return err
		}
		log.Printf("prize notification failed: notification_id=%d redemption_id=%d attempt=%d exhausted=%t error=%v",
			notification.ID, notification.RedemptionID, notification.AttemptNumber, exhausted, sendErr)
	}
	return nil
}

func notificationMessage(item domain.PrizeNotification) Message {
	emoji := item.PrizeEmoji
	if emoji != "" {
		emoji += " "
	}
	return Message{
		To:      item.Recipient,
		Subject: fmt.Sprintf("[Attendance Quest] Prize purchase: %s — %s", item.StudentName, item.PrizeName),
		Body: fmt.Sprintf("A student purchased a classroom prize.\n\nRedemption: #%d\nStudent: %s\nClassroom: %s\nPrize: %s%s\nCost: %d coins\nPurchased: %s\n\nLog in to Attendance Quest and open Teacher Prizes to fulfill or cancel this redemption.\n",
			item.RedemptionID, item.StudentName, item.ClassroomName, emoji, item.PrizeName,
			item.CoinPrice, item.PurchasedAt.Format(time.RFC1123)),
	}
}
