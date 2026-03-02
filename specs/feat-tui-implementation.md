# Feature: TUI Implementation

## Metadata

type: `feat`
task_id: `tui-implementation`
prompt: `implementation of specs/tui.md`

## Feature Description

Implement the terminal user interface as specified in `specs/tui.md`. The current TUI is a minimal prototype — a flat message list, basic `textinput`, SSE streaming, and double-escape cancel. The spec calls for a full-featured interface with a scrollable stream viewport, side panel, multiline input, input history, color theming, markdown rendering, pipe mode, colon commands, autocomplete, responsive layout, and auto-reconnection.

This spec covers the full gap between what exists today and what the TUI spec describes. Due to scope, it is decomposed into sub-tasks that can be implemented independently.

## User Story

As a Virgil user
I want a polished, minimal terminal interface that feels like a conversation
So that I can interact with pipes and pipelines naturally without managing complexity

## Relevant Files

- `internal/tui/tui.go` — Main bubbletea model, Update loop, View rendering. Currently uses `textinput.Model`, flat `[]string` messages, animated dots. Needs rewrite to viewport-based layout with textarea, theming, and panel support.
- `internal/tui/client.go` — SSE client (`openSSEStream`, `sseReader`, `postSignal`). Mostly complete. Needs reconnection logic.
- `internal/tui/oneshot.go` — One-shot mode. Functional. Needs streaming support (currently uses sync `postSignal`).
- `internal/tui/autostart.go` — Server auto-start. Functional. No changes needed.
- `internal/tui/oneshot_test.go` — One-shot tests. Functional.
- `internal/envelope/envelope.go` — Envelope types and content rendering. No changes needed.
- `internal/server/api.go` — SSE handler, signal handler. May need new endpoints for commands (`:pipes`, `:status`).
- `internal/server/server.go` — HTTP server, route registration. May need new routes.
- `cmd/virgil/main.go` — Entry point. Needs pipe mode detection (stdin is not a terminal).

### New Files

- `internal/tui/theme.go` — Centralized lipgloss theme (dark/light palettes, style definitions).
- `internal/tui/panel.go` — Panel model (viewport, toggle, pipeline detail rendering).
- `internal/tui/stream.go` — Stream model (viewport, message buffer, markdown rendering).
- `internal/tui/input.go` — Input model (textarea, history, multiline expansion).
- `internal/tui/command.go` — Colon command parser and dispatcher.
- `internal/tui/pipe.go` — Pipe mode (stdin detection, processing, stdout output).

## Implementation Plan

### Phase 1: Foundation — Layout, Viewport, Resize

Replace the flat message list with a proper bubbletea layout: a scrollable stream viewport (using `bubbles/viewport`) and a textarea input at the bottom. Handle `tea.WindowSizeMsg` to adapt dimensions. This is the foundation everything else builds on.

### Phase 2: Input Upgrade — Textarea, History, Keybindings

Replace `textinput.Model` with `bubbles/textarea` for multiline support (`Shift+Enter`). Add input history (Up/Down recall). Implement core keybindings: `Ctrl+D` exit, `Esc` clear input (when not cancelling a stream), `Ctrl+C` clear-or-exit.

### Phase 3: Visual — Theme, Styles, Symbols

Create a centralized lipgloss theme with dark/light palettes. Style user input, Virgil responses, pipeline notifications, and errors with distinct visual treatments. Use the spec's symbol vocabulary (`❯`, `▸`, `✓`, `✗`, `◉`, `○`). Change the prompt character from `>` to `❯`.

### Phase 4: Markdown Rendering

Integrate `glamour` for rendering markdown in Virgil's responses. Use a custom style that matches the TUI's restrained palette. Code blocks get subtle syntax highlighting. Lists use `·` bullets.

### Phase 5: Panel

Add a side panel (toggled by `Tab` or `Ctrl+P`) that shows pipeline detail, expanded content, or structured data. Panel takes ~1/3 of terminal width. Stream compresses when panel is open. Panel scrolls with `Ctrl+J/K`.

