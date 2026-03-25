package tools

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"
	vgoogle "github.com/justinpbarnett/virgil/internal/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// RegisterDriveTools registers drive_list and drive_read.
func RegisterDriveTools(reg *Registry, cfg *config.Config) {
	var clients []*driveClient

	for _, acctName := range cfg.Channels.Drive.Accounts {
		emailCfg, ok := cfg.Channels.Email.Accounts[acctName]
		if !ok || emailCfg.CredentialsPath == "" {
			continue
		}
		httpClient, err := vgoogle.NewHTTPClient(emailCfg.CredentialsPath)
		if err != nil {
			slog.Warn("skip drive account", "account", acctName, "err", err)
			continue
		}
		svc, err := drive.NewService(context.Background(), option.WithHTTPClient(httpClient))
		if err != nil {
			slog.Warn("skip drive account", "account", acctName, "err", err)
			continue
		}
		clients = append(clients, &driveClient{name: acctName, svc: svc})
	}

	reg.Register(&internal.Tool{
		Name:        "drive_list",
		Description: "List files from Google Drive. Filter by folder, type, or search query.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (Drive search syntax or natural language)",
				},
				"folder": map[string]any{
					"type":        "string",
					"description": "Folder name or ID to search within",
				},
				"type": map[string]any{
					"type":    "string",
					"enum":    []string{"document", "spreadsheet", "presentation", "pdf", "any"},
					"default": "any",
				},
				"limit": map[string]any{"type": "integer", "default": 20},
			},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			if len(clients) == 0 {
				return &internal.ToolResult{Error: "no drive accounts configured"}, nil
			}

			query, _ := params["query"].(string)
			folder, _ := params["folder"].(string)
			fileType, _ := params["type"].(string)
			limit := intParam(params, "limit", 20)

			q := buildDriveQuery(query, folder, fileType)

			var results []map[string]any
			var errs []string
			for _, c := range clients {
				call := c.svc.Files.List().
					Q(q).
					PageSize(int64(limit)).
					Fields("files(id,name,mimeType,modifiedTime,owners,webViewLink)")
				resp, err := call.Do()
				if err != nil {
					slog.Warn("drive_list error", "account", c.name, "err", err)
					errs = append(errs, fmt.Sprintf("%s: %s", c.name, err))
					continue
				}
				for _, f := range resp.Files {
					owners := make([]string, 0, len(f.Owners))
					for _, o := range f.Owners {
						owners = append(owners, o.EmailAddress)
					}
					results = append(results, map[string]any{
						"id":        f.Id,
						"name":      f.Name,
						"mime_type": f.MimeType,
						"modified":  f.ModifiedTime,
						"owners":    owners,
						"link":      f.WebViewLink,
						"account":   c.name,
					})
				}
				if len(results) >= limit {
					break
				}
			}
			if len(results) == 0 && len(errs) > 0 {
				return &internal.ToolResult{Error: fmt.Sprintf("all drive accounts failed: %s", strings.Join(errs, "; "))}, nil
			}
			if results == nil {
				results = []map[string]any{}
			}
			return &internal.ToolResult{Data: results}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "drive_read",
		Description: "Read the text content of a Google Drive file (Docs, Sheets, PDFs).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_id": map[string]any{"type": "string"},
			},
			"required": []string{"file_id"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			if len(clients) == 0 {
				return &internal.ToolResult{Error: "no drive accounts configured"}, nil
			}

			fileID, _ := params["file_id"].(string)
			if fileID == "" {
				return &internal.ToolResult{Error: "file_id is required"}, nil
			}

			var lastErr error
			for _, c := range clients {
				content, err := driveReadFile(c.svc, fileID)
				if err != nil {
					slog.Warn("drive_read error", "account", c.name, "file_id", fileID, "err", err)
					lastErr = err
					continue
				}
				return &internal.ToolResult{Data: map[string]any{
					"file_id": fileID,
					"account": c.name,
					"content": content,
				}}, nil
			}

			if lastErr != nil {
				return &internal.ToolResult{Error: fmt.Sprintf("file %q: %v", fileID, lastErr)}, nil
			}
			return &internal.ToolResult{Error: fmt.Sprintf("file %q not found", fileID)}, nil
		},
	})
}

type driveClient struct {
	name string
	svc  *drive.Service
}

func buildDriveQuery(query, folder, fileType string) string {
	var parts []string

	if query != "" {
		parts = append(parts, fmt.Sprintf("fullText contains '%s'", strings.ReplaceAll(query, "'", "\\'")))
	}
	if folder != "" {
		parts = append(parts, fmt.Sprintf("'%s' in parents", strings.ReplaceAll(folder, "'", "\\'")))
	}

	mimeMap := map[string]string{
		"document":     "application/vnd.google-apps.document",
		"spreadsheet":  "application/vnd.google-apps.spreadsheet",
		"presentation": "application/vnd.google-apps.presentation",
		"pdf":          "application/pdf",
	}
	if mime, ok := mimeMap[fileType]; ok {
		parts = append(parts, fmt.Sprintf("mimeType = '%s'", mime))
	}

	parts = append(parts, "trashed = false")
	return strings.Join(parts, " and ")
}

func driveReadFile(svc *drive.Service, fileID string) (string, error) {
	file, err := svc.Files.Get(fileID).Fields("mimeType").Do()
	if err != nil {
		return "", err
	}

	exportMIME := map[string]string{
		"application/vnd.google-apps.document":     "text/plain",
		"application/vnd.google-apps.spreadsheet":  "text/csv",
		"application/vnd.google-apps.presentation": "text/plain",
	}

	var resp *http.Response
	if mime, ok := exportMIME[file.MimeType]; ok {
		resp, err = svc.Files.Export(fileID, mime).Download()
	} else {
		resp, err = svc.Files.Get(fileID).Download()
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return string(data), err
}
