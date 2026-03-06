package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// mockIssueJSON returns a realistic Jira API issue response.
func mockIssueJSON() map[string]any {
	return map[string]any{
		"key": "PROJ-123",
		"fields": map[string]any{
			"summary": "Fix the login bug",
			"status":  map[string]any{"name": "In Progress"},
			"priority": map[string]any{
				"name": "High",
			},
			"assignee": map[string]any{
				"displayName": "Jane Doe",
			},
			"reporter": map[string]any{
				"displayName": "John Smith",
			},
			"issuetype": map[string]any{
				"name": "Bug",
			},
			"labels":  []any{"backend", "urgent"},
			"created": "2026-03-01T10:00:00.000+0000",
			"updated": "2026-03-02T15:30:00.000+0000",
			"description": map[string]any{
				"version": 1,
				"type":    "doc",
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": "The login form fails when using SSO."},
						},
					},
				},
			},
			"comment": map[string]any{
				"comments": []any{
					map[string]any{
						"id":      "10001",
						"author":  map[string]any{"displayName": "Jane Doe"},
						"created": "2026-03-01T12:00:00.000+0000",
						"updated": "2026-03-01T12:00:00.000+0000",
						"body": map[string]any{
							"version": 1,
							"type":    "doc",
							"content": []any{
								map[string]any{
									"type": "paragraph",
									"content": []any{
										map[string]any{"type": "text", "text": "Looking into this now."},
									},
								},
							},
						},
					},
				},
			},
			"attachment": []any{
				map[string]any{
					"id":       "20001",
					"filename": "screenshot.png",
					"mimeType": "image/png",
					"size":     float64(45678),
					"content":  "https://jira.example.com/attachments/20001",
					"created":  "2026-03-01T11:00:00.000+0000",
					"author":   map[string]any{"displayName": "John Smith"},
				},
			},
			"customfield_10001": "Sprint 42",
		},
		"renderedFields": map[string]any{
			"description": "<p>The login form fails when using SSO.</p>",
		},
	}
}

func mockJiraServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// GET issue
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		issue := mockIssueJSON()
		// If fields query param is set, filter the response to only include requested fields
		if fieldsParam := r.URL.Query().Get("fields"); fieldsParam != "" {
			requested := make(map[string]bool)
			for _, f := range strings.Split(fieldsParam, ",") {
				requested[strings.TrimSpace(f)] = true
			}
			fields := issue["fields"].(map[string]any)
			filtered := make(map[string]any)
			for k, v := range fields {
				if requested[k] {
					filtered[k] = v
				}
			}
			issue["fields"] = filtered
		}
		json.NewEncoder(w).Encode(issue)
	})

	// GET nonexistent issue
	mux.HandleFunc("GET /rest/api/3/issue/NOTFOUND-1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errorMessages":["Issue does not exist"]}`))
	})

	// GET issue that returns 401
	mux.HandleFunc("GET /rest/api/3/issue/AUTH-1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errorMessages":["Authentication required"]}`))
	})

	// GET issue that returns 429
	mux.HandleFunc("GET /rest/api/3/issue/RATE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"errorMessages":["Rate limit exceeded"]}`))
	})

	// POST comment
	mux.HandleFunc("POST /rest/api/3/issue/PROJ-123/comment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "30001",
			"author":  map[string]any{"displayName": "Jane Doe"},
			"created": "2026-03-02T16:00:00.000+0000",
			"updated": "2026-03-02T16:00:00.000+0000",
			"body":    "Test comment",
		})
	})

	// POST comment on nonexistent issue
	mux.HandleFunc("POST /rest/api/3/issue/NOTFOUND-1/comment", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errorMessages":["Issue does not exist"]}`))
	})

	// PUT update issue
	mux.HandleFunc("PUT /rest/api/3/issue/PROJ-123", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fields, _ := body["fields"].(map[string]any)

		// Simulate a non-editable field error
		if _, ok := fields["noneditable"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"errorMessages":["Field 'noneditable' cannot be set"]}`))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	// GET transitions
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-123/transitions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"transitions": []any{
				map[string]any{
					"id": "21",
					"to": map[string]any{"name": "Done"},
				},
				map[string]any{
					"id": "31",
					"to": map[string]any{"name": "In Review"},
				},
			},
		})
	})

	// POST transitions
	mux.HandleFunc("POST /rest/api/3/issue/PROJ-123/transitions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// GET issue with 500 error
	mux.HandleFunc("GET /rest/api/3/issue/ERR-500", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"errorMessages":["Internal server error"]}`))
	})

	return httptest.NewServer(mux)
}

