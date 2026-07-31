package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	datastore "github.com/PeterGrunig/Attendance-HackDay/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type securityTestAuthStore struct {
	user domain.User
	err  error
}

func (s securityTestAuthStore) FindUserByEmail(context.Context, string) (domain.User, error) {
	return s.user, s.err
}
func (s securityTestAuthStore) FindUserByID(context.Context, string) (domain.User, error) {
	return s.user, s.err
}

func resetSecurityTestGlobals(t *testing.T) {
	t.Helper()
	previousAuthStore := authStore
	previousAdminStudentStore := adminStudentStore
	previousAdminTeacherStore := adminTeacherStore
	previousAdminClassroomStore := adminClassroomStore
	previousAdminUserStore := adminUserStore
	previousTeacherStudentStore := teacherStudentStore
	previousStudentStore := studentStore
	previousCanvasIntegrationStore := canvasIntegrationStore
	previousAttendanceApprovalStore := attendanceApprovalStore
	previousAttendanceExportStore := attendanceExportStore
	sessionMu.Lock()
	previousSessions := sessionStore
	sessionStore = map[string]sessionRecord{}
	sessionMu.Unlock()
	t.Cleanup(func() {
		authStore = previousAuthStore
		adminStudentStore = previousAdminStudentStore
		adminTeacherStore = previousAdminTeacherStore
		adminClassroomStore = previousAdminClassroomStore
		adminUserStore = previousAdminUserStore
		teacherStudentStore = previousTeacherStudentStore
		studentStore = previousStudentStore
		canvasIntegrationStore = previousCanvasIntegrationStore
		attendanceApprovalStore = previousAttendanceApprovalStore
		attendanceExportStore = previousAttendanceExportStore
		sessionMu.Lock()
		sessionStore = previousSessions
		sessionMu.Unlock()
	})
}

func TestLoginHandlerAuthenticatesAndRejectsInvalidPasswords(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}

	t.Run("valid student credentials create session and redirect", func(t *testing.T) {
		resetSecurityTestGlobals(t)
		authStore = securityTestAuthStore{user: domain.User{
			UserID: "student-1", Role: "student", Email: "student@example.com", PasswordHash: string(hash),
		}}
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=student%40example.com&password=correct-password"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()

		loginHandler(recorder, request)
		if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/studentDashboard" {
			t.Fatalf("response = %d location %q", recorder.Code, recorder.Header().Get("Location"))
		}
		if cookies := recorder.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "session" {
			t.Fatalf("session cookies = %#v", cookies)
		}
	})

	t.Run("invalid password returns generic error without session", func(t *testing.T) {
		resetSecurityTestGlobals(t)
		authStore = securityTestAuthStore{user: domain.User{
			UserID: "student-1", Role: "student", Email: "student@example.com", PasswordHash: string(hash),
		}}
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=student%40example.com&password=wrong-password"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()

		loginHandler(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Invalid email or password.") {
			t.Fatalf("response = %d body %q", recorder.Code, recorder.Body.String())
		}
		if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
			t.Fatalf("invalid login set cookies: %#v", cookies)
		}
	})
}

func TestSessionLifecycleAndCSRFTokens(t *testing.T) {
	resetSecurityTestGlobals(t)
	recorder := httptest.NewRecorder()
	if err := createSession(recorder, "student-1"); err != nil {
		t.Fatalf("createSession: %v", err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "session" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("session cookie = %#v", cookie)
	}

	request := httptest.NewRequest(http.MethodGet, "/studentDashboard", nil)
	request.AddCookie(cookie)
	userID, err := getSessionUserID(request)
	if err != nil || userID != "student-1" {
		t.Fatalf("getSessionUserID = %q, %v", userID, err)
	}
	csrfToken, err := getCSRFToken(request)
	if err != nil || csrfToken == "" {
		t.Fatalf("getCSRFToken = %q, %v", csrfToken, err)
	}
	if !validCSRFToken(request, csrfToken) || validCSRFToken(request, csrfToken+"x") || validCSRFToken(request, "") {
		t.Fatal("CSRF validation accepted an invalid token or rejected the valid token")
	}

	clearRecorder := httptest.NewRecorder()
	clearSessionUser(clearRecorder, request)
	if _, err := getSessionUserID(request); err == nil {
		t.Fatal("cleared session remained valid")
	}
	cleared := clearRecorder.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge != -1 {
		t.Fatalf("cleared cookie = %#v", cleared)
	}
}

func TestExpiredSessionIsRejectedAndRemoved(t *testing.T) {
	resetSecurityTestGlobals(t)
	sessionMu.Lock()
	sessionStore["expired"] = sessionRecord{UserID: "student-1", CSRFToken: "csrf", ExpiresAt: time.Now().Add(-time.Minute)}
	sessionMu.Unlock()
	request := httptest.NewRequest(http.MethodGet, "/studentDashboard", nil)
	request.AddCookie(&http.Cookie{Name: "session", Value: "expired"})

	if _, err := getSessionUserID(request); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("getSessionUserID error = %v, want expired", err)
	}
	sessionMu.RLock()
	_, exists := sessionStore["expired"]
	sessionMu.RUnlock()
	if exists {
		t.Fatal("expired session was not removed")
	}
}

