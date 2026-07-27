package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

type attendanceDestinationProviderView struct {
	Kind         string
	DisplayName  string
	Capabilities string
}

type attendanceDestinationConnectionView struct {
	ID           int64
	DisplayName  string
	Provider     string
	Status       string
	Capabilities string
	PresentCode  string
	AbsentCode   string
	UpdatedAt    string
}

type attendanceExportPageData struct {
	Title          string
	HeaderTitle    string
	HeaderSubtitle string
	HeaderBadge    string
	CSRFToken      string
	Providers      []attendanceDestinationProviderView
	Connections    []attendanceDestinationConnectionView
	Queue          []domain.AttendanceExportQueueItem
	Message        string
	Error          string
}

func attendanceExportView(w http.ResponseWriter, r *http.Request) {
	data := attendanceExportPageData{
		Title: "Attendance Exports", HeaderTitle: "Attendance Exports",
		HeaderSubtitle: "Configure a destination and monitor official attendance delivery.",
		HeaderBadge:    "Admin View", Message: r.URL.Query().Get("msg"),
		Error: r.URL.Query().Get("error"),
	}
	token, err := getCSRFToken(r)
	if err != nil {
		http.Error(w, "could not secure export forms", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = token

	if attendanceRegistry != nil {
		for _, metadata := range attendanceRegistry.List() {
			provider, providerErr := attendanceRegistry.Get(metadata.Kind)
			_, isDestination := provider.(integrations.AttendanceDestination)
			if providerErr != nil || !isDestination ||
				!metadata.Supports(integrations.CapabilityAttendanceWrite) ||
				(!metadata.Supports(integrations.CapabilityAttendanceSafeUpsert) &&
					!metadata.Supports(integrations.CapabilityAttendanceCorrections)) {
				continue
			}
			data.Providers = append(data.Providers, attendanceDestinationProviderView{
				Kind: metadata.Kind, DisplayName: metadata.DisplayName,
				Capabilities: capabilitySummary(metadata),
			})
		}
	}
	connections, err := attendanceExportStore.ListAttendanceDestinationConnections(r.Context())
	if err != nil {
		log.Printf("attendance export admin failed: stage=list_connections error=%v", err)
		data.Error = "Could not load attendance destinations."
		renderAdmin(w, "attendanceExports.html", data)
		return
	}
	for _, connection := range connections {
		var config integrations.AttendanceDestinationConfiguration
		_ = json.Unmarshal(connection.Configuration, &config)
		capabilities := "Adapter unavailable"
		if attendanceRegistry != nil {
			if provider, providerErr := attendanceRegistry.Get(connection.ProviderKind); providerErr == nil {
				capabilities = capabilitySummary(provider.Metadata())
			}
		}
		data.Connections = append(data.Connections, attendanceDestinationConnectionView{
			ID: connection.ID, DisplayName: connection.DisplayName,
			Provider: connection.ProviderKind, Status: connection.Status,
			Capabilities: capabilities,
			PresentCode:  config.Codes[integrations.AttendancePresent],
			AbsentCode:   config.Codes[integrations.AttendanceAbsent],
			UpdatedAt:    connection.UpdatedAt.Format("Jan 2, 2006 3:04 PM"),
		})
	}
	data.Queue, err = attendanceExportStore.ListAttendanceExportQueue(r.Context(), 100)
	if err != nil {
		log.Printf("attendance export admin failed: stage=list_queue error=%v", err)
		data.Error = "Could not load the attendance export queue."
	}
	renderAdmin(w, "attendanceExports.html", data)
}

// attendanceDestinationCreate validates both adapter behavior and code
// mappings before the encrypted connection can be enabled.
func attendanceDestinationCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if !parseSecureAdminForm(w, r) {
		return
	}
	if attendanceExporter == nil || attendanceRegistry == nil {
		redirectExports(w, r, "", "No attendance destination adapter is installed.")
		return
	}
	providerKind := strings.TrimSpace(r.PostFormValue("provider_kind"))
	displayName := strings.TrimSpace(r.PostFormValue("display_name"))
	if displayName == "" {
		displayName = providerKind + " attendance"
	}
	providerConfig := json.RawMessage(strings.TrimSpace(r.PostFormValue("provider_configuration")))
	credentials := json.RawMessage(strings.TrimSpace(r.PostFormValue("credentials")))
	if len(providerConfig) == 0 {
		providerConfig = json.RawMessage(`{}`)
	}
	if len(credentials) == 0 {
		credentials = json.RawMessage(`{}`)
	}
	if !json.Valid(providerConfig) || !json.Valid(credentials) {
		redirectExports(w, r, "", "Configuration and credentials must be valid JSON.")
		return
	}
	config, _ := json.Marshal(integrations.AttendanceDestinationConfiguration{
		Provider: providerConfig,
		Codes: integrations.AttendanceCodeMapping{
			integrations.AttendancePresent: strings.TrimSpace(r.PostFormValue("present_code")),
			integrations.AttendanceAbsent:  strings.TrimSpace(r.PostFormValue("absent_code")),
		},
	})
	connection := integrations.Connection{
		ProviderKind: providerKind, Role: integrations.ConnectionRoleAttendanceDestination,
		DisplayName: displayName, Configuration: config, Credentials: credentials,
	}
	if err := attendanceExporter.ValidateDestination(r.Context(), connection); err != nil {
		log.Printf("attendance destination validation failed: provider=%q admin_user_id=%q error=%v", providerKind, user.UserID, err)
		redirectExports(w, r, "", integrationMessage(err))
		return
	}
	connectionID, err := attendanceExportStore.CreateIntegrationConnection(r.Context(), connection, "disabled")
	if err != nil {
		log.Printf("attendance destination persistence failed: provider=%q admin_user_id=%q error=%v", providerKind, user.UserID, err)
		redirectExports(w, r, "", integrationMessage(err))
		return
	}
	if err := attendanceExportStore.EnableAttendanceDestination(r.Context(), connectionID); err != nil {
		_ = attendanceExportStore.SetAttendanceDestinationStatus(r.Context(), connectionID, "error")
		redirectExports(w, r, "", "Destination was saved but could not be enabled.")
		return
	}
	appendAttendanceExportAudit(r, user.UserID, "attendance.destination_enabled", "integration_connection", fmt.Sprint(connectionID), map[string]any{"provider": providerKind})
	log.Printf("attendance destination enabled: connection_id=%d provider=%q admin_user_id=%q", connectionID, providerKind, user.UserID)
	redirectExports(w, r, "Attendance destination connected and enabled.", "")
}

func attendanceDestinationValidate(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if !parseSecureAdminForm(w, r) {
		return
	}
	if attendanceExporter == nil {
		redirectExports(w, r, "", "No attendance destination adapter is installed.")
		return
	}
	connectionID, err := strconv.ParseInt(r.PostFormValue("connection_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	connection, _, err := attendanceExportStore.LoadIntegrationConnection(r.Context(), connectionID)
	if err == nil {
		err = attendanceExporter.ValidateDestination(r.Context(), connection)
	}
	if err != nil {
		_ = attendanceExportStore.SetAttendanceDestinationStatus(r.Context(), connectionID, "error")
		appendAttendanceExportAudit(r, user.UserID, "attendance.destination_validation_failed", "integration_connection", fmt.Sprint(connectionID), map[string]any{"error": err.Error()})
		redirectExports(w, r, "", integrationMessage(err))
		return
	}
	if err := attendanceExportStore.EnableAttendanceDestination(r.Context(), connectionID); err != nil {
		redirectExports(w, r, "", "Destination validated but could not be enabled.")
		return
	}
	appendAttendanceExportAudit(r, user.UserID, "attendance.destination_validated", "integration_connection", fmt.Sprint(connectionID), nil)
	redirectExports(w, r, "Destination is healthy and enabled.", "")
}

func attendanceDestinationDisable(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if !parseSecureAdminForm(w, r) {
		return
	}
	connectionID, err := strconv.ParseInt(r.PostFormValue("connection_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	if err := attendanceExportStore.SetAttendanceDestinationStatus(r.Context(), connectionID, "disabled"); err != nil {
		redirectExports(w, r, "", "Could not disable destination.")
		return
	}
	appendAttendanceExportAudit(r, user.UserID, "attendance.destination_disabled", "integration_connection", fmt.Sprint(connectionID), nil)
	redirectExports(w, r, "Attendance destination disabled.", "")
}

func attendanceExportRetry(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if !parseSecureAdminForm(w, r) {
		return
	}
	if attendanceExporter == nil {
		redirectExports(w, r, "", "Attendance export service is unavailable.")
		return
	}
	batchID, err := strconv.ParseInt(r.PostFormValue("batch_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid batch", http.StatusBadRequest)
		return
	}
	appendAttendanceExportAudit(r, user.UserID, "attendance.export_manual_retry", "attendance_batch", fmt.Sprint(batchID), nil)
	if err := attendanceExporter.Retry(r.Context(), batchID); err != nil {
		log.Printf("manual attendance export failed: batch_id=%d admin_user_id=%q error=%v", batchID, user.UserID, err)
		redirectExports(w, r, "", "Manual retry did not record attendance: "+integrationMessage(err))
		return
	}
	redirectExports(w, r, "Manual attendance export completed.", "")
}

func parseSecureAdminForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return false
	}
	if !validCSRFToken(r, r.PostFormValue("csrf_token")) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return false
	}
	return true
}

func appendAttendanceExportAudit(r *http.Request, actor, eventType, entityType, entityID string, metadata any) {
	data := json.RawMessage(`{}`)
	if metadata != nil {
		data, _ = json.Marshal(metadata)
	}
	if _, err := attendanceExportStore.AppendIntegrationAuditEvent(r.Context(), domain.IntegrationAuditEvent{
		ActorUserID: actor, EventType: eventType, EntityType: entityType,
		EntityID: entityID, Metadata: data, OccurredAt: time.Now(),
	}); err != nil {
		log.Printf("attendance export audit failed: event_type=%q entity_id=%q error=%v", eventType, entityID, err)
	}
}

func capabilitySummary(metadata integrations.ProviderMetadata) string {
	var labels []string
	if metadata.Supports(integrations.CapabilityAttendanceSafeUpsert) {
		labels = append(labels, "safe upsert")
	}
	if metadata.Supports(integrations.CapabilityAttendanceCorrections) {
		labels = append(labels, "corrections")
	}
	if len(labels) == 0 {
		return "attendance write only"
	}
	return strings.Join(labels, ", ")
}

func redirectExports(w http.ResponseWriter, r *http.Request, message, errorMessage string) {
	values := url.Values{}
	if message != "" {
		values.Set("msg", message)
	}
	if errorMessage != "" {
		values.Set("error", errorMessage)
	}
	target := "/admin/integrations/attendance"
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
