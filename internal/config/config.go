package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/pipe"
	"gopkg.in/yaml.v3"
)

type VoiceOutputMode string

const (
	VoiceModeSilent VoiceOutputMode = "silent"
	VoiceModeNotify VoiceOutputMode = "notify"
	VoiceModeSteps  VoiceOutputMode = "steps"
	VoiceModeFull   VoiceOutputMode = "full"
)

type VoiceConfig struct {
	OpenAIKey       string          `yaml:"openai_api_key"`
	ElevenLabsKey   string          `yaml:"elevenlabs_api_key"`
	ElevenLabsVoice string          `yaml:"elevenlabs_voice_id"`
	ElevenLabsModel string          `yaml:"elevenlabs_model_id"`
	PushToTalkKey   string          `yaml:"push_to_talk_key"`
	ModeCycleKey    string          `yaml:"mode_cycle_key"`
	OutputMode      VoiceOutputMode `yaml:"output_mode"`
	MaxSpokenChars  int             `yaml:"max_spoken_chars"`
	VoiceModel      string          `yaml:"voice_model"`
}

func (c *VoiceConfig) Validate() error {
	if c.OpenAIKey == "" {
		return fmt.Errorf("openai_api_key is required in voice.yaml")
	}
	if c.ElevenLabsKey == "" {
		return fmt.Errorf("elevenlabs_api_key is required in voice.yaml")
	}
	if c.ElevenLabsVoice == "" {
		return fmt.Errorf("elevenlabs_voice_id is required in voice.yaml")
	}
	return nil
}

// LoadVoiceConfig reads voice.yaml from configDir. Returns nil, nil if the file doesn't exist.
func LoadVoiceConfig(configDir string) (*VoiceConfig, error) {
	path := filepath.Join(configDir, "voice.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading voice.yaml: %w", err)
	}

	var cfg VoiceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing voice.yaml: %w", err)
	}

	defaults := map[string]struct {
		field *string
		value string
	}{
		"elevenlabs_model": {&cfg.ElevenLabsModel, "eleven_turbo_v2_5"},
		"push_to_talk_key": {&cfg.PushToTalkKey, "right_option"},
		"mode_cycle_key":   {&cfg.ModeCycleKey, "f8"},
		"output_mode":      {(*string)(&cfg.OutputMode), string(VoiceModeNotify)},
		"voice_model":      {&cfg.VoiceModel, "gemini-3.1-flash-lite-preview"},
	}
	for _, d := range defaults {
		if *d.field == "" {
			*d.field = d.value
		}
	}
	if cfg.MaxSpokenChars == 0 {
		cfg.MaxSpokenChars = 200
	}

	switch cfg.OutputMode {
	case VoiceModeSilent, VoiceModeNotify, VoiceModeSteps, VoiceModeFull:
	default:
		return nil, fmt.Errorf("invalid output_mode %q: must be silent, notify, steps, or full", cfg.OutputMode)
	}

	return &cfg, nil
}

// LogLevel represents a logging verbosity level.
type LogLevel int

const (
	Unset LogLevel = iota // zero value; means "inherit from parent"
	Silent
	Error
	Warn
	Info
	Debug
	Verbose
)

// ParseLogLevel maps a string name to a LogLevel constant.
// Unknown strings default to Info.
func ParseLogLevel(s string) LogLevel {
	switch strings.ToLower(s) {
	case "silent":
		return Silent
	case "error":
		return Error
	case "warn":
		return Warn
	case "info":
		return Info
	case "debug":
		return Debug
	case "verbose":
		return Verbose
	default:
		return Info
	}
}

// UnmarshalYAML decodes a YAML string directly into a typed LogLevel.
// An empty or absent field leaves the value unchanged (stays at Unset).
func (l *LogLevel) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		return nil // leave at zero value (Unset)
	}
	*l = ParseLogLevel(s)
	return nil
}

