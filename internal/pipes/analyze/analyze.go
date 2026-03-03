package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// AnalysisResult is the structured output parsed from the AI response.
type AnalysisResult struct {
	Scope         Scope          `json:"scope"`
	Components    []Component    `json:"components"`
	Risks         []Risk         `json:"risks"`
	Approaches    []Approach     `json:"approaches"`
	OpenQuestions []OpenQuestion `json:"open_questions"`
	Resolved      []Resolved     `json:"resolved"`
}

type Scope struct {
	In       []string `json:"in"`
	Out      []string `json:"out"`
	Boundary []string `json:"boundary"`
}

type Component struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Dependencies []string `json:"dependencies"`
	Complexity   string   `json:"complexity"`
}

type Risk struct {
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Likelihood  string `json:"likelihood"`
	Mitigation  string `json:"mitigation"`
}

type Approach struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Tradeoffs      string `json:"tradeoffs"`
	Recommendation bool   `json:"recommendation"`
}

type OpenQuestion struct {
	Question     string   `json:"question"`
	WhyItMatters string   `json:"why_it_matters"`
	Options      []string `json:"options"`
}

type Resolved struct {
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	Implications string `json:"implications"`
}

type templateData struct {
	Content         string
	CodebaseContext string
	State           string
	OpenQuestions   string
	Depth           string
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("analyze", "analyze")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		logger.Debug("analyzing", "phase", flags["phase"], "prompt_len", len(userPrompt))
		raw, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("analysis failed", "error", err)
			out.Error = envelope.ClassifyError("analysis failed", err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		result, err := parseAnalysisResult(raw)
		if err != nil {
			logger.Error("analysis parse failed", "error", err)
			out.Error = envelope.FatalError(fmt.Sprintf("%s — raw response: %s", err.Error(), raw))
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("analyzed", "phase", flags["phase"], "components", len(result.Components))
		out.Content = result
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		content = flags["topic"]
	}
	if content == "" {
		return "", "", envelope.FatalError("no content provided for analysis")
	}

	phase := pipeutil.FlagOrDefault(flags, "phase", "initial")
	if _, ok := compiled[phase]; !ok {
		return "", "", envelope.FatalError(fmt.Sprintf("unknown phase: %q", phase))
	}

	depth := pipeutil.FlagOrDefault(flags, "depth", "standard")

	systemPrompt = pipeConfig.Prompts.System

	userPrompt, err := pipeutil.ExecuteTemplate(compiled, phase, templateData{
		Content:         content,
		CodebaseContext: flags["context"],
		State:           flags["state"],
		OpenQuestions:   flags["open_questions"],
		Depth:           depth,
	})
	if err != nil {
		return "", "", envelope.FatalError(fmt.Sprintf("template execution failed: %v", err))
	}

	return systemPrompt, userPrompt, nil
}

func parseAnalysisResult(raw string) (AnalysisResult, error) {
	cleaned := stripMarkdownFences(raw)
	var result AnalysisResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return AnalysisResult{}, fmt.Errorf("model returned invalid JSON: %w", err)
	}
	if len(result.Scope.In) == 0 {
		return AnalysisResult{}, fmt.Errorf("scope.in must have at least one item")
	}
	// Normalize nil slices to empty slices
	if result.Components == nil {
		result.Components = []Component{}
	}
	if result.Risks == nil {
		result.Risks = []Risk{}
	}
	if result.Approaches == nil {
		result.Approaches = []Approach{}
	}
	if result.OpenQuestions == nil {
		result.OpenQuestions = []OpenQuestion{}
	}
	if result.Resolved == nil {
		result.Resolved = []Resolved{}
	}
	if result.Scope.Out == nil {
		result.Scope.Out = []string{}
	}
	if result.Scope.Boundary == nil {
		result.Scope.Boundary = []string{}
	}
	return result, nil
}

var stripMarkdownFences = pipeutil.StripMarkdownFences
