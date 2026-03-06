package sync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	jirapkg "github.com/justinpbarnett/virgil/internal/pipes/jira"
	slackpkg "github.com/justinpbarnett/virgil/internal/pipes/slack"
	"github.com/justinpbarnett/virgil/internal/store"
)

// --- Test helpers ---

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func runSync(jiraClient *jirapkg.JiraClient, slackClient *slackpkg.SlackClient, st *store.Store, flags map[string]string) envelope.Envelope {
	if flags == nil {
		flags = map[string]string{}
	}
	h := NewHandler(jiraClient, slackClient, st, nil)
	return h(envelope.Envelope{}, flags)
}

// mockJiraServer builds a JiraClient backed by an httptest.Server that serves
// a fixed SearchIssues response.
func mockJiraServer(t *testing.T, issues []jirapkg.Issue) *jirapkg.JiraClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/search/jql" {
			apiIssues := make([]map[string]any, 0, len(issues))
			for _, issue := range issues {
				comments := make([]any, 0, len(issue.Comments))
				for _, c := range issue.Comments {
					comments = append(comments, map[string]any{
						"id":      c.ID,
						"created": c.Created,
						"updated": c.Updated,
						"author":  map[string]any{"displayName": c.Author},
						"body":    c.Body,
					})
				}
				labels := make([]any, len(issue.Labels))
				for i, l := range issue.Labels {
					labels[i] = l
				}
				apiIssues = append(apiIssues, map[string]any{
					"key": issue.Key,
					"fields": map[string]any{
						"summary":   issue.Summary,
						"status":    map[string]any{"name": issue.Status},
						"priority":  map[string]any{"name": issue.Priority},
						"assignee":  map[string]any{"displayName": issue.Assignee},
						"reporter":  map[string]any{"displayName": issue.Reporter},
						"labels":    labels,
						"issuetype": map[string]any{"name": "Story"},
						"created":   issue.Created,
						"updated":   issue.Updated,
						"comment":   map[string]any{"comments": comments},
					},
				})
			}
			resp := map[string]any{
				"total":      len(issues),
				"startAt":    0,
				"maxResults": len(issues),
				"issues":     apiIssues,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	return jirapkg.NewClient(jirapkg.JiraConfig{
		BaseURL: srv.URL,
		Email:   "test@example.com",
		Token:   "testtoken",
		Project: "PTP",
	})
}

// mockSlackServer builds a SlackClient backed by an httptest.Server.
// Mentions are served as channel history; their Thread field is served as replies.
func mockSlackServer(t *testing.T, userID string, channels []string, mentions []slackpkg.SlackMention) *slackpkg.SlackClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.history":
			ch := r.URL.Query().Get("channel")
			msgs := make([]map[string]any, 0)
			for _, m := range mentions {
				if m.Channel != ch {
					continue
				}
				msgs = append(msgs, map[string]any{
					"user":      "U_AUTHOR",
					"text":      m.Text,
					"ts":        m.Timestamp,
					"thread_ts": m.ThreadTS,
				})
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": msgs})
		case "/conversations.replies":
			ts := r.URL.Query().Get("ts")
			var threadMsgs []map[string]any
			for _, m := range mentions {
				if m.ThreadTS == ts || m.Timestamp == ts {
					for _, line := range m.Thread {
						threadMsgs = append(threadMsgs, map[string]any{
							"user": "U_AUTHOR",
							"text": line,
							"ts":   m.Timestamp,
						})
					}
					break
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": threadMsgs})
		case "/users.info":
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"user": map[string]any{
					"profile": map[string]any{"display_name": "TestUser"},
				},
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not_found"})
		}
	}))
	t.Cleanup(srv.Close)

	client := slackpkg.NewClient(slackpkg.SlackConfig{
		Token:    "xoxp-test",
		UserID:   userID,
		Channels: channels,
	})
	client.BaseURL = srv.URL
	return client
}

// --- Tests ---

