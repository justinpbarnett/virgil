package sync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	jirapkg "github.com/justinpbarnett/virgil/internal/pipes/jira"
	slackpkg "github.com/justinpbarnett/virgil/internal/pipes/slack"
	"github.com/justinpbarnett/virgil/internal/store"
)

// SyncSummary reports the results of a sync operation.
type SyncSummary struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Merged  int      `json:"merged"`
	Stale   int      `json:"stale"`
	Errors  []string `json:"errors,omitempty"`
}

// NewHandler returns a pipe.Handler for the sync pipe.
func NewHandler(jiraClient *jirapkg.JiraClient, slackClient *slackpkg.SlackClient, st *store.Store, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("sync", "sync")
		out.Args = flags
		defer func() { out.Duration = time.Since(out.Timestamp) }()

		ctx := context.Background()
		since := flags["since"]
		if since == "" {
			since = "7d"
		}

		summary := &SyncSummary{}

		// Phase 1 -- JIRA Fetch
		var issues []jirapkg.Issue
		if jiraClient != nil {
			jql := "assignee = currentUser() AND status NOT IN (Done) ORDER BY updated DESC"
			if jiraClient.Project != "" {
				jql = "assignee = currentUser() AND project = " + jiraClient.Project + " AND status NOT IN (Done) ORDER BY updated DESC"
			}
			var err error
			issues, err = jiraClient.SearchIssues(ctx, jql, []string{"comments"}, 100)
			if err != nil {
				logger.Error("jira fetch failed", "error", err)
				summary.Errors = append(summary.Errors, "JIRA: "+err.Error())
			}
		}

		// Phase 2 -- Slack Fetch
		var mentions []slackpkg.SlackMention
		jiraProject := ""
		if jiraClient != nil {
			jiraProject = jiraClient.Project
		}
		if slackClient != nil {
			oldest := slackpkg.ParseSinceDuration(since)
			var scanErrs []error
			mentions, scanErrs = slackClient.ScanMentions(ctx, oldest, jiraProject)
			for _, e := range scanErrs {
				logger.Warn("slack scan error", "error", e)
				summary.Errors = append(summary.Errors, "Slack: "+e.Error())
			}
		}

		// Phase 3 -- JIRA Upsert
		syncedExternalIDs := make(map[string]bool)
		for i := range issues {
			issue := &issues[i]
			externalID := "jira:" + issue.Key
			syncedExternalIDs[externalID] = true

			title := "[" + issue.Key + "] " + issue.Summary
			details := buildJiraDetails(issue)
			priority := mapJiraPriority(issue.Priority)
			tags := append([]string{"jira"}, issue.Labels...)

			if jirapkg.DetectNeedsAttention(issue) {
				priority = 2
				tags = appendUnique(tags, "needs-fixes")
			}

			_, created, err := st.UpsertTodoByExternalID(externalID, title, details, priority, "", tags)
			if err != nil {
				logger.Warn("upsert jira todo failed", "key", issue.Key, "error", err)
				summary.Errors = append(summary.Errors, fmt.Sprintf("upsert %s: %v", issue.Key, err))
				continue
			}
			if created {
				summary.Created++
			} else {
				summary.Updated++
			}
		}

		// Phase 4 -- Slack Merge
		for _, mention := range mentions {
			if len(mention.JiraKeys) > 0 {
				for _, key := range mention.JiraKeys {
					todo, err := st.FindTodoByExternalID("jira:" + key)
					if err != nil {
						continue
					}
					threadCtx := buildSlackThreadContext(mention)
					updatedDetails := strings.TrimSpace(todo.Details) + "\n\n" + threadCtx
					if err := st.UpdateTodo(todo.ID, map[string]string{"details": updatedDetails}); err != nil {
						logger.Warn("merge slack thread failed", "jira_key", key, "error", err)
						summary.Errors = append(summary.Errors, fmt.Sprintf("merge slack->%s: %v", key, err))
						continue
					}
					summary.Merged++
				}
			} else {
				externalID := "slack:" + mention.Channel + ":" + mention.ThreadTS
				title := truncate(mention.Text, 80)
				details := strings.Join(mention.Thread, "\n")
				_, created, err := st.UpsertTodoByExternalID(externalID, title, details, 3, "", []string{"slack"})
				if err != nil {
					logger.Warn("upsert slack todo failed", "external_id", externalID, "error", err)
					summary.Errors = append(summary.Errors, fmt.Sprintf("upsert slack todo: %v", err))
					continue
				}
				if created {
					summary.Created++
				}
			}
		}

		// Phase 5 -- Stale Detection
		jiraTodos, err := st.ListTodosWithExternalIDPrefix("jira:")
		if err != nil {
			logger.Warn("stale detection query failed", "error", err)
			summary.Errors = append(summary.Errors, fmt.Sprintf("stale detection: %v", err))
		} else {
			for _, todo := range jiraTodos {
				if syncedExternalIDs[todo.ExternalID] {
					// Remove stale tag if present from a previous sync
					newTags := removeTag(todo.Tags, "stale")
					if len(newTags) != len(todo.Tags) {
						_ = st.UpdateTodo(todo.ID, map[string]string{"tags": strings.Join(newTags, ",")})
					}
					continue
				}
				newTags := appendUnique(todo.Tags, "stale")
				if err := st.UpdateTodo(todo.ID, map[string]string{"tags": strings.Join(newTags, ",")}); err != nil {
					logger.Warn("mark stale failed", "id", todo.ID, "error", err)
				} else {
					summary.Stale++
				}
			}
		}

		out.Content = map[string]any{
			"created": summary.Created,
			"updated": summary.Updated,
			"merged":  summary.Merged,
			"stale":   summary.Stale,
			"errors":  summary.Errors,
		}
		out.ContentType = envelope.ContentStructured
		return out
	}
}

func buildJiraDetails(issue *jirapkg.Issue) string {
	var sb strings.Builder
	if issue.Description != "" {
		sb.WriteString("## Description\n")
		sb.WriteString(issue.Description)
		sb.WriteString("\n")
	}
	if len(issue.Comments) > 0 {
		sb.WriteString("\n## Comments\n")
		for _, c := range issue.Comments {
			sb.WriteString(fmt.Sprintf("-- %s (%s): %s\n", c.Author, c.Created, c.Body))
		}
	}
	return strings.TrimSpace(sb.String())
}

func buildSlackThreadContext(mention slackpkg.SlackMention) string {
	header := fmt.Sprintf("## Slack Thread (%s, %s)", mention.Channel, mention.Timestamp)
	body := strings.Join(mention.Thread, "\n")
	return header + "\n" + body
}

func mapJiraPriority(priority string) int {
	switch strings.ToLower(priority) {
	case "highest", "blocker":
		return 1
	case "high":
		return 2
	case "medium", "":
		return 3
	case "low":
		return 4
	case "lowest":
		return 5
	default:
		return 3
	}
}

func appendUnique(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func removeTag(tags []string, tag string) []string {
	result := tags[:0:0]
	for _, t := range tags {
		if t != tag {
			result = append(result, t)
		}
	}
	return result
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
