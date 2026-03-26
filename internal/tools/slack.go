package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/trust"
)

// RegisterSlackTools registers slack_read, slack_post, and slack_search.
func RegisterSlackTools(reg *Registry, cfg *config.Config, ts *trust.Store) {
	clients := make(map[string]*slackClient)
	var initErrs []string

	for name, ws := range cfg.Channels.Slack.Workspaces {
		sc := &slackClient{workspace: name}

		if ws.TokenPath != "" {
			tok, cookie, err := loadSlackTokenFile(ws.TokenPath)
			if err != nil {
				slog.Warn("skip slack workspace: token file error", "workspace", name, "err", err)
				initErrs = append(initErrs, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			sc.token = tok
			sc.cookie = cookie
		} else if ws.TokenEnv != "" {
			sc.token = os.Getenv(ws.TokenEnv)
		}

		if sc.token == "" {
			slog.Warn("skip slack workspace: no token", "workspace", name)
			initErrs = append(initErrs, fmt.Sprintf("%s: no token configured", name))
			continue
		}
		clients[name] = sc
	}

	reg.Register(&internal.Tool{
		Name:        "slack_read",
		Description: "Read messages from a Slack channel or thread.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workspace": map[string]any{"type": "string"},
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel name or ID",
				},
				"thread_ts": map[string]any{
					"type":        "string",
					"description": "Thread timestamp to read replies",
				},
				"limit": map[string]any{"type": "integer", "default": 20},
				"since": map[string]any{
					"type":        "string",
					"description": "Only messages after this time",
				},
			},
			"required": []string{"workspace", "channel"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			workspace, _ := params["workspace"].(string)
			channel, _ := params["channel"].(string)
			threadTS, _ := params["thread_ts"].(string)
			limit := intParam(params, "limit", 20)
			since, _ := params["since"].(string)

			c, ok := clients[workspace]
			if !ok {
				if len(initErrs) > 0 {
					return &internal.ToolResult{Error: fmt.Sprintf("workspace %q not available (init errors: %s)", workspace, strings.Join(initErrs, "; "))}, nil
				}
				return &internal.ToolResult{Error: fmt.Sprintf("workspace %q not configured", workspace)}, nil
			}

			channelID, err := c.resolveChannel(ctx, channel)
			if err != nil {
				return nil, fmt.Errorf("slack_read resolve channel: %w", err)
			}

			v := url.Values{
				"channel": {channelID},
				"limit":   {fmt.Sprintf("%d", limit)},
			}
			if since != "" {
				if t := parseDateTime(since); !t.IsZero() {
					v.Set("oldest", fmt.Sprintf("%d", t.Unix()))
				}
			}

			method := "conversations.history"
			if threadTS != "" {
				method = "conversations.replies"
				v.Set("ts", threadTS)
			}

			resp, err := c.apiGet(ctx, method, v)
			if err != nil {
				return nil, fmt.Errorf("slack_read: %w", err)
			}

			messages, _ := resp["messages"].([]any)
			return &internal.ToolResult{Data: map[string]any{
				"workspace": workspace,
				"channel":   channel,
				"messages":  messages,
			}}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "slack_post",
		Description: "Post a message to a Slack channel or thread.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workspace": map[string]any{"type": "string"},
				"channel":   map[string]any{"type": "string"},
				"text":      map[string]any{"type": "string"},
				"thread_ts": map[string]any{
					"type":        "string",
					"description": "Reply to this thread",
				},
			},
			"required": []string{"workspace", "channel", "text"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			workspace, _ := params["workspace"].(string)
			channel, _ := params["channel"].(string)
			text, _ := params["text"].(string)
			threadTS, _ := params["thread_ts"].(string)

			if workspace == "" || channel == "" || text == "" {
				return &internal.ToolResult{Error: "workspace, channel, and text are required"}, nil
			}

			if blocked := checkTrust(ctx, ts, "slack_post", workspace, "*"); blocked != nil {
				return blocked, nil
			}

			c, ok := clients[workspace]
			if !ok {
				if len(initErrs) > 0 {
					return &internal.ToolResult{Error: fmt.Sprintf("workspace %q not available (init errors: %s)", workspace, strings.Join(initErrs, "; "))}, nil
				}
				return &internal.ToolResult{Error: fmt.Sprintf("workspace %q not configured", workspace)}, nil
			}

			channelID, err := c.resolveChannel(ctx, channel)
			if err != nil {
				return nil, fmt.Errorf("slack_post resolve channel: %w", err)
			}

			body := map[string]any{
				"channel": channelID,
				"text":    text,
			}
			if threadTS != "" {
				body["thread_ts"] = threadTS
			}

			resp, err := c.apiPost(ctx, "chat.postMessage", body)
			if err != nil {
				return nil, fmt.Errorf("slack_post: %w", err)
			}

			return &internal.ToolResult{Data: map[string]any{
				"workspace": workspace,
				"channel":   channel,
				"ts":        resp["ts"],
				"status":    "posted",
			}}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "slack_search",
		Description: "Search Slack messages across workspaces.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"workspace": map[string]any{
					"type":        "string",
					"description": "Limit to one workspace",
				},
				"limit": map[string]any{"type": "integer", "default": 20},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			query, _ := params["query"].(string)
			workspace, _ := params["workspace"].(string)
			limit := intParam(params, "limit", 20)

			if query == "" {
				return &internal.ToolResult{Error: "query is required"}, nil
			}

			var allMatches []any
			var errs []string
			for wsName, c := range filterClients(clients, workspace) {
				v := url.Values{
					"query": {query},
					"count": {fmt.Sprintf("%d", limit)},
				}
				resp, err := c.apiGet(ctx, "search.messages", v)
				if err != nil {
					slog.Warn("slack_search error", "workspace", wsName, "err", err)
					errs = append(errs, fmt.Sprintf("%s: %s", wsName, err))
					continue
				}
				if msgs, ok := resp["messages"].(map[string]any); ok {
					if matches, ok := msgs["matches"].([]any); ok {
						allMatches = append(allMatches, matches...)
					}
				}
			}
			if len(allMatches) == 0 && len(errs) > 0 {
				return &internal.ToolResult{Error: fmt.Sprintf("all workspaces failed: %s", strings.Join(errs, "; "))}, nil
			}
			if allMatches == nil {
				allMatches = []any{}
			}
			return partialResult("matches", allMatches, errs), nil
		},
	})
}