func TestSyncJIRAOnly(t *testing.T) {
	issues := []jirapkg.Issue{
		{Key: "PTP-1", Summary: "Fix login", Status: "In Progress", Priority: "High", Assignee: "Jane"},
		{Key: "PTP-2", Summary: "Add dashboard", Status: "To Do", Priority: "Medium"},
	}
	st := testStore(t)

	out := runSync(mockJiraServer(t, issues), nil, st, nil)

	if out.Error != nil {
		t.Fatalf("unexpected error: %v", out.Error.Message)
	}
	m := out.Content.(map[string]any)
	if got := m["created"].(int); got != 2 {
		t.Errorf("created=%d want 2", got)
	}
	if got := m["merged"].(int); got != 0 {
		t.Errorf("merged=%d want 0", got)
	}

	todo1, err := st.FindTodoByExternalID("jira:PTP-1")
	if err != nil {
		t.Fatalf("find PTP-1: %v", err)
	}
	if want := "[PTP-1] Fix login"; todo1.Title != want {
		t.Errorf("title=%q want %q", todo1.Title, want)
	}
	if !containsTag(todo1.Tags, "jira") {
		t.Errorf("expected 'jira' tag, got %v", todo1.Tags)
	}
}

func TestSyncIdempotent(t *testing.T) {
	issues := []jirapkg.Issue{
		{Key: "PTP-10", Summary: "Idempotent task", Status: "In Progress", Priority: "Medium"},
	}
	st := testStore(t)

	out1 := runSync(mockJiraServer(t, issues), nil, st, nil)
	m1 := out1.Content.(map[string]any)
	if got := m1["created"].(int); got != 1 {
		t.Fatalf("first sync created=%d want 1", got)
	}

	out2 := runSync(mockJiraServer(t, issues), nil, st, nil)
	m2 := out2.Content.(map[string]any)
	if got := m2["created"].(int); got != 0 {
		t.Errorf("second sync created=%d want 0 (idempotent)", got)
	}
	if got := m2["updated"].(int); got != 1 {
		t.Errorf("second sync updated=%d want 1", got)
	}
}

func TestSyncJIRANil_SlackRuns(t *testing.T) {
	userID := "U123"
	mention := slackpkg.SlackMention{
		Channel:   "C_GENERAL",
		ThreadTS:  "111.000",
		Text:      "<@U123> please look at this",
		Thread:    []string{"TestUser: please look at this"},
		Timestamp: "111.000",
	}
	st := testStore(t)

	out := runSync(nil, mockSlackServer(t, userID, []string{"C_GENERAL"}, []slackpkg.SlackMention{mention}), st, nil)

	m := out.Content.(map[string]any)
	if got := m["created"].(int); got != 1 {
		t.Errorf("created=%d want 1", got)
	}
}

func TestSyncSlackNil_JIRARuns(t *testing.T) {
	issues := []jirapkg.Issue{
		{Key: "PTP-5", Summary: "Auth fix", Status: "In Progress", Priority: "Low"},
	}
	st := testStore(t)

	out := runSync(mockJiraServer(t, issues), nil, st, nil)

	m := out.Content.(map[string]any)
	if got := m["created"].(int); got != 1 {
		t.Errorf("created=%d want 1", got)
	}
}

func TestSyncPriorityMapping(t *testing.T) {
	cases := []struct {
		jiraPriority string
		want         int
	}{
		{"Highest", 1},
		{"Blocker", 1},
		{"High", 2},
		{"Medium", 3},
		{"", 3},
		{"Low", 4},
		{"Lowest", 5},
		{"Unknown", 3},
	}

	for _, tc := range cases {
		t.Run(tc.jiraPriority, func(t *testing.T) {
			got := mapJiraPriority(tc.jiraPriority)
			if got != tc.want {
				t.Errorf("mapJiraPriority(%q) = %d, want %d", tc.jiraPriority, got, tc.want)
			}
		})
	}
}

