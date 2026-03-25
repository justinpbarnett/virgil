package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/agent"
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/db"
	"github.com/justinpbarnett/virgil/internal/memory"
	"github.com/justinpbarnett/virgil/internal/observe"
	"github.com/justinpbarnett/virgil/internal/skills"
	"github.com/justinpbarnett/virgil/internal/tools"
)

type CLI struct {
	Serve  ServeCmd  `cmd:"" help:"Start guide server (HTTP + Telegram + cron)"`
	MCP    MCPCmd    `cmd:"" help:"Start MCP server on stdio"`
	Auth   AuthCmd   `cmd:"" help:"Set up OAuth tokens"`
	Init   InitCmd   `cmd:"" help:"Initialize data directory and config"`
	Seed   SeedCmd   `cmd:"" help:"Ingest a markdown file as facts into memory"`
	Memory MemoryCmd `cmd:"" help:"Memory operations"`

	// Tool CLI commands
	Email    EmailCmd    `cmd:"" help:"Email operations"`
	Calendar CalendarCmd `cmd:"" help:"Calendar operations"`
	Slack    SlackCmd    `cmd:"" help:"Slack operations"`
	JIRA     JIRACmd     `cmd:"" help:"JIRA operations"`
	Tasks    TasksCmd    `cmd:"" help:"Task operations"`
	People   PeopleCmd   `cmd:"" help:"People lookup"`
	Drive    DriveCmd    `cmd:"" help:"Drive operations"`
	Omi      OmiCmd      `cmd:"" help:"Omi operations"`

	Ask    AskCmd    `cmd:"" help:"Send a message to the AI bridge"`
	Embed  EmbedCmd  `cmd:"" help:"Generate an embedding vector"`
	Signal SignalCmd `cmd:"" help:"Send a message through the agent"`
	Run    RunCmd    `cmd:"" help:"Run a skill by name"`
	Status StatusCmd `cmd:"" help:"Guide health check"`
	Events EventsCmd `cmd:"" help:"Event log queries"`

	Config string `help:"Config file path" default:"~/.virgil/virgil.yaml" type:"path"`
}

func main() {
	cli := &CLI{}
	ctx := kong.Parse(cli,
		kong.Name("virgil"),
		kong.Description("Personal AI guide"),
		kong.UsageOnError(),
	)
	err := ctx.Run(&Context{ConfigPath: cli.Config})
	ctx.FatalIfErrorf(err)
}

type Context struct {
	ConfigPath string
}

// ---------- init ----------

type InitCmd struct{}

func (c *InitCmd) Run(ctx *Context) error {
	cfg, err := config.Load(ctx.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = config.Default()
			if err := cfg.ExpandDataDir(); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("load config: %w", err)
		}
	}
	dataDir := cfg.Guide.DataDir

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	cfgPath := cfg.ConfigPath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := config.WriteDefault(cfgPath); err != nil {
			return fmt.Errorf("write default config: %w", err)
		}
		slog.Info("wrote default config", "path", cfgPath)
	}

	slog.Info("initialized", "data_dir", dataDir, "db", cfg.DBPath())
	return nil
}

// ---------- status ----------

type StatusCmd struct{}

type StatusOutput struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	DataDir string `json:"data_dir"`
	DBPath  string `json:"db_path"`
	DBSize  int64  `json:"db_size_bytes"`
	Tables  int    `json:"tables"`
	Events  int    `json:"events"`
}

