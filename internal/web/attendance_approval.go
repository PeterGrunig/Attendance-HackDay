package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	datastore "github.com/PeterGrunig/Attendance-HackDay/internal/store"
)

type AttendanceApprovalPageData struct {
	Title          string
	HeaderTitle    string
	HeaderSubtitle string
	HeaderBadge    string
	CSRFToken      string
	Classes        []domain.Classroom
	SelectedClass  string
	DateValue      string
	Approval       domain.AttendanceApproval
	StateLabel     string
	StateClass     string
	Notice         string
	Error          string
}

// attendanceApprovalView assembles the authorized class list and one daily
// roster review without changing attendance or reward data.
func attendanceApprovalView(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if attendanceApprovalStore == nil {
		http.Error(w, "attendance approval store is not configured", http.StatusInternalServerError)
		return
	}

	classes, err := attendanceApprovalStore.ListAttendanceApprovalClasses(r.Context(), user.UserID, user.Role)
	if err != nil {
		log.Printf("attendance approval page failed: user_id=%q stage=list_classes error=%v", user.UserID, err)
		http.Error(w, "could not load attendance classes", http.StatusInternalServerError)
		return
	}

	date, err := approvalDate(r.URL.Query().Get("date"))
	if err != nil {
		http.Error(w, "invalid attendance date", http.StatusBadRequest)
		return
	}
	selectedClass := r.URL.Query().Get("classroom")
	if selectedClass == "" && len(classes) > 0 {
		selectedClass = classes[0].ID
	}
	csrfToken, err := getCSRFToken(r)
	if err != nil {
		log.Printf("attendance approval page failed: user_id=%q stage=csrf error=%v", user.UserID, err)
		http.Error(w, "could not secure attendance form", http.StatusInternalServerError)
		return
	}

	data := AttendanceApprovalPageData{
		Title:          "Attendance Approval",
		HeaderTitle:    "Daily Attendance",
		HeaderSubtitle: "Review student check-ins and approve the complete class roster.",
		HeaderBadge:    roleHeaderBadge(user.Role),
		CSRFToken:      csrfToken,
		Classes:        classes,
		SelectedClass:  selectedClass,
		DateValue:      date.Format("2006-01-02"),
		Notice:         r.URL.Query().Get("notice"),
		Error:          r.URL.Query().Get("error"),
		StateLabel:     "Awaiting approval",
		StateClass:     "awaiting",
	}
	if selectedClass != "" {
		approval, loadErr := attendanceApprovalStore.LoadAttendanceApproval(r.Context(), user.UserID, user.Role, selectedClass, date)
		if errors.Is(loadErr, datastore.ErrAttendanceClassForbidden) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if loadErr != nil {
			log.Printf("attendance approval page failed: user_id=%q classroom_id=%q date=%s stage=load_roster error=%v",
				user.UserID, selectedClass, data.DateValue, loadErr)
			http.Error(w, "could not load attendance roster", http.StatusInternalServerError)
			return
		}
		data.Approval = approval
		data.StateLabel, data.StateClass = attendanceWorkflowDisplay(approval.WorkflowState)
	}
	renderAttendanceApproval(w, user.Role, data)
}

// attendanceApprovalSubmit validates the entire posted roster before asking
// the store to create a new immutable approval version.
func attendanceApprovalSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if attendanceApprovalStore == nil {
		http.Error(w, "attendance approval store is not configured", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid attendance form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r, r.PostFormValue("csrf_token")) {
		log.Printf("attendance approval rejected: user_id=%q reason=invalid_csrf", user.UserID)
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	date, err := approvalDate(r.PostFormValue("date"))
	if err != nil {
		http.Error(w, "invalid attendance date", http.StatusBadRequest)
		return
	}
	classroomID := r.PostFormValue("classroom")
	studentIDs := r.PostForm["student_id"]
	statusValues := r.PostForm["status"]
	if classroomID == "" || len(studentIDs) == 0 || len(studentIDs) != len(statusValues) {
		redirectAttendanceError(w, r, classroomID, date, "The roster was incomplete. Reload and try again.")
		return
	}
	statuses := make(map[string]string, len(studentIDs))
	for index, studentID := range studentIDs {
		status := statusValues[index]
		if studentID == "" || (status != "present" && status != "absent") {
			redirectAttendanceError(w, r, classroomID, date, "Every student must be marked present or absent.")
			return
		}
		statuses[studentID] = status
	}
	if len(statuses) != len(studentIDs) {
		redirectAttendanceError(w, r, classroomID, date, "The roster contained duplicate students. Reload and try again.")
		return
	}

	batch, err := attendanceApprovalStore.ApproveAttendance(r.Context(), domain.AttendanceApprovalRequest{
		ClassroomID: classroomID,
		Date:        date,
		ActorUserID: user.UserID,
		ActorRole:   user.Role,
		Statuses:    statuses,
	})
	if errors.Is(err, datastore.ErrAttendanceClassForbidden) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, datastore.ErrAttendanceRosterChanged) || errors.Is(err, datastore.ErrInvalidAttendanceStatus) {
		redirectAttendanceError(w, r, classroomID, date, "The roster changed. Review the latest list before approving.")
		return
	}
	if err != nil {
		log.Printf("attendance approval failed: user_id=%q classroom_id=%q date=%s error=%v",
			user.UserID, classroomID, date.Format("2006-01-02"), err)
		redirectAttendanceError(w, r, classroomID, date, "Attendance could not be approved.")
		return
	}

	message := "Attendance approved locally."
	if batch.WorkflowState == "correction_pending" {
		message = "Attendance correction saved locally."
	}
	target := "/attendance/approval?classroom=" + url.QueryEscape(classroomID) +
		"&date=" + url.QueryEscape(date.Format("2006-01-02")) +
		"&notice=" + url.QueryEscape(message)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func approvalDate(value string) (time.Time, error) {
	if value == "" {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), nil
	}
	return time.Parse("2006-01-02", value)
}

func attendanceWorkflowDisplay(state string) (string, string) {
	switch state {
	case "approved":
		return "Approved locally", "approved"
	case "export_pending":
		return "Pending export", "pending-export"
	case "exported":
		return "Officially recorded", "recorded"
	case "export_failed":
		return "Export failed", "failed"
	case "correction_pending":
		return "Correction pending", "correction"
	default:
		return "Awaiting approval", "awaiting"
	}
}

func roleHeaderBadge(role string) string {
	if role == "admin" {
		return "Admin View"
	}
	return "Teacher View"
}

func renderAttendanceApproval(w http.ResponseWriter, role string, data AttendanceApprovalPageData) {
	if role == "admin" {
		renderAdmin(w, "attendanceApproval.html", data)
		return
	}
	renderTeacher(w, "attendanceApproval.html", data)
}

func redirectAttendanceError(w http.ResponseWriter, r *http.Request, classroomID string, date time.Time, message string) {
	target := "/attendance/approval?classroom=" + url.QueryEscape(classroomID) +
		"&date=" + url.QueryEscape(date.Format("2006-01-02")) +
		"&error=" + url.QueryEscape(message)
	http.Redirect(w, r, target, http.StatusSeeOther)
}
