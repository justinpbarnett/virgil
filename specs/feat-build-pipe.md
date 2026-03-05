# Feature: Build Pipe

## Metadata

type: `feat`
task_id: `build-pipe`
prompt: `Add a non-deterministic build pipe that takes a feature spec and codebase context, plans the implementation approach, then writes code and tests in TDD style. On subsequent cycles, it receives structured reviewer findings and addresses them specifically rather than reimplementing from scratch. The pipe operates within a worktree — all file writes are isolated.`

## Feature Description

The `build` pipe is the core implementation engine for the `dev-feature` pipeline. It receives a feature spec, codebase context (from the study pipe), and optionally reviewer findings (from a previous cycle), then produces working code and tests.

Unlike the existing `code` pipe (which generates a single code artifact from a prompt), `build` orchestrates a multi-file implementation: it plans which files to create or modify, determines test strategy, writes tests first (TDD), then implements until tests pass. It uses a provider-agnostic agentic multi-turn loop — tools for file I/O and shell commands are defined in Go and executed by the loop runner. Any provider (Anthropic, OpenAI, Gemini) can be used; the pipe does not delegate to a specific CLI.

On cycle 2+, the pipe receives structured findings from the reviewer. These findings become the primary instruction — the builder addresses each finding specifically (category, file, line, issue, action) rather than starting over.

## User Story