func (c *StatusCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return outputJSON(&StatusOutput{OK: false, Error: err.Error()})
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return outputJSON(&StatusOutput{OK: false, Error: err.Error(), DataDir: cfg.Guide.DataDir, DBPath: cfg.DBPath()})
	}
	defer database.Close()

	out := &StatusOutput{
		OK:      true,
		DataDir: cfg.Guide.DataDir,
		DBPath:  cfg.DBPath(),
	}

	if info, err := os.Stat(cfg.DBPath()); err == nil {
		out.DBSize = info.Size()
	}

	var errs []string
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&out.Tables); err != nil {
		errs = append(errs, fmt.Sprintf("count tables: %v", err))
	}
	if err := database.QueryRow("SELECT count(*) FROM events").Scan(&out.Events); err != nil {
		errs = append(errs, fmt.Sprintf("count events: %v", err))
	}
	if len(errs) > 0 {
		out.OK = false
		out.Error = strings.Join(errs, "; ")
	}

	return outputJSON(out)
}

// ---------- events ----------

type EventsCmd struct {
	Trace     string `help:"Filter by trace ID"`
	Component string `help:"Filter by component"`
	Limit     int    `help:"Max events to return" default:"50"`
}

func (c *EventsCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer database.Close()

	el := observe.NewEventLog(database)
	events, err := el.Query(c.Trace, c.Component, c.Limit)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	return outputJSON(events)
}

// ---------- stubs (later stages) ----------

type ServeCmd struct{}
type MCPCmd struct{}
type AuthCmd struct{}
type SeedCmd struct{}

func (c *ServeCmd) Run(ctx *Context) error { return fmt.Errorf("not yet implemented") }
func (c *MCPCmd) Run(ctx *Context) error   { return fmt.Errorf("not yet implemented") }
func (c *AuthCmd) Run(ctx *Context) error  { return fmt.Errorf("not yet implemented") }
func (c *SeedCmd) Run(ctx *Context) error  { return fmt.Errorf("not yet implemented") }

// ---------- memory CLI ----------

type MemoryCmd struct {
	Store  MemoryStoreCmd  `cmd:"" help:"Store a memory"`
	Search MemorySearchCmd `cmd:"" help:"Search memories"`
	Facts  MemoryFactsCmd  `cmd:"" help:"Get facts about a topic"`
}

type MemoryStoreCmd struct {
	Type     string   `arg:"" help:"Memory type: observation, interaction, or fact"`
	Content  string   `arg:"" help:"What to remember"`
	Topic    string   `help:"Primary topic or entity"`
	Scope    string   `help:"Scope (personal or bridge:{org_id})" default:"personal"`
	Entities []string `help:"Entities as name:type:role (e.g. 'Alice:person:subject')"`
}

type MemorySearchCmd struct {
	Query string `arg:"" help:"Search query"`
	Type  string `help:"Filter by memory type"`
	Scope string `help:"Filter by scope"`
	Limit int    `help:"Max results" default:"10"`
}

type MemoryFactsCmd struct {
	About string `arg:"" help:"Person, topic, or project name"`
	Scope string `help:"Filter by scope"`
}