### Phase 6: Pipe Mode

Detect when stdin is not a terminal. Read piped content, combine with CLI args as the signal, send to server, print response to stdout, exit. Composes with Unix tools.

### Phase 7: Commands

Implement `:` prefix command system. Parse colon commands (`:panel`, `:clear`, `:quit`, `:pipes`, `:status`, `:config`, `:log`). Commands are handled by the TUI or forwarded to the server. Bare `:` opens a command palette (filterable list).

### Phase 8: Resilience — Reconnection, Queueing

Detect connection drops. Show status in stream (`✗ Server connection lost. Reconnecting...`). Auto-reconnect with retry. Queue inputs during reconnection. Recover pipeline state after reconnect.

### Phase 9: Responsive Layout

Adapt to terminal width: full (120+ cols) side-by-side, medium (80-119) compressed panel, narrow (<80) panel as overlay. Warn at minimum (60x15).

### Phase 10: Autocomplete

Tab completion for pipe names, flags, flag values, and command names. Ghost text style (dimmed inline). Tab cycles alternatives.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create theme system (`internal/tui/theme.go`)

- Define a `Theme` struct with lipgloss styles for: user input, response text, pipeline notification, error, success, active, prompt character, separator.
- Define dark and light palettes using the color spec (white/default for input, soft gray for responses, dim gray for notifications, muted red/green/blue for status).
- Add a `NewTheme(mode string) Theme` constructor (`mode` is "dark", "light", or "auto").
- Include the symbol vocabulary as theme constants: `❯`, `▸`, `✓`, `✗`, `◉`, `○`, `⟳`, `↺`, `…`.

### 2. Create stream model (`internal/tui/stream.go`)

- Define a `Stream` struct wrapping a `viewport.Model` and a message buffer (`[]StreamEntry` where `StreamEntry` has `Kind` (input/response/notification/error) and `Text`).
- `Append(kind, text)` adds to the buffer and re-renders the viewport content using the theme.
- Respect `maxBuffer` limit (default 5000 lines).
- The viewport auto-scrolls to bottom on new content unless the user has scrolled up.

### 3. Create input model (`internal/tui/input.go`)

- Define an `Input` struct wrapping `textarea.Model` with history (`[]string`, cursor index).
- Configure textarea: max 6 lines, auto-collapse after submit, prompt character `❯ `.
- `Submit() string` returns the current value, adds to history, clears the input.
- `HistoryUp()` / `HistoryDown()` navigate history.
- History is in-memory, per-session, capped at 100 entries.

### 4. Rewrite the main model (`internal/tui/tui.go`)

- Replace the `model` struct with a composition of `Stream`, `Input`, `Theme`, and layout state (`width`, `height`, `panelOpen`).
- Handle `tea.WindowSizeMsg` to set dimensions and resize sub-models.
- In `Update`, route keys through: escape-cancel logic (preserved), input keybindings (Enter submit, Shift+Enter newline, Up/Down history, Ctrl+C clear-or-exit, Ctrl+D exit, Esc clear input when idle).
- In `View`, render: stream viewport at top (full height minus input height), separator line, input at bottom. Use lipgloss `JoinVertical`.
- Preserve all existing streaming logic (`streamChunkMsg`, `streamDoneMsg`, `startStream`, `readNextEvent`).
- Apply theme styles: user input lines styled as input kind, response lines as response kind, errors as error kind.

### 5. Create panel model (`internal/tui/panel.go`)

- Define a `Panel` struct wrapping a `viewport.Model` with content state (pipeline steps, expanded content, structured data).
- `Toggle()` opens/closes. `SetContent(content)` updates what's shown.
- `Scroll(direction)` for `Ctrl+J/K`.
- Rendering: pipeline steps shown with `✓`/`◉`/`○` prefixes, durations, tree structure for sub-steps.

