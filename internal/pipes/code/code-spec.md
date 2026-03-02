# Code Pipe Specification

## Identity

```yaml
name: code
description: Generates source code from specifications and examples.
category: dev
streaming: true
timeout: 120s
```

Non-deterministic. Invokes an AI provider to produce compilable, tested, spec-compliant source code. Follows the project's conventions exactly — language idioms, naming, structure, error handling. The prompt is the pipe's soul; flags select the template.

Longer timeout than other non-deterministic pipes (120s vs 60s) because code generation with full context can take longer, especially for module-level output.

---

## Triggers

```yaml
triggers:
  exact:
    - "write code"
    - "generate code"
    - "write a function"
  keywords:
    - code
    - implement
    - generate
    - function
    - module
    - refactor
  patterns:
    - "code {type}"
    - "generate {type}"
    - "implement {type} for {topic}"
    - "write {type} for {topic}"
    - "refactor {topic}"
```

"code" and "implement" are the primary verbs. "generate" overlaps with draft but the type slot disambiguates — `generate function` routes here, `generate blog` routes to draft.

---

## Flags

```yaml
flags:
  type:
    description: What kind of code to produce.
    values: [function, module, test, refactor]
    default: function

  lang:
    description: Target programming language.
    default: ""

  style:
    description: Code style preference.
    values: [idiomatic, minimal, verbose]
    default: idiomatic
```

**type** — Selects the prompt template.

- `function` — A single function or method with its signature, body, and doc comment.
- `module` — A complete file or package with imports, types, and functions.
- `test` — Test code for the provided source. Produces table-driven tests in Go, pytest-style in Python, etc.
- `refactor` — Rewrites existing code from the input envelope. Preserves behavior, improves structure.

**lang** — Target language. When empty, the pipe infers from the input content (existing code, file extensions in the spec, or project context). When set, the pipe produces code in that language regardless of input signals. No fixed values list — accepts any language string the model can handle.

**style** — Controls verbosity and convention adherence.

- `idiomatic` — Default. Follows the language's community conventions. Go: short names, early returns, error wrapping. Python: PEP 8, type hints. TypeScript: strict mode, explicit types.
- `minimal` — Least code that works. No doc comments, no defensive checks beyond what's required.
- `verbose` — Full documentation, extensive error handling, defensive validation. Use when the code will be read by others unfamiliar with the codebase.

---

## Vocabulary

```yaml
vocabulary:
  verbs:
    code: code
    implement: code
    generate: code
    refactor: code.refactor
  types:
    function: function
    func: function
    module: module
    package: module
    test: test
    tests: test
    refactor: refactor
  sources: {}
  modifiers: {}
```

**Verb notes:**

- `code`, `implement`, `generate` all resolve to the `code` pipe.
- `refactor` maps to `code.refactor` — the parser extracts both the pipe (`code`) and the action (`refactor`), so the handler can default to `--type=refactor` without the flag being explicit.

**Conflict check:** `generate` is not claimed by any existing pipe. `write` is owned by draft and intentionally not claimed here — "write a function" routes through the keyword layer (`function` → code) rather than the verb layer. If the keyword layer misses, the AI fallback resolves it.

---

## Templates

```yaml
templates:
  priority: 50
  entries:
    - requires: [verb, type, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, sort: recent, limit: "10", topic: "{topic}" }
        - pipe: "{verb}"
          flags: { type: "{type}" }

    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { type: "{type}", topic: "{topic}" }

    - requires: [verb]
      plan:
        - pipe: "{verb}"
          flags: { type: function }
```

Standard composition patterns. The source-aware template fetches context from memory (specs, examples, prior code) before generating. The verb-only fallback defaults to `function` type.

---

## Prompts

