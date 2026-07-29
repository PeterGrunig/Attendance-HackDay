package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations/canvas"
)

const canvasFlowLifetime = 15 * time.Minute

var (
	canvasClient           *canvas.Client
	canvasIntegrationStore CanvasIntegrationStore
	canvasFlowMu           sync.Mutex
	canvasOAuthFlows       = map[string]canvasOAuthFlow{}
	canvasProposals        = map[string]canvasProposalFlow{}
)

type canvasOAuthFlow struct {
	AdminUserID string
	BaseURL     string
	AccountID   string
	DisplayName string
	ExpiresAt   time.Time
}

type canvasProposalFlow struct {
	AdminUserID string
	Proposal    domain.RosterImportProposal
	ExpiresAt   time.Time
}

type canvasCourseView struct {
	ID       string
	Name     string
	SISID    string
	Selected bool
}

type canvasConnectionView struct {
	ID          int64
	DisplayName string
	Status      string
	BaseURL     string
	UpdatedAt   string
}

type canvasIntegrationPageData struct {
	Title          string
	HeaderTitle    string
	HeaderSubtitle string
	HeaderBadge    string
	Configured     bool
	Connections    []canvasConnectionView
	ActiveID       int64
	Courses        []canvasCourseView
	Message        string
	Error          string
}

type canvasPreviewPageData struct {
	Title           string
	HeaderTitle     string
	HeaderSubtitle  string
	HeaderBadge     string
	Token           string
	ConnectionID    int64
	Classes         []integrations.Class
	People          []domain.RosterImportPerson
	ClassCount      int
	PeopleCount     int
	MembershipCount int
}

func canvasSync(w http.ResponseWriter, r *http.Request) {
	connectionID, err := formConnectionID(r)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/integrations/canvas/preview?connection_id="+strconv.FormatInt(connectionID, 10), http.StatusSeeOther)
}

func ConfigureCanvas(client *canvas.Client) {
	canvasClient = client
}

func canvasIntegrationView(w http.ResponseWriter, r *http.Request) {
	data := canvasIntegrationPageData{
		Title: "Canvas Integration", HeaderTitle: "Admin Tools",
		HeaderSubtitle: "Import organizational roster data from Canvas.",
		HeaderBadge:    "Admin View", Configured: canvasClient != nil && canvasClient.Configured(),
		Message: r.URL.Query().Get("msg"),
	}
	connections, err := canvasIntegrationStore.ListIntegrationConnections(r.Context(), "canvas")
	if err != nil {
		data.Error = "Could not load Canvas connections."
		log.Printf("load Canvas connections: %v", err)
		renderAdmin(w, "canvasIntegration.html", data)
		return
	}
	for _, connection := range connections {
		var config canvas.ConnectionConfig
		_ = json.Unmarshal(connection.Configuration, &config)
		data.Connections = append(data.Connections, canvasConnectionView{
			ID: connection.ID, DisplayName: connection.DisplayName, Status: connection.Status,
			BaseURL: config.BaseURL, UpdatedAt: connection.UpdatedAt.Format("Jan 2, 2006 3:04 PM"),
		})
	}
	if value := r.URL.Query().Get("connection_id"); value != "" {
		data.ActiveID, _ = strconv.ParseInt(value, 10, 64)
		connection, _, err := loadReadyCanvasConnection(r.Context(), data.ActiveID)
		if err != nil {
			data.Error = integrationMessage(err)
		} else {
			courses, err := canvasClient.ListCourses(r.Context(), connection)
			if err != nil {
				data.Error = integrationMessage(err)
			} else {
				var config canvas.ConnectionConfig
				_ = json.Unmarshal(connection.Configuration, &config)
				selected := stringSet(config.SelectedCourseIDs)
				for _, course := range courses {
					data.Courses = append(data.Courses, canvasCourseView{
						ID: course.ExternalID, Name: course.Name, SISID: course.SISID, Selected: selected[course.ExternalID],
					})
				}
				sort.Slice(data.Courses, func(i, j int) bool { return data.Courses[i].Name < data.Courses[j].Name })
			}
		}
	}
	renderAdmin(w, "canvasIntegration.html", data)
}