As a pipeline step
I want to plan and implement a feature with tests from a spec and codebase context
So that the pipeline produces working, tested code ready for verification

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/code/code.go` — existing code generation pipe (simpler, single-artifact)
- `internal/pipes/study/study.go` — study pipe whose output is this pipe's input context
- `internal/pipehost/host.go` — subprocess harness

### Existing Files (Modified)

- `internal/bridge/bridge.go` — add `Tool`, `ToolCall`, `AgenticResponse`, and `AgenticProvider` interface; add `RunAgenticLoop` helper
- `internal/bridge/anthropic.go` — implement `CompleteWithTools` using `anthropic-sdk-go` tool use API
- `internal/bridge/openai.go` — implement `CompleteWithTools` using `openai-go` function calling API
- `internal/bridge/gemini.go` — implement `CompleteWithTools` by extending `buildRequest` with `functionDeclarations`
- `internal/pipes/build/build.go` — update handler to use `AgenticProvider` and `RunAgenticLoop` instead of `provider.Complete()`; populate `FilesCreated`/`FilesModified` from go-git diff
- `internal/pipes/build/build_test.go` — add tests for tool execution, agentic loop, worktree diffing
- `internal/pipes/build/cmd/main.go` — update to resolve `AgenticProvider` from env

### New Files

- `internal/bridge/agentic.go` — `RunAgenticLoop`: the shared turn management loop (call provider → execute tool calls → feed results back → repeat until final text or max turns)
- `internal/bridge/agentic_test.go` — tests for the loop: tool execution, max turns, error paths
- `internal/pipes/build/pipe.yaml` — pipe definition (already exists as stub; update `cwd` flag)
- `internal/pipes/build/tools.go` — `read_file`, `write_file`, `edit_file`, `run_shell`, `list_dir` tool constructors scoped to worktree path
- `internal/pipes/build/tools_test.go` — unit tests for each tool: path escaping, sandboxing enforcement, error cases

### New Dependency

- `github.com/go-git/go-git/v5` — post-execution worktree diff to populate `FilesCreated`/`FilesModified` in `BuildOutput`; provider-agnostic (works regardless of which model ran the build)

## Implementation Plan

### Phase 0: Bridge Extension (prerequisite)

Add provider-agnostic tool use to the bridge before touching the build pipe. This is a standalone change that benefits any future agentic pipe.

Define `Tool`, `ToolCall`, `AgenticResponse`, and `AgenticProvider` in `bridge.go`. Implement `RunAgenticLoop` in `agentic.go`. Implement `CompleteWithTools` on `anthropicProvider`, `openaiProvider`, and `geminiProvider`. The `claudeProvider` (CLI) does not need `CompleteWithTools` — it is not the target for agentic pipes.

### Phase 1: Pipe Definition

`pipe.yaml` already exists as a stub. Add the `cwd` flag (worktree path, required for agentic execution) and verify `max_turns` is configurable.

### Phase 2: Prompt Engineering

The system prompt and templates already exist as stubs. The agentic approach changes what the user prompt says: instead of asking the model to describe what it would do, instruct it to use the provided tools to actually do it. The prompt tells the model it has `read_file`, `write_file`, `edit_file`, `run_shell`, and `list_dir` available.

### Phase 3: Tools

Implement the five tools in `tools.go`. Each tool constructor takes the worktree path and returns a `bridge.Tool`. All file operations must validate that the resolved path stays within the worktree (no path traversal). `run_shell` executes with the worktree as working directory.

### Phase 4: Handler

Update the handler to use `AgenticProvider` and `bridge.RunAgenticLoop`. After the loop completes, use `go-git` to diff the worktree HEAD and populate `FilesCreated`/`FilesModified`. The handler collects the final text response as the summary.

### Phase 5: Tests

Test prompt construction, findings integration, tool sandboxing, agentic loop integration, and worktree diffing.

## Step by Step Tasks

### 0. Extend the bridge with tool use support

Add to `internal/bridge/bridge.go`:

```go
// Tool defines a capability the model can invoke during an agentic loop.
type Tool struct {
    Name        string
    Description string
    InputSchema json.RawMessage // JSON Schema object describing the input
    Execute     func(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolCall is a single tool invocation returned by the model.
type ToolCall struct {
    ID    string          // provider-assigned call ID, echoed in the result
    Name  string
    Input json.RawMessage
}

// AgenticResponse is the result of one turn in an agentic loop.
// Either Text is set (model is done) or ToolCalls is set (model wants tools executed).
type AgenticResponse struct {
    Text      string
    ToolCalls []ToolCall
}

// AgenticProvider extends Provider with tool use support.
type AgenticProvider interface {
    Provider
    // CompleteWithTools sends one turn with the current message history and
    // available tools. Returns either a final text response or tool call
    // requests. The caller manages the loop.
    CompleteWithTools(ctx context.Context, system string, messages []AgenticMessage, tools []Tool) (AgenticResponse, error)
}

// AgenticMessage is a single entry in the conversation history.
type AgenticMessage struct {
    Role        string      // "user", "assistant", "tool_result"
    Content     string
    ToolCalls   []ToolCall  // populated for assistant turns that requested tools
    ToolResults []ToolResult
}

// ToolResult carries the output of an executed tool call.
type ToolResult struct {
    CallID  string
    Content string
    IsError bool
}
```

Create `internal/bridge/agentic.go`:

```go
// RunAgenticLoop drives the tool use loop until the model returns a final
// text response or maxTurns is exhausted.
//
// Loop:
//   1. Call CompleteWithTools with current history and tools.
//   2. If response has ToolCalls: execute each tool, append results to history, continue.
//   3. If response has Text: return it.
//   4. If maxTurns reached without Text: return error.
func RunAgenticLoop(ctx context.Context, p AgenticProvider, system, user string, tools []Tool, maxTurns int) (string, error)
```

Implement `CompleteWithTools` on each provider:

- `anthropicProvider` — use `anthropic.MessageNewParams.Tools []ToolParam` and parse `ToolUseBlock` from the response. Feed results back as `anthropic.NewToolResultsMessage`.
- `openaiProvider` — use `ChatCompletionNewParams.Tools []ChatCompletionToolParam` and parse `ToolCalls` from `Choices[0].Message`. Feed results back as `openai.ToolMessage`.
- `geminiProvider` — extend `buildRequest` to include `tools.functionDeclarations`. Parse `functionCall` parts from candidates. Feed results back as `functionResponse` parts in the next turn.

### 1. Update `pipe.yaml`

`internal/pipes/build/pipe.yaml` already exists. Add the `cwd` flag and `max_turns` flag:

```yaml
flags:
  spec:
    description: The feature description to implement.
    default: ""
  cwd:
    description: Worktree path. All file operations are confined to this directory.
    default: ""
  max_turns:
    description: Maximum agentic loop turns before giving up.
    default: "20"
  style:
    description: Implementation approach.
    values: [tdd, impl-first]
    default: tdd
  findings:
    description: Structured reviewer findings from a previous cycle (JSON string).
    default: ""
```

Update the system prompt to tell the model it has tools:

```yaml
prompts:
  system: |
    You are a meticulous software developer. You plan before you code,
    write tests before implementation, and follow project conventions exactly.

    You have the following tools available:
    - read_file: read the contents of a file
    - write_file: create or overwrite a file
    - edit_file: replace a specific string in a file (prefer over write_file for targeted changes)
    - run_shell: execute a shell command in the working directory
    - list_dir: list the contents of a directory

    Rules:
    - Read the codebase context carefully. Match existing patterns — naming,
      error handling, import style, test structure.
    - Plan your approach first: list which files to create or modify, what
      tests to write, and the implementation order.
    - Write tests first (TDD). Make them fail. Then implement until they pass.
    - Use run_shell to verify tests pass before declaring done.
    - Handle errors explicitly. No silent failures.
    - Do not read or write files outside the working directory.
    - When findings from a reviewer are present, address each finding
      specifically. Do not reimplement from scratch — make targeted changes
      that resolve each issue.
    - Keep changes minimal and focused. Do not refactor code that isn't
      related to the feature or findings.
```

### 2. Create `tools.go`

Create `internal/pipes/build/tools.go`:

```go
// BuildTools returns the set of tools available to the build pipe.
// All file operations are sandboxed to worktreePath.
func BuildTools(worktreePath string) []bridge.Tool
```

Implement five tools. Each validates the resolved path stays within `worktreePath` before executing:

- `read_file(path string)` — read and return file contents; error if outside worktree
- `write_file(path string, content string)` — create parent dirs if needed, write file
- `edit_file(path string, old_str string, new_str string)` — read file, replace first occurrence of `old_str` with `new_str`, write back; error if `old_str` not found
- `run_shell(command string)` — execute via `sh -c` with cwd set to worktreePath; return combined stdout+stderr; cap output at 64KB
- `list_dir(path string)` — return newline-separated directory listing

### 3. Define output types

`BuildOutput` and `ReviewFinding` already exist in `build.go`. No changes needed — the struct fields are correct for the agentic approach. `FilesCreated` and `FilesModified` are populated post-loop via go-git rather than parsed from text.

### 4. Update the handler

`internal/pipes/build/build.go` exists as a stub using `provider.Complete()`. Replace the core invocation:

- Change `NewHandler` and `NewStreamHandler` signatures to accept `bridge.AgenticProvider` instead of `bridge.Provider`/`bridge.StreamingProvider`.
- Handler logic:
  1. Extract `spec` from flags. If empty, use input envelope's text content as the spec.
  2. Extract `cwd` from flags. If empty, return fatal error — agentic execution requires a worktree path.
  3. Extract `style` from flags (default: `tdd`).
  4. Extract `max_turns` from flags (default: `20`).
  5. Extract `findings` from flags. If non-empty, parse as `[]ReviewFinding` JSON.
  6. Extract codebase context from the input envelope's structured content (the study pipe's output).
  7. Select prompt template: `rework` if findings present, `initial` otherwise.
  8. Execute the prompt template with spec, context, style, and findings data.
  9. Build tools slice via `BuildTools(cwd)`.
  10. Call `bridge.RunAgenticLoop(ctx, provider, systemPrompt, userPrompt, tools, maxTurns)`.
  11. After loop returns, use `go-git` to open the repo at `cwd`, call `worktree.Status()`, and split entries into `FilesCreated` (untracked or added) and `FilesModified` (modified).
  12. Build `BuildOutput` with the loop's final text as `Summary`, file lists, `Style`, and `CycleNumber`.
  13. Return envelope with `content_type: structured`.

The stream handler follows the same logic. Streaming chunks come from the agentic loop's text deltas — expose a `onChunk` callback through `RunAgenticLoop` (or via a streaming variant) so the TUI sees incremental output during long builds.

### 5. Create subprocess entry point

`internal/pipes/build/cmd/main.go` already exists as a stub. Update to resolve an `AgenticProvider`:

```go
func main() {
    logger := pipehost.NewPipeLogger("build")
    provider, err := pipehost.BuildAgenticProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("build", err.Error())
    }
    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("build", err.Error())
    }
    compiled := build.CompileTemplates(pc)
    pipehost.Run(provider,
        build.NewHandlerWith(provider, pc, compiled, logger),
    )
}
```

`BuildAgenticProviderFromEnvWithLogger` is a new helper in `pipehost` that calls `bridge.CreateProvider` and type-asserts to `AgenticProvider`, returning an error if the resolved provider does not implement it.

### 6. Write tests

`internal/pipes/build/build_test.go` exists with prompt and output tests. Add:

- **TestBuildAgenticLoop** — mock `AgenticProvider` returns one tool call (`write_file`) then a final text response. Verify tool was executed, `FilesCreated` is populated, `Summary` matches final text.
- **TestBuildCwdRequired** — no `cwd` flag, returns fatal error before calling the provider.
- **TestBuildMaxTurnsExhausted** — mock provider always returns tool calls, never text. Verify error is returned after `max_turns`.
- **TestBuildToolSandbox** — `write_file` with path `../../etc/passwd` returns an error; loop surfaces it as a tool result error; final summary reflects failure.

`internal/pipes/build/tools_test.go` (new):

- **TestReadFile** — reads an existing file within worktree
- **TestReadFileOutsideWorktree** — path traversal returns error
- **TestWriteFile** — creates file and parent dirs
- **TestWriteFileOutsideWorktree** — returns error
- **TestEditFile** — replaces target string
- **TestEditFileStringNotFound** — returns error
- **TestRunShell** — executes `echo hello`, returns stdout
- **TestRunShellOutputCapped** — output > 64KB is truncated, no error
- **TestListDir** — returns directory entries

`internal/bridge/agentic_test.go` (new):

- **TestRunAgenticLoopImmediateText** — provider returns text on first turn, no tools called
- **TestRunAgenticLoopOneTool** — provider returns one tool call, then text
- **TestRunAgenticLoopToolError** — tool execute returns error; error is fed back as tool result; loop continues
- **TestRunAgenticLoopMaxTurns** — loop exhausts max turns, returns error
- **TestRunAgenticLoopContextCancel** — context cancelled mid-loop, returns context error

### 7. Add go-git dependency

```bash
go get github.com/go-git/go-git/v5
```

### 8. Add to justfile build

The `justfile` already builds all pipes via glob. No change needed — `internal/pipes/build/cmd/main.go` is already covered.

## Testing Strategy

### Unit Tests
- `internal/bridge/agentic_test.go` — loop management, tool execution, max turns, context cancellation
- `internal/pipes/build/tools_test.go` — each tool: happy path, path sandboxing, error cases
- `internal/pipes/build/build_test.go` — prompt construction, findings integration, template selection, agentic integration, worktree diffing

### Edge Cases
- Empty findings array (first cycle, no rework needed)
- Findings with missing fields (graceful handling)
- `cwd` flag missing — fatal before any provider call
- Tool returns error — fed back as tool result, loop continues
- Model attempts path traversal — sandboxing returns error as tool result
- Loop exhausts `max_turns` without final text — error envelope
- Context cancelled mid-loop — context error propagated

## Risk Assessment

- **Medium risk — prompt quality is critical.** The system prompt must clearly describe the available tools and the expected workflow. Poor tool descriptions lead to incorrect invocations.
- **Tool sandboxing is a security boundary.** The `cwd` enforcement in each tool must be airtight. Path traversal through symlinks or `..` sequences must be rejected before any I/O.
- **Provider parity for tool use.** Anthropic and OpenAI have mature tool use APIs. Gemini's `functionDeclarations` are stable but the raw HTTP implementation will need careful testing. Consider adding a `CompleteWithTools` smoke test per provider in CI.
- **Findings integration must be precise.** The builder must address findings specifically (file, line, action) not vaguely. The prompt template must present findings in a format the model can act on mechanically.

## Validation Commands

```bash
go get github.com/go-git/go-git/v5
go test ./internal/bridge/... -v -count=1
go test ./internal/pipes/build/... -v -count=1
go build ./internal/pipes/build/cmd/
```

## Sub-Tasks

Two tasks in sequence:

1. **Bridge tool use extension** — `bridge.go` types, `agentic.go` loop, `CompleteWithTools` on all three providers, `agentic_test.go`. No build pipe changes yet.
2. **Build pipe agentic upgrade** — `tools.go`, update `build.go` handler, update `cmd/main.go`, add `go-git`, write tool and integration tests.