// ToSlogLevel maps a Virgil LogLevel to an slog.Level.
func ToSlogLevel(level LogLevel) slog.Level {
	switch level {
	case Silent:
		return slog.LevelError + 1 // above error so nothing prints
	case Error:
		return slog.LevelError
	case Warn:
		return slog.LevelWarn
	case Info:
		return slog.LevelInfo
	case Debug, Verbose:
		return slog.LevelDebug
	default:
		return slog.LevelInfo // covers Unset
	}
}

// String returns the string representation of a LogLevel.
func (l LogLevel) String() string {
	switch l {
	case Unset:
		return ""
	case Silent:
		return "silent"
	case Error:
		return "error"
	case Warn:
		return "warn"
	case Info:
		return "info"
	case Debug:
		return "debug"
	case Verbose:
		return "verbose"
	default:
		return "info"
	}
}

type ModelEndpoint struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
}

// SetDefaults fills in zero-value fields with the provided defaults.
func (m *ModelEndpoint) SetDefaults(provider, model string, maxTokens int) {
	if m.Provider == "" {
		m.Provider = provider
	}
	if m.Model == "" {
		m.Model = model
	}
	if m.MaxTokens == 0 {
		m.MaxTokens = maxTokens
	}
}

type Config struct {
	Server       ServerConfig              `yaml:"server"`
	Provider     ProviderConfig            `yaml:"provider"`
	Ack          ModelEndpoint             `yaml:"ack"`
	Planner      ModelEndpoint             `yaml:"planner"`
	Identity     string                    `yaml:"identity"`
	LogLevel     LogLevel                  `yaml:"log_level"`
	DatabasePath string                    `yaml:"database_path"`
	ConfigDir    string                    `yaml:"-"`
	Pipes        map[string]PipeConfig     `yaml:"-"`
	Pipelines    map[string]PipelineConfig `yaml:"-"`
	Vocabulary   VocabularyConfig          `yaml:"-"`
	Templates    TemplatesConfig           `yaml:"-"`
}

// RawFormats extracts the per-pipe format templates from all configured pipes.
func (c Config) RawFormats() map[string]map[string]string {
	raw := make(map[string]map[string]string)
	for name, pc := range c.Pipes {
		if len(pc.Format) > 0 {
			raw[name] = pc.Format
		}
	}
	return raw
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type ProviderConfig struct {
	Name      string `yaml:"name"`
	Model     string `yaml:"model"`
	Binary    string `yaml:"binary"`
	MaxTokens int    `yaml:"max_tokens"`
}

type MemoryContextEntry struct {
	Type      string   `yaml:"type"`
	Depth     string   `yaml:"depth"`
	Relations []string `yaml:"relations"`
	Kind      string   `yaml:"kind"`
}

type MemoryConfig struct {
	Context  []MemoryContextEntry `yaml:"context"`
	Budget   int                  `yaml:"budget"`
	Disabled bool                 `yaml:"disabled"`
}

// DefaultMemoryConfig returns the default memory config for pipes that don't declare one.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		Context: []MemoryContextEntry{{Type: "working_state"}},
		Budget:  500,
	}
}

type PipeConfig struct {
	Name         string               `yaml:"name"`
	Description  string               `yaml:"description"`
	Category     string               `yaml:"category"`
	Streaming    bool                 `yaml:"streaming"`
	Timeout      string               `yaml:"timeout"`
	Provider     string               `yaml:"provider"`
	Model        string               `yaml:"model"`
	MaxTokens    *int                 `yaml:"max_tokens"`
	MaxTurns     *int                 `yaml:"max_turns"`
	PipeLogLevel LogLevel             `yaml:"log_level"`
	Triggers     pipe.Triggers        `yaml:"triggers"`
	Flags        map[string]pipe.Flag `yaml:"flags"`
	Prompts      PromptsConfig        `yaml:"prompts"`
	Format       map[string]string    `yaml:"format"`
	Vocabulary   VocabularyConfig     `yaml:"vocabulary"`
	Templates    TemplateContrib      `yaml:"templates"`
	Memory       MemoryConfig         `yaml:"memory"`
	Dir          string               `yaml:"-"`
}

