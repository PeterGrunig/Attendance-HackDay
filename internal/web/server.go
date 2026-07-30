package web

import (
	"io/fs"
	"net/http"
	"path"

	"github.com/PeterGrunig/Attendance-HackDay/internal/view"
)

type AppStore interface {
	AdminStudentStore
	AdminTeacherStore
	AdminClassroomStore
	AdminUserStore
	AuthStore
	StudentStore
	TeacherStudentStore
	CanvasIntegrationStore
	AttendanceApprovalStore
	AttendanceExportStore
}

func NewRouter(appStore AppStore) http.Handler {
	adminStudentStore = appStore
	adminTeacherStore = appStore
	adminClassroomStore = appStore
	adminUserStore = appStore
	authStore = appStore
	studentStore = appStore
	teacherStudentStore = appStore
	canvasIntegrationStore = appStore
	attendanceApprovalStore = appStore
	attendanceExportStore = appStore
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(view.FS, "static")
	if err != nil {
		panic(err)
	}

	fileServer := http.FileServer(http.FS(staticFS))
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if extension := path.Ext(r.URL.Path); extension == ".css" || extension == ".js" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		fileServer.ServeHTTP(w, r)
	})
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler))

	// auth routes
	mux.HandleFunc("/", loginHandler)
	mux.HandleFunc("/login", loginHandler)
	mux.HandleFunc("POST /logout", logoutView)

	// student routes
	mux.Handle("GET /studentDashboard", RequireRole(http.HandlerFunc(studentView), "student"))
	mux.Handle("GET /shop", RequireRole(http.HandlerFunc(shopView), "student"))
	mux.Handle("POST /shop/buy", RequireRole(http.HandlerFunc(shopBuyView), "student"))
	mux.Handle("GET /avatar", RequireRole(http.HandlerFunc(avatarView), "student"))
	mux.Handle("POST /avatar/preview", RequireRole(http.HandlerFunc(avatarPreviewView), "student"))
	mux.Handle("POST /avatar", RequireRole(http.HandlerFunc(avatarSaveView), "student"))
	mux.Handle("POST /attendance", RequireRole(http.HandlerFunc(attendanceView), "student"))
	mux.Handle("GET /student/integrations/canvas", RequireRole(http.HandlerFunc(studentCanvasView), "student"))

	// teacher routes
	mux.Handle("GET /teacherDashboard", RequireRole(http.HandlerFunc(teacherView), "teacher"))
	mux.Handle("POST /teacherDashboard/edit", RequireRole(http.HandlerFunc(teacherEditView), "teacher"))
	mux.Handle("GET /teacherDashboard/addStudent", RequireRole(http.HandlerFunc(teacherAddStudent), "teacher"))
	mux.Handle("GET /attendance/approval", RequireRole(http.HandlerFunc(attendanceApprovalView), "teacher", "admin"))
	mux.Handle("POST /attendance/approval", RequireRole(http.HandlerFunc(attendanceApprovalSubmit), "teacher", "admin"))

	// admin routes
	mux.Handle("GET /adminDashboard", RequireRole(http.HandlerFunc(adminView), "admin"))
	mux.Handle("POST /adminDashboard/edit", RequireRole(http.HandlerFunc(adminEditView), "admin"))
	mux.Handle("POST /classrooms", RequireRole(http.HandlerFunc(createClassroomView), "admin"))
	mux.Handle("GET /classrooms", RequireRole(http.HandlerFunc(listClassroomsView), "admin"))
	mux.Handle("GET /classrooms/edit", RequireRole(http.HandlerFunc(editClassrooms), "admin"))
	mux.Handle("POST /classrooms/edit", RequireRole(http.HandlerFunc(saveClassrooms), "admin"))
	mux.Handle("GET /addTeacher", RequireRole(http.HandlerFunc(createTeacher), "admin"))
	mux.Handle("POST /addTeacher", RequireRole(http.HandlerFunc(teacherCreateSubmitView), "admin"))
	mux.Handle("GET /addStudent", RequireRole(http.HandlerFunc(createStudent), "admin", "teacher"))
	mux.Handle("POST /addStudent", RequireRole(http.HandlerFunc(studentCreateSubmitView), "admin", "teacher"))
	mux.Handle("GET /userSettings", RequireRole(http.HandlerFunc(userSettingsView), "admin"))
	mux.Handle("POST /userSettings/role", RequireRole(http.HandlerFunc(updateUserRoleView), "admin"))
	mux.Handle("GET /admin/integrations/canvas", RequireRole(http.HandlerFunc(canvasIntegrationView), "admin"))
	mux.Handle("GET /admin/integrations/canvas/preview", RequireRole(http.HandlerFunc(canvasIntegrationView), "admin"))
	mux.Handle("GET /admin/integrations/attendance", RequireRole(http.HandlerFunc(attendanceExportView), "admin"))
	mux.Handle("GET /admin/integrations/attendance/mappings", RequireRole(http.HandlerFunc(attendanceDestinationMappingsView), "admin"))

	return mux
}