```yaml
prompts:
  system: |
    You are a precise code generator. You produce clean, compilable,
    spec-compliant source code.

    Rules:
    - Follow the target language's idioms and conventions exactly.
    - Include necessary imports and type definitions.
    - Handle errors explicitly — no silent failures, no ignored returns.
    - Name things clearly. Favor readability over brevity.
    - Do not explain the code. Output only the code itself.
    - When given existing code as context, match its style — naming
      conventions, error patterns, import style, comment density.
    - When given a specification, implement every requirement. Do not
      skip edge cases. Do not add features not in the spec.

  templates:
    function: |
      Write a function based on the following specification.

      Specification:
      {{.Content}}

      {{if .Topic}}Function purpose: {{.Topic}}{{end}}
      {{if .Lang}}Language: {{.Lang}}{{end}}
      {{if .Style}}Style: {{.Style}}{{end}}

      Produce only the function, its doc comment, and any helper types
      it needs. Include imports if the function requires external packages.

    module: |
      Write a complete module based on the following specification.

      Specification:
      {{.Content}}

      {{if .Topic}}Module purpose: {{.Topic}}{{end}}
      {{if .Lang}}Language: {{.Lang}}{{end}}
      {{if .Style}}Style: {{.Style}}{{end}}

      Produce a complete, self-contained file: package declaration,
      imports, type definitions, exported functions, and internal helpers.
      Order declarations top-down — types first, then constructors,
      then methods, then helpers.

    test: |
      Write tests for the following code.

      Source code:
      {{.Content}}

      {{if .Topic}}Focus on: {{.Topic}}{{end}}
      {{if .Lang}}Language: {{.Lang}}{{end}}
      {{if .Style}}Style: {{.Style}}{{end}}

      Produce comprehensive tests:
      - Happy path for each exported function.
      - Edge cases: empty input, nil values, boundary conditions.
      - Error paths: verify error returns and messages.
      - Use table-driven tests when the language supports them (Go, Rust).
      - Use descriptive test names that state what is being verified.
      - Do not test unexported/private functions directly.

    refactor: |
      Refactor the following code. Preserve its external behavior exactly.

      Source code:
      {{.Content}}

      {{if .Topic}}Focus: {{.Topic}}{{end}}
      {{if .Lang}}Language: {{.Lang}}{{end}}
      {{if .Style}}Target style: {{.Style}}{{end}}

      Improve structure, naming, and clarity. Eliminate duplication.
      Simplify control flow. Do not change the public API — same
      function signatures, same types, same behavior. If you find
      a bug, fix it and note the fix in a comment.
```

### Template Data

The prompt templates use Go `text/template`. The data struct extends the standard fields:

| Field       | Source                                      |
| ----------- | ------------------------------------------- |
| `.Content`  | Input envelope content (spec, code, or raw) |
| `.Topic`    | Parsed topic from the signal                |
| `.Lang`     | `--lang` flag value                         |
| `.Style`    | `--style` flag value                        |

The `templateData` struct in `code.go`:

```go
type templateData struct {
    Content string
    Topic   string
    Lang    string
    Style   string
}
```

---

## Handler

### Behavior

1. Extract content from the input envelope via `envelope.ContentToText()`. If content is empty, fall back to `flags["topic"]`. If both are empty, return a fatal error — the pipe needs something to work from.
2. Resolve the prompt template from `flags["type"]`. If the type has no matching template, fall back to raw content with the system prompt (same pattern as draft).
3. Populate the template data struct from the envelope and flags.
4. Execute the template. Send system prompt + rendered user prompt to the provider.
5. Return the model's response as `content` with `content_type: "text"`.

### Error Handling

| Condition              | Severity  | Retryable | Content        |
| ---------------------- | --------- | --------- | -------------- |
| No content or topic    | fatal     | false     | empty          |
| Provider timeout       | error     | true      | empty          |
| Provider auth failure  | fatal     | false     | empty          |
| Template render error  | fatal     | false     | empty          |
| Provider returns empty | warn      | false     | empty string   |

Use `envelope.ClassifyError()` for provider errors — it auto-detects timeouts as retryable.

### Actions

The envelope's `action` field is always `"generate"` regardless of type flag. The type flag selects the template, not the action. Even refactoring is a form of generation.

---

## File Layout

```
internal/pipes/code/
├── pipe.yaml        # definition (copy from spec, finalize prompts)
├── code.go          # handler: CompileTemplates, NewHandler, NewStreamHandler
├── code_test.go     # handler tests
└── cmd/
    └── main.go      # subprocess entry point
```

### cmd/main.go

Follows the draft pattern exactly:

```go
package main

import (
    "github.com/justinpbarnett/virgil/internal/bridge"
    "github.com/justinpbarnett/virgil/internal/pipe"
    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/code"
)

func main() {
    logger := pipehost.NewPipeLogger("code")

    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("code", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("code", err.Error())
    }

    compiled := code.CompileTemplates(pc)

    logger.Info("initialized")
    pipehost.RunWithStreaming(provider, code.NewHandlerWith(provider, pc, compiled, logger), func(sp bridge.StreamingProvider) pipe.StreamHandler {
        return code.NewStreamHandlerWith(sp, pc, compiled, logger)
    })
}
```