// canvasConnect starts an administrator-bound OAuth flow. The state record
// keeps tenant input off the callback query string and expires after 15 minutes.
func canvasConnect(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid connection form", http.StatusBadRequest)
		return
	}
	if canvasClient == nil || !canvasClient.Configured() {
		redirectCanvas(w, r, "Canvas OAuth environment settings are incomplete.")
		return
	}
	state, err := secureFlowToken()
	if err != nil {
		http.Error(w, "could not start Canvas connection", http.StatusInternalServerError)
		return
	}
	flow := canvasOAuthFlow{
		AdminUserID: user.UserID,
		BaseURL:     strings.TrimSpace(r.FormValue("base_url")),
		AccountID:   strings.TrimSpace(r.FormValue("account_id")),
		DisplayName: strings.TrimSpace(r.FormValue("display_name")),
		ExpiresAt:   time.Now().Add(canvasFlowLifetime),
	}
	if flow.DisplayName == "" {
		flow.DisplayName = "Canvas"
	}
	authorizationURL, err := canvasClient.AuthorizationURL(flow.BaseURL, state)
	if err != nil {
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	canvasFlowMu.Lock()
	canvasOAuthFlows[state] = flow
	canvasFlowMu.Unlock()
	http.Redirect(w, r, authorizationURL, http.StatusSeeOther)
}

