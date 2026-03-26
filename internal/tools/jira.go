package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"
)

// RegisterJIRATools registers jira_search, jira_read, and jira_update.
func RegisterJIRATools(reg *Registry, cfg *config.Config) {
	clients := make(map[string]*jiraClient)
	var initErrs []string

	for name, inst := range cfg.Channels.JIRA.Instances {
		token := os.Getenv(inst.APITokenEnv)
		if token == "" || inst.BaseURL == "" || inst.Email == "" {
			slog.Warn("skip jira instance: missing config", "instance", name)
			initErrs = append(initErrs, fmt.Sprintf("%s: missing config (base_url, email, or token)", name))
			continue
		}
		clients[name] = &jiraClient{
			baseURL:  strings.TrimRight(inst.BaseURL, "/"),
			email:    inst.Email,
			token:    token,
			instance: name,
		}
	}

	reg.Register(&internal.Tool{
		Name:        "jira_search",
		Description: "Search JIRA issues using JQL or natural language.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "JQL query or natural language search",
				},
				"instance": map[string]any{
					"type":        "string",
					"description": "JIRA instance (passion, enver)",
				},
				"assignee": map[string]any{
					"type":        "string",
					"description": "Filter by assignee",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status",
				},
				"limit": map[string]any{"type": "integer", "default": 20},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			if len(clients) == 0 {
				if len(initErrs) > 0 {
					return &internal.ToolResult{Error: fmt.Sprintf("no JIRA instances available (init errors: %s)", strings.Join(initErrs, "; "))}, nil
				}
				return &internal.ToolResult{Error: "no JIRA instances configured"}, nil
			}

			query, _ := params["query"].(string)
			instance, _ := params["instance"].(string)
			assignee, _ := params["assignee"].(string)
			status, _ := params["status"].(string)
			limit := intParam(params, "limit", 20)

			jql := buildJQL(query, assignee, status)

			var allIssues []map[string]any
			var errs []string
			for instName, c := range filterClients(clients, instance) {
				issues, err := c.search(ctx, jql, limit)
				if err != nil {
					slog.Warn("jira_search error", "instance", instName, "err", err)
					errs = append(errs, fmt.Sprintf("%s: %s", instName, err))
					continue
				}
				allIssues = append(allIssues, issues...)
			}
			if len(allIssues) == 0 && len(errs) > 0 {
				return &internal.ToolResult{Error: fmt.Sprintf("all instances failed: %s", strings.Join(errs, "; "))}, nil
			}
			if allIssues == nil {
				allIssues = []map[string]any{}
			}
			return partialResult("issues", allIssues, errs), nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "jira_read",
		Description: "Read full details of a JIRA issue.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"issue_key": map[string]any{
					"type":        "string",
					"description": "e.g. PASS-123",
				},
			},
			"required": []string{"issue_key"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			issueKey, _ := params["issue_key"].(string)
			if issueKey == "" {
				return &internal.ToolResult{Error: "issue_key is required"}, nil
			}

			var lastErr error
			for _, c := range clients {
				issue, err := c.getIssue(ctx, issueKey)
				if err != nil {
					lastErr = err
					continue
				}
				return &internal.ToolResult{Data: issue}, nil
			}

			if lastErr != nil {
				return &internal.ToolResult{Error: fmt.Sprintf("issue %q: %v", issueKey, lastErr)}, nil
			}
			return &internal.ToolResult{Error: fmt.Sprintf("issue %q not found", issueKey)}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "jira_update",
		Description: "Update a JIRA issue: add comment, change status, update fields.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"issue_key": map[string]any{"type": "string"},
				"comment":   map[string]any{"type": "string"},
				"transition": map[string]any{
					"type":        "string",
					"description": "Status transition name (e.g. 'Done', 'In Progress')",
				},
				"fields": map[string]any{
					"type":        "object",
					"description": "Fields to update",
				},
			},
			"required": []string{"issue_key"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			issueKey, _ := params["issue_key"].(string)
			comment, _ := params["comment"].(string)
			transition, _ := params["transition"].(string)
			fields, _ := params["fields"].(map[string]any)

			if issueKey == "" {
				return &internal.ToolResult{Error: "issue_key is required"}, nil
			}

			if len(clients) == 0 {
				if len(initErrs) > 0 {
					return &internal.ToolResult{Error: fmt.Sprintf("no JIRA instances available (init errors: %s)", strings.Join(initErrs, "; "))}, nil
				}
				return &internal.ToolResult{Error: "no JIRA instances configured"}, nil
			}

			// Find the instance that owns this issue
			var owner *jiraClient
			for _, c := range clients {
				if _, err := c.getIssue(ctx, issueKey); err == nil {
					owner = c
					break
				}
			}
			if owner == nil {
				return &internal.ToolResult{Error: fmt.Sprintf("issue %q not found on any instance", issueKey)}, nil
			}

			var actions []string
			var errors []string

			if comment != "" {
				if err := owner.addComment(ctx, issueKey, comment); err != nil {
					errors = append(errors, "comment: "+err.Error())
				} else {
					actions = append(actions, "comment added")
				}
			}

			if transition != "" {
				if err := owner.doTransition(ctx, issueKey, transition); err != nil {
					errors = append(errors, "transition: "+err.Error())
				} else {
					actions = append(actions, "transitioned to "+transition)
				}
			}

			if len(fields) > 0 {
				if err := owner.updateFields(ctx, issueKey, fields); err != nil {
					errors = append(errors, "fields: "+err.Error())
				} else {
					actions = append(actions, "fields updated")
				}
			}

			if len(actions) == 0 && len(errors) > 0 {
				return &internal.ToolResult{Error: strings.Join(errors, "; ")}, nil
			}
			result := map[string]any{
				"issue_key": issueKey,
				"instance":  owner.instance,
				"actions":   actions,
			}
			if len(errors) > 0 {
				result["errors"] = errors
			}
			return &internal.ToolResult{Data: result}, nil
		},
	})
}

