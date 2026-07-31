package canvas

import (
	"errors"
	"net/url"
	"testing"

	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

func TestCanvasConfigurationAndAuthorizationURLs(t *testing.T) {
	client := New(" client-id ", " client-secret ", " https://app.example/callback ", " https://school.instructure.com/ ")
	if !client.Configured() || !client.StudentConfigured() {
		t.Fatalf("configured client rejected: %#v", client)
	}
	if client.DefaultBaseURL() != "https://school.instructure.com" {
		t.Fatalf("DefaultBaseURL = %q", client.DefaultBaseURL())
	}

	standard, err := client.AuthorizationURL(client.BaseURL, "state-value")
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	identity, err := client.IdentityAuthorizationURL(client.BaseURL, "identity-state")
	if err != nil {
		t.Fatalf("IdentityAuthorizationURL: %v", err)
	}
	for _, tc := range []struct {
		value     string
		state     string
		wantScope string
	}{
		{value: standard, state: "state-value"},
		{value: identity, state: "identity-state", wantScope: canvasIdentityScope},
	} {
		parsed, err := url.Parse(tc.value)
		if err != nil {
			t.Fatalf("parse authorization URL: %v", err)
		}
		if parsed.Scheme != "https" || parsed.Path != "/login/oauth2/auth" {
			t.Fatalf("authorization URL = %q", tc.value)
		}
		query := parsed.Query()
		if query.Get("client_id") != "client-id" || query.Get("redirect_uri") != "https://app.example/callback" || query.Get("state") != tc.state || query.Get("scope") != tc.wantScope {
			t.Fatalf("authorization query = %#v", query)
		}
	}
}

func TestCanvasRejectsUnsafeOrIncompleteConfiguration(t *testing.T) {
	for _, baseURL := range []string{"", "school.instructure.com", "http://school.instructure.com"} {
		client := New("id", "secret", "https://app.example/callback", baseURL)
		if client.StudentConfigured() {
			t.Fatalf("StudentConfigured accepted %q", baseURL)
		}
		if _, err := client.AuthorizationURL(baseURL, "state"); !errors.Is(err, integrations.ErrInvalidConfiguration) {
			t.Fatalf("AuthorizationURL(%q) error = %v", baseURL, err)
		}
	}
	if New("", "secret", "https://app.example/callback", "https://school.instructure.com").Configured() {
		t.Fatal("Configured accepted missing client ID")
	}
}

func TestCanvasCredentialIdentity(t *testing.T) {
	person, err := (Credentials{CanvasUserID: 42, CanvasName: "Student Name"}).Identity()
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if person.ExternalID != "42" || person.Name != "Student Name" || person.Role != integrations.PersonRoleStudent || !person.Active {
		t.Fatalf("Identity = %#v", person)
	}
	if _, err := (Credentials{}).Identity(); !errors.Is(err, integrations.ErrAuthentication) {
		t.Fatalf("empty Identity error = %v, want ErrAuthentication", err)
	}
}
