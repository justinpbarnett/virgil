package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

var issueKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

// JiraConfig holds credentials for connecting to a Jira instance.
type JiraConfig struct {
	BaseURL string `json:"base_url"`
	Email   string `json:"email"`
	Token   string `json:"token"`
}

// Issue represents a Jira issue with its core fields.
type Issue struct {
	Key          string         `json:"key"`
	Summary      string         `json:"summary"`
	Status       string         `json:"status"`
	Priority     string         `json:"priority"`
	Assignee     string         `json:"assignee"`
	Reporter     string         `json:"reporter"`
	Labels       []string       `json:"labels"`
	Description  string         `json:"description"`
	IssueType    string         `json:"issue_type"`
	Created      string         `json:"created"`
	Updated      string         `json:"updated"`
	Comments     []Comment      `json:"comments,omitempty"`
	Attachments  []Attachment   `json:"attachments,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// Comment represents a Jira issue comment.
type Comment struct {
	ID      string `json:"id"`
	Author  string `json:"author"`
	Created string `json:"created"`
	Updated string `json:"updated"`
	Body    string `json:"body"`
}

// Attachment represents a Jira issue attachment.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
	Created  string `json:"created"`
	Author   string `json:"author"`
}

// JiraClient encapsulates auth, base URL, and HTTP transport for Jira API calls.
type JiraClient struct {
	BaseURL    string
	Email      string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a JiraClient from config.
func NewClient(cfg JiraConfig) *JiraClient {
	return &JiraClient{
		BaseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		Email:      cfg.Email,
		Token:      cfg.Token,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *JiraClient) isCloud() bool {
	return strings.Contains(c.BaseURL, ".atlassian.net")
}

func (c *JiraClient) setAuth(req *http.Request) {
	if c.isCloud() {
		creds := base64.StdEncoding.EncodeToString([]byte(c.Email + ":" + c.Token))
		req.Header.Set("Authorization", "Basic "+creds)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *JiraClient) do(req *http.Request) (*http.Response, error) {
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return c.HTTPClient.Do(req)
}

// GetIssue retrieves an issue by key with optional expand sections and field filtering.
func (c *JiraClient) GetIssue(ctx context.Context, key string, expand []string, fields string) (*Issue, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s?expand=renderedFields,names", c.BaseURL, key)
	for _, e := range expand {
		if e == "comments" || e == "all" {
			url += ",comment"
			break
		}
	}
	if fields != "" {
		url += "&fields=" + fields
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return parseIssue(raw, expand)
}

// AddComment posts a comment on an issue.
func (c *JiraClient) AddComment(ctx context.Context, key string, body string) (*Comment, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", c.BaseURL, key)

	adf := map[string]any{
		"body": map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{"type": "text", "text": body},
					},
				},
			},
		},
	}

	payload, err := json.Marshal(adf)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode comment response: %w", err)
	}

	return parseComment(raw), nil
}

// UpdateIssue updates fields on an issue.
func (c *JiraClient) UpdateIssue(ctx context.Context, key string, fields map[string]any) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s", c.BaseURL, key)

	payload, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	return checkResponse(resp)
}

// TransitionIssue transitions an issue to a new status.
func (c *JiraClient) TransitionIssue(ctx context.Context, key string, statusName string) error {
	// Get available transitions
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", c.BaseURL, key)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return err
	}

	var result struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode transitions: %w", err)
	}

	// Find matching transition
	var available []string
	for _, t := range result.Transitions {
		available = append(available, t.To.Name)
		if strings.EqualFold(t.To.Name, statusName) {
			return c.doTransition(ctx, key, t.ID)
		}
	}

	return &jiraError{
		statusCode: 400,
		message:    fmt.Sprintf("no transition to '%s' — available: %s", statusName, strings.Join(available, ", ")),
	}
}

func (c *JiraClient) doTransition(ctx context.Context, key string, transitionID string) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", c.BaseURL, key)

	payload, err := json.Marshal(map[string]any{
		"transition": map[string]any{"id": transitionID},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	return checkResponse(resp)
}

// GetAttachment downloads an attachment's content by ID.
func (c *JiraClient) GetAttachment(ctx context.Context, attachmentID string) ([]byte, string, error) {
	url := fmt.Sprintf("%s/rest/api/3/attachment/content/%s", c.BaseURL, attachmentID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, "", err
	}

	const maxAttachmentSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > maxAttachmentSize {
		return nil, "", fmt.Errorf("attachment exceeds %d byte limit", maxAttachmentSize)
	}

	contentType := resp.Header.Get("Content-Type")
	return data, contentType, nil
}

// jiraError represents an HTTP error from the Jira API.
type jiraError struct {
	statusCode int
	message    string
}

func (e *jiraError) Error() string {
	return e.message
}

func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusNotFound:
		return &jiraError{statusCode: 404, message: "not found"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &jiraError{statusCode: resp.StatusCode, message: "authentication failed — check jira.json credentials"}
	case http.StatusTooManyRequests:
		msg := "rate limited by Jira"
		if retry := resp.Header.Get("Retry-After"); retry != "" {
			msg += fmt.Sprintf(" — retry after %ss", retry)
		} else {
			msg += " — retry after a few seconds"
		}
		return &jiraError{statusCode: 429, message: msg}
	default:
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &jiraError{statusCode: resp.StatusCode, message: msg}
	}
}

// parseIssue extracts an Issue from the raw Jira API response.
func parseIssue(raw map[string]any, expand []string) (*Issue, error) {
	fields, _ := raw["fields"].(map[string]any)
	rendered, _ := raw["renderedFields"].(map[string]any)
	if fields == nil {
		return nil, fmt.Errorf("missing fields in response")
	}

	issue := &Issue{
		Key:     strVal(raw, "key"),
		Summary: strVal(fields, "summary"),
		Labels:  strSlice(fields, "labels"),
	}

	if status, ok := fields["status"].(map[string]any); ok {
		issue.Status = strVal(status, "name")
	}
	if priority, ok := fields["priority"].(map[string]any); ok {
		issue.Priority = strVal(priority, "name")
	}
	if assignee, ok := fields["assignee"].(map[string]any); ok {
		issue.Assignee = strVal(assignee, "displayName")
	}
	if reporter, ok := fields["reporter"].(map[string]any); ok {
		issue.Reporter = strVal(reporter, "displayName")
	}
	if issueType, ok := fields["issuetype"].(map[string]any); ok {
		issue.IssueType = strVal(issueType, "name")
	}

	issue.Created = strVal(fields, "created")
	issue.Updated = strVal(fields, "updated")

	// Description: prefer rendered HTML, strip tags; fall back to ADF
	if rendered != nil {
		if desc, ok := rendered["description"].(string); ok && desc != "" {
			issue.Description = stripHTML(desc)
		}
	}
	if issue.Description == "" {
		if desc, ok := fields["description"].(map[string]any); ok {
			issue.Description = adfToPlainText(desc)
		}
	}

	expandSet := make(map[string]bool)
	for _, e := range expand {
		expandSet[e] = true
	}

	// Comments
	if expandSet["comments"] || expandSet["all"] {
		if commentField, ok := fields["comment"].(map[string]any); ok {
			if comments, ok := commentField["comments"].([]any); ok {
				for _, c := range comments {
					if cm, ok := c.(map[string]any); ok {
						issue.Comments = append(issue.Comments, *parseComment(cm))
					}
				}
			}
		}
	}

	// Attachments
	if expandSet["attachments"] || expandSet["all"] {
		if attachments, ok := fields["attachment"].([]any); ok {
			for _, a := range attachments {
				if am, ok := a.(map[string]any); ok {
					issue.Attachments = append(issue.Attachments, parseAttachment(am))
				}
			}
		}
	}

	// Custom fields
	for k, v := range fields {
		if !strings.HasPrefix(k, "customfield_") || v == nil {
			continue
		}
		if issue.CustomFields == nil {
			issue.CustomFields = make(map[string]any)
		}
		issue.CustomFields[k] = v
	}

	return issue, nil
}

func parseComment(raw map[string]any) *Comment {
	c := &Comment{
		ID:      strVal(raw, "id"),
		Created: strVal(raw, "created"),
		Updated: strVal(raw, "updated"),
	}
	if author, ok := raw["author"].(map[string]any); ok {
		c.Author = strVal(author, "displayName")
	}
	if body, ok := raw["body"].(map[string]any); ok {
		c.Body = adfToPlainText(body)
	} else if body, ok := raw["body"].(string); ok {
		c.Body = body
	}
	return c
}

func parseAttachment(raw map[string]any) Attachment {
	size, _ := raw["size"].(float64)
	a := Attachment{
		ID:       strVal(raw, "id"),
		Filename: strVal(raw, "filename"),
		MimeType: strVal(raw, "mimeType"),
		Size:     int64(size),
		URL:      strVal(raw, "content"),
		Created:  strVal(raw, "created"),
	}
	if author, ok := raw["author"].(map[string]any); ok {
		a.Author = strVal(author, "displayName")
	}
	return a
}

// adfToPlainText converts Atlassian Document Format to plain text.
func adfToPlainText(doc map[string]any) string {
	if doc == nil {
		return ""
	}
	var buf strings.Builder
	adfWalk(&buf, doc, 0)
	return strings.TrimSpace(buf.String())
}

func adfWalkChildren(buf *strings.Builder, children []any, listIndex int) {
	for _, child := range children {
		if c, ok := child.(map[string]any); ok {
			adfWalk(buf, c, listIndex)
		}
	}
}

func adfWalk(buf *strings.Builder, node map[string]any, listIndex int) {
	nodeType, _ := node["type"].(string)

	switch nodeType {
	case "text":
		text, _ := node["text"].(string)
		buf.WriteString(text)
		return
	case "hardBreak":
		buf.WriteString("\n")
		return
	}

	children, _ := node["content"].([]any)

	switch nodeType {
	case "paragraph", "heading":
		adfWalkChildren(buf, children, 0)
		buf.WriteString("\n")
	case "orderedList":
		for i, child := range children {
			if c, ok := child.(map[string]any); ok {
				adfWalk(buf, c, i+1)
			}
		}
	case "listItem":
		if listIndex > 0 {
			buf.WriteString(fmt.Sprintf("%d. ", listIndex))
		} else {
			buf.WriteString("- ")
		}
		adfWalkChildren(buf, children, 0)
	case "codeBlock":
		buf.WriteString("```\n")
		adfWalkChildren(buf, children, 0)
		buf.WriteString("\n```\n")
	default:
		adfWalkChildren(buf, children, 0)
	}
}

