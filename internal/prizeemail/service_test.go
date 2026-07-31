package prizeemail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	datastore "github.com/PeterGrunig/Attendance-HackDay/internal/store"
)

type notificationStoreStub struct {
	items    []domain.PrizeNotification
	finished []notificationFinish
}

type notificationFinish struct {
	id        int64
	sent      bool
	exhausted bool
	message   string
}

func (s *notificationStoreStub) ClaimPrizeNotification(context.Context, time.Time, time.Time, int) (domain.PrizeNotification, error) {
	if len(s.items) == 0 {
		return domain.PrizeNotification{}, datastore.ErrNoPrizeNotification
	}
	item := s.items[0]
	s.items = s.items[1:]
	return item, nil
}

func (s *notificationStoreStub) FinishPrizeNotification(_ context.Context, id int64, sent bool, _ time.Time, message string, exhausted bool) error {
	s.finished = append(s.finished, notificationFinish{id: id, sent: sent, exhausted: exhausted, message: message})
	return nil
}

type mailSenderStub struct {
	messages []Message
	err      error
}

func (s *mailSenderStub) Send(_ context.Context, message Message) error {
	s.messages = append(s.messages, message)
	return s.err
}

func TestProcessDueSendsPrizeNotification(t *testing.T) {
	store := &notificationStoreStub{items: []domain.PrizeNotification{{ID: 4, RedemptionID: 9, Recipient: "teacher@example.com",
		AttemptNumber: 1, StudentName: "Student", ClassroomName: "Room 1", PrizeName: "Snack", PrizeEmoji: "🍎",
		CoinPrice: 5, PurchasedAt: time.Date(2026, 8, 1, 15, 0, 0, 0, time.UTC)}}}
	sender := &mailSenderStub{}
	if err := New(store, sender).ProcessDue(context.Background()); err != nil {
		t.Fatalf("ProcessDue: %v", err)
	}
	if len(sender.messages) != 1 || sender.messages[0].To != "teacher@example.com" {
		t.Fatalf("messages = %#v", sender.messages)
	}
	if !strings.Contains(sender.messages[0].Body, "Redemption: #9") || !strings.Contains(sender.messages[0].Body, "🍎 Snack") {
		t.Fatalf("body = %q", sender.messages[0].Body)
	}
	if len(store.finished) != 1 || !store.finished[0].sent {
		t.Fatalf("finished = %#v", store.finished)
	}
}

func TestProcessDueMarksSixthFailureExhausted(t *testing.T) {
	store := &notificationStoreStub{items: []domain.PrizeNotification{{ID: 5, RedemptionID: 10, Recipient: "teacher@example.com", AttemptNumber: 6}}}
	sender := &mailSenderStub{err: errors.New("SMTP unavailable")}
	if err := New(store, sender).ProcessDue(context.Background()); err != nil {
		t.Fatalf("ProcessDue: %v", err)
	}
	if len(store.finished) != 1 || store.finished[0].sent || !store.finished[0].exhausted || !strings.Contains(store.finished[0].message, "SMTP unavailable") {
		t.Fatalf("finished = %#v", store.finished)
	}
}

func TestSMTPConfigRequiresTLSForRemoteHosts(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_FROM", "attendance@example.com")
	t.Setenv("SMTP_REQUIRE_TLS", "false")
	if _, err := SMTPConfigFromEnvironment(); err == nil {
		t.Fatal("remote plaintext SMTP configuration succeeded")
	}
	t.Setenv("SMTP_HOST", "localhost")
	config, err := SMTPConfigFromEnvironment()
	if err != nil {
		t.Fatalf("local SMTP config: %v", err)
	}
	if config.RequireTLS || config.Port != "587" {
		t.Fatalf("config = %#v", config)
	}
}

func TestSMTPConfigRejectsPartialCredentials(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_FROM", "attendance@example.com")
	t.Setenv("SMTP_USERNAME", "user")
	t.Setenv("SMTP_PASSWORD", "")
	if _, err := SMTPConfigFromEnvironment(); err == nil {
		t.Fatal("partial SMTP credentials succeeded")
	}
}
