package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations/canvas"
)

func studentCanvasView(w http.ResponseWriter, r *http.Request) {
	state, ok := currentStudentState(w, r, StudentStore.LoadStudentAvatarState)
	if !ok {
		return
	}
	avatar := savedAvatarConfig(state.AvatarConfig, state.OwnedShopItemIDs)
	status, message, canMark := getTodayAttendanceState(state.Attendance, time.Now())
	data := PageData{
		Title: "School Portal", Username: state.User.Name, Coins: state.CoinBalance,
		AvatarImage: getAvatarImage(avatar), AvatarPreview: buildAvatarPreview(avatar),
		AttendanceStatus: status, AttendanceMessage: message, CanMarkAttendance: canMark,
		ActiveNav: "canvas", UseStudentCSS: true,
		ThemeBackgroundOptions: ownedThemeBackgroundOptionViews(state.OwnedShopItemIDs),
	}
	renderStudent(w, "studentCanvas.html", data)
}

// studentCanvasConnect starts an owner-bound OAuth flow. The callback may only
// persist the resulting credentials for the same authenticated student.
func studentCanvasConnect(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid Canvas connection form", http.StatusBadRequest)
		return
	}
	if !validCSRFToken(r, r.PostFormValue("csrf_token")) {
		log.Printf("student Canvas connect rejected: user_id=%q reason=invalid_csrf", user.UserID)
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	if canvasClient == nil || !canvasClient.StudentConfigured() {
		redirectStudentCanvas(w, r, "", studentCanvasConfigurationMessage())
		return
	}
	state, err := secureFlowToken()
	if err != nil {
		http.Error(w, "could not start Canvas connection", http.StatusInternalServerError)
		return
	}
	flow := canvasOAuthFlow{
		UserID:      user.UserID,
		Purpose:     canvasOAuthPurposeStudentLink,
		BaseURL:     canvasClient.DefaultBaseURL(),
		DisplayName: "My Canvas",
		ExpiresAt:   time.Now().Add(canvasFlowLifetime),
	}
	authorizationURL, err := canvasClient.IdentityAuthorizationURL(flow.BaseURL, state)
	if err != nil {
		redirectStudentCanvas(w, r, "", integrationMessage(err))
		return
	}
	canvasFlowMu.Lock()
	canvasOAuthFlows[state] = flow
	canvasFlowMu.Unlock()
	http.Redirect(w, r, authorizationURL, http.StatusSeeOther)
}

func defaultCanvasBaseURL() string {
	if canvasClient == nil {
		return ""
	}
	return canvasClient.DefaultBaseURL()
}

func studentCanvasConfigurationMessage() string {
	if canvasClient == nil {
		return "Canvas administrator setup is required."
	}
	missing := []string{}
	if strings.TrimSpace(canvasClient.BaseURL) == "" {
		missing = append(missing, "CANVAS_BASE_URL")
	}
	if strings.TrimSpace(canvasClient.ClientID) == "" {
		missing = append(missing, "CANVAS_CLIENT_ID")
	}
	if strings.TrimSpace(canvasClient.ClientSecret) == "" {
		missing = append(missing, "CANVAS_CLIENT_SECRET")
	}
	if strings.TrimSpace(canvasClient.RedirectURL) == "" {
		missing = append(missing, "CANVAS_REDIRECT_URL")
	}
	if len(missing) == 0 {
		return "CANVAS_BASE_URL must be a valid Canvas HTTP or HTTPS address."
	}
	return "Canvas administrator setup is required. Missing: " + strings.Join(missing, ", ") + "."
}

// completeStudentCanvasOAuth verifies the returned token against Canvas before
// crossing the encrypted, owner-scoped persistence boundary.
func completeStudentCanvasOAuth(w http.ResponseWriter, r *http.Request, user domain.User, flow canvasOAuthFlow, credentials canvas.Credentials) {
	config := canvas.ConnectionConfig{BaseURL: strings.TrimRight(flow.BaseURL, "/")}
	configJSON, _ := json.Marshal(config)
	credentialJSON, _ := json.Marshal(credentials)
	connection := integrations.Connection{
		ProviderKind: "canvas", Role: integrations.ConnectionRoleStudentLink,
		DisplayName: flow.DisplayName, Configuration: configJSON, Credentials: credentialJSON,
	}
	person, err := credentials.Identity()
	if err != nil {
		log.Printf("validate student Canvas link failed: user_id=%q error=%v", user.UserID, err)
		redirectStudentCanvas(w, r, "", integrationMessage(err))
		return
	}
	config.LinkedUserID = person.ExternalID
	config.LinkedUserName = person.Name
	connection.Configuration, _ = json.Marshal(config)
	connectionID, err := canvasIntegrationStore.UpsertStudentIntegrationConnection(r.Context(), user.UserID, connection)
	if err != nil {
		log.Printf("persist student Canvas link failed: user_id=%q error=%v", user.UserID, err)
		redirectStudentCanvas(w, r, "", integrationMessage(err))
		return
	}
	appendCanvasAudit(r, user.UserID, "student.connection.linked", connectionID, map[string]any{"provider": "canvas"})
	log.Printf("student Canvas account linked: connection_id=%d user_id=%q", connectionID, user.UserID)
	redirectStudentCanvas(w, r, "Canvas account connected.", "")
}

func studentCanvasDisconnect(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if !validCSRFToken(r, r.PostFormValue("csrf_token")) {
		log.Printf("student Canvas disconnect rejected: user_id=%q reason=invalid_csrf", user.UserID)
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	summary, err := canvasIntegrationStore.LoadStudentIntegrationSummary(r.Context(), user.UserID, "canvas")
	if err != nil && !isNoStudentCanvasConnection(err) {
		log.Printf("load student Canvas link for disconnect failed: user_id=%q error=%v", user.UserID, err)
		redirectStudentCanvas(w, r, "", "Canvas account could not be disconnected.")
		return
	}
	if err := canvasIntegrationStore.DisableStudentIntegrationConnection(r.Context(), user.UserID, "canvas"); err != nil && !isNoStudentCanvasConnection(err) {
		log.Printf("disconnect student Canvas link failed: user_id=%q error=%v", user.UserID, err)
		redirectStudentCanvas(w, r, "", "Canvas account could not be disconnected.")
		return
	}
	if summary.ID != 0 {
		appendCanvasAudit(r, user.UserID, "student.connection.disconnected", summary.ID, map[string]any{"provider": "canvas"})
		log.Printf("student Canvas account disconnected: connection_id=%d user_id=%q", summary.ID, user.UserID)
	}
	redirectStudentCanvas(w, r, "Canvas account disconnected.", "")
}

func isNoStudentCanvasConnection(err error) bool {
	return err == nil || errors.Is(err, sql.ErrNoRows)
}

func redirectStudentCanvas(w http.ResponseWriter, r *http.Request, message, errorMessage string) {
	values := url.Values{}
	if message != "" {
		values.Set("msg", message)
	}
	if errorMessage != "" {
		values.Set("error", errorMessage)
	}
	target := "/student/integrations/canvas"
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
