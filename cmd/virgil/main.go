package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/alecthomas/kong"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/db"
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
	// Load config if it exists, otherwise use defaults.
	cfg, err := config.Load(ctx.ConfigPath)
	if err != nil {
		cfg = config.Default()
	}
	cfg.ExpandDataDir()
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

type StatusCmd struct {
	JSON bool `help:"Output as JSON" default:"true"`
}

type StatusOutput struct {
	OK      bool   `json:"ok"`
	DataDir string `json:"data_dir"`
	DBPath  string `json:"db_path"`
	DBSize  int64  `json:"db_size_bytes"`
	Tables  int    `json:"tables"`
	Events  int    `json:"events"`
}

func (c *StatusCmd) Run(ctx *Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return outputStatus(&StatusOutput{OK: false})
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return outputStatus(&StatusOutput{OK: false, DataDir: cfg.Guide.DataDir, DBPath: cfg.DBPath()})
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

	row := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'")
	row.Scan(&out.Tables)

	row = database.QueryRow("SELECT count(*) FROM events")
	row.Scan(&out.Events)

	return outputStatus(out)
}

func outputStatus(s *StatusOutput) error {
	data, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(data))
	return nil
}

// ---------- events ----------

type EventsCmd struct {
	Trace     string `help:"Filter by trace ID"`
	Component string `help:"Filter by component"`
	Limit     int    `help:"Max events to return" default:"50"`
	JSON      bool   `help:"Output as JSON" default:"true"`
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

	out, err := observe.EventsToJSON(events)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// ---------- stubs (later stages) ----------

type ServeCmd struct{}
type MCPCmd struct{}
type AuthCmd struct{}
type SeedCmd struct{}
type MemoryCmd struct{}
type EmailCmd struct{}
type CalendarCmd struct{}
type SlackCmd struct{}
type JIRACmd struct{}
type TasksCmd struct{}
type PeopleCmd struct{}
type RunCmd struct{}

func (c *ServeCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *MCPCmd) Run(ctx *Context) error      { return fmt.Errorf("not yet implemented") }
func (c *AuthCmd) Run(ctx *Context) error     { return fmt.Errorf("not yet implemented") }
func (c *SeedCmd) Run(ctx *Context) error     { return fmt.Errorf("not yet implemented") }
func (c *MemoryCmd) Run(ctx *Context) error   { return fmt.Errorf("not yet implemented") }
func (c *EmailCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *CalendarCmd) Run(ctx *Context) error { return fmt.Errorf("not yet implemented") }
func (c *SlackCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *JIRACmd) Run(ctx *Context) error     { return fmt.Errorf("not yet implemented") }
func (c *TasksCmd) Run(ctx *Context) error    { return fmt.Errorf("not yet implemented") }
func (c *PeopleCmd) Run(ctx *Context) error   { return fmt.Errorf("not yet implemented") }
func (c *RunCmd) Run(ctx *Context) error      { return fmt.Errorf("not yet implemented") }

// ---------- helpers ----------

func loadConfig(ctx *Context) (*config.Config, error) {
	path := ctx.ConfigPath
	if path == "" {
		path = "~/.virgil/virgil.yaml"
	}
	return config.Load(path)
}
