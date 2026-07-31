package domain

import (
	"fmt"
	"time"
)

type ClassroomPrize struct {
	ID             int64
	ClassroomID    string
	ClassroomName  string
	CreatedBy      string
	Name           string
	Description    string
	Emoji          string
	CoinPrice      int
	AvailableStock *int
	Active         bool
}

func (p ClassroomPrize) IsOutOfStock() bool {
	return p.AvailableStock != nil && *p.AvailableStock == 0
}

func (p ClassroomPrize) StockLabel() string {
	if p.AvailableStock == nil {
		return "Unlimited"
	}
	return fmt.Sprintf("%d available", *p.AvailableStock)
}

type PrizeCatalog struct {
	ClassroomID   string
	ClassroomName string
	Prizes        []ClassroomPrize
}

type PrizeRedemption struct {
	ID                 int64
	PrizeID            int64
	ClassroomID        string
	ClassroomName      string
	StudentUserID      string
	StudentName        string
	PrizeName          string
	PrizeEmoji         string
	CoinPrice          int
	Status             string
	NotificationStatus string
	PurchasedAt        time.Time
	FulfilledAt        *time.Time
	CanceledAt         *time.Time
}

type StudentPrizeState struct {
	CoinBalance int
	Catalogs    []PrizeCatalog
	Redemptions []PrizeRedemption
}

type TeacherPrizeState struct {
	Classrooms  []Classroom
	Prizes      []ClassroomPrize
	Redemptions []PrizeRedemption
}

type PrizeInput struct {
	ID             int64
	ClassroomID    string
	Name           string
	Description    string
	Emoji          string
	CoinPrice      int
	AvailableStock *int
	Active         bool
}

type PrizeNotification struct {
	ID            int64
	RedemptionID  int64
	Recipient     string
	AttemptNumber int
	StudentName   string
	ClassroomName string
	PrizeName     string
	PrizeEmoji    string
	CoinPrice     int
	PurchasedAt   time.Time
}