// EffectiveLogLevel returns the pipe's log level if set, otherwise the global default.
func (pc PipeConfig) EffectiveLogLevel(globalDefault LogLevel) LogLevel {
	if pc.PipeLogLevel != Unset {
		return pc.PipeLogLevel
	}
	return globalDefault
}

// EffectiveModel returns the pipe's model if set, otherwise the global default.
func (pc PipeConfig) EffectiveModel(globalDefault string) string {
	if pc.Model != "" {
		return pc.Model
	}
	return globalDefault
}

// EffectiveProvider returns the pipe's provider if set, otherwise the global default.
func (pc PipeConfig) EffectiveProvider(globalDefault string) string {
	if pc.Provider != "" {
		return pc.Provider
	}
	return globalDefault
}

// EffectiveMaxTokens returns the pipe's max_tokens if set, otherwise the global default.
func (pc PipeConfig) EffectiveMaxTokens(globalDefault int) int {
	if pc.MaxTokens != nil {
		return *pc.MaxTokens
	}
	return globalDefault
}

// EffectiveMaxTurns returns the pipe's max_turns if set, otherwise 1.
func (pc PipeConfig) EffectiveMaxTurns() int {
	if pc.MaxTurns != nil {
		return *pc.MaxTurns
	}
	return 1
}

type PromptsConfig struct {
	System    string            `yaml:"system"`
	Templates map[string]string `yaml:"templates"`
}

type TemplateContrib struct {
	Priority int             `yaml:"priority"`
	Entries  []TemplateEntry `yaml:"entries"`
}

type VocabularyConfig struct {
	Verbs     map[string][]string `yaml:"verbs"`
	Types     map[string][]string `yaml:"types"`
	Sources   map[string][]string `yaml:"sources"`
	Modifiers map[string][]string `yaml:"modifiers"`
}

type TemplatesConfig struct {
	Templates []TemplateEntry `yaml:"templates"`
}

type TemplateEntry struct {
	Pipe     string     `yaml:"-"` // set by config loader; restricts template to a specific routed pipe
	Requires []string   `yaml:"requires"`
	Plan     []PlanStep `yaml:"plan"`
}

type PlanStep struct {
	Pipe  string            `yaml:"pipe"`
	Flags map[string]string `yaml:"flags"`
}

// LoopConfig declares a repeating loop within a pipeline.
// The loop executes its named steps in order, evaluates the Until condition
// after each full iteration, and exits when satisfied or when Max is reached.
type LoopConfig struct {
	Name  string   `yaml:"name"`
	Steps []string `yaml:"steps"` // step names forming the loop body
	Until string   `yaml:"until"` // condition expression evaluated after each iteration
	Max   int      `yaml:"max"`   // iteration cap; 0 means unlimited (not recommended)
}

// ParallelBranch declares one branch of a parallel fan-out within a pipeline step.
type ParallelBranch struct {
	Pipe string            `yaml:"pipe"`
	Args map[string]string `yaml:"args"`
}

// GraphStepConfig holds the configuration for a graph execution step
// where a single pipe processes a dynamically-generated DAG of tasks.
type GraphStepConfig struct {
	Source        string            `yaml:"source"`
	Pipe          string            `yaml:"pipe"`
	Args          map[string]string `yaml:"args"`
	OnTaskFailure string            `yaml:"on_task_failure"`
	MaxParallel   int               `yaml:"max_parallel"`
}

// CycleConfig declares a backward edge in a pipeline DAG, allowing control
// flow to jump from one step to an earlier step under a condition.
type CycleConfig struct {
	Name      string `yaml:"name"`
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Condition string `yaml:"condition"`
	Carry     string `yaml:"carry"`
	Max       int    `yaml:"max"`
}

// PipelineStepConfig is a single step in a pipeline.
type PipelineStepConfig struct {
	Name            string            `yaml:"name"`
	Pipe            string            `yaml:"pipe"`
	Pipeline        string            `yaml:"pipeline"`
	Parallel        []ParallelBranch  `yaml:"parallel"`
	Graph           *GraphStepConfig  `yaml:"graph"`
	Args            map[string]string `yaml:"args"`
	Condition       string            `yaml:"condition"`
	OnBranchFailure string            `yaml:"on_branch_failure"`
}