func (c *MemoryStoreCmd) Run(ctx *Context) error {
	store, cleanup, err := openMemoryStore(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	p := memory.StoreParams{
		Type:    c.Type,
		Content: c.Content,
		Topic:   c.Topic,
		Scope:   c.Scope,
	}
	for _, raw := range c.Entities {
		e := parseEntityFlag(raw)
		if e.Name != "" {
			p.Entities = append(p.Entities, e)
		}
	}

	id, err := store.Store(p)
	if err != nil {
		return fmt.Errorf("store memory: %w", err)
	}

	entry, err := store.Get(id)
	if err != nil {
		return fmt.Errorf("get memory: %w", err)
	}
	if entry == nil {
		return fmt.Errorf("stored memory %s not found after insert", id)
	}

	return outputJSON(entry)
}

func (c *MemorySearchCmd) Run(ctx *Context) error {
	store, cleanup, err := openMemoryStore(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	results, err := store.Search(memory.SearchParams{
		Query: c.Query,
		Type:  c.Type,
		Scope: c.Scope,
		Limit: c.Limit,
	})
	if err != nil {
		return fmt.Errorf("search memory: %w", err)
	}

	return outputJSON(results)
}

func (c *MemoryFactsCmd) Run(ctx *Context) error {
	store, cleanup, err := openMemoryStore(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	facts, err := store.Facts(c.About, c.Scope)
	if err != nil {
		return fmt.Errorf("get facts: %w", err)
	}

	return outputJSON(facts)
}

// ---------- tool CLI commands ----------

type EmailCmd struct {
	List       EmailListCmd       `cmd:"" help:"List emails"`
	Read       EmailReadCmd       `cmd:"" help:"Read an email thread"`
	Send       EmailSendCmd       `cmd:"" help:"Send an email"`
	Categorize EmailCategorizeCmd `cmd:"" help:"Categorize an email"`
}
type EmailListCmd struct {
	Account string `help:"Email account"`
	Unread  bool   `help:"Only unread"`
	From    string `help:"Filter by sender"`
	Since   string `help:"Since date"`
	Query   string `help:"Gmail search query"`
	Limit   int    `help:"Max results" default:"20"`
}
type EmailReadCmd struct {
	ThreadID string `arg:"" help:"Thread ID"`
	Account  string `arg:"" help:"Account name"`
}
type EmailSendCmd struct {
	Account string `arg:"" help:"Account to send from"`
	To      string `arg:"" help:"Recipient"`
	Body    string `arg:"" help:"Message body"`
	Subject string `help:"Subject line"`
	ReplyTo string `help:"Thread ID to reply to"`
	Cc      string `help:"CC recipients"`
}
type EmailCategorizeCmd struct {
	MessageID string `arg:"" help:"Message ID"`
	Account   string `arg:"" help:"Account"`
	Category  string `arg:"" help:"Category: imbox, feed, paper_trail, noise"`
}

func (c *EmailListCmd) Run(ctx *Context) error {
	p := map[string]any{"limit": float64(c.Limit)}
	if c.Account != "" {
		p["account"] = c.Account
	}
	if c.Unread {
		p["unread_only"] = true
	}
	if c.From != "" {
		p["from"] = c.From
	}
	if c.Since != "" {
		p["since"] = c.Since
	}
	if c.Query != "" {
		p["query"] = c.Query
	}
	return runTool(ctx, "email_list", p)
}
func (c *EmailReadCmd) Run(ctx *Context) error {
	return runTool(ctx, "email_read", map[string]any{"thread_id": c.ThreadID, "account": c.Account})
}
func (c *EmailSendCmd) Run(ctx *Context) error {
	p := map[string]any{"account": c.Account, "to": c.To, "body": c.Body}
	if c.Subject != "" {
		p["subject"] = c.Subject
	}
	if c.ReplyTo != "" {
		p["reply_to_thread"] = c.ReplyTo
	}
	if c.Cc != "" {
		p["cc"] = c.Cc
	}
	return runTool(ctx, "email_send", p)
}
func (c *EmailCategorizeCmd) Run(ctx *Context) error {
	return runTool(ctx, "email_categorize", map[string]any{"message_id": c.MessageID, "account": c.Account, "category": c.Category})
}

type CalendarCmd struct {
	Check  CalendarCheckCmd  `cmd:"" help:"Check calendar events"`
	Create CalendarCreateCmd `cmd:"" help:"Create an event"`
	Delete CalendarDeleteCmd `cmd:"" help:"Delete an event"`
}
type CalendarCheckCmd struct {
	Account string `help:"Calendar account"`
	Start   string `help:"Start date"`
	End     string `help:"End date"`
	Days    int    `help:"Number of days" default:"1"`
}
type CalendarCreateCmd struct {
	Account string `arg:"" help:"Account"`
	Title   string `arg:"" help:"Event title"`
	Start   string `arg:"" help:"Start time"`
	End     string `arg:"" help:"End time"`
}
type CalendarDeleteCmd struct {
	EventID string `arg:"" help:"Event ID"`
	Account string `arg:"" help:"Account"`
}

func (c *CalendarCheckCmd) Run(ctx *Context) error {
	p := map[string]any{"days": float64(c.Days)}
	if c.Account != "" {
		p["account"] = c.Account
	}
	if c.Start != "" {
		p["start"] = c.Start
	}
	if c.End != "" {
		p["end"] = c.End
	}
	return runTool(ctx, "calendar_check", p)
}
func (c *CalendarCreateCmd) Run(ctx *Context) error {
	return runTool(ctx, "calendar_create", map[string]any{"account": c.Account, "title": c.Title, "start": c.Start, "end": c.End})
}
func (c *CalendarDeleteCmd) Run(ctx *Context) error {
	return runTool(ctx, "calendar_delete", map[string]any{"event_id": c.EventID, "account": c.Account})
}

type SlackCmd struct {
	Read   SlackReadCmd   `cmd:"" help:"Read channel messages"`
	Post   SlackPostCmd   `cmd:"" help:"Post a message"`
	Search SlackSearchCmd `cmd:"" help:"Search messages"`
}
type SlackReadCmd struct {
	Workspace string `arg:"" help:"Workspace name"`
	Channel   string `arg:"" help:"Channel name or ID"`
	ThreadTS  string `help:"Thread timestamp"`
	Limit     int    `help:"Max messages" default:"20"`
}
type SlackPostCmd struct {
	Workspace string `arg:"" help:"Workspace"`
	Channel   string `arg:"" help:"Channel"`
	Text      string `arg:"" help:"Message text"`
	ThreadTS  string `help:"Reply to thread"`
}
type SlackSearchCmd struct {
	Query     string `arg:"" help:"Search query"`
	Workspace string `help:"Limit to workspace"`
	Limit     int    `help:"Max results" default:"20"`
}

func (c *SlackReadCmd) Run(ctx *Context) error {
	p := map[string]any{"workspace": c.Workspace, "channel": c.Channel, "limit": float64(c.Limit)}
	if c.ThreadTS != "" {
		p["thread_ts"] = c.ThreadTS
	}
	return runTool(ctx, "slack_read", p)
}
func (c *SlackPostCmd) Run(ctx *Context) error {
	p := map[string]any{"workspace": c.Workspace, "channel": c.Channel, "text": c.Text}
	if c.ThreadTS != "" {
		p["thread_ts"] = c.ThreadTS
	}
	return runTool(ctx, "slack_post", p)
}
func (c *SlackSearchCmd) Run(ctx *Context) error {
	p := map[string]any{"query": c.Query, "limit": float64(c.Limit)}
	if c.Workspace != "" {
		p["workspace"] = c.Workspace
	}
	return runTool(ctx, "slack_search", p)
}

type JIRACmd struct {
	Search JIRASearchCmd `cmd:"" help:"Search issues"`
	Read   JIRAReadCmd   `cmd:"" help:"Read an issue"`
	Update JIRAUpdateCmd `cmd:"" help:"Update an issue"`
}
type JIRASearchCmd struct {
	Query    string `arg:"" help:"JQL or text query"`
	Instance string `help:"JIRA instance"`
	Assignee string `help:"Filter by assignee"`
	Status   string `help:"Filter by status"`
	Limit    int    `help:"Max results" default:"20"`
}
type JIRAReadCmd struct {
	IssueKey string `arg:"" help:"Issue key (e.g. PASS-123)"`
}
type JIRAUpdateCmd struct {
	IssueKey   string `arg:"" help:"Issue key"`
	Comment    string `help:"Add a comment"`
	Transition string `help:"Status transition name"`
}

func (c *JIRASearchCmd) Run(ctx *Context) error {
	p := map[string]any{"query": c.Query, "limit": float64(c.Limit)}
	if c.Instance != "" {
		p["instance"] = c.Instance
	}
	if c.Assignee != "" {
		p["assignee"] = c.Assignee
	}
	if c.Status != "" {
		p["status"] = c.Status
	}
	return runTool(ctx, "jira_search", p)
}
func (c *JIRAReadCmd) Run(ctx *Context) error {
	return runTool(ctx, "jira_read", map[string]any{"issue_key": c.IssueKey})
}
func (c *JIRAUpdateCmd) Run(ctx *Context) error {
	p := map[string]any{"issue_key": c.IssueKey}
	if c.Comment != "" {
		p["comment"] = c.Comment
	}
	if c.Transition != "" {
		p["transition"] = c.Transition
	}
	return runTool(ctx, "jira_update", p)
}

type TasksCmd struct {
	List     TasksListCmd     `cmd:"" help:"List tasks"`
	Create   TasksCreateCmd   `cmd:"" help:"Create a task"`
	Complete TasksCompleteCmd `cmd:"" help:"Complete a task"`
}
type TasksListCmd struct {
	Status   string `help:"Filter by status (open, done, dropped)"`
	Priority string `help:"Filter by priority"`
	Source   string `help:"Filter by source"`
	Limit    int    `help:"Max results" default:"20"`
}
type TasksCreateCmd struct {
	Title       string `arg:"" help:"Task title"`
	Description string `help:"Task description"`
	Priority    string `help:"Priority (urgent, high, normal, low)" default:"normal"`
	Source      string `help:"Source (email, meeting, jira, user)"`
	DueAt       string `help:"Due date"`
}
type TasksCompleteCmd struct {
	TaskID string `arg:"" help:"Task ID"`
}

func (c *TasksListCmd) Run(ctx *Context) error {
	p := map[string]any{"limit": float64(c.Limit)}
	if c.Status != "" {
		p["status"] = c.Status
	}
	if c.Priority != "" {
		p["priority"] = c.Priority
	}
	if c.Source != "" {
		p["source"] = c.Source
	}
	return runTool(ctx, "task_list", p)
}
func (c *TasksCreateCmd) Run(ctx *Context) error {
	p := map[string]any{"title": c.Title, "priority": c.Priority}
	if c.Description != "" {
		p["description"] = c.Description
	}
	if c.Source != "" {
		p["source"] = c.Source
	}
	if c.DueAt != "" {
		p["due_at"] = c.DueAt
	}
	return runTool(ctx, "task_create", p)
}
func (c *TasksCompleteCmd) Run(ctx *Context) error {
	return runTool(ctx, "task_complete", map[string]any{"task_id": c.TaskID})
}

type PeopleCmd struct {
	Lookup PeopleLookupCmd `cmd:"" help:"Look up a person"`
}
type PeopleLookupCmd struct {
	Name string `arg:"" help:"Person's name or email"`
}

func (c *PeopleLookupCmd) Run(ctx *Context) error {
	return runTool(ctx, "people_lookup", map[string]any{"name": c.Name})
}

type DriveCmd struct {
	List DriveListCmd `cmd:"" help:"List files"`
	Read DriveReadCmd `cmd:"" help:"Read a file"`
}
type DriveListCmd struct {
	Query string `help:"Search query"`
	Type  string `help:"File type (document, spreadsheet, presentation, pdf)"`
	Limit int    `help:"Max results" default:"20"`
}
type DriveReadCmd struct {
	FileID string `arg:"" help:"File ID"`
}

func (c *DriveListCmd) Run(ctx *Context) error {
	p := map[string]any{"limit": float64(c.Limit)}
	if c.Query != "" {
		p["query"] = c.Query
	}
	if c.Type != "" {
		p["type"] = c.Type
	}
	return runTool(ctx, "drive_list", p)
}
func (c *DriveReadCmd) Run(ctx *Context) error {
	return runTool(ctx, "drive_read", map[string]any{"file_id": c.FileID})
}

type OmiCmd struct {
	Ingest OmiIngestCmd `cmd:"" help:"Ingest conversations from Omi"`
}
type OmiIngestCmd struct {
	Since string `help:"Only conversations after this time"`
	Limit int    `help:"Max conversations" default:"50"`
}

func (c *OmiIngestCmd) Run(ctx *Context) error {
	p := map[string]any{"limit": float64(c.Limit)}
	if c.Since != "" {
		p["since"] = c.Since
	}
	return runTool(ctx, "omi_ingest", p)
}

// ---------- AI bridge ----------

type AskCmd struct {
	Message  string   `arg:"" help:"Message to send"`
	Model    string   `help:"Model ref (e.g. anthropic/sonnet)" default:""`
	Fallback []string `help:"Fallback model refs"`
}

type EmbedCmd struct {
	Text string `arg:"" help:"Text to embed"`
}

func (c *AskCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	fb, cleanup, err := openBridge(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	modelRef := c.Model
	if modelRef == "" {
		modelRef = cfg.AI.Interactive.Model
	}
	primary := bridge.NewModelConfig(cfg, modelRef)

	fallbacks := c.Fallback
	if len(fallbacks) == 0 && c.Model == "" {
		fallbacks = cfg.AI.Interactive.Fallback
	}
	var fbConfigs []bridge.ModelConfig
	for _, ref := range fallbacks {
		fbConfigs = append(fbConfigs, bridge.NewModelConfig(cfg, ref))
	}

	messages := []bridge.Message{
		{Role: "user", Content: c.Message},
	}

	resp, err := fb.Complete(context.Background(), primary, messages, nil, fbConfigs)
	if err != nil {
		return fmt.Errorf("ask: %w", err)
	}

	return outputJSON(resp)
}

func (c *EmbedCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	fb, cleanup, err := openBridge(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	vec, err := fb.Embed(context.Background(), c.Text)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	return outputJSON(vec)
}

// ---------- agent ----------

type SignalCmd struct {
	Message   string `arg:"" help:"Message to send through the agent"`
	Channel   string `help:"Channel" default:"cli"`
	SkillsDir string `help:"Skills directory override" name:"skills-dir"`
}

type RunCmd struct {
	Name      string `arg:"" help:"Skill name to run"`
	SkillsDir string `help:"Skills directory override" name:"skills-dir"`
}

func (c *SignalCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	if c.SkillsDir != "" {
		cfg.Skills.Dir = c.SkillsDir
	}

	ag, cleanup, err := openAgent(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	sig := internal.Signal{
		ID:        observe.GenerateSpanID(),
		Channel:   c.Channel,
		Content:   c.Message,
		Timestamp: time.Now(),
	}

	text, err := ag.Run(context.Background(), sig)
	if err != nil {
		return fmt.Errorf("signal: %w", err)
	}

	return outputJSON(map[string]string{"response": text})
}

func (c *RunCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	if c.SkillsDir != "" {
		cfg.Skills.Dir = c.SkillsDir
	}

	ag, cleanup, err := openAgent(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	sk := ag.FindSkill(c.Name)
	if sk == nil {
		return fmt.Errorf("skill %q not found", c.Name)
	}

	text, err := ag.RunSkill(context.Background(), sk, "cli")
	if err != nil {
		return fmt.Errorf("run skill: %w", err)
	}

	return outputJSON(map[string]string{"response": text})
}

// ---------- helpers ----------

func loadConfig(ctx *Context) (*config.Config, error) {
	path := ctx.ConfigPath
	if path == "" {
		path = "~/.virgil/virgil.yaml"
	}
	return config.Load(path)
}

func openMemoryStore(ctx *Context) (*memory.Store, func(), error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	return memory.NewStore(database), func() { database.Close() }, nil
}

func openBridge(cfg *config.Config) (*bridge.FallbackBridge, func(), error) {
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	events := observe.NewEventLog(database)

	fb, err := buildBridge(cfg, events)
	if err != nil {
		database.Close()
		return nil, nil, err
	}
	return fb, func() { database.Close() }, nil
}

func registerAllTools(reg *tools.Registry, cfg *config.Config, database *sql.DB, memStore *memory.Store) {
	tools.RegisterMemoryTools(reg, memStore)
	tools.RegisterTaskTools(reg, database)
	tools.RegisterPeopleTools(reg, memStore)
	tools.RegisterEmailTools(reg, cfg)
	tools.RegisterCalendarTools(reg, cfg)
	tools.RegisterDriveTools(reg, cfg)
	tools.RegisterSlackTools(reg, cfg)
	tools.RegisterJIRATools(reg, cfg)
	tools.RegisterOmiTools(reg, cfg, memStore)
}

func openToolRegistry(ctx *Context) (*tools.Registry, func(), error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	memStore := memory.NewStore(database)
	reg := tools.NewRegistry()
	registerAllTools(reg, cfg, database, memStore)
	return reg, func() { database.Close() }, nil
}

func runTool(ctx *Context, toolName string, params map[string]any) error {
	reg, cleanup, err := openToolRegistry(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	t := reg.Get(toolName)
	if t == nil {
		return fmt.Errorf("tool %q not registered", toolName)
	}

	result, err := t.Execute(context.Background(), params)
	if err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("%s", result.Error)
	}
	if result.Data != nil {
		return outputJSON(result.Data)
	}
	return outputJSON(result)
}

func openAgent(cfg *config.Config) (*agent.Agent, func(), error) {
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}

	events := observe.NewEventLog(database)
	memStore := memory.NewStore(database)

	fb, err := buildBridge(cfg, events)
	if err != nil {
		database.Close()
		return nil, nil, err
	}

	loaded, err := skills.LoadAll(cfg.Skills.Dir)
	if err != nil {
		slog.Error("skills failed to load, agent will run without skills", "dir", cfg.Skills.Dir, "err", err)
	}

	reg := tools.NewRegistry()
	registerAllTools(reg, cfg, database, memStore)

	ag, err := agent.NewAgent(cfg, memStore, fb, reg, loaded, events)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create agent: %w", err)
	}
	return ag, func() { database.Close() }, nil
}

func buildBridge(cfg *config.Config, events *observe.EventLog) (*bridge.FallbackBridge, error) {
	providers := make(map[string]bridge.Bridge)
	var embedder bridge.Bridge

	for name, pc := range cfg.AI.Providers {
		switch name {
		case "anthropic":
			p, err := bridge.NewAnthropicProvider(pc.APIKeyEnv)
			if err != nil {
				slog.Warn("skip provider", "name", name, "err", err)
				continue
			}
			providers[name] = p
		default:
			p, err := bridge.NewOpenAIProvider(pc.APIKeyEnv, pc.BaseURL, pc.EmbeddingModel)
			if err != nil {
				slog.Warn("skip provider", "name", name, "err", err)
				continue
			}
			providers[name] = p
			if pc.EmbeddingModel != "" && embedder == nil {
				embedder = p
			}
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no AI providers initialized (check API key environment variables)")
	}

	if cfg.AI.Interactive.Model != "" {
		primaryProvider, _ := bridge.ParseModelRef(cfg.AI.Interactive.Model)
		if _, ok := providers[primaryProvider]; !ok {
			slog.Error("primary provider failed to initialize, will use fallback",
				"primary", primaryProvider)
		}
	}

	if embedder == nil {
		if p, ok := providers["openai"]; ok {
			embedder = p
		}
	}

	return bridge.NewFallbackBridge(providers, embedder, events), nil
}

func outputJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func parseEntityFlag(raw string) memory.Entity {
	parts := strings.SplitN(raw, ":", 3)
	e := memory.Entity{Name: parts[0]}
	if len(parts) > 1 {
		e.Type = parts[1]
	}
	if len(parts) > 2 {
		e.Role = parts[2]
	}
	return e
}