### code.go

Mirrors draft.go structure:

- `CompileTemplates(pc config.PipeConfig) map[string]*template.Template` — pre-parses prompt templates from pipe config.
- `preparePrompt(compiled, pipeConfig, input, flags) (system, user string, err *EnvelopeError)` — extracts content, resolves template, returns prompts.
- `NewHandler(provider, pipeConfig, logger) pipe.Handler` — convenience wrapper.
- `NewHandlerWith(provider, pipeConfig, compiled, logger) pipe.Handler` — full constructor.
- `NewStreamHandler(provider, pipeConfig, logger) pipe.StreamHandler` — convenience wrapper.
- `NewStreamHandlerWith(provider, pipeConfig, compiled, logger) pipe.StreamHandler` — full constructor.
- `codeError(err error) *EnvelopeError` — wraps errors with `ClassifyError`.

The only structural difference from draft: the `templateData` struct adds `Lang` and `Style` fields, and `preparePrompt` reads those from flags.

---

## Tests

### Test Cases

**TestCodeWithType** — Provide content + `type=function`, assert output is text with no error.

**TestCodeModule** — Provide content + `type=module`, assert correct template used via mock provider receiving the rendered prompt.

**TestCodeTest** — Provide source code + `type=test`, assert test generation template used.

**TestCodeRefactor** — Provide source code + `type=refactor`, assert refactor template used.

**TestCodeNoType** — Provide content with no type flag, assert defaults to `function` template.

**TestCodeEmptyContent** — Empty envelope, no topic. Assert fatal error.

**TestCodeTopicFallback** — Empty content but `topic` flag set. Assert no error (topic used as content).

**TestCodeProviderError** — Mock provider returns error. Assert fatal error envelope via `testutil.AssertFatalError`.

**TestStreamHandler** — Mock streaming provider, assert chunks delivered and final content correct.

**TestStreamHandlerProviderError** — Mock streaming provider error, assert fatal error.

**TestStreamHandlerEmptyContent** — Empty content in stream mode, assert fatal error.

**TestCodeTemplateResolution** — Table-driven: each type value resolves to the right template. Unknown type falls back to raw content.

**TestCodeLangFlag** — Assert `--lang` value appears in rendered prompt.

### Test Helpers

Use `testutil.MockProvider` and `testutil.MockStreamProvider` from `internal/testutil/`. Use `testutil.AssertFatalError` for error assertions. Use `testutil.AssertEnvelope` for envelope field checks.

---

## Checklist

```
File Layout
  ☐  pipe.yaml at internal/pipes/code/pipe.yaml
  ☐  code.go alongside pipe.yaml
  ☐  code_test.go alongside pipe.yaml
  ☐  cmd/main.go subprocess entry point
  ☐  no configuration outside the code/ folder

Definition
  ☐  name: code (unique, no conflicts)
  ☐  description: one clear sentence
  ☐  category: dev
  ☐  triggers cover: code, implement, generate, function, module, refactor
  ☐  flags: type, lang, style — all with descriptions and defaults
  ☐  streaming: true, timeout: 120s
  ☐  prompts tested against real inputs

Vocabulary
  ☐  verbs: code, implement, generate, refactor → code
  ☐  types: function, func, module, package, test, tests, refactor
  ☐  no conflicts with existing pipe vocabulary
  ☐  sources and modifiers empty (not applicable)

Templates
  ☐  source-aware pattern (fetch context → generate)
  ☐  type-aware pattern (generate with type)
  ☐  verb-only fallback (defaults to function)
  ☐  priority 50, template variables only

Handler
  ☐  returns complete envelope (pipe, action, args, timestamp, duration, content, content_type, error)
  ☐  content_type: "text"
  ☐  errors via envelope, never panics
  ☐  handles missing type (defaults to function)
  ☐  handles empty content (fatal error)
  ☐  handles provider failures (ClassifyError)
  ☐  streaming support via StreamHandler

Subprocess
  ☐  cmd/main.go with pipehost.RunWithStreaming
  ☐  run binary gitignored
  ☐  graceful startup failure via pipehost.Fatal

Testing
  ☐  happy path per type (function, module, test, refactor)
  ☐  missing type defaults to function
  ☐  empty content → fatal error
  ☐  topic fallback when content empty
  ☐  provider error handling
  ☐  streaming: chunks + final content
  ☐  template resolution table-driven
  ☐  lang flag in rendered prompt
```