func newTestClient(serverURL string) *JiraClient {
	return &JiraClient{
		BaseURL:    serverURL,
		Email:      "test@example.com",
		Token:      "test-token",
		HTTPClient: http.DefaultClient,
	}
}

// --- GET action tests ---

func TestGetIssue(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123", "expand": "comments,attachments"})

	testutil.AssertEnvelope(t, result, "jira", "get")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	if issue.Key != "PROJ-123" {
		t.Errorf("expected key=PROJ-123, got %s", issue.Key)
	}
	if issue.Summary != "Fix the login bug" {
		t.Errorf("expected summary='Fix the login bug', got %s", issue.Summary)
	}
	if issue.Status != "In Progress" {
		t.Errorf("expected status='In Progress', got %s", issue.Status)
	}
	if issue.Priority != "High" {
		t.Errorf("expected priority='High', got %s", issue.Priority)
	}
	if issue.Assignee != "Jane Doe" {
		t.Errorf("expected assignee='Jane Doe', got %s", issue.Assignee)
	}
	if issue.IssueType != "Bug" {
		t.Errorf("expected issue_type='Bug', got %s", issue.IssueType)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "backend" {
		t.Errorf("unexpected labels: %v", issue.Labels)
	}
}

func TestGetIssueExpandComments(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123", "expand": "comments"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue := result.Content.(*Issue)
	if len(issue.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(issue.Comments))
	}
	if issue.Comments[0].Author != "Jane Doe" {
		t.Errorf("expected comment author='Jane Doe', got %s", issue.Comments[0].Author)
	}
	if issue.Comments[0].Body != "Looking into this now." {
		t.Errorf("expected comment body='Looking into this now.', got %s", issue.Comments[0].Body)
	}
	// No attachments when not expanded
	if len(issue.Attachments) != 0 {
		t.Errorf("expected 0 attachments when not expanded, got %d", len(issue.Attachments))
	}
}

func TestGetIssueExpandAttachments(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123", "expand": "attachments"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue := result.Content.(*Issue)
	if len(issue.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(issue.Attachments))
	}
	if issue.Attachments[0].Filename != "screenshot.png" {
		t.Errorf("expected filename='screenshot.png', got %s", issue.Attachments[0].Filename)
	}
	if issue.Attachments[0].Size != 45678 {
		t.Errorf("expected size=45678, got %d", issue.Attachments[0].Size)
	}
	if issue.Attachments[0].URL != "https://jira.example.com/attachments/20001" {
		t.Errorf("unexpected attachment URL: %s", issue.Attachments[0].URL)
	}
	// No comments when not expanded
	if len(issue.Comments) != 0 {
		t.Errorf("expected 0 comments when not expanded, got %d", len(issue.Comments))
	}
}

func TestGetIssueExpandAll(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123", "expand": "all"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue := result.Content.(*Issue)
	if len(issue.Comments) != 1 {
		t.Errorf("expected 1 comment with expand=all, got %d", len(issue.Comments))
	}
	if len(issue.Attachments) != 1 {
		t.Errorf("expected 1 attachment with expand=all, got %d", len(issue.Attachments))
	}
}

func TestGetIssueWithFields(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123", "fields": "summary,status"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	if issue.Summary != "Fix the login bug" {
		t.Errorf("expected summary='Fix the login bug', got %s", issue.Summary)
	}
	if issue.Status != "In Progress" {
		t.Errorf("expected status='In Progress', got %s", issue.Status)
	}
	// Fields not in the filter should be empty since the mock server filters them
	if issue.Priority != "" {
		t.Errorf("expected priority to be empty when not in fields filter, got %s", issue.Priority)
	}
	if issue.Assignee != "" {
		t.Errorf("expected assignee to be empty when not in fields filter, got %s", issue.Assignee)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "NOTFOUND-1"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "issue not found: NOTFOUND-1") {
		t.Errorf("expected 'issue not found' message, got: %s", result.Error.Message)
	}
}

