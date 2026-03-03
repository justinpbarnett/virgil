# Feature: Analyze Pipe

## Metadata

type: `feat`
task_id: `analyze-pipe`
prompt: `Create the analyze pipe — a non-deterministic pipe that takes a request (or follow-up input with existing state) and produces structured analysis: scope boundaries, affected components, risks, approach comparisons, and targeted open questions. It outputs structured JSON, not prose. It is the analytical engine that feeds into draft for document production.`

## Feature Description

The analyze pipe is the thinking step in any pipeline that needs to decompose a problem before acting on it. It receives a user request plus optional context (codebase study output, existing working state) and produces a structured assessment: what's in scope, what's risky, what approaches exist and their tradeoffs, and what questions remain unanswered.

It does not write documents. It does not present to the user. It produces an intermediate structured representation that downstream pipes (draft, review) consume. The separation matters because analysis needs to be rigorous and honest — different quality criteria than prose writing.

The pipe operates in two modes via the `phase` flag:
- **initial** — no existing state. Analyze the raw request from scratch.
- **refine** — existing state present. Process new input against what's already decided.

## User Story

As a Virgil pipeline author
I want a pipe that produces structured technical analysis from a request
So that downstream pipes can draft documents, review plans, or present findings from a rigorous analytical foundation

## Relevant Files

- `internal/pipes/analyze/pipe.yaml` — pipe config (new)
- `internal/pipes/analyze/analyze.go` — handler implementation (new)
- `internal/pipes/analyze/analyze_test.go` — unit tests (new)
- `internal/pipes/analyze/cmd/main.go` — subprocess entry point (new)
- `internal/bridge/bridge.go` — `Provider` interface used for AI completion
- `internal/config/config.go` — `PipeConfig` struct, auto-discovered by config loader
- `internal/envelope/envelope.go` — `Envelope`, `ContentToText`, `ContentStructured`
- `internal/pipe/pipe.go` — `Handler` type
- `internal/pipehost/pipehost.go` — `Run`, `BuildProviderFromEnvWithLogger`, `LoadPipeConfig`

### New Files

- `internal/pipes/analyze/pipe.yaml`
- `internal/pipes/analyze/analyze.go`
- `internal/pipes/analyze/analyze_test.go`
- `internal/pipes/analyze/cmd/main.go`

## Implementation Plan

### Phase 1: Pipe Definition

Create the `pipe.yaml` with triggers, flags, vocabulary, and prompt templates for both phases (initial and refine).

### Phase 2: Handler

Build the handler that selects the prompt template based on `phase`, assembles context from the input envelope and flags, sends to the provider, parses the structured JSON response, and returns it as `content_type: structured`.

### Phase 3: Subprocess Entry Point

Wire up `cmd/main.go` following the established pattern (build provider, load config, compile templates, run).

## Step by Step Tasks

### 1. Create `internal/pipes/analyze/pipe.yaml`

```yaml
name: analyze
description: Produces structured technical analysis — scope, risks, approach comparisons, and targeted questions.
category: dev
streaming: false
timeout: 120s

triggers:
  exact:
    - "analyze this"
  keywords:
    - analyze
    - scope
    - assess
    - evaluate
    - decompose
  patterns:
    - "analyze {topic}"
    - "scope {topic}"
    - "assess {topic}"

flags:
  phase:
    description: Whether this is an initial analysis or a refinement pass.
    values: [initial, refine]
    default: initial
  depth:
    description: How deep to analyze.
    values: [shallow, standard, deep]
    default: standard

vocabulary:
  verbs:
    analyze: analyze
    scope: analyze
    assess: analyze
    decompose: analyze
  types: {}
  sources: {}
  modifiers:
    shallow: shallow
    deep: deep

prompts:
  system: |
    You are a senior engineer conducting a technical analysis. You think
    critically. You identify what's ambiguous, what's risky, and what
    decisions need to be made before implementation begins.

    You are not a yes-person. If the proposed approach has problems, say so.
    If there's a better way, present it as a comparison. If a question is
    unanswerable without more context, name exactly what context is missing.

    You MUST respond with valid JSON matching this schema:

    {
      "scope": {
        "in": ["items in scope"],
        "out": ["items explicitly excluded"],
        "boundary": ["items that required explicit in/out decisions"]
      },
      "components": [
        {
          "name": "component name",
          "description": "what it does",
          "dependencies": ["other component names"],
          "complexity": "low | medium | high"
        }
      ],
      "risks": [
        {
          "description": "what could go wrong",
          "severity": "low | medium | high",
          "likelihood": "low | medium | high",
          "mitigation": "how to address it"
        }
      ],
      "approaches": [
        {
          "name": "approach name",
          "description": "what this approach does",
          "tradeoffs": "concrete consequences for this system",
          "recommendation": true | false
        }
      ],
      "open_questions": [
        {
          "question": "the specific question",
          "why_it_matters": "impact on implementation if unanswered",
          "options": ["known options, if any"]
        }
      ],
      "resolved": [
        {
          "question": "what was asked",
          "answer": "what was decided",
          "implications": "downstream effects"
        }
      ]
    }

    The "resolved" array is only populated during refine phase.
    The "approaches" array is only populated when multiple viable approaches exist.

    Respond ONLY with JSON — no text outside the JSON object.

  templates:
    initial: |
      Analyze this proposed change and produce a structured assessment.

      Request:
      {{.Content}}

      {{if .CodebaseContext}}
      Codebase context:
      {{.CodebaseContext}}
      {{end}}

      Depth: {{.Depth}}

      Produce scope, components, risks, approaches (if multiple viable
      options exist), and open questions. Each open question must explain
      WHY it matters to the implementation — not vague "what do you want?"
      but pointed questions with consequences.

    refine: |
      Process the user's input against the current state.

      Current state:
      {{.State}}

      User's latest input:
      {{.Content}}

      {{if .OpenQuestions}}
      Previous open questions:
      {{.OpenQuestions}}
      {{end}}

      Determine which open questions are now answered. For each resolved
      question, state what was decided and what it implies for scope, risk,
      and implementation. Surface any new questions that emerge from the
      implications. Update risks based on decisions made.
```