func TestSyncNeedsAttention(t *testing.T) {
	issues := []jirapkg.Issue{
		{
			Key:      "PTP-20",
			Summary:  "UAT fix needed",
			Status:   "UAT",
			Priority: "Medium",
			Assignee: "Dev",
			Comments: []jirapkg.Comment{
				{Author: "Client", Body: "Please fix this"},
			},
		},
	}
	st := testStore(t)
	runSync(mockJiraServer(t, issues), nil, st, nil)

	todo, err := st.FindTodoByExternalID("jira:PTP-20")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if todo.Priority != 2 {
		t.Errorf("priority=%d want 2", todo.Priority)
	}
	if !containsTag(todo.Tags, "needs-fixes") {
		t.Errorf("expected 'needs-fixes' tag, got %v", todo.Tags)
	}
}

func TestSyncNeedsAttentionSelfComment(t *testing.T) {
	issues := []jirapkg.Issue{
		{
			Key:      "PTP-21",
			Summary:  "UAT self comment",
			Status:   "UAT",
			Priority: "Medium",
			Assignee: "Dev",
			Comments: []jirapkg.Comment{
				{Author: "Dev", Body: "I reviewed this"},
			},
		},
	}
	st := testStore(t)
	runSync(mockJiraServer(t, issues), nil, st, nil)

	todo, err := st.FindTodoByExternalID("jira:PTP-21")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if todo.Priority != 3 {
		t.Errorf("priority=%d want 3 (no override for self comment)", todo.Priority)
	}
	if containsTag(todo.Tags, "needs-fixes") {
		t.Errorf("unexpected 'needs-fixes' tag")
	}
}

func TestSyncLabelsInTags(t *testing.T) {
	issues := []jirapkg.Issue{
		{Key: "PTP-30", Summary: "Labeled task", Status: "In Progress", Priority: "Low", Labels: []string{"frontend", "critical"}},
	}
	st := testStore(t)
	runSync(mockJiraServer(t, issues), nil, st, nil)

	todo, err := st.FindTodoByExternalID("jira:PTP-30")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	for _, tag := range []string{"jira", "frontend", "critical"} {
		if !containsTag(todo.Tags, tag) {
			t.Errorf("missing tag %q in %v", tag, todo.Tags)
		}
	}
}

func TestSyncSlackMergeIntoJIRATodo(t *testing.T) {
	issues := []jirapkg.Issue{
		{Key: "PTP-40", Summary: "Slack mentioned task", Status: "In Progress", Priority: "Medium"},
	}
	st := testStore(t)

	// First pass: create JIRA todo
	runSync(mockJiraServer(t, issues), nil, st, nil)

	// Second pass: JIRA re-sync + Slack mention referencing PTP-40
	userID := "U999"
	mention := slackpkg.SlackMention{
		Channel:   "C_DEV",
		ThreadTS:  "222.000",
		Text:      "<@U999> check PTP-40",
		Thread:    []string{"TestUser: check PTP-40", "TestUser: needs fix"},
		JiraKeys:  []string{"PTP-40"},
		Timestamp: "222.000",
	}

	out := runSync(
		mockJiraServer(t, issues),
		mockSlackServer(t, userID, []string{"C_DEV"}, []slackpkg.SlackMention{mention}),
		st,
		nil,
	)

	m := out.Content.(map[string]any)
	if got := m["merged"].(int); got != 1 {
		t.Errorf("merged=%d want 1", got)
	}

	todo, err := st.FindTodoByExternalID("jira:PTP-40")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(todo.Details, "Slack Thread") {
		t.Errorf("expected 'Slack Thread' in details, got: %q", todo.Details)
	}
}

func TestSyncStandaloneSlackTodo(t *testing.T) {
	userID := "U777"
	mention := slackpkg.SlackMention{
		Channel:   "C_SUPPORT",
		ThreadTS:  "333.000",
		Text:      "<@U777> can you help with the onboarding flow?",
		Thread:    []string{"TestUser: can you help with the onboarding flow?"},
		Timestamp: "333.000",
	}
	st := testStore(t)

	out := runSync(nil, mockSlackServer(t, userID, []string{"C_SUPPORT"}, []slackpkg.SlackMention{mention}), st, nil)

	m := out.Content.(map[string]any)
	if got := m["created"].(int); got != 1 {
		t.Errorf("created=%d want 1", got)
	}

	todo, err := st.FindTodoByExternalID("slack:C_SUPPORT:333.000")
	if err != nil {
		t.Fatalf("find standalone slack todo: %v", err)
	}
	if !containsTag(todo.Tags, "slack") {
		t.Errorf("expected 'slack' tag, got %v", todo.Tags)
	}
}

