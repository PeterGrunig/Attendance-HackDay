package web

import (
	"context"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

type AttendanceApprovalStore interface {
	ListAttendanceApprovalClasses(context.Context, string, string) ([]domain.Classroom, error)
	LoadAttendanceApproval(context.Context, string, string, string, time.Time) (domain.AttendanceApproval, error)
	ApproveAttendance(context.Context, domain.AttendanceApprovalRequest) (domain.AttendanceBatch, error)
}

var attendanceApprovalStore AttendanceApprovalStore