### 6. Integrate panel into main layout

- When panel is open, split the terminal: stream gets ~2/3, panel gets ~1/3. Use lipgloss `JoinHorizontal`.
- Input line spans only the stream region.
- `Tab` and `Ctrl+P` toggle the panel.
- Panel closes automatically when content is no longer relevant.

### 7. Add pipe mode (`internal/tui/pipe.go`, `cmd/virgil/main.go`)

- In `cmd/virgil/main.go`, detect stdin is not a terminal (`os.Stdin.Stat()` checking `ModeCharDevice`).
- When piped: read all stdin, combine with CLI args as signal text, send to server via `postSignal`, print response to stdout, exit with appropriate code.
- Errors go to stderr.

### 8. Add colon commands (`internal/tui/command.go`)

- Define a command registry: map of name → handler function.
- Built-in commands: `:panel` (toggle), `:panel close`, `:clear` (clear stream), `:quit`/`:q` (exit), `:pipes` (list available pipes — needs server endpoint), `:status` (server status — needs server endpoint).
- In the input submit handler, check if input starts with `:`. If so, route to command dispatcher instead of server.
- Bare `:` opens a simple filterable list of available commands.

### 9. Add server endpoints for commands

- `GET /pipes` — returns list of registered pipe definitions (name, description, category, triggers).
- `GET /status` — returns server status (uptime, active pipelines, pipe count).
- Add routes in `server.go` `Handler()`.

### 10. One-shot streaming support

- Modify `RunOneShot` to use SSE streaming (like interactive mode) so one-shot responses stream to stdout character-by-character.
- Detect long-running pipelines and print acknowledgment + exit (as spec describes).

### 11. Add reconnection logic (`internal/tui/client.go`)

- Add a `connection` struct that tracks server health.
- On connection failure, show `✗ Server connection lost. Reconnecting...` in stream.
- Retry connection with backoff (100ms, 200ms, 400ms... up to 3s).
- Queue input submissions during disconnection. Flush queue on reconnect.

### 12. Add responsive layout logic

- In the `tea.WindowSizeMsg` handler, determine layout mode based on width: full (120+), medium (80-119), narrow (<80).
- Full: panel side-by-side. Medium: panel narrows to ~30 cols minimum. Narrow: panel is overlay (replaces stream when open).
- Below 60x15: show warning and suggest one-shot mode.

### 13. Add markdown rendering

- Add `glamour` dependency.
- Create a custom glamour style matching the TUI theme (restrained colors, subtle code highlighting, `·` bullets, bold headings without `#`).
- In `Stream.Append` for response-kind entries, render through glamour before adding to viewport.

### 14. Add autocomplete

- Build a completer that knows pipe names (from server `/pipes` endpoint), flags (from pipe definitions), and command names.
- On `Tab` in the input, check context: after `:` → complete commands, after pipe name + `--` → complete flags, after flag `=` → complete values, otherwise → complete pipe names.
- Render as ghost text (dimmed) after the cursor. Tab accepts, continued typing dismisses.

## Testing Strategy

### Unit Tests

- `internal/tui/theme_test.go` — Theme construction, dark/light palette differences, style application.
- `internal/tui/stream_test.go` — Buffer append, max buffer truncation, entry kind styling.
- `internal/tui/input_test.go` — History navigation (up/down/bounds), submit clears input, multiline handling.
- `internal/tui/command_test.go` — Command parsing (`:panel` → "panel", `:quit` → "quit"), unknown command error, bare `:` returns empty.
- `internal/tui/pipe_test.go` — Stdin detection, piped content processing, output formatting.
- `internal/tui/panel_test.go` — Toggle state, scroll bounds, pipeline step rendering.

### Edge Cases

- Terminal resize during active stream (viewport must re-render without losing content).
- Double-escape cancel still works with textarea input model.
- Very long responses (exceeding buffer) — oldest entries truncated.
- Pipe mode with empty stdin — signal is CLI args only.
- Colon command with extra spaces (`: panel` should still work or error clearly).
- Server unreachable on startup — clear error message, not a panic.
- Rapid input during streaming — messages queue correctly.