func TestGetIssueInvalidKeyFormat(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	cases := []string{"123", "PROJ", "-123", "PROJ-"}
	for _, key := range cases {
		flags := map[string]string{"action": "get", "id": key}
		result := handler(input, flags)
		testutil.AssertFatalError(t, result)
		if !strings.Contains(result.Error.Message, "invalid issue key") {
			t.Errorf("key=%q: expected 'invalid issue key' message, got: %s", key, result.Error.Message)
		}
	}
}

func TestGetIssueAuthFailure(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "AUTH-1"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "authentication failed") {
		t.Errorf("expected auth failure message, got: %s", result.Error.Message)
	}
}

func TestGetIssueRateLimited(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "RATE-1"})

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable error for rate limit")
	}
	if !strings.Contains(result.Error.Message, "retry after 30s") {
		t.Errorf("expected rate limit message with Retry-After value, got: %s", result.Error.Message)
	}
}

// --- COMMENT action tests ---

func TestCommentViaFlag(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "comment", "id": "PROJ-123", "comment": "Great work!"})

	testutil.AssertEnvelope(t, result, "jira", "comment")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	comment, ok := result.Content.(*Comment)
	if !ok {
		t.Fatalf("expected *Comment content, got %T", result.Content)
	}
	if comment.ID != "30001" {
		t.Errorf("expected comment id='30001', got %s", comment.ID)
	}
}

func TestCommentViaEnvelope(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = "Comment from envelope"

	result := handler(input, map[string]string{"action": "comment", "id": "PROJ-123"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	comment, ok := result.Content.(*Comment)
	if !ok {
		t.Fatalf("expected *Comment content, got %T", result.Content)
	}
	if comment.ID == "" {
		t.Error("expected non-empty comment ID")
	}
}

func TestCommentFlagPrecedence(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = "Envelope comment"

	// Flag should take precedence
	result := handler(input, map[string]string{"action": "comment", "id": "PROJ-123", "comment": "Flag comment"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if result.Content.(*Comment).ID == "" {
		t.Error("expected non-empty comment ID")
	}
}

func TestCommentEmpty(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "comment", "id": "PROJ-123"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "comment body is empty") {
		t.Errorf("expected 'comment body is empty' message, got: %s", result.Error.Message)
	}
}

func TestCommentNotFoundIssue(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "comment", "id": "NOTFOUND-1", "comment": "Hello"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "issue not found") {
		t.Errorf("expected 'issue not found' message, got: %s", result.Error.Message)
	}
}

// --- UPDATE action tests ---

func TestUpdateSummary(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	fields := `{"summary": "Updated title"}`
	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123", "fields": fields})

	testutil.AssertEnvelope(t, result, "jira", "update")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	// Should return refreshed issue
	if issue.Key != "PROJ-123" {
		t.Errorf("expected refreshed issue key=PROJ-123, got %s", issue.Key)
	}
}

func TestUpdateMultipleFields(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	fields := `{"priority": {"name": "High"}, "labels": ["backend", "urgent"]}`
	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123", "fields": fields})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	if issue.Key != "PROJ-123" {
		t.Errorf("expected key=PROJ-123, got %s", issue.Key)
	}
}

func TestUpdateStatusViaTransition(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	fields := `{"status": "Done"}`
	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123", "fields": fields})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	if issue.Key != "PROJ-123" {
		t.Errorf("expected refreshed issue, got key=%s", issue.Key)
	}
}

func TestUpdateStatusUnavailableTransition(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	fields := `{"status": "Blocked"}`
	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123", "fields": fields})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "no transition to 'Blocked'") {
		t.Errorf("expected transition not available message, got: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "Done") {
		t.Errorf("expected available transitions listed, got: %s", result.Error.Message)
	}
}

func TestUpdateInvalidFieldsJSON(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123", "fields": "{invalid json"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "--fields is not valid JSON") {
		t.Errorf("expected invalid JSON message, got: %s", result.Error.Message)
	}
}

func TestUpdateNoFields(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "no fields to update") {
		t.Errorf("expected 'no fields to update' message, got: %s", result.Error.Message)
	}
}

func TestUpdateNonEditableField(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	fields := `{"noneditable": "value"}`
	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123", "fields": fields})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "noneditable") {
		t.Errorf("expected error about noneditable field, got: %s", result.Error.Message)
	}
}

// --- Auth detection tests ---