// PipelineConfig declares a pipeline: an ordered list of steps with optional loop overlays.
type PipelineConfig struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Category    string               `yaml:"category"`
	Triggers    pipe.Triggers        `yaml:"triggers"`
	Steps       []PipelineStepConfig `yaml:"steps"`
	Loops       []LoopConfig         `yaml:"loops"`
	Cycles      []CycleConfig        `yaml:"cycles"`
}

func Load(configDir string, pipeDirs ...string) (*Config, error) {
	cfg := &Config{
		ConfigDir: configDir,
		Server:    ServerConfig{Host: "localhost", Port: 7890},
		Provider:  ProviderConfig{Name: "claude", Model: "sonnet", Binary: "claude", MaxTokens: 8192},
		LogLevel:  Info,
		Pipes:     make(map[string]PipeConfig),
		Pipelines: make(map[string]PipelineConfig),
	}

	// Load virgil.yaml
	if err := loadYAML(filepath.Join(configDir, "virgil.yaml"), cfg); err != nil {
		return nil, fmt.Errorf("loading virgil.yaml: %w", err)
	}

	cfg.Ack.SetDefaults("gemini", "gemini-3-flash-preview", 256)
	cfg.Planner.SetDefaults("xai", "grok-4-fast", 1024)

	// Expand ~ in database path
	if cfg.DatabasePath != "" {
		cfg.DatabasePath = expandHome(cfg.DatabasePath)
	}

	// Load pipe definitions from each pipe directory
	for _, pipesDir := range pipeDirs {
		absPipesDir, err := filepath.Abs(pipesDir)
		if err != nil {
			return nil, fmt.Errorf("resolving pipes directory %s: %w", pipesDir, err)
		}

		entries, err := os.ReadDir(absPipesDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // optional directory (e.g., cloud pipes may not exist)
			}
			return nil, fmt.Errorf("reading pipes directory %s: %w", pipesDir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pipeDir := filepath.Join(absPipesDir, entry.Name())
			pipeYAML := filepath.Join(pipeDir, "pipe.yaml")
			var pc PipeConfig
			if err := loadYAML(pipeYAML, &pc); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("loading pipe config %s: %w", entry.Name(), err)
			}
			pc.Dir = pipeDir
			cfg.Pipes[pc.Name] = pc
		}
	}

	// Load pipeline definitions from pipelines/*/pipeline.yaml (sibling to first pipesDir)
	if len(pipeDirs) > 0 {
		absPipesDir, _ := filepath.Abs(pipeDirs[0])
		pipelinesDir := filepath.Join(filepath.Dir(absPipesDir), "pipelines")
		pipelineEntries, err := os.ReadDir(pipelinesDir)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading pipelines directory: %w", err)
		}
		for _, entry := range pipelineEntries {
			if !entry.IsDir() {
				continue
			}
			plYAML := filepath.Join(pipelinesDir, entry.Name(), "pipeline.yaml")
			var pc PipelineConfig
			if err := loadYAML(plYAML, &pc); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("loading pipeline config %s: %w", entry.Name(), err)
			}
			cfg.Pipelines[pc.Name] = pc
		}
	}

	// Validate each loaded pipeline against known pipe names.
	pipeNames := make(map[string]bool, len(cfg.Pipes))
	for name := range cfg.Pipes {
		pipeNames[name] = true
	}
	for _, pl := range cfg.Pipelines {
		if err := ValidatePipeline(pl, pipeNames); err != nil {
			return nil, fmt.Errorf("validating pipeline %s: %w", pl.Name, err)
		}
	}

	// Merge vocabulary from all pipes
	mergeVocabulary(cfg)

	// Merge templates from all pipes
	mergeTemplates(cfg)

	return cfg, nil
}