type jiraClient struct {
	baseURL  string
	email    string
	token    string
	instance string
}

func (c *jiraClient) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var reqBody *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	}
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return httpJSON(req)
}

func (c *jiraClient) search(ctx context.Context, jql string, limit int) ([]map[string]any, error) {
	resp, err := c.request(ctx, "POST", "/rest/api/3/search/jql", map[string]any{
		"jql":        jql,
		"maxResults": limit,
		"fields":     []string{"summary", "status", "assignee", "priority", "created", "updated", "issuetype", "labels"},
	})
	if err != nil {
		return nil, err
	}

	rawIssues, _ := resp["issues"].([]any)
	issues := make([]map[string]any, 0, len(rawIssues))
	for _, ri := range rawIssues {
		if issue, ok := ri.(map[string]any); ok {
			issues = append(issues, jiraFormatIssue(issue, c.instance))
		}
	}
	return issues, nil
}

func (c *jiraClient) getIssue(ctx context.Context, key string) (map[string]any, error) {
	resp, err := c.request(ctx, "GET", "/rest/api/2/issue/"+key, nil)
	if err != nil {
		return nil, err
	}
	return jiraFormatIssue(resp, c.instance), nil
}

func (c *jiraClient) addComment(ctx context.Context, key, body string) error {
	_, err := c.request(ctx, "POST", "/rest/api/2/issue/"+key+"/comment", map[string]any{
		"body": body,
	})
	return err
}

func (c *jiraClient) doTransition(ctx context.Context, key, transitionName string) error {
	resp, err := c.request(ctx, "GET", "/rest/api/2/issue/"+key+"/transitions", nil)
	if err != nil {
		return err
	}

	transitions, _ := resp["transitions"].([]any)
	for _, t := range transitions {
		if tm, ok := t.(map[string]any); ok {
			name, _ := tm["name"].(string)
			if strings.EqualFold(name, transitionName) {
				id, _ := tm["id"].(string)
				_, err := c.request(ctx, "POST", "/rest/api/2/issue/"+key+"/transitions", map[string]any{
					"transition": map[string]any{"id": id},
				})
				return err
			}
		}
	}

	return fmt.Errorf("transition %q not found for issue %s", transitionName, key)
}