func canvasOAuthCallback(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	state := r.URL.Query().Get("state")
	canvasFlowMu.Lock()
	flow, ok := canvasOAuthFlows[state]
	delete(canvasOAuthFlows, state)
	canvasFlowMu.Unlock()
	if !ok || flow.AdminUserID != user.UserID || time.Now().After(flow.ExpiresAt) {
		redirectCanvas(w, r, "Canvas authorization expired or was not started by this administrator.")
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		redirectCanvas(w, r, "Canvas authorization was not approved.")
		return
	}
	credentials, err := canvasClient.ExchangeCode(r.Context(), flow.BaseURL, r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("Canvas OAuth token exchange failed admin_user_id=%q: %v", user.UserID, err)
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	configJSON, _ := json.Marshal(canvas.ConnectionConfig{BaseURL: strings.TrimRight(flow.BaseURL, "/"), AccountID: flow.AccountID})
	credentialJSON, _ := json.Marshal(credentials)
	connection := integrations.Connection{
		ProviderKind: "canvas", Role: integrations.ConnectionRoleRosterSource,
		DisplayName: flow.DisplayName, Configuration: configJSON, Credentials: credentialJSON,
	}
	if err := canvasClient.ValidateConnection(r.Context(), connection); err != nil {
		log.Printf("validate Canvas connection admin_user_id=%q: %v", user.UserID, err)
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	connectionID, err := canvasIntegrationStore.CreateIntegrationConnection(r.Context(), connection, "active")
	if err != nil {
		log.Printf("persist Canvas connection admin_user_id=%q: %v", user.UserID, err)
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	appendCanvasAudit(r, user.UserID, "connection.created", connectionID, map[string]any{"provider": "canvas"})
	log.Printf("Canvas connection created connection_id=%d admin_user_id=%q", connectionID, user.UserID)
	http.Redirect(w, r, "/admin/integrations/canvas?connection_id="+strconv.FormatInt(connectionID, 10)+"&msg="+url.QueryEscape("Canvas connected."), http.StatusSeeOther)
}

func canvasDisconnect(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	connectionID, err := formConnectionID(r)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	if err := canvasIntegrationStore.DisableIntegrationConnection(r.Context(), connectionID); err != nil {
		http.Error(w, "could not disconnect Canvas", http.StatusInternalServerError)
		return
	}
	appendCanvasAudit(r, user.UserID, "connection.disconnected", connectionID, map[string]any{"provider": "canvas"})
	log.Printf("Canvas connection disabled connection_id=%d", connectionID)
	redirectCanvas(w, r, "Canvas disconnected.")
}

func canvasSelectCourses(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	connectionID, err := formConnectionID(r)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	connection, _, err := loadReadyCanvasConnection(r.Context(), connectionID)
	if err != nil {
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	var config canvas.ConnectionConfig
	if err := json.Unmarshal(connection.Configuration, &config); err != nil {
		redirectCanvas(w, r, "Canvas connection configuration is invalid.")
		return
	}
	config.SelectedCourseIDs = uniqueStrings(r.Form["course_id"])
	connection.Configuration, _ = json.Marshal(config)
	if err := canvasIntegrationStore.UpdateIntegrationConnection(r.Context(), connection, "active"); err != nil {
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	appendCanvasAudit(r, user.UserID, "roster.courses_selected", connectionID, map[string]any{
		"course_count": len(config.SelectedCourseIDs),
	})
	http.Redirect(w, r, "/admin/integrations/canvas/preview?connection_id="+strconv.FormatInt(connectionID, 10), http.StatusSeeOther)
}

func canvasPreview(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	connectionID, err := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	connection, _, err := loadReadyCanvasConnection(r.Context(), connectionID)
	if err != nil {
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	proposal, err := buildCanvasProposal(r, connection)
	if err != nil {
		log.Printf("build Canvas import preview connection_id=%d: %v", connectionID, err)
		redirectCanvas(w, r, integrationMessage(err))
		return
	}
	token, err := secureFlowToken()
	if err != nil {
		http.Error(w, "could not create import preview", http.StatusInternalServerError)
		return
	}
	canvasFlowMu.Lock()
	canvasProposals[token] = canvasProposalFlow{AdminUserID: user.UserID, Proposal: proposal, ExpiresAt: time.Now().Add(canvasFlowLifetime)}
	canvasFlowMu.Unlock()
	renderAdmin(w, "canvasPreview.html", canvasPreviewPageData{
		Title: "Preview Canvas Import", HeaderTitle: "Admin Tools",
		HeaderSubtitle: "Review matches before changing Attendance Quest.",
		HeaderBadge:    "Admin View", Token: token, ConnectionID: connectionID,
		Classes: proposal.Classes, People: proposal.People,
		ClassCount: len(proposal.Classes), PeopleCount: len(proposal.People),
		MembershipCount: len(proposal.Memberships),
	})
}

func canvasConfirmImport(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid import confirmation", http.StatusBadRequest)
		return
	}
	token := r.FormValue("proposal_token")
	canvasFlowMu.Lock()
	flow, ok := canvasProposals[token]
	delete(canvasProposals, token)
	canvasFlowMu.Unlock()
	if !ok || flow.AdminUserID != user.UserID || time.Now().After(flow.ExpiresAt) {
		redirectCanvas(w, r, "Import preview expired. Run the preview again.")
		return
	}
	decisions := map[string]string{}
	for _, person := range flow.Proposal.People {
		if person.AutoMatched {
			continue
		}
		decisions[person.Person.ExternalID] = strings.TrimSpace(r.FormValue("person_" + person.Person.ExternalID))
	}
	result, err := canvasIntegrationStore.ApplyRosterImport(r.Context(), flow.Proposal, decisions, user.UserID)
	if err != nil {
		log.Printf("apply Canvas roster import connection_id=%d admin_user_id=%q: %v", flow.Proposal.ConnectionID, user.UserID, err)
		http.Error(w, "could not import Canvas roster", http.StatusInternalServerError)
		return
	}
	message := fmt.Sprintf("Canvas import complete: %d classes, %d new accounts, %d memberships.",
		result.ClassesCreated, result.UsersCreated, result.MembershipsImported)
	log.Printf("Canvas roster import completed: connection_id=%d admin_user_id=%q classes_created=%d users_created=%d memberships_imported=%d",
		flow.Proposal.ConnectionID, user.UserID, result.ClassesCreated, result.UsersCreated, result.MembershipsImported)
	http.Redirect(w, r, "/admin/integrations/canvas?connection_id="+strconv.FormatInt(flow.Proposal.ConnectionID, 10)+"&msg="+url.QueryEscape(message), http.StatusSeeOther)
}

func buildCanvasProposal(r *http.Request, connection integrations.Connection) (domain.RosterImportProposal, error) {
	proposal := domain.RosterImportProposal{ConnectionID: connection.ID, CreatedAt: time.Now()}
	cursor := ""
	for {
		page, err := canvasClient.ReadRoster(r.Context(), connection, integrations.RosterRequest{Cursor: cursor, Limit: 100})
		if err != nil {
			return proposal, err
		}
		proposal.Schools = append(proposal.Schools, page.Schools...)
		proposal.Classes = append(proposal.Classes, page.Classes...)
		proposal.Memberships = append(proposal.Memberships, page.Memberships...)
		for _, person := range page.People {
			proposal.People = append(proposal.People, domain.RosterImportPerson{Person: person})
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	proposal.Schools = uniqueSchools(proposal.Schools)
	proposal.Classes = uniqueClasses(proposal.Classes)
	proposal.People = mergePeople(proposal.People)

	snapshot, err := canvasIntegrationStore.LoadRosterMatchSnapshot(r.Context(), connection.ID)
	if err != nil {
		return proposal, err
	}
	applyRosterMatches(&proposal, snapshot)
	return proposal, nil
}

// applyRosterMatches auto-applies only durable mapping/SIS identities. Email
// and local-ID comparisons remain suggestions that an admin must confirm.
func applyRosterMatches(proposal *domain.RosterImportProposal, snapshot domain.RosterMatchSnapshot) {
	external, sis := map[string]string{}, map[string]string{}
	for _, mapping := range snapshot.Mappings {
		if mapping.EntityKind != "user" {
			continue
		}
		if mapping.ConnectionID == proposal.ConnectionID {
			external[mapping.ExternalID] = mapping.LocalID
		}
		if mapping.SISID != "" {
			sis[strings.ToLower(mapping.SISID)] = mapping.LocalID
		}
	}
	for index := range proposal.People {
		item := &proposal.People[index]
		if localID := external[item.Person.ExternalID]; localID != "" {
			item.LocalID, item.AutoMatched = localID, true
			continue
		}
		if localID := sis[strings.ToLower(item.Person.SISID)]; item.Person.SISID != "" && localID != "" {
			item.LocalID, item.AutoMatched = localID, true
			continue
		}
		for _, user := range snapshot.Users {
			if user.Role != string(item.Person.Role) {
				continue
			}
			if item.Person.SISID != "" && strings.EqualFold(user.UserID, item.Person.SISID) {
				item.SuggestedID, item.SuggestionKind = user.UserID, "local ID"
				break
			}
			if item.Person.Email != "" && strings.EqualFold(user.Email, item.Person.Email) {
				item.SuggestedID, item.SuggestionKind = user.UserID, "email"
				break
			}
		}
	}
}

func loadReadyCanvasConnection(ctx context.Context, connectionID int64) (integrations.Connection, string, error) {
	connection, status, err := canvasIntegrationStore.LoadIntegrationConnection(ctx, connectionID)
	if err != nil {
		return connection, status, err
	}
	if connection.ProviderKind != "canvas" || status != "active" {
		return connection, status, integrations.ErrInvalidConfiguration
	}
	var config canvas.ConnectionConfig
	var credentials canvas.Credentials
	if err := json.Unmarshal(connection.Configuration, &config); err != nil {
		return connection, status, integrations.ErrInvalidConfiguration
	}
	if err := json.Unmarshal(connection.Credentials, &credentials); err != nil {
		return connection, status, integrations.ErrAuthentication
	}
	if !credentials.ExpiresAt.IsZero() && time.Until(credentials.ExpiresAt) < 2*time.Minute {
		credentials, err = canvasClient.Refresh(ctx, config.BaseURL, credentials)
		if err != nil {
			return connection, status, err
		}
		connection.Credentials, _ = json.Marshal(credentials)
		if err := canvasIntegrationStore.UpdateIntegrationConnection(ctx, connection, status); err != nil {
			return connection, status, err
		}
	}
	return connection, status, nil
}

func formConnectionID(r *http.Request) (int64, error) {
	if err := r.ParseForm(); err != nil {
		return 0, err
	}
	return strconv.ParseInt(r.FormValue("connection_id"), 10, 64)
}

func secureFlowToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func integrationMessage(err error) string {
	switch {
	case errors.Is(err, integrations.ErrEncryptionUnavailable):
		return "Integration credential encryption is unavailable. Configure INTEGRATION_CREDENTIAL_KEY."
	case errors.Is(err, integrations.ErrAuthentication):
		return "Canvas authentication failed. Reconnect Canvas."
	case errors.Is(err, integrations.ErrPermission):
		return "The connected Canvas administrator does not have roster permission."
	case errors.Is(err, integrations.ErrRateLimited), errors.Is(err, integrations.ErrTemporaryFailure):
		return "Canvas is temporarily unavailable. Try again later."
	default:
		return "Could not complete the Canvas integration request."
	}
}

func redirectCanvas(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/admin/integrations/canvas?msg="+url.QueryEscape(message), http.StatusSeeOther)
}

func appendCanvasAudit(r *http.Request, actorUserID, eventType string, connectionID int64, metadata map[string]any) {
	encoded, _ := json.Marshal(metadata)
	if _, err := canvasIntegrationStore.AppendIntegrationAuditEvent(r.Context(), domain.IntegrationAuditEvent{
		ActorUserID: actorUserID,
		EventType:   eventType,
		EntityType:  "integration_connection",
		EntityID:    strconv.FormatInt(connectionID, 10),
		Metadata:    encoded,
		OccurredAt:  time.Now(),
	}); err != nil {
		log.Printf("append Canvas audit event type=%q connection_id=%d: %v", eventType, connectionID, err)
	}
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen, result := map[string]bool{}, []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func uniqueSchools(values []integrations.School) []integrations.School {
	seen, result := map[string]bool{}, []integrations.School{}
	for _, value := range values {
		if !seen[value.ExternalID] {
			seen[value.ExternalID] = true
			result = append(result, value)
		}
	}
	return result
}

func uniqueClasses(values []integrations.Class) []integrations.Class {
	seen, result := map[string]bool{}, []integrations.Class{}
	for _, value := range values {
		if !seen[value.ExternalID] {
			seen[value.ExternalID] = true
			result = append(result, value)
		}
	}
	return result
}

func mergePeople(values []domain.RosterImportPerson) []domain.RosterImportPerson {
	byID := map[string]domain.RosterImportPerson{}
	for _, value := range values {
		current, ok := byID[value.Person.ExternalID]
		if !ok || value.Person.Role == integrations.PersonRoleTeacher {
			byID[value.Person.ExternalID] = value
			continue
		}
		current.Person.Active = current.Person.Active || value.Person.Active
		byID[value.Person.ExternalID] = current
	}
	result := make([]domain.RosterImportPerson, 0, len(byID))
	for _, value := range byID {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Person.Name < result[j].Person.Name })
	return result
}
