package canvas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
)

type Client struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

type ConnectionConfig struct {
	BaseURL           string   `json:"base_url"`
	AccountID         string   `json:"account_id"`
	SelectedCourseIDs []string `json:"selected_course_ids"`
}

type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type courseResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SISID     string `json:"sis_course_id"`
	AccountID int64  `json:"account_id"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type enrollment struct {
	ID       int64  `json:"id"`
	CourseID int64  `json:"course_id"`
	Type     string `json:"type"`
	State    string `json:"enrollment_state"`
	User     struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		SISID   string `json:"sis_user_id"`
		Email   string `json:"email"`
		LoginID string `json:"login_id"`
	} `json:"user"`
}

type rosterCursor struct {
	CourseIndex int    `json:"course_index"`
	NextURL     string `json:"next_url"`
}

func New(clientID, clientSecret, redirectURL string) *Client {
	return &Client{
		ClientID:     strings.TrimSpace(clientID),
		ClientSecret: strings.TrimSpace(clientSecret),
		RedirectURL:  strings.TrimSpace(redirectURL),
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Metadata() integrations.ProviderMetadata {
	return integrations.ProviderMetadata{
		Kind:               "canvas",
		DisplayName:        "Canvas",
		AuthenticationMode: integrations.AuthenticationOAuth2,
		Capabilities:       []integrations.Capability{integrations.CapabilityRosterRead},
	}
}

func (c *Client) Configured() bool {
	return c != nil && c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

func (c *Client) AuthorizationURL(baseURL, state string) (string, error) {
	base, err := normalizeBaseURL(baseURL)
	if err != nil || !c.Configured() {
		return "", fmt.Errorf("%w: Canvas OAuth is not configured", integrations.ErrInvalidConfiguration)
	}
	query := url.Values{
		"client_id":     {c.ClientID},
		"response_type": {"code"},
		"redirect_uri":  {c.RedirectURL},
		"state":         {state},
	}
	return base + "/login/oauth2/auth?" + query.Encode(), nil
}

func (c *Client) ExchangeCode(ctx context.Context, baseURL, code string) (Credentials, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"redirect_uri":  {c.RedirectURL},
		"code":          {code},
	}
	return c.exchangeToken(ctx, baseURL, values, "")
}

func (c *Client) Refresh(ctx context.Context, baseURL string, current Credentials) (Credentials, error) {
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"redirect_uri":  {c.RedirectURL},
		"refresh_token": {current.RefreshToken},
	}
	return c.exchangeToken(ctx, baseURL, values, current.RefreshToken)
}

func (c *Client) exchangeToken(ctx context.Context, baseURL string, values url.Values, retainedRefreshToken string) (Credentials, error) {
	base, err := normalizeBaseURL(baseURL)
	if err != nil {
		return Credentials{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/login/oauth2/token", strings.NewReader(values.Encode()))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("%w: %v", integrations.ErrTemporaryFailure, err)
	}
	defer resp.Body.Close()
	if err := responseError(resp); err != nil {
		return Credentials{}, err
	}
	var token tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return Credentials{}, fmt.Errorf("%w: decode token response", integrations.ErrPermanentRejection)
	}
	if token.RefreshToken == "" {
		token.RefreshToken = retainedRefreshToken
	}
	return Credentials{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second),
	}, nil
}

func (c *Client) ValidateConnection(ctx context.Context, connection integrations.Connection) error {
	config, credentials, err := decodeConnection(connection)
	if err != nil {
		return err
	}
	var current struct {
		ID int64 `json:"id"`
	}
	return c.getJSON(ctx, config.BaseURL+"/api/v1/users/self", credentials.AccessToken, &current, nil)
}

// ListCourses returns account-visible courses and follows Canvas Link headers
// internally so the admin selection page receives a complete list.
func (c *Client) ListCourses(ctx context.Context, connection integrations.Connection) ([]integrations.Class, error) {
	config, credentials, err := decodeConnection(connection)
	if err != nil {
		return nil, err
	}
	accountID := config.AccountID
	if accountID == "" {
		accountID = "self"
	}
	next := config.BaseURL + "/api/v1/accounts/" + url.PathEscape(accountID) + "/courses?per_page=100"
	courses := []integrations.Class{}
	for next != "" {
		var page []courseResponse
		var header http.Header
		if err := c.getJSON(ctx, next, credentials.AccessToken, &page, &header); err != nil {
			return nil, err
		}
		for _, course := range page {
			courses = append(courses, integrations.Class{
				ExternalID: strconv.FormatInt(course.ID, 10),
				SISID:      course.SISID,
				Name:       course.Name,
				Active:     true,
			})
		}
		next = nextLink(header.Get("Link"))
	}
	return courses, nil
}

// ReadRoster exposes Canvas pagination as an opaque cursor while returning
// only courses, people, and memberships from the selected course list.
func (c *Client) ReadRoster(ctx context.Context, connection integrations.Connection, request integrations.RosterRequest) (integrations.RosterPage, error) {
	config, credentials, err := decodeConnection(connection)
	if err != nil {
		return integrations.RosterPage{}, err
	}
	cursor, err := decodeCursor(request.Cursor)
	if err != nil {
		return integrations.RosterPage{}, fmt.Errorf("%w: invalid roster cursor", integrations.ErrInvalidConfiguration)
	}
	if cursor.CourseIndex >= len(config.SelectedCourseIDs) {
		return integrations.RosterPage{}, nil
	}

	courseID := config.SelectedCourseIDs[cursor.CourseIndex]
	var course courseResponse
	if err := c.getJSON(ctx, config.BaseURL+"/api/v1/courses/"+url.PathEscape(courseID), credentials.AccessToken, &course, nil); err != nil {
		return integrations.RosterPage{}, err
	}
	enrollmentURL := cursor.NextURL
	if enrollmentURL == "" {
		enrollmentURL = config.BaseURL + "/api/v1/courses/" + url.PathEscape(courseID) +
			"/enrollments?per_page=100&state[]=active&state[]=invited&type[]=StudentEnrollment&type[]=TeacherEnrollment"
	}
	var enrollments []enrollment
	var header http.Header
	if err := c.getJSON(ctx, enrollmentURL, credentials.AccessToken, &enrollments, &header); err != nil {
		return integrations.RosterPage{}, err
	}

	schoolID := "account:" + strconv.FormatInt(course.AccountID, 10)
	page := integrations.RosterPage{
		Schools: []integrations.School{{ExternalID: schoolID, Name: "Canvas account " + strconv.FormatInt(course.AccountID, 10), Active: true}},
		Classes: []integrations.Class{{
			ExternalID: courseID, SISID: course.SISID, SchoolExternalID: schoolID, Name: course.Name, Active: true,
		}},
	}
	people := map[string]integrations.Person{}
	for _, item := range enrollments {
		role := integrations.PersonRoleStudent
		if item.Type == "TeacherEnrollment" {
			role = integrations.PersonRoleTeacher
		}
		externalUserID := strconv.FormatInt(item.User.ID, 10)
		email := item.User.Email
		if email == "" && strings.Contains(item.User.LoginID, "@") {
			email = item.User.LoginID
		}
		people[externalUserID] = integrations.Person{
			ExternalID: externalUserID, SISID: item.User.SISID, Name: item.User.Name,
			Email: email, Role: role, Active: item.State == "active" || item.State == "invited",
		}
		page.Memberships = append(page.Memberships, integrations.Membership{
			ExternalID: strconv.FormatInt(item.ID, 10), ClassExternalID: courseID,
			PersonExternalID: externalUserID, Role: role, Active: people[externalUserID].Active,
		})
	}
	for _, person := range people {
		page.People = append(page.People, person)
	}

	next := nextLink(header.Get("Link"))
	nextCursor := rosterCursor{CourseIndex: cursor.CourseIndex, NextURL: next}
	if next == "" {
		nextCursor.CourseIndex++
	}
	if nextCursor.CourseIndex < len(config.SelectedCourseIDs) {
		encoded, _ := json.Marshal(nextCursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return page, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint, token string, target any, responseHeader *http.Header) error {
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
	if err := responseError(resp); err != nil {
		return err
	}
	if responseHeader != nil {
		*responseHeader = resp.Header.Clone()
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(target); err != nil {
		return fmt.Errorf("%w: decode Canvas response", integrations.ErrPermanentRejection)
	}
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func decodeConnection(connection integrations.Connection) (ConnectionConfig, Credentials, error) {
	var config ConnectionConfig
	var credentials Credentials
	if err := json.Unmarshal(connection.Configuration, &config); err != nil {
		return config, credentials, fmt.Errorf("%w: Canvas connection configuration", integrations.ErrInvalidConfiguration)
	}
	base, err := normalizeBaseURL(config.BaseURL)
	if err != nil {
		return config, credentials, err
	}
	config.BaseURL = base
	if err := json.Unmarshal(connection.Credentials, &credentials); err != nil || credentials.AccessToken == "" {
		return config, credentials, fmt.Errorf("%w: Canvas access token is missing", integrations.ErrAuthentication)
	}
	return config, credentials, nil
}

func normalizeBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("%w: valid Canvas base URL is required", integrations.ErrInvalidConfiguration)
	}
	return parsed.String(), nil
}

func decodeCursor(value string) (rosterCursor, error) {
	if value == "" {
		return rosterCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return rosterCursor{}, err
	}
	var cursor rosterCursor
	err = json.Unmarshal(data, &cursor)
	return cursor, err
}

func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		sections := strings.Split(part, ";")
		if len(sections) < 2 || !strings.Contains(sections[1], `rel="next"`) {
			continue
		}
		return strings.Trim(strings.TrimSpace(sections[0]), "<>")
	}
	return ""
}

func responseError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return integrations.ErrAuthentication
	case resp.StatusCode == http.StatusForbidden:
		return integrations.ErrPermission
	case resp.StatusCode == http.StatusTooManyRequests:
		return integrations.ErrRateLimited
	case resp.StatusCode >= 500:
		return integrations.ErrTemporaryFailure
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: Canvas returned HTTP %d", integrations.ErrPermanentRejection, resp.StatusCode)
	default:
		return nil
	}
}