## Risk Assessment

- **textarea vs textinput migration**: The existing `textinput.Model` handles double-escape cancel and submit. Switching to `textarea.Model` changes key handling (Enter inserts newline by default in textarea). Mitigation: configure textarea to treat Enter as submit and Shift+Enter as newline.
- **glamour dependency**: Adds a significant dependency tree (chroma for syntax highlighting). Mitigation: use glamour only for response rendering, not for all text. Can defer to a later phase if dependency size is a concern.
- **Panel layout complexity**: lipgloss horizontal joining with dynamic widths requires careful calculation. Mitigation: implement panel as a separate phase, test at multiple terminal widths.
- **Reconnection state**: Queued inputs during disconnection could become stale or conflict. Mitigation: keep queue simple (FIFO), warn user if queue grows large.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
go build ./...
go test ./... -v -count=1
```

## Open Questions (Unresolved)

1. **glamour vs custom renderer**: The spec mentions glamour for markdown rendering, but glamour's default styles are heavy. Should we use glamour with a custom style, or build a lightweight custom markdown renderer? **Recommendation**: Start with glamour + custom style. Replace later only if glamour proves too heavy or inflexible.

2. **textarea Enter/Shift+Enter behavior**: bubbletea's `textarea` treats Enter as newline by default. The spec wants Enter=submit and Shift+Enter=newline, which is the opposite of textarea's default. This requires intercepting Enter in the Update loop before it reaches the textarea. **Recommendation**: Intercept `tea.KeyEnter` in the model's Update, call submit, and only forward `tea.KeyShiftEnter` (mapped to newline) to the textarea.

3. **Panel auto-close**: The spec says the panel "closes automatically when its content is no longer relevant." What triggers this? Pipeline completion + user sends a new message? Timer? **Recommendation**: Panel auto-closes 5 seconds after a pipeline completes and the user submits a new unrelated input. Explicit close (Tab or `:panel close`) always works.

4. **TUI config file**: The spec describes `~/.config/virgil/tui.yaml` for client preferences. Should this be implemented now or deferred? **Recommendation**: Defer to a follow-up. Use hardcoded defaults for now. The config system can be layered in without changing the TUI architecture.

## Sub-Tasks

This feature is too large for a single implementation pass. Decompose into these sub-tasks, each implementable independently:

### Sub-task 1: Foundation (Steps 1-4)
**Scope**: Theme system, stream viewport, textarea input with history, main model rewrite.
**Depends on**: Nothing.
**Delivers**: A functional TUI with scrollable output, multiline input, styled text, and all existing streaming preserved.

### Sub-task 2: Panel (Steps 5-6)
**Scope**: Panel model, layout integration, toggle, scroll.
**Depends on**: Sub-task 1 (needs the viewport-based layout).
**Delivers**: Side panel that can display pipeline detail and toggle open/closed.

### Sub-task 3: Pipe Mode (Step 7)
**Scope**: Stdin detection, piped content handling.
**Depends on**: Nothing (standalone).
**Delivers**: `cat file | virgil summarize` works.

### Sub-task 4: Commands (Steps 8-9)
**Scope**: Colon command system, server endpoints.
**Depends on**: Sub-task 1 (needs the new input model).
**Delivers**: `:clear`, `:quit`, `:panel`, `:pipes`, `:status` commands.

### Sub-task 5: Resilience (Step 11)
**Scope**: Reconnection, input queueing.
**Depends on**: Sub-task 1.
**Delivers**: Auto-reconnect on connection drop with queued inputs.

### Sub-task 6: Polish (Steps 10, 12-14)
**Scope**: One-shot streaming, responsive layout, markdown rendering, autocomplete.
**Depends on**: Sub-tasks 1-4.
**Delivers**: Final polish matching the full spec.