func mergeVocabulary(cfg *Config) {
	cfg.Vocabulary = VocabularyConfig{
		Verbs:     make(map[string][]string),
		Types:     make(map[string][]string),
		Sources:   make(map[string][]string),
		Modifiers: make(map[string][]string),
	}

	merge := func(target map[string][]string, source map[string][]string) {
		for word, mappings := range source {
			target[word] = append(target[word], mappings...)
		}
	}

	for _, pc := range cfg.Pipes {
		merge(cfg.Vocabulary.Verbs, pc.Vocabulary.Verbs)
		merge(cfg.Vocabulary.Types, pc.Vocabulary.Types)
		merge(cfg.Vocabulary.Sources, pc.Vocabulary.Sources)
		merge(cfg.Vocabulary.Modifiers, pc.Vocabulary.Modifiers)
	}
}

func mergeTemplates(cfg *Config) {
	type prioritizedEntry struct {
		priority int
		pipeName string
		entry    TemplateEntry
	}

	var all []prioritizedEntry
	for _, pc := range cfg.Pipes {
		if len(pc.Templates.Entries) == 0 {
			continue
		}
		priority := pc.Templates.Priority
		if priority == 0 {
			priority = 50
		}
		for _, e := range pc.Templates.Entries {
			all = append(all, prioritizedEntry{
				priority: priority,
				pipeName: pc.Name,
				entry:    e,
			})
		}
	}

	// Sort: priority ascending → specificity descending (more requires first) → pipe name alphabetical
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].priority != all[j].priority {
			return all[i].priority < all[j].priority
		}
		if len(all[i].entry.Requires) != len(all[j].entry.Requires) {
			return len(all[i].entry.Requires) > len(all[j].entry.Requires)
		}
		return all[i].pipeName < all[j].pipeName
	})

	cfg.Templates.Templates = make([]TemplateEntry, len(all))
	for i, pe := range all {
		e := pe.entry
		e.Pipe = pe.pipeName
		cfg.Templates.Templates[i] = e
	}
}

// validateConditionExpr checks that a condition expression uses supported
// syntax: truthy ("field"), null check ("field == null"), or equality
// ("field == value"). Rejects unsupported operators at config load time
// so invalid pipelines fail fast rather than at first execution.
func validateConditionExpr(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("empty condition expression")
	}
	for _, op := range []string{" != ", " > ", " < ", " >= ", " <= ", " && ", " || "} {
		if strings.Contains(expr, op) {
			return fmt.Errorf("unsupported operator in condition: %q", expr)
		}
	}
	parts := strings.SplitN(expr, " == ", 2)
	if strings.TrimSpace(parts[0]) == "" {
		return fmt.Errorf("empty field in condition: %q", expr)
	}
	return nil
}

// ValidatePipeline checks a PipelineConfig for structural errors.
// pipeNames is the set of known pipe names from cfg.Pipes.
func ValidatePipeline(pc PipelineConfig, pipeNames map[string]bool) error {
	stepNames := make(map[string]bool, len(pc.Steps))

	for i, step := range pc.Steps {
		// Unique step names.
		if step.Name != "" {
			if stepNames[step.Name] {
				return fmt.Errorf("duplicate step name %q", step.Name)
			}
			stepNames[step.Name] = true
		}

		// Nested pipeline references not yet supported.
		if step.Pipeline != "" {
			return fmt.Errorf("step %d (%s): nested pipeline references not yet supported", i, stepName(step))
		}

		// Validate pipe references.
		if step.Pipe != "" && !pipeNames[step.Pipe] {
			return fmt.Errorf("step %d (%s): unknown pipe %q", i, stepName(step), step.Pipe)
		}

		// Validate parallel branch pipes.
		for _, branch := range step.Parallel {
			if branch.Pipe != "" && !pipeNames[branch.Pipe] {
				return fmt.Errorf("step %d (%s): parallel branch references unknown pipe %q", i, stepName(step), branch.Pipe)
			}
		}

		// Validate graph step pipe.
		if step.Graph != nil && step.Graph.Pipe != "" && !pipeNames[step.Graph.Pipe] {
			return fmt.Errorf("step %d (%s): graph references unknown pipe %q", i, stepName(step), step.Graph.Pipe)
		}
	}

	// Validate loop step references.
	for _, loop := range pc.Loops {
		for _, s := range loop.Steps {
			if !stepNames[s] {
				return fmt.Errorf("loop %q references unknown step %q", loop.Name, s)
			}
		}
	}

	// Validate cycle from/to references.
	for _, cycle := range pc.Cycles {
		if !stepNames[cycle.From] {
			return fmt.Errorf("cycle %q: from references unknown step %q", cycle.Name, cycle.From)
		}
		if !stepNames[cycle.To] {
			return fmt.Errorf("cycle %q: to references unknown step %q", cycle.Name, cycle.To)
		}
	}

	// Validate all condition expressions parse as supported syntax.
	for _, step := range pc.Steps {
		if step.Condition != "" {
			if err := validateConditionExpr(step.Condition); err != nil {
				return fmt.Errorf("step %q: invalid condition: %w", stepName(step), err)
			}
		}
	}
	for _, loop := range pc.Loops {
		if err := validateConditionExpr(loop.Until); err != nil {
			return fmt.Errorf("loop %q: invalid until condition: %w", loop.Name, err)
		}
	}
	for _, cycle := range pc.Cycles {
		if err := validateConditionExpr(cycle.Condition); err != nil {
			return fmt.Errorf("cycle %q: invalid condition: %w", cycle.Name, err)
		}
	}

	return nil
}