### 2. Create `internal/pipes/analyze/analyze.go`

Define the output types, template data struct, and handler:

```go
package analyze

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
```

**Template data struct:**

```go
type templateData struct {
    Content         string
    CodebaseContext string
    State           string
    OpenQuestions   string
    Depth           string
}
```

**Handler behavior:**

1. `CompileTemplates(pipeConfig)` — same pattern as draft and review. Pre-compile `initial` and `refine` templates.
2. `preparePrompt(compiled, pipeConfig, input, flags)` — extract content from input envelope via `ContentToText`. Populate `CodebaseContext` from `flags["context"]`. Populate `State` from `flags["state"]`. Populate `OpenQuestions` from `flags["open_questions"]`. Select template by `flags["phase"]` (default `"initial"`). Set `Depth` from `flags["depth"]` (default `"standard"`).
3. `NewHandler(provider, pipeConfig, logger)` — send system prompt + user prompt to provider. Parse response as JSON into `AnalysisResult` (reuse the `stripMarkdownFences` pattern from review). Validate required fields are present. Return envelope with `content_type: "structured"` and `AnalysisResult` as content.
4. `parseAnalysisResult(raw string)` — strip markdown fences, unmarshal JSON, validate that `scope` has at least one `in` item, return result.

**No streaming.** Analysis produces structured output that must be parsed as a whole.

### 3. Create `internal/pipes/analyze/cmd/main.go`

Follow the established pattern:

```go
func main() {
    logger := pipehost.NewPipeLogger("analyze")
    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    // handle err
    pc, err := pipehost.LoadPipeConfig()
    // handle err
    compiled := analyze.CompileTemplates(pc)
    handler := analyze.NewHandlerWith(provider, pc, compiled, logger)
    pipehost.Run(handler, nil) // no stream handler
}
```

### 4. Write tests in `internal/pipes/analyze/analyze_test.go`

**Test cases:**

- `TestPreparePrompt_Initial` — phase=initial, no state flag. Verify template selects `initial`, content is populated, state/open_questions fields are empty.
- `TestPreparePrompt_Refine` — phase=refine, state and open_questions flags set. Verify template selects `refine`, all fields populated.
- `TestPreparePrompt_NoContent` — empty input, no topic flag. Returns fatal error.
- `TestPreparePrompt_DepthFlag` — depth=deep flows through to template data.
- `TestParseAnalysisResult_Valid` — well-formed JSON parses correctly.
- `TestParseAnalysisResult_WithFences` — JSON wrapped in ```json fences parses correctly.
- `TestParseAnalysisResult_Invalid` — malformed JSON returns error.
- `TestParseAnalysisResult_MissingScope` — JSON with empty scope.in returns error.
- `TestNewHandler_Initial` — mock provider returning valid JSON, verify envelope has `content_type: structured` and `AnalysisResult` content with populated fields.
- `TestNewHandler_Refine` — mock provider, verify resolved array is populated.
- `TestNewHandler_ProviderError` — provider returns error, verify retryable error classification.

## Testing Strategy

### Unit Tests

- Template compilation and selection by phase
- Prompt assembly with all flag combinations
- JSON parsing with valid, fenced, and invalid inputs
- Handler integration with mock provider

### Edge Cases

- Refine phase with no previous open questions (user volunteered info unprompted)
- Provider returns valid JSON but with empty arrays — should pass (not everything has risks or approaches)
- Provider returns extra fields not in schema — should be ignored (forward compatibility)
- Very long codebase context — pipe doesn't truncate, that's the caller's job (study pipe handles budget)

## Validation Commands

```bash
go test ./internal/pipes/analyze/... -v -count=1
go build ./internal/pipes/analyze/cmd/
```