// stripHTML removes HTML tags for a basic plain-text conversion.
func stripHTML(s string) string {
	var buf strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			buf.WriteRune(r)
		}
	}
	return strings.TrimSpace(buf.String())
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func strSlice(m map[string]any, key string) []string {
	arr, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// NewHandler returns a pipe.Handler for the jira pipe.
func NewHandler(client *JiraClient, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		if action == "" {
			action = "get"
		}

		out := envelope.New("jira", action)
		out.Args = flags

		id := flags["id"]
		if id == "" {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError("--id is required: provide a Jira issue key (e.g., PROJ-123)")
			return out
		}

		if !issueKeyRe.MatchString(id) {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("invalid issue key: %s (expected format: PROJ-123)", id))
			return out
		}

		ctx := context.Background()

		switch action {
		case "get":
			out = handleGet(ctx, client, logger, id, flags, out)
		case "comment":
			out = handleComment(ctx, client, logger, id, flags, input, out)
		case "update":
			out = handleUpdate(ctx, client, logger, id, flags, input, out)
		default:
			out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s (expected: get, comment, update)", action))
		}

		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func handleGet(ctx context.Context, client *JiraClient, logger *slog.Logger, id string, flags map[string]string, out envelope.Envelope) envelope.Envelope {
	expandStr := flags["expand"]
	if expandStr == "" {
		expandStr = "comments,attachments"
	}
	expand := strings.Split(expandStr, ",")
	for i := range expand {
		expand[i] = strings.TrimSpace(expand[i])
	}

	fieldsParam := flags["fields"]

	logger.Debug("get issue", "id", id, "expand", expand, "fields", fieldsParam)

	issue, err := client.GetIssue(ctx, id, expand, fieldsParam)
	if err != nil {
		out.Error = classifyJiraError(id, err)
		return out
	}

	out.Content = issue
	out.ContentType = envelope.ContentStructured
	return out
}

func handleComment(ctx context.Context, client *JiraClient, logger *slog.Logger, id string, flags map[string]string, input envelope.Envelope, out envelope.Envelope) envelope.Envelope {
	body := flags["comment"]
	if body == "" {
		if s, ok := input.Content.(string); ok {
			body = s
		}
	}

	if body == "" {
		out.Error = envelope.FatalError("comment body is empty — provide via --comment flag or envelope content")
		return out
	}

	logger.Debug("add comment", "id", id, "body_len", len(body))

	comment, err := client.AddComment(ctx, id, body)
	if err != nil {
		out.Error = classifyJiraError(id, err)
		return out
	}

	out.Content = comment
	out.ContentType = envelope.ContentStructured
	return out
}

func handleUpdate(ctx context.Context, client *JiraClient, logger *slog.Logger, id string, flags map[string]string, input envelope.Envelope, out envelope.Envelope) envelope.Envelope {
	fieldsJSON := flags["fields"]

	var fields map[string]any

	if fieldsJSON != "" {
		if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("--fields is not valid JSON: %v", err))
			return out
		}
	} else if input.Content != nil {
		if m, ok := input.Content.(map[string]any); ok {
			fields = m
		}
	}

	if len(fields) == 0 {
		out.Error = envelope.FatalError("no fields to update — provide via --fields flag or envelope content")
		return out
	}

	logger.Debug("update issue", "id", id, "fields", fields)

	// Handle status transitions separately
	if statusVal, ok := fields["status"]; ok {
		delete(fields, "status")
		statusName := ""
		switch v := statusVal.(type) {
		case string:
			statusName = v
		case map[string]any:
			statusName, _ = v["name"].(string)
		}
		if statusName != "" {
			if err := client.TransitionIssue(ctx, id, statusName); err != nil {
				out.Error = classifyJiraError(id, err)
				return out
			}
		}
	}

	// Update remaining fields if any
	if len(fields) > 0 {
		if err := client.UpdateIssue(ctx, id, fields); err != nil {
			out.Error = classifyJiraError(id, err)
			return out
		}
	}

	// Fetch refreshed issue
	issue, err := client.GetIssue(ctx, id, []string{"comments", "attachments"}, "")
	if err != nil {
		out.Error = classifyJiraError(id, err)
		return out
	}

	out.Content = issue
	out.ContentType = envelope.ContentStructured
	return out
}

func classifyJiraError(id string, err error) *envelope.EnvelopeError {
	je, ok := err.(*jiraError)
	if !ok {
		return envelope.ClassifyError("jira", err)
	}

	switch je.statusCode {
	case 404:
		return envelope.FatalError(fmt.Sprintf("issue not found: %s", id))
	case 401, 403:
		return envelope.FatalError("authentication failed — check jira.json credentials")
	case 429:
		return &envelope.EnvelopeError{
			Message:   je.message,
			Severity:  envelope.SeverityError,
			Retryable: true,
		}
	default:
		if je.statusCode >= 500 {
			return &envelope.EnvelopeError{
				Message:   fmt.Sprintf("Jira server error (%d): %s", je.statusCode, je.message),
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
		}
		return envelope.FatalError(je.message)
	}
}