// stepName returns a display name for a pipeline step, falling back to "<unnamed>".
func stepName(step PipelineStepConfig) string {
	if step.Name != "" {
		return step.Name
	}
	return "<unnamed>"
}

func loadYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

// UnmarshalPipeConfig parses YAML data into a PipeConfig.
func UnmarshalPipeConfig(data []byte, pc *PipeConfig) error {
	return yaml.Unmarshal(data, pc)
}

func (pc PipeConfig) ToDefinition() pipe.Definition {
	return pipe.Definition{
		Name:        pc.Name,
		Description: pc.Description,
		Category:    pc.Category,
		Triggers:    pc.Triggers,
		Flags:       pc.Flags,
		Vocabulary: pipe.DefinitionVocabulary{
			Verbs:     pc.Vocabulary.Verbs,
			Sources:   pc.Vocabulary.Sources,
			Types:     pc.Vocabulary.Types,
			Modifiers: pc.Vocabulary.Modifiers,
		},
	}
}

// HandlerPath returns the path to the pipe's executable binary.
func (pc PipeConfig) HandlerPath() string {
	return filepath.Join(pc.Dir, "run")
}

// TimeoutDuration parses the Timeout string as a time.Duration.
// Returns 30s if Timeout is empty or unparseable.
func (pc PipeConfig) TimeoutDuration() time.Duration {
	if pc.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(pc.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// LoadCredentials reads ~/.config/virgil/credentials.yaml and sets any listed
// environment variables that are not already present in the process environment.
// System environment variables always take precedence — this file is a fallback.
// Returns nil if the file doesn't exist.
func LoadCredentials() error {
	path := filepath.Join(UserDir(), "credentials.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading credentials.yaml: %w", err)
	}
	var creds map[string]string
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("parsing credentials.yaml: %w", err)
	}
	for k, v := range creds {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	return nil
}

// UserDir returns the path to the user's virgil config directory (~/.config/virgil).
// This is where user-specific files like credentials and tokens are stored,
// independent of which config directory the server resolves for pipe definitions.
func UserDir() string {
	return homeRelDir(".config", "virgil")
}

// DataDir returns the path to the shared virgil data directory (~/.local/share/virgil).
func DataDir() string {
	return homeRelDir(".local", "share", "virgil")
}

// LogDir returns the path to the virgil logs directory (~/.local/share/virgil/logs).
func LogDir() string {
	return filepath.Join(DataDir(), "logs")
}

// DailyPath returns a dated file path of the form dir/prefix-YYYY-MM-DD.ext.
func DailyPath(dir, prefix, ext string) string {
	return filepath.Join(dir, prefix+"-"+time.Now().Format("2006-01-02")+ext)
}

func homeRelDir(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(append([]string{"."}, parts...)...)
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