func (c *jiraClient) updateFields(ctx context.Context, key string, fields map[string]any) error {
	_, err := c.request(ctx, "PUT", "/rest/api/2/issue/"+key, map[string]any{
		"fields": fields,
	})
	return err
}

func buildJQL(query, assignee, status string) string {
	isJQL := strings.ContainsAny(query, "=~") || jqlHasKeyword(query)

	var parts []string
	if isJQL {
		parts = append(parts, query)
	} else if query != "" {
		parts = append(parts, fmt.Sprintf("text ~ %q", query))
	}

	if assignee != "" {
		parts = append(parts, fmt.Sprintf("assignee = %q", assignee))
	}
	if status != "" {
		parts = append(parts, fmt.Sprintf("status = %q", status))
	}

	jql := strings.Join(parts, " AND ")
	if !strings.Contains(strings.ToUpper(jql), "ORDER") {
		if jql != "" {
			jql += " ORDER BY updated DESC"
		} else {
			jql = "ORDER BY updated DESC"
		}
	}
	return jql
}

func jqlHasKeyword(q string) bool {
	upper := strings.ToUpper(q)
	for _, kw := range []string{" AND ", " OR ", " NOT ", " IN ", " ORDER "} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

func jiraFormatIssue(issue map[string]any, instance string) map[string]any {
	key, _ := issue["key"].(string)
	fields, _ := issue["fields"].(map[string]any)
	if fields == nil {
		fields = map[string]any{}
	}

	result := map[string]any{
		"key":      key,
		"instance": instance,
	}

	if v, ok := fields["summary"].(string); ok {
		result["summary"] = v
	}
	if v, ok := fields["status"].(map[string]any); ok {
		result["status"], _ = v["name"].(string)
	}
	if v, ok := fields["assignee"].(map[string]any); ok {
		result["assignee"], _ = v["displayName"].(string)
	}
	if v, ok := fields["priority"].(map[string]any); ok {
		result["priority"], _ = v["name"].(string)
	}
	if v, ok := fields["issuetype"].(map[string]any); ok {
		result["type"], _ = v["name"].(string)
	}
	if v, ok := fields["created"].(string); ok {
		result["created"] = v
	}
	if v, ok := fields["updated"].(string); ok {
		result["updated"] = v
	}
	if v, ok := fields["labels"].([]any); ok {
		result["labels"] = v
	}
	switch v := fields["description"].(type) {
	case string:
		if v != "" {
			result["description"] = v
		}
	case map[string]any:
		if text := jiraExtractADFText(v); text != "" {
			result["description"] = text
		}
	}

	return result
}

// jiraExtractADFText extracts plain text from an Atlassian Document Format node.
// The v3 search API returns descriptions as ADF objects rather than strings.
func jiraExtractADFText(node map[string]any) string {
	var sb strings.Builder
	jiraWalkADF(node, &sb)
	return strings.TrimSpace(sb.String())
}

func jiraWalkADF(node map[string]any, sb *strings.Builder) {
	if t, _ := node["type"].(string); t == "text" {
		if text, _ := node["text"].(string); text != "" {
			sb.WriteString(text)
		}
		return
	}
	if content, ok := node["content"].([]any); ok {
		for _, item := range content {
			if m, ok := item.(map[string]any); ok {
				jiraWalkADF(m, sb)
			}
		}
	}
	switch t, _ := node["type"].(string); t {
	case "paragraph", "heading", "listItem", "bulletList", "orderedList", "blockquote", "codeBlock":
		sb.WriteString("\n")
	}
}