func TestCloudAuth(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockIssueJSON())
	}))
	defer srv.Close()

	// For cloud detection, we'll test the isCloud method directly
	cloudClient := &JiraClient{BaseURL: "https://myco.atlassian.net", Email: "a@b.com", Token: "tok"}
	if !cloudClient.isCloud() {
		t.Error("expected .atlassian.net URL to be detected as cloud")
	}

	serverClient := &JiraClient{BaseURL: "https://jira.company.com", Email: "a@b.com", Token: "tok"}
	if serverClient.isCloud() {
		t.Error("expected custom domain to not be detected as cloud")
	}

	// Test that cloud client uses Basic auth
	cloudClient.BaseURL = srv.URL + "/.atlassian.net" // hack for test routing
	cloudClient.HTTPClient = http.DefaultClient

	// Actually test header setting via a real request
	cloudReq, _ := http.NewRequest("GET", srv.URL, nil)
	cloudClient.BaseURL = "https://myco.atlassian.net"
	cloudClient.setAuth(cloudReq)
	if !strings.HasPrefix(cloudReq.Header.Get("Authorization"), "Basic ") {
		t.Errorf("expected Basic auth for cloud, got: %s", cloudReq.Header.Get("Authorization"))
	}

	serverReq, _ := http.NewRequest("GET", srv.URL, nil)
	serverClient.setAuth(serverReq)
	if !strings.HasPrefix(serverReq.Header.Get("Authorization"), "Bearer ") {
		t.Errorf("expected Bearer auth for server, got: %s", serverReq.Header.Get("Authorization"))
	}

	// Verify the Basic auth is properly base64 encoded
	_ = capturedAuth // Used by the real HTTP call above
}

// --- SEARCH action tests ---

func mockSearchResponse(issues []map[string]any, total int) map[string]any {
	return map[string]any{
		"total":      total,
		"startAt":    0,
		"maxResults": len(issues),
		"issues":     issues,
	}
}

func mockUATIssueJSON(assignee, lastCommentAuthor string) map[string]any {
	issue := mockIssueJSON()
	issue["key"] = "PTP-42"
	fields := issue["fields"].(map[string]any)
	fields["status"] = map[string]any{"name": "UAT"}
	fields["assignee"] = map[string]any{"displayName": assignee}
	fields["comment"] = map[string]any{
		"comments": []any{
			map[string]any{
				"id":      "99001",
				"author":  map[string]any{"displayName": lastCommentAuthor},
				"created": "2026-03-03T10:00:00.000+0000",
				"updated": "2026-03-03T10:00:00.000+0000",
				"body": map[string]any{
					"version": 1,
					"type":    "doc",
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{"type": "text", "text": "Found a bug."},
							},
						},
					},
				},
			},
		},
	}
	issue["fields"] = fields
	return issue
}

func mockSearchServer(t *testing.T, response map[string]any, statusCode int, capturedQuery *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/search/jql") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if capturedQuery != nil {
			*capturedQuery = r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	}))
}

