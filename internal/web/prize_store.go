package web

import (
	"context"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

type PrizeStore interface {
	LoadStudentPrizeState(context.Context, string) (domain.StudentPrizeState, error)
	LoadTeacherPrizeState(context.Context, string) (domain.TeacherPrizeState, error)
	PurchaseClassroomPrize(context.Context, string, int64, time.Time) (domain.PrizeRedemption, error)
	CreateClassroomPrize(context.Context, string, domain.PrizeInput) (int64, error)
	UpdateClassroomPrize(context.Context, string, domain.PrizeInput) error
	FulfillPrizeRedemption(context.Context, string, int64, time.Time) error
	CancelPrizeRedemption(context.Context, string, int64, time.Time) error
}

var prizeStore PrizeStore