type slackClient struct {
	token     string
	cookie    string // xoxd- session cookie, empty for bot tokens
	workspace string
	mu        sync.Mutex
	channels  map[string]string // name -> ID cache
}

func (c *slackClient) setAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.cookie != "" {
		req.Header.Set("Cookie", "d="+c.cookie)
	}
}

func (c *slackClient) apiGet(ctx context.Context, method string, params url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://slack.com/api/"+method+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	result, err := httpJSON(req)
	if err != nil {
		return nil, err
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return nil, fmt.Errorf("slack API error: %s", errMsg)
	}
	return result, nil
}

func (c *slackClient) apiPost(ctx context.Context, method string, body map[string]any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/"+method, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	result, err := httpJSON(req)
	if err != nil {
		return nil, err
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return nil, fmt.Errorf("slack API error: %s", errMsg)
	}
	return result, nil
}

func loadSlackTokenFile(path string) (token, cookie string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read token file: %w", err)
	}
	var tf struct {
		Token  string `json:"token"`
		Cookie string `json:"cookie"`
	}
	if err := json.Unmarshal(data, &tf); err != nil {
		return "", "", fmt.Errorf("parse token file: %w", err)
	}
	if tf.Token == "" {
		return "", "", fmt.Errorf("token file has no token")
	}
	return tf.Token, tf.Cookie, nil
}

func (c *slackClient) resolveChannel(ctx context.Context, channel string) (string, error) {
	if len(channel) > 0 && (channel[0] == 'C' || channel[0] == 'D' || channel[0] == 'G') && len(channel) >= 9 {
		return channel, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.channels != nil {
		if id, ok := c.channels[channel]; ok {
			return id, nil
		}
	}

	// Fetch under lock to prevent concurrent fetches. Fetches at most 200 channels;
	// workspaces with more channels may fail to resolve by name.
	resp, err := c.apiGet(ctx, "conversations.list", url.Values{
		"types": {"public_channel,private_channel"},
		"limit": {"200"},
	})
	if err != nil {
		return "", err
	}

	if meta, ok := resp["response_metadata"].(map[string]any); ok {
		if cursor, _ := meta["next_cursor"].(string); cursor != "" {
			slog.Warn("slack: channel list truncated at 200, some channels may not resolve by name", "workspace", c.workspace)
		}
	}

	c.channels = make(map[string]string)
	if chans, ok := resp["channels"].([]any); ok {
		for _, ch := range chans {
			if m, ok := ch.(map[string]any); ok {
				name, _ := m["name"].(string)
				id, _ := m["id"].(string)
				c.channels[name] = id
			}
		}
	}

	if id, ok := c.channels[channel]; ok {
		return id, nil
	}
	return "", fmt.Errorf("channel %q not found in workspace %q", channel, c.workspace)
}
