package edfi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

const (
	providerKind             = "edfi"
	exceptionOnlyPresentCode = "exception-only"
	maxResponseBytes         = 32 << 10
)

type Client struct {
	HTTPClient *http.Client
}

type ConnectionConfig struct {
	BaseURL     string `json:"base_url"`
	DataPath    string `json:"data_path"`
	SessionName string `json:"session_name"`
	SchoolYear  int    `json:"school_year"`
}

type Credentials struct {
	ClientKey    string `json:"client_key"`
	ClientSecret string `json:"client_secret"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

type attendancePayload struct {
	SchoolReference struct {
		SchoolID int64 `json:"schoolId"`
	} `json:"schoolReference"`
	SessionReference struct {
		SchoolID    int64  `json:"schoolId"`
		SchoolYear  int    `json:"schoolYear"`
		SessionName string `json:"sessionName"`
	} `json:"sessionReference"`
	StudentReference struct {
		StudentUniqueID string `json:"studentUniqueId"`
	} `json:"studentReference"`
	AttendanceEventCategoryDescriptor string `json:"attendanceEventCategoryDescriptor"`
	EventDate                         string `json:"eventDate"`
}

func New() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) Metadata() integrations.ProviderMetadata {
	return integrations.ProviderMetadata{
		Kind:               providerKind,
		DisplayName:        "Ed-Fi ODS/API",
		AuthenticationMode: integrations.AuthenticationClientCredentials,
		Capabilities: []integrations.Capability{
			integrations.CapabilityAttendanceWrite,
			integrations.CapabilityAttendanceCorrections,
			integrations.CapabilityAttendanceSafeUpsert,
		},
	}
}

// ValidateConnection confirms client-credentials authentication and access to
// the Ed-Fi daily attendance collection without writing a probe record.
func (c *Client) ValidateConnection(ctx context.Context, connection integrations.Connection) error {
	config, credentials, err := decodeConnection(connection)
	if err != nil {
		return err
	}
	token, err := c.accessToken(ctx, config, credentials)
	if err != nil {
		return err
	}
	endpoint := resourceURL(config, "studentSchoolAttendanceEvents") + "?limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", integrations.ErrTemporaryFailure, err)
	}
	defer resp.Body.Close()
	return responseError(resp)
}

func (c *Client) ValidateAttendanceCodes(_ context.Context, _ integrations.Connection, codes integrations.AttendanceCodeMapping) error {
	if codes[integrations.AttendancePresent] != exceptionOnlyPresentCode {
		return fmt.Errorf("%w: Ed-Fi present code must be %q for exception-only attendance", integrations.ErrInvalidConfiguration, exceptionOnlyPresentCode)
	}
	absent := strings.TrimSpace(codes[integrations.AttendanceAbsent])
	if absent == "" || !strings.Contains(absent, "AttendanceEventCategoryDescriptor#") {
		return fmt.Errorf("%w: Ed-Fi absent code must be a full AttendanceEventCategory descriptor URI", integrations.ErrInvalidConfiguration)
	}
	return nil
}

func (c *Client) ValidateExternalMapping(entityKind, externalID string) error {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return fmt.Errorf("%w: Ed-Fi external identifier is required", integrations.ErrInvalidConfiguration)
	}
	if entityKind == "school" {
		schoolID, err := strconv.ParseInt(externalID, 10, 64)
		if err != nil || schoolID <= 0 {
			return fmt.Errorf("%w: Ed-Fi school identifier must be a positive numeric schoolId", integrations.ErrInvalidConfiguration)
		}
	}
	return nil
}

// UpsertAttendanceBatch writes absence events and treats presence as the
// absence of an event. Previously accepted absence IDs are deleted on a
// present correction, making the correction idempotent even after retries.
func (c *Client) UpsertAttendanceBatch(ctx context.Context, connection integrations.Connection, batch integrations.AttendanceBatch) (integrations.DeliveryResult, error) {
	config, credentials, err := decodeConnection(connection)
	if err != nil {
		return integrations.DeliveryResult{}, err
	}
	if err := c.ValidateAttendanceCodes(ctx, connection, connection.AttendanceCodes); err != nil {
		return integrations.DeliveryResult{}, err
	}
	schoolID, err := strconv.ParseInt(batch.SchoolExternalID, 10, 64)
	if err != nil || schoolID <= 0 {
		return integrations.DeliveryResult{}, fmt.Errorf("%w: Ed-Fi school mapping must be a positive numeric schoolId", integrations.ErrInvalidConfiguration)
	}
	token, err := c.accessToken(ctx, config, credentials)
	if err != nil {
		return integrations.DeliveryResult{}, err
	}

	result := integrations.DeliveryResult{Entries: make([]integrations.DeliveryEntryResult, 0, len(batch.Entries))}
	for _, entry := range batch.Entries {
		entryResult, err := c.upsertEntry(ctx, token, config, batch, schoolID, entry, connection.AttendanceCodes)
		if err != nil {
			if retryableOrConnectionError(err) {
				return integrations.DeliveryResult{}, err
			}
			entryResult = integrations.DeliveryEntryResult{
				StudentExternalID: entry.StudentExternalID,
				Accepted:          false,
				Message:           err.Error(),
			}
		}
		result.Entries = append(result.Entries, entryResult)
	}
	return result, nil
}

func (c *Client) upsertEntry(ctx context.Context, token string, config ConnectionConfig, batch integrations.AttendanceBatch, schoolID int64, entry integrations.AttendanceEntry, codes integrations.AttendanceCodeMapping) (integrations.DeliveryEntryResult, error) {
	if strings.TrimSpace(entry.StudentExternalID) == "" {
		return integrations.DeliveryEntryResult{}, fmt.Errorf("%w: Ed-Fi studentUniqueId is required", integrations.ErrPermanentRejection)
	}
	if entry.Status == integrations.AttendancePresent {
		if isEdFiResourceID(entry.ExternalRecordID) {
			if err := c.deleteAttendance(ctx, token, config, entry.ExternalRecordID); err != nil {
				return integrations.DeliveryEntryResult{}, err
			}
		}
		return integrations.DeliveryEntryResult{
			StudentExternalID: entry.StudentExternalID,
			ExternalRecordID:  noEventID(batch, entry.StudentExternalID),
			Accepted:          true,
			Message:           "present by Ed-Fi exception-only attendance",
		}, nil
	}
	if entry.Status != integrations.AttendanceAbsent {
		return integrations.DeliveryEntryResult{}, fmt.Errorf("%w: unsupported attendance status %q", integrations.ErrPermanentRejection, entry.Status)
	}

	payload := attendancePayload{AttendanceEventCategoryDescriptor: codes[integrations.AttendanceAbsent], EventDate: batch.SchoolDate.Format("2006-01-02")}
	payload.SchoolReference.SchoolID = schoolID
	payload.SessionReference.SchoolID = schoolID
	payload.SessionReference.SchoolYear = config.SchoolYear
	payload.SessionReference.SessionName = config.SessionName
	payload.StudentReference.StudentUniqueID = entry.StudentExternalID

	resourceID, err := c.postAttendance(ctx, token, config, payload, entry.ExternalRecordID)
	if err != nil {
		return integrations.DeliveryEntryResult{}, err
	}
	return integrations.DeliveryEntryResult{
		StudentExternalID: entry.StudentExternalID,
		ExternalRecordID:  resourceID,
		Accepted:          true,
	}, nil
}

func (c *Client) accessToken(ctx context.Context, config ConnectionConfig, credentials Credentials) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL(config), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(credentials.ClientKey, credentials.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", integrations.ErrTemporaryFailure, err)
	}
	defer resp.Body.Close()
	if err := responseError(resp); err != nil {
		return "", err
	}
	var token tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&token); err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("%w: invalid Ed-Fi token response", integrations.ErrAuthentication)
	}
	return token.AccessToken, nil
}

func (c *Client) postAttendance(ctx context.Context, token string, config ConnectionConfig, payload attendancePayload, existingRecordID string) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resourceURL(config, "studentSchoolAttendanceEvents"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", integrations.ErrTemporaryFailure, err)
	}
	defer resp.Body.Close()
	if err := responseError(resp); err != nil {
		return "", err
	}
	resourceID := resourceIDFromLocation(resp.Header.Get("Location"))
	if resourceID == "" && isEdFiResourceID(existingRecordID) {
		resourceID = existingRecordID
	}
	if resourceID == "" {
		resourceID, err = c.findAttendanceID(ctx, token, config, payload)
		if err != nil {
			return "", err
		}
	}
	if resourceID == "" {
		return "", fmt.Errorf("%w: Ed-Fi response did not include a resource Location", integrations.ErrPermanentRejection)
	}
	return resourceID, nil
}

func (c *Client) findAttendanceID(ctx context.Context, token string, config ConnectionConfig, payload attendancePayload) (string, error) {
	query := url.Values{
		"schoolId":                          {strconv.FormatInt(payload.SchoolReference.SchoolID, 10)},
		"schoolYear":                        {strconv.Itoa(payload.SessionReference.SchoolYear)},
		"sessionName":                       {payload.SessionReference.SessionName},
		"studentUniqueId":                   {payload.StudentReference.StudentUniqueID},
		"attendanceEventCategoryDescriptor": {payload.AttendanceEventCategoryDescriptor},
		"eventDate":                         {payload.EventDate},
		"limit":                             {"2"},
	}
	endpoint := resourceURL(config, "studentSchoolAttendanceEvents") + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", integrations.ErrTemporaryFailure, err)
	}
	defer resp.Body.Close()
	if err := responseError(resp); err != nil {
		return "", err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&records); err != nil {
		return "", fmt.Errorf("%w: invalid Ed-Fi attendance lookup response", integrations.ErrPermanentRejection)
	}
	if len(records) != 1 {
		return "", fmt.Errorf("%w: Ed-Fi attendance lookup returned %d records", integrations.ErrPermanentRejection, len(records))
	}
	return records[0].ID, nil
}

func (c *Client) deleteAttendance(ctx context.Context, token string, config ConnectionConfig, resourceID string) error {
	endpoint := resourceURL(config, "studentSchoolAttendanceEvents") + "/" + url.PathEscape(resourceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", integrations.ErrTemporaryFailure, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return responseError(resp)
}

func decodeConnection(connection integrations.Connection) (ConnectionConfig, Credentials, error) {
	var config ConnectionConfig
	var credentials Credentials
	if err := json.Unmarshal(connection.Configuration, &config); err != nil {
		return config, credentials, fmt.Errorf("%w: Ed-Fi provider configuration", integrations.ErrInvalidConfiguration)
	}
	base, err := normalizeBaseURL(config.BaseURL)
	if err != nil {
		return config, credentials, err
	}
	config.BaseURL = base
	if config.DataPath == "" {
		config.DataPath = "/data/v3/ed-fi"
	}
	if !strings.HasPrefix(config.DataPath, "/") || strings.Contains(config.DataPath, "..") {
		return config, credentials, fmt.Errorf("%w: Ed-Fi data_path must be an absolute API path", integrations.ErrInvalidConfiguration)
	}
	if strings.TrimSpace(config.SessionName) == "" || config.SchoolYear < 2000 || config.SchoolYear > 2200 {
		return config, credentials, fmt.Errorf("%w: Ed-Fi session_name and school_year are required", integrations.ErrInvalidConfiguration)
	}
	if err := json.Unmarshal(connection.Credentials, &credentials); err != nil ||
		strings.TrimSpace(credentials.ClientKey) == "" || strings.TrimSpace(credentials.ClientSecret) == "" {
		return config, credentials, fmt.Errorf("%w: Ed-Fi client key and secret are required", integrations.ErrAuthentication)
	}
	return config, credentials, nil
}

func normalizeBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("%w: valid Ed-Fi base_url is required", integrations.ErrInvalidConfiguration)
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1")) {
		return "", fmt.Errorf("%w: Ed-Fi base_url must use HTTPS", integrations.ErrInvalidConfiguration)
	}
	return parsed.String(), nil
}

func tokenURL(config ConnectionConfig) string {
	return config.BaseURL + "/oauth/token"
}

func resourceURL(config ConnectionConfig, resource string) string {
	return config.BaseURL + path.Clean("/"+config.DataPath) + "/" + resource
}

func responseError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(resp.StatusCode)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w: Ed-Fi authentication rejected", integrations.ErrAuthentication)
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: Ed-Fi attendance permission denied", integrations.ErrPermission)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: Ed-Fi rate limit", integrations.ErrRateLimited)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: Ed-Fi HTTP %d", integrations.ErrTemporaryFailure, resp.StatusCode)
	default:
		return fmt.Errorf("%w: Ed-Fi HTTP %d: %s", integrations.ErrPermanentRejection, resp.StatusCode, detail)
	}
}

func retryableOrConnectionError(err error) bool {
	return errors.Is(err, integrations.ErrAuthentication) ||
		errors.Is(err, integrations.ErrPermission) ||
		errors.Is(err, integrations.ErrRateLimited) ||
		errors.Is(err, integrations.ErrTemporaryFailure)
}

func resourceIDFromLocation(location string) string {
	if strings.TrimSpace(location) == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil {
		return ""
	}
	resourceID := path.Base(strings.TrimRight(parsed.Path, "/"))
	if resourceID == "." || resourceID == "/" {
		return ""
	}
	return resourceID
}

func isEdFiResourceID(value string) bool {
	return value != "" && !strings.HasPrefix(value, "edfi:none:")
}

func noEventID(batch integrations.AttendanceBatch, studentID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", batch.SchoolExternalID, batch.SchoolDate.Format("2006-01-02"), studentID, batch.Version)))
	return "edfi:none:" + hex.EncodeToString(sum[:])
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