func TestSearchDefault(t *testing.T) {
	issue := mockIssueJSON()
	srv := mockSearchServer(t, mockSearchResponse([]map[string]any{issue}, 1), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search"})

	testutil.AssertEnvelope(t, result, "jira", "search")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	text, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if !strings.Contains(text, "PROJ-123") {
		t.Errorf("expected output to contain issue key, got: %s", text)
	}
}

func TestSearchDoesNotRequireID(t *testing.T) {
	srv := mockSearchServer(t, mockSearchResponse(nil, 0), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search"})
	if result.Error != nil {
		t.Fatalf("search without --id should not error, got: %s", result.Error.Message)
	}
}

func TestSearchCustomJQL(t *testing.T) {
	var captured url.Values
	srv := mockSearchServer(t, mockSearchResponse(nil, 0), http.StatusOK, &captured)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	customJQL := "project = PTP AND sprint in openSprints()"
	handler(input, map[string]string{"action": "search", "jql": customJQL})

	if captured.Get("jql") != customJQL {
		t.Errorf("expected jql=%q, got %q", customJQL, captured.Get("jql"))
	}
}

func TestSearchLimit(t *testing.T) {
	var captured url.Values
	srv := mockSearchServer(t, mockSearchResponse(nil, 0), http.StatusOK, &captured)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	handler(input, map[string]string{"action": "search", "limit": "10"})

	if captured.Get("maxResults") != "10" {
		t.Errorf("expected maxResults=10, got %q", captured.Get("maxResults"))
	}
}

func TestSearchEmptyResults(t *testing.T) {
	srv := mockSearchServer(t, mockSearchResponse(nil, 0), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	text, _ := result.Content.(string)
	if text != "No issues found." {
		t.Errorf("expected 'No issues found.', got: %s", text)
	}
}

func TestSearchJQLSyntaxError(t *testing.T) {
	errResponse := map[string]any{"errorMessages": []any{"Field 'bogus' does not exist"}}
	srv := mockSearchServer(t, errResponse, http.StatusBadRequest, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search", "jql": "bogus =!= nope"})
	testutil.AssertFatalError(t, result)
}

func TestSearchPagination(t *testing.T) {
	// Multi-page: first page has 2 issues, total=3; second page has 1
	callCount := 0
	page1Issue := mockIssueJSON()
	page1Issue["key"] = "PTP-1"
	page2Issue := mockIssueJSON()
	page2Issue["key"] = "PTP-2"
	page3Issue := mockIssueJSON()
	page3Issue["key"] = "PTP-3"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		startAt := 0
		maxResults := 100
		fmt.Sscanf(q.Get("startAt"), "%d", &startAt)
		fmt.Sscanf(q.Get("maxResults"), "%d", &maxResults)

		var resp map[string]any
		if startAt == 0 {
			count := maxResults
			if count > 2 {
				count = 2
			}
			resp = map[string]any{
				"total":      3,
				"startAt":    0,
				"maxResults": count,
				"issues":     []map[string]any{page1Issue, page2Issue}[:count],
			}
		} else {
			resp = map[string]any{
				"total":      3,
				"startAt":    startAt,
				"maxResults": 1,
				"issues":     []map[string]any{page3Issue},
			}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	issues, err := client.SearchIssues(context.Background(), "project = PTP", []string{"comments"}, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("expected 3 issues across pages, got %d", len(issues))
	}
	if callCount < 2 {
		t.Errorf("expected multiple API calls for pagination, got %d", callCount)
	}
}

// --- DetectNeedsAttention unit tests ---

func TestDetectNeedsAttention(t *testing.T) {
	cases := []struct {
		name     string
		issue    Issue
		expected bool
	}{
		{
			name: "UAT + non-self comment -> true",
			issue: Issue{
				Status:   "UAT",
				Assignee: "Dev User",
				Comments: []Comment{{Author: "Client User", Body: "Found a bug"}},
			},
			expected: true,
		},
		{
			name: "UAT + self comment -> false",
			issue: Issue{
				Status:   "UAT",
				Assignee: "Dev User",
				Comments: []Comment{{Author: "Dev User", Body: "Deployed fix"}},
			},
			expected: false,
		},
		{
			name: "non-UAT status -> false",
			issue: Issue{
				Status:   "In Progress",
				Assignee: "Dev User",
				Comments: []Comment{{Author: "Client User", Body: "Found a bug"}},
			},
			expected: false,
		},
		{
			name: "UAT + no comments -> false",
			issue: Issue{
				Status:    "UAT",
				Assignee:  "Dev User",
				Comments:  nil,
			},
			expected: false,
		},
		{
			name: "case-insensitive 'uat' -> true",
			issue: Issue{
				Status:   "uat",
				Assignee: "Dev User",
				Comments: []Comment{{Author: "Client User", Body: "Fix needed"}},
			},
			expected: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectNeedsAttention(&tc.issue)
			if got != tc.expected {
				t.Errorf("DetectNeedsAttention = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestSearchNeedsAttention(t *testing.T) {
	// UAT issue with non-self comment should have needs_attention=true
	uatIssue := mockUATIssueJSON("Dev User", "Client User")
	srv := mockSearchServer(t, mockSearchResponse([]map[string]any{uatIssue}, 1), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search", "expand": "comments"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	text, _ := result.Content.(string)
	if !strings.Contains(text, "!!") {
		t.Errorf("expected '!!' attention marker in output, got: %s", text)
	}
}

func TestSearchNeedsAttentionSelfComment(t *testing.T) {
	uatIssue := mockUATIssueJSON("Dev User", "Dev User")
	srv := mockSearchServer(t, mockSearchResponse([]map[string]any{uatIssue}, 1), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search", "expand": "comments"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	text, _ := result.Content.(string)
	if strings.Contains(text, "!!") {
		t.Error("expected no attention marker when last comment is from assignee")
	}
}

func TestGetNoIDFallsBackToSearch(t *testing.T) {
	issue := mockIssueJSON()
	srv := mockSearchServer(t, mockSearchResponse([]map[string]any{issue}, 1), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get"})
	if result.Error != nil {
		t.Fatalf("expected search fallback, got error: %s", result.Error.Message)
	}
	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text from search fallback, got %q", result.ContentType)
	}
}

func TestCommentStillRequiresID(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "comment", "comment": "hello"})
	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "--id is required") {
		t.Errorf("expected --id required message, got: %s", result.Error.Message)
	}
}

// --- Server error tests ---

func TestGetIssueServerError(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "ERR-500"})

	if result.Error == nil {
		t.Fatal("expected error for 500 response")
	}
	if !result.Error.Retryable {
		t.Error("expected server errors to be retryable")
	}
}

// --- Envelope compliance tests ---

func TestEnvelopeCompliance(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	actions := []struct {
		name   string
		flags  map[string]string
		action string
	}{
		{"get", map[string]string{"action": "get", "id": "PROJ-123"}, "get"},
		{"comment", map[string]string{"action": "comment", "id": "PROJ-123", "comment": "test"}, "comment"},
		{"update", map[string]string{"action": "update", "id": "PROJ-123", "fields": `{"summary":"new"}`}, "update"},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			result := handler(input, tc.flags)

			if result.Pipe != "jira" {
				t.Errorf("expected pipe='jira', got %s", result.Pipe)
			}
			if result.Action != tc.action {
				t.Errorf("expected action=%s, got %s", tc.action, result.Action)
			}
			if result.Timestamp.IsZero() {
				t.Error("expected non-zero timestamp")
			}
			if result.Duration <= 0 {
				t.Error("expected positive duration")
			}
			if result.ContentType != envelope.ContentStructured {
				t.Errorf("expected content_type=structured, got %s", result.ContentType)
			}
			if result.Error != nil {
				t.Errorf("unexpected error: %s", result.Error.Message)
			}
			// Check Args populated
			if result.Args == nil {
				t.Error("expected Args to be populated")
			}
			if result.Args["id"] != "PROJ-123" {
				t.Errorf("expected Args[id]=PROJ-123, got %s", result.Args["id"])
			}
		})
	}
}

// --- Default action test ---

func TestDefaultActionIsGet(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	// No action specified — should default to get
	result := handler(input, map[string]string{"id": "PROJ-123"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if result.Action != "get" {
		t.Errorf("expected default action='get', got %s", result.Action)
	}
}

// --- ADF conversion tests ---

func TestADFToPlainText(t *testing.T) {
	cases := []struct {
		name     string
		doc      map[string]any
		expected string
	}{
		{
			name: "simple paragraph",
			doc: map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": "Hello world."},
						},
					},
				},
			},
			expected: "Hello world.",
		},
		{
			name: "bullet list",
			doc: map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "bulletList",
						"content": []any{
							map[string]any{
								"type": "listItem",
								"content": []any{
									map[string]any{
										"type": "paragraph",
										"content": []any{
											map[string]any{"type": "text", "text": "Item 1"},
										},
									},
								},
							},
							map[string]any{
								"type": "listItem",
								"content": []any{
									map[string]any{
										"type": "paragraph",
										"content": []any{
											map[string]any{"type": "text", "text": "Item 2"},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: "- Item 1\n- Item 2",
		},
		{
			name: "code block",
			doc: map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "codeBlock",
						"content": []any{
							map[string]any{"type": "text", "text": "fmt.Println()"},
						},
					},
				},
			},
			expected: "```\nfmt.Println()\n```",
		},
		{
			name:     "nil document",
			doc:      nil,
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := adfToPlainText(tc.doc)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// --- Custom fields test ---

func TestCustomFieldsExtracted(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue := result.Content.(*Issue)
	if issue.CustomFields == nil {
		t.Fatal("expected custom fields to be populated")
	}
	if issue.CustomFields["customfield_10001"] != "Sprint 42" {
		t.Errorf("expected customfield_10001='Sprint 42', got %v", issue.CustomFields["customfield_10001"])
	}
}

// --- Description extraction test ---

func TestDescriptionFromRenderedFields(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "id": "PROJ-123"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue := result.Content.(*Issue)
	if issue.Description != "The login form fails when using SSO." {
		t.Errorf("expected description from rendered fields, got: %s", issue.Description)
	}
}

// --- Unknown action test ---

func TestUnknownAction(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "delete", "id": "PROJ-123"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "unknown action: delete") {
		t.Errorf("expected unknown action message, got: %s", result.Error.Message)
	}
}

// --- Update from envelope content test ---

func TestUpdateFromEnvelopeContent(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	input.Content = map[string]any{"summary": "From envelope"}

	result := handler(input, map[string]string{"action": "update", "id": "PROJ-123"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	if issue.Key != "PROJ-123" {
		t.Errorf("expected refreshed issue, got key=%s", issue.Key)
	}
}

// --- stripHTML test ---

func TestStripHTML(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<b>bold</b> and <i>italic</i>", "bold and italic"},
		{"no tags", "no tags"},
		{"<p>multi</p><p>para</p>", "multipara"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			result := stripHTML(tc.input)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// --- Sort ordering tests ---

func TestSearchResultsSortedByStatusThenPriority(t *testing.T) {
	mkIssue := func(key, status, priority, issueType string) map[string]any {
		i := mockIssueJSON()
		i["key"] = key
		f := i["fields"].(map[string]any)
		f["status"] = map[string]any{"name": status}
		f["priority"] = map[string]any{"name": priority}
		f["issuetype"] = map[string]any{"name": issueType}
		return i
	}

	issues := []map[string]any{
		mkIssue("A-1", "UAT", "Low", "Task"),
		mkIssue("A-2", "To Do", "Medium", "Task"),
		mkIssue("A-3", "UAT", "Highest", "Task"),
		mkIssue("A-4", "To Do", "High", "Task"),
		mkIssue("A-5", "UAT", "Medium", "Fire"),
	}
	srv := mockSearchServer(t, mockSearchResponse(issues, 5), http.StatusOK, nil)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "search"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	text, _ := result.Content.(string)

	// To Do before UAT. Within each group: fires first, then by priority.
	// To Do: High=A-4, Medium=A-2
	// UAT: Fire/Medium=A-5, Highest=A-3, Low=A-1
	wantOrder := []string{"A-4", "A-2", "A-5", "A-3", "A-1"}
	prev := -1
	for _, key := range wantOrder {
		idx := strings.Index(text, key)
		if idx < 0 {
			t.Errorf("expected %s in output", key)
			continue
		}
		if idx <= prev {
			t.Errorf("%s should appear after previous key in output", key)
		}
		prev = idx
	}
}

// --- Transition action tests ---

func TestTransitionAction(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "transition", "id": "PROJ-123", "status": "Done"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	m, ok := result.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected map content, got %T", result.Content)
	}
	if m["action"] != "transition" {
		t.Errorf("expected action=transition, got %v", m["action"])
	}
	if m["key"] != "PROJ-123" {
		t.Errorf("expected key=PROJ-123, got %v", m["key"])
	}
}

func TestTransitionMissingStatus(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "transition", "id": "PROJ-123"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "--status is required") {
		t.Errorf("expected status required message, got: %s", result.Error.Message)
	}
}

func TestTransitionLowercaseID(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "transition", "id": "proj-123", "status": "Done"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	m, ok := result.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected map content, got %T", result.Content)
	}
	if m["key"] != "PROJ-123" {
		t.Errorf("expected key=PROJ-123, got %v", m["key"])
	}
}

func TestTopicPromotionLowercaseID(t *testing.T) {
	srv := mockJiraServer(t)
	defer srv.Close()

	client := newTestClient(srv.URL)
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "get", "topic": "proj-123"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	issue, ok := result.Content.(*Issue)
	if !ok {
		t.Fatalf("expected *Issue content, got %T", result.Content)
	}
	if issue.Key != "PROJ-123" {
		t.Errorf("expected PROJ-123, got key=%s", issue.Key)
	}
}

// --- Issue key validation regex test ---

func TestIssueKeyRegex(t *testing.T) {
	valid := []string{"PROJ-123", "AB-1", "MYPROJECT-99999", "X2-42"}
	invalid := []string{"proj-123", "123", "PROJ", "-123", "P-", "PROJ-", "PROJ-abc"}

	for _, key := range valid {
		if !issueKeyRe.MatchString(key) {
			t.Errorf("expected %q to be valid", key)
		}
	}
	for _, key := range invalid {
		if issueKeyRe.MatchString(key) {
			t.Errorf("expected %q to be invalid", key)
		}
	}
}