func TestSyncStaleDetection(t *testing.T) {
	st := testStore(t)
	// Seed an old JIRA todo that won't appear in current results
	_, _, err := st.UpsertTodoByExternalID("jira:PTP-OLD", "[PTP-OLD] Old task", "", 3, "", []string{"jira"})
	if err != nil {
		t.Fatalf("seed stale todo: %v", err)
	}

	issues := []jirapkg.Issue{
		{Key: "PTP-NEW", Summary: "New task", Status: "In Progress", Priority: "Medium"},
	}

	out := runSync(mockJiraServer(t, issues), nil, st, nil)
	m := out.Content.(map[string]any)
	if got := m["stale"].(int); got != 1 {
		t.Errorf("stale=%d want 1", got)
	}

	stale, err := st.FindTodoByExternalID("jira:PTP-OLD")
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if !containsTag(stale.Tags, "stale") {
		t.Errorf("expected 'stale' tag, got %v", stale.Tags)
	}
}

func TestSyncStaleRemovedWhenReappears(t *testing.T) {
	st := testStore(t)
	_, _, err := st.UpsertTodoByExternalID("jira:PTP-BACK", "[PTP-BACK] Returning task", "", 3, "", []string{"jira", "stale"})
	if err != nil {
		t.Fatalf("seed stale todo: %v", err)
	}

	issues := []jirapkg.Issue{
		{Key: "PTP-BACK", Summary: "Returning task", Status: "In Progress", Priority: "Medium"},
	}

	runSync(mockJiraServer(t, issues), nil, st, nil)

	todo, err := st.FindTodoByExternalID("jira:PTP-BACK")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if containsTag(todo.Tags, "stale") {
		t.Errorf("stale tag should have been removed, got %v", todo.Tags)
	}
}

func TestSyncEnvelopeCompliance(t *testing.T) {
	st := testStore(t)

	out := runSync(mockJiraServer(t, nil), nil, st, nil)

	if out.Pipe != "sync" {
		t.Errorf("pipe=%q want sync", out.Pipe)
	}
	if out.Action != "sync" {
		t.Errorf("action=%q want sync", out.Action)
	}
	if out.ContentType != "structured" {
		t.Errorf("content_type=%q want structured", out.ContentType)
	}
	if out.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
	if out.Duration < 0 {
		t.Error("duration should be non-negative")
	}
	if out.Error != nil {
		t.Errorf("unexpected error: %v", out.Error)
	}
	m, ok := out.Content.(map[string]any)
	if !ok {
		t.Fatalf("content type %T, want map", out.Content)
	}
	for _, key := range []string{"created", "updated", "merged", "stale"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in content", key)
		}
	}
}

func TestSyncArgsPopulated(t *testing.T) {
	st := testStore(t)
	out := runSync(mockJiraServer(t, nil), nil, st, map[string]string{"since": "3d"})

	if out.Args["since"] != "3d" {
		t.Errorf("args[since]=%q want 3d", out.Args["since"])
	}
}

func TestSyncBothEmpty(t *testing.T) {
	st := testStore(t)

	out := runSync(mockJiraServer(t, nil), nil, st, nil)

	if out.Error != nil {
		t.Fatalf("unexpected error: %v", out.Error)
	}
	m := out.Content.(map[string]any)
	if got := m["created"].(int); got != 0 {
		t.Errorf("created=%d want 0", got)
	}
	if got := m["stale"].(int); got != 0 {
		t.Errorf("stale=%d want 0", got)
	}
}

// --- Helpers ---

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
