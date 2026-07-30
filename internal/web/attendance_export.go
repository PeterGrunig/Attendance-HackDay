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

type attendanceMappingPageData struct {
	Title          string
	HeaderTitle    string
	HeaderSubtitle string
	HeaderBadge    string
	CSRFToken      string
	ConnectionID   int64
	ConnectionName string
	ProviderKind   string
	Mappings       []domain.AttendanceDestinationMapping
	MissingCount   int
	Message        string
	Error          string
}

// attendanceExportView presents the planned official-records workflow without
// loading destinations, credentials, mappings, or delivery failures.
func attendanceExportView(w http.ResponseWriter, _ *http.Request) {
	renderAdmin(w, "adminIntegrationPlaceholder.html", adminIntegrationPlaceholderData{
		Title:           "Official Records",
		HeaderTitle:     "School Connections",
		HeaderSubtitle:  "Preview how approved attendance could reach an official system.",
		HeaderBadge:     "Presentation",
		PlaceholderKind: "records",
	})
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
	if err := attendanceExportStore.SeedAttendanceDestinationMappings(r.Context(), connectionID); err != nil {
		log.Printf("attendance destination SIS mapping seed failed: connection_id=%d error=%v", connectionID, err)
	}
	missing, err := attendanceExportStore.CountMissingAttendanceDestinationMappings(r.Context(), connectionID)
	if err != nil {
		redirectExports(w, r, "", "Destination was saved but mappings could not be checked.")
		return
	}
	if missing > 0 {
		appendAttendanceExportAudit(r, user.UserID, "attendance.destination_created", "integration_connection", fmt.Sprint(connectionID), map[string]any{"provider": providerKind, "missing_mappings": missing})
		redirectAttendanceMappings(w, r, connectionID, "Destination validated. Complete the remaining identifiers before enabling it.", "")
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
	if err := attendanceExportStore.SeedAttendanceDestinationMappings(r.Context(), connectionID); err != nil {
		log.Printf("attendance destination SIS mapping seed failed: connection_id=%d error=%v", connectionID, err)
	}
	missing, err := attendanceExportStore.CountMissingAttendanceDestinationMappings(r.Context(), connectionID)
	if err != nil {
		redirectExports(w, r, "", "Destination validated but mappings could not be checked.")
		return
	}
	if missing > 0 {
		redirectAttendanceMappings(w, r, connectionID, "Connection is healthy. Complete the remaining identifiers before enabling it.", "")
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

func attendanceDestinationMappingsView(w http.ResponseWriter, _ *http.Request) {
	renderAdmin(w, "adminIntegrationPlaceholder.html", adminIntegrationPlaceholderData{
		Title:           "Identifier Matching",
		HeaderTitle:     "School Connections",
		HeaderSubtitle:  "Preview how local records could match a school's official identifiers.",
		HeaderBadge:     "Presentation",
		PlaceholderKind: "mapping",
	})
}

// attendanceDestinationMappingsSave validates the adapter-specific identifier
// shape, saves every supplied mapping, and enables only a complete destination.
func attendanceDestinationMappingsSave(w http.ResponseWriter, r *http.Request) {
	user, _ := authenticatedUser(r)
	if !parseSecureAdminForm(w, r) {
		return
	}
	if attendanceRegistry == nil || attendanceExporter == nil {
		http.Error(w, "attendance export service is unavailable", http.StatusServiceUnavailable)
		return
	}
	connectionID, err := strconv.ParseInt(r.PostFormValue("connection_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid connection", http.StatusBadRequest)
		return
	}
	connection, _, err := attendanceExportStore.LoadIntegrationConnection(r.Context(), connectionID)
	if err != nil || connection.Role != integrations.ConnectionRoleAttendanceDestination {
		http.Error(w, "attendance destination not found", http.StatusNotFound)
		return
	}
	provider, err := attendanceRegistry.Get(connection.ProviderKind)
	if err != nil {
		redirectAttendanceMappings(w, r, connectionID, "", integrationMessage(err))
		return
	}
	validator, _ := provider.(integrations.DestinationMappingValidator)
	kinds := r.PostForm["entity_kind"]
	localIDs := r.PostForm["local_id"]
	localNames := r.PostForm["local_name"]
	sisIDs := r.PostForm["sis_id"]
	externalIDs := r.PostForm["external_id"]
	if len(kinds) != len(localIDs) || len(kinds) != len(localNames) ||
		len(kinds) != len(sisIDs) || len(kinds) != len(externalIDs) {
		http.Error(w, "incomplete mapping form", http.StatusBadRequest)
		return
	}
	for index, externalID := range externalIDs {
		externalID = strings.TrimSpace(externalID)
		if externalID == "" {
			continue
		}
		if validator != nil {
			if err := validator.ValidateExternalMapping(kinds[index], externalID); err != nil {
				redirectAttendanceMappings(w, r, connectionID, "", integrationMessage(err))
				return
			}
		}
		mapping := domain.AttendanceDestinationMapping{
			EntityKind: kinds[index], LocalID: localIDs[index],
			LocalName: localNames[index], SISID: sisIDs[index], ExternalID: externalID,
		}
		if err := attendanceExportStore.SetAttendanceDestinationMapping(r.Context(), connectionID, mapping); err != nil {
			log.Printf("attendance destination mapping save failed: connection_id=%d kind=%q local_id=%q error=%v",
				connectionID, mapping.EntityKind, mapping.LocalID, err)
			redirectAttendanceMappings(w, r, connectionID, "", "Could not save identifier mappings.")
			return
		}
	}
	missing, err := attendanceExportStore.CountMissingAttendanceDestinationMappings(r.Context(), connectionID)
	if err != nil {
		redirectAttendanceMappings(w, r, connectionID, "", "Could not verify identifier mappings.")
		return
	}
	appendAttendanceExportAudit(r, user.UserID, "attendance.destination_mappings_updated", "integration_connection", fmt.Sprint(connectionID), map[string]any{"missing_mappings": missing})
	if missing > 0 {
		redirectAttendanceMappings(w, r, connectionID, fmt.Sprintf("Mappings saved. %d identifiers still need attention.", missing), "")
		return
	}
	if err := attendanceExporter.ValidateDestination(r.Context(), connection); err != nil {
		_ = attendanceExportStore.SetAttendanceDestinationStatus(r.Context(), connectionID, "error")
		redirectAttendanceMappings(w, r, connectionID, "", integrationMessage(err))
		return
	}
	if err := attendanceExportStore.EnableAttendanceDestination(r.Context(), connectionID); err != nil {
		redirectAttendanceMappings(w, r, connectionID, "", "Mappings are complete but the destination could not be enabled.")
		return
	}
	appendAttendanceExportAudit(r, user.UserID, "attendance.destination_enabled", "integration_connection", fmt.Sprint(connectionID), map[string]any{"provider": connection.ProviderKind})
	redirectExports(w, r, "Identifier mappings complete. Attendance destination enabled.", "")
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

func redirectAttendanceMappings(w http.ResponseWriter, r *http.Request, connectionID int64, message, errorMessage string) {
	values := url.Values{"connection_id": {strconv.FormatInt(connectionID, 10)}}
	if message != "" {
		values.Set("msg", message)
	}
	if errorMessage != "" {
		values.Set("error", errorMessage)
	}
	http.Redirect(w, r, "/admin/integrations/attendance/mappings?"+values.Encode(), http.StatusSeeOther)
}
