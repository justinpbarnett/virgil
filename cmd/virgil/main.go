package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/db"
	"github.com/justinpbarnett/virgil/internal/memory"
	"github.com/justinpbarnett/virgil/internal/observe"
)

type CLI struct {
	// Serve mode (production)
	Serve ServeCmd `cmd:"" help:"Start guide server (HTTP + Telegram + cron)"`

	// MCP mode (Claude Code)
	MCP MCPCmd `cmd:"" help:"Start MCP server on stdio"`

	// Auth setup
	Auth AuthCmd `cmd:"" help:"Set up OAuth tokens"`

	// Setup
	Init InitCmd `cmd:"" help:"Initialize data directory and config"`
	Seed SeedCmd `cmd:"" help:"Ingest a markdown file as facts into memory"`

	// Direct tool access
	Memory   MemoryCmd   `cmd:"" help:"Memory operations"`
	Email    EmailCmd    `cmd:"" help:"Email operations"`
	Calendar CalendarCmd `cmd:"" help:"Calendar operations"`
	Slack    SlackCmd    `cmd:"" help:"Slack operations"`
	JIRA     JIRACmd     `cmd:"" help:"JIRA operations"`
	Tasks    TasksCmd    `cmd:"" help:"Task operations"`
	People   PeopleCmd   `cmd:"" help:"People lookup"`

	// AI bridge
	Ask   AskCmd   `cmd:"" help:"Send a message to the AI bridge"`
	Embed EmbedCmd `cmd:"" help:"Generate an embedding vector"`

	// Skills
	Run RunCmd `cmd:"" help:"Run a skill by name"`

	// System
	Status StatusCmd `cmd:"" help:"Guide health check"`
	Events EventsCmd `cmd:"" help:"Event log queries"`

	// Global flags
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

// Context is passed to all commands.
type Context struct {
	ConfigPath string
}

// ---------- init ----------

type InitCmd struct{}

func (c *InitCmd) Run(ctx *Context) error {
	// Load config if it exists, otherwise use defaults for first-time init.
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
type AskCmd struct {
	Message  string   `arg:"" help:"Message to send"`
	Model    string   `help:"Model ref (e.g. anthropic/sonnet)" default:""`
	Fallback []string `help:"Fallback model refs"`
}

type EmbedCmd struct {
	Text string `arg:"" help:"Text to embed"`
}

type EmailCmd struct{}
type CalendarCmd struct{}
type SlackCmd struct{}
type JIRACmd struct{}
type TasksCmd struct{}
type PeopleCmd struct{}
type RunCmd struct{}

func (c *ServeCmd) Run(ctx *Context) error { return fmt.Errorf("not yet implemented") }
func (c *MCPCmd) Run(ctx *Context) error   { return fmt.Errorf("not yet implemented") }
func (c *AuthCmd) Run(ctx *Context) error  { return fmt.Errorf("not yet implemented") }
func (c *SeedCmd) Run(ctx *Context) error  { return fmt.Errorf("not yet implemented") }
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
func (c *EmailCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *CalendarCmd) Run(ctx *Context) error { return fmt.Errorf("not yet implemented") }
func (c *SlackCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *JIRACmd) Run(ctx *Context) error     { return fmt.Errorf("not yet implemented") }
func (c *TasksCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *PeopleCmd) Run(ctx *Context) error   { return fmt.Errorf("not yet implemented") }
func (c *RunCmd) Run(ctx *Context) error      { return fmt.Errorf("not yet implemented") }

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
		database.Close()
		return nil, nil, fmt.Errorf("no AI providers initialized (check API key environment variables)")
	}

	if embedder == nil {
		if p, ok := providers["openai"]; ok {
			embedder = p
		}
	}

	fb := bridge.NewFallbackBridge(providers, embedder, events)
	return fb, func() { database.Close() }, nil
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
