package edfi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

func TestUpsertAttendanceBatchWritesAbsenceEvent(t *testing.T) {
	var received attendancePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/token":
			_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "token"})
		case "/api/data/v3/ed-fi/studentSchoolAttendanceEvents":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Fatalf("decode attendance payload: %v", err)
			}
			w.Header().Set("Location", serverURL(r)+"/api/data/v3/ed-fi/studentSchoolAttendanceEvents/record-123")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New()
	client.HTTPClient = server.Client()
	result, err := client.UpsertAttendanceBatch(context.Background(), testConnection(server.URL), integrations.AttendanceBatch{
		SchoolExternalID: "255901001",
		SchoolDate:       time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC),
		Entries: []integrations.AttendanceEntry{{
			StudentExternalID: "604874",
			Status:            integrations.AttendanceAbsent,
		}},
	})
	if err != nil {
		t.Fatalf("UpsertAttendanceBatch: %v", err)
	}
	if len(result.Entries) != 1 || !result.Entries[0].Accepted || result.Entries[0].ExternalRecordID != "record-123" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if received.StudentReference.StudentUniqueID != "604874" ||
		received.SessionReference.SchoolYear != 2027 ||
		received.AttendanceEventCategoryDescriptor != testAbsentDescriptor {
		t.Fatalf("unexpected payload: %+v", received)
	}
}

func TestUpsertAttendanceBatchDeletesAcceptedAbsenceForPresentCorrection(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/token":
			_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "token"})
		case "/api/data/v3/ed-fi/studentSchoolAttendanceEvents/record-123":
			deleted = r.Method == http.MethodDelete
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New()
	client.HTTPClient = server.Client()
	result, err := client.UpsertAttendanceBatch(context.Background(), testConnection(server.URL), integrations.AttendanceBatch{
		Version:          2,
		SchoolExternalID: "255901001",
		SchoolDate:       time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC),
		Entries: []integrations.AttendanceEntry{{
			StudentExternalID: "604874",
			Status:            integrations.AttendancePresent,
			ExternalRecordID:  "record-123",
		}},
	})
	if err != nil {
		t.Fatalf("UpsertAttendanceBatch: %v", err)
	}
	if !deleted {
		t.Fatal("expected the accepted absence record to be deleted")
	}
	if len(result.Entries) != 1 || !result.Entries[0].Accepted ||
		result.Entries[0].ExternalRecordID == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

const testAbsentDescriptor = "uri://ed-fi.org/AttendanceEventCategoryDescriptor#Unexcused Absence"

func testConnection(baseURL string) integrations.Connection {
	config, _ := json.Marshal(ConnectionConfig{
		BaseURL: baseURL + "/api", DataPath: "/data/v3/ed-fi",
		SessionName: "2026-2027 School Year", SchoolYear: 2027,
	})
	credentials, _ := json.Marshal(Credentials{ClientKey: "key", ClientSecret: "secret"})
	return integrations.Connection{
		ProviderKind: providerKind, Role: integrations.ConnectionRoleAttendanceDestination,
		Configuration: config, Credentials: credentials,
		AttendanceCodes: integrations.AttendanceCodeMapping{
			integrations.AttendancePresent: exceptionOnlyPresentCode,
			integrations.AttendanceAbsent:  testAbsentDescriptor,
		},
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