func TestRequireRoleEnforcesAuthenticationAndAuthorization(t *testing.T) {
	t.Run("allowed role reaches handler with user context", func(t *testing.T) {
		resetSecurityTestGlobals(t)
		authStore = securityTestAuthStore{user: domain.User{UserID: "teacher-1", Role: "teacher"}}
		sessionMu.Lock()
		sessionStore["valid"] = sessionRecord{UserID: "teacher-1", ExpiresAt: time.Now().Add(time.Hour)}
		sessionMu.Unlock()
		request := httptest.NewRequest(http.MethodGet, "/teacherDashboard", nil)
		request.AddCookie(&http.Cookie{Name: "session", Value: "valid"})
		recorder := httptest.NewRecorder()
		handler := RequireRole(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := authenticatedUser(r)
			if !ok || user.UserID != "teacher-1" {
				t.Fatalf("authenticated user = %#v, %t", user, ok)
			}
			w.WriteHeader(http.StatusNoContent)
		}), "teacher")

		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", recorder.Code)
		}
	})

	t.Run("wrong role is forbidden", func(t *testing.T) {
		resetSecurityTestGlobals(t)
		authStore = securityTestAuthStore{user: domain.User{UserID: "student-1", Role: "student"}}
		sessionMu.Lock()
		sessionStore["valid"] = sessionRecord{UserID: "student-1", ExpiresAt: time.Now().Add(time.Hour)}
		sessionMu.Unlock()
		request := httptest.NewRequest(http.MethodGet, "/adminDashboard", nil)
		request.AddCookie(&http.Cookie{Name: "session", Value: "valid"})
		recorder := httptest.NewRecorder()

		RequireRole(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("forbidden request reached handler")
		}), "admin").ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", recorder.Code)
		}
	})

	t.Run("missing session redirects to login", func(t *testing.T) {
		resetSecurityTestGlobals(t)
		recorder := httptest.NewRecorder()
		RequireLogin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("unauthenticated request reached handler")
		})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/studentDashboard", nil))
		if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/login" {
			t.Fatalf("response = %d location %q", recorder.Code, recorder.Header().Get("Location"))
		}
	})

	t.Run("deleted user clears session", func(t *testing.T) {
		resetSecurityTestGlobals(t)
		authStore = securityTestAuthStore{err: datastore.ErrUserNotFound}
		sessionMu.Lock()
		sessionStore["orphan"] = sessionRecord{UserID: "deleted", ExpiresAt: time.Now().Add(time.Hour)}
		sessionMu.Unlock()
		request := httptest.NewRequest(http.MethodGet, "/studentDashboard", nil)
		request.AddCookie(&http.Cookie{Name: "session", Value: "orphan"})
		recorder := httptest.NewRecorder()

		RequireLogin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("deleted user reached handler")
		})).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", recorder.Code)
		}
		sessionMu.RLock()
		_, exists := sessionStore["orphan"]
		sessionMu.RUnlock()
		if exists {
			t.Fatal("deleted user's session was not cleared")
		}
	})
}

func TestRouterServesStaticAssetsWithCachePolicy(t *testing.T) {
	resetSecurityTestGlobals(t)
	router := NewRouter(nil)
	for _, tc := range []struct {
		path        string
		wantCache   string
		contentType string
	}{
		{path: "/static/css/main.css", wantCache: "no-cache", contentType: "text/css"},
		{path: "/static/images/Gopher.PNG", wantCache: "public, max-age=3600", contentType: "image/"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if got := recorder.Header().Get("Cache-Control"); got != tc.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, tc.wantCache)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", got, tc.contentType)
			}
		})
	}
}

func TestRequireRoleReturnsServerErrorWhenUserLookupFails(t *testing.T) {
	resetSecurityTestGlobals(t)
	authStore = securityTestAuthStore{err: errors.New("database unavailable")}
	sessionMu.Lock()
	sessionStore["valid"] = sessionRecord{UserID: "student-1", ExpiresAt: time.Now().Add(time.Hour)}
	sessionMu.Unlock()
	request := httptest.NewRequest(http.MethodGet, "/studentDashboard", nil)
	request.AddCookie(&http.Cookie{Name: "session", Value: "valid"})
	recorder := httptest.NewRecorder()

	RequireRole(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("failed lookup reached handler")
	}), "student").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}
