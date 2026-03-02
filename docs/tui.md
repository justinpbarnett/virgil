# Terminal Interface Specification

This document defines the design of Virgil's terminal user interface — the primary client. It covers visual structure, interaction patterns, the three operating modes, and the principles that govern every pixel and every keystroke.

For the philosophy behind Virgil, see `virgil.md`. For architecture, see `ARCHITECTURE.md`. For the server API the TUI connects to, see (future) `api.md`.

---

## Design Philosophy

Virgil's interface exists to disappear. The best moment in the Divine Comedy is not when Virgil speaks — it's when Dante realizes he's already where he needs to be. The guide did the work so quietly that arrival felt like inevitability.

The TUI follows the same principle. The interface should feel like talking to someone who knows the terrain. Not a dashboard. Not a control panel. Not a chat app with extra features. A presence. Calm when things are simple, precise when things are complex, silent when there's nothing to say.

Three rules govern every design decision:

**Proportional presence.** The interface scales its complexity to match the moment. A quick answer gets a quick response — no chrome, no ceremony. A complex pipeline gets structure, progress, detail. The interface never shows more than the moment demands.

**Earned attention.** Every element on screen must justify its existence. If removing something wouldn't hurt comprehension or control, it shouldn't be there. No decorative borders. No persistent status bars for information that rarely changes. No color for color's sake.

**Continuity over sessions.** The interface feels like one ongoing relationship, not a series of isolated transactions. There are no "new chat" buttons, no session labels, no conversation management. You open Virgil. You talk. You close it. You open it again later. It's the same conversation because it's always been the same conversation.

---

## The Three Modes

The TUI operates in three modes. Each serves a different context but connects to the same server and accesses the same memory.

### Interactive Mode

The default. A rich terminal interface for focused sessions. This is where you iterate on specs, monitor pipelines, refine drafts, and have extended conversations.

```
virgil
```

Entering `virgil` with no arguments launches the interactive TUI. If the server isn't running, the CLI starts it silently before connecting. The user never manages server lifecycle.

### One-Shot Mode

A single query, answered in the terminal, then Virgil exits. For quick lookups and commands that don't need a persistent session.

```
virgil what's on my calendar today
virgil remember that the deploy key is in 1password
virgil how's the builder doing this week
```

The query is sent to the server, the response is printed to stdout, and the process exits. No TUI is drawn. No interactive session is created. The response is plain text, formatted for the terminal width.

One-shot mode respects the same routing, planning, and execution as interactive mode. The only difference is the rendering surface — stdout instead of a TUI frame.

If a one-shot query triggers a long-running pipeline, the CLI prints an acknowledgment and exits. The pipeline runs on the server. The next time the user opens interactive mode, the result is waiting in the stream.

```
$ virgil add OAuth login to Keep
▸ Starting dev-feature pipeline. Check back in interactive mode for progress.
```

### Pipe Mode

Virgil participates in Unix pipelines. It reads from stdin, processes the input as a signal with the piped content as context, and writes to stdout.

```
cat spec.md | virgil summarize
git diff HEAD~3 | virgil write a pr description
cat error.log | virgil what's going wrong here
```

Pipe mode is detected automatically when stdin is not a terminal (i.e., when input is piped). The piped content becomes the envelope's `content` field. The words after `virgil` become the signal. Routing, planning, and execution proceed normally.

Output is plain text to stdout. Errors go to stderr. Exit codes follow convention: 0 for success, 1 for errors.

Pipe mode composes with other Unix tools:

```
virgil what's on my calendar today | grep -i standup
cat notes/*.md | virgil draft a blog post | pbcopy
```

---

## Visual Structure

### Interactive Layout

The interactive TUI has two regions: the **stream** and the **panel**. Only the stream is visible by default. The panel appears when there's something worth showing in it.

```
┌─────────────────────────────────────────────────────────────────────┐
│                                                                     │
│  You have three meetings today. First one's at 10 — standup         │
│  with the platform team. Then a design review at 1 and your         │
│  1:1 with Sarah at 3.                                               │
│                                                                     │
│  ▸ dev-feature: building...                                         │
│                                                                     │
│  Done — reminder set for 3:45 PM.                                   │
│                                                                     │
│  ▸ dev-feature: verify passed, publishing PR...                     │
│                                                                     │
│  dev-feature complete. PR #47 ready for review.                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│                                                                     │
│─────────────────────────────────────────────────────────────────────│
│ ❯                                                                   │
└─────────────────────────────────────────────────────────────────────┘
```

When the panel is open (toggled by the user, or auto-opened for a pipeline), the screen splits:

```
┌──────────────────────────────────────────┬──────────────────────────┐
│                                          │                          │
│  You have three meetings today.          │  dev-feature             │
│  First one's at 10 — standup with the    │  OAuth login → Keep      │
│  platform team.                          │                          │
│                                          │  ✓ spec         1.2s     │
│  ▸ dev-feature: verify passed            │  ✓ prepare      3.4s     │
│                                          │  ◉ build-verify          │
│  Done — reminder set for 3:45 PM.        │    ▸ build      12.1s    │
│                                          │    ✓ verify     pass     │
│  ▸ dev-feature: publishing...            │  ○ publish               │
│                                          │  ○ review                │
│                                          │                          │
│                                          │                          │
│                                          │                          │
│                                          │                          │
│                                          │                          │
│                                          │                          │
│──────────────────────────────────────────│                          │
│ ❯                                        │                          │
└──────────────────────────────────────────┴──────────────────────────┘
```

The panel takes roughly one-third of the terminal width. The stream compresses but remains the primary interaction surface. The input line spans only the stream region.

### The Stream

The stream is the soul of the interface. It is one continuous, scrollable flow of interaction. Everything appears here: your input, Virgil's responses, pipeline notifications, errors, ambient alerts.

The stream has no avatars, no usernames, no timestamps on individual messages, no message bubbles, no horizontal rules between exchanges. It is a conversation, not a chat log. Exchanges are separated by whitespace alone.

**Your input** appears exactly as you typed it, flush left, preceded by the prompt character.

**Virgil's responses** appear as plain text, indented slightly or differentiated by a subtle color shift — enough to distinguish speaker, not enough to create visual noise. Responses are not boxed, bordered, or decorated.

**Pipeline notifications** are single lines, dimmed relative to conversational text, prefixed with `▸`. They interleave naturally in the stream. They never push conversation off-screen — if a response and a notification arrive simultaneously, the response takes priority and the notification queues below it.

**Streaming responses** appear character-by-character or chunk-by-chunk as they arrive from a non-deterministic pipe. No typing indicator, no loading animation. The text simply appears, the way it does when someone is writing to you in real time. The cursor in the input line remains active during streaming — you can type your next message before the current response finishes.

### The Input Line

The input line sits at the bottom of the stream region. It is a single-line text field that expands to multiline when the content exceeds one line (up to a configurable maximum, default 6 lines). It auto-collapses after submission.

```
❯ draft a blog post based on my recent notes about church tech
```

The prompt character is `❯` — clean, minimal, directional. It communicates "your turn" without the noise of a full shell prompt. No path, no hostname, no git branch. This isn't a shell. It's a conversation.

**Keybindings on the input line:**

| Key           | Action                                             |
| ------------- | -------------------------------------------------- |
| `Enter`       | Submit the current input                           |
| `Shift+Enter` | Newline (multiline input)                          |
| `Up`          | Recall previous input (history)                    |
| `Down`        | Recall next input (history)                        |
| `Ctrl+C`      | Clear current input, or exit if empty              |
| `Ctrl+D`      | Exit Virgil                                        |
| `Esc`         | Clear current input                                |
| `Tab`         | Autocomplete pipe names, flags, and common phrases |

Input history is stored per-session in memory. It is not persisted across sessions — Virgil's memory system handles long-term recall, not a shell history file.

### The Panel

The panel provides depth without disrupting the conversation. It is a secondary viewport that shows the internal state of pipelines, expanded content, or structured data that doesn't belong in the conversational stream.

**Panel content types:**

**Pipeline detail** — the step-by-step progress of a running or completed pipeline. Each step shows its name, status, duration, and can be expanded to show the envelope content.

```
dev-feature
OAuth login → Keep

✓ spec             1.2s
✓ prepare           3.4s
  ├ worktree.create  0.3s
  ├ codebase.study   3.1s  (builder)
  └ codebase.study   3.4s  (reviewer)
◉ build-verify
  ▸ iteration 2
    ▸ build         12.1s
    ✓ verify        pass
○ publish
○ review
```

**Expanded content** — when a response in the stream is long (a full draft, a detailed research summary, a code review), the stream shows a truncated version and the panel shows the full content. The user can read, scroll, copy, or dismiss without losing their place in the stream.

**Structured data** — tables, lists, or hierarchical data that benefits from a fixed-width layout. Calendar events, search results, metrics summaries.

**Panel controls:**

| Key        | Action                                                      |
| ---------- | ----------------------------------------------------------- |
| `Tab`      | Toggle panel open/closed                                    |
| `Ctrl+P`   | Toggle panel (alternative)                                  |
| `Ctrl+J/K` | Scroll panel content                                        |
| `1-9`      | Switch between active pipelines (when multiple are running) |

The panel closes automatically when its content is no longer relevant (pipeline completes and user moves on, expanded content is dismissed). It does not persist empty.

---

## Typography and Color

### Color Palette

The TUI uses a restrained palette that works on both dark and light terminal backgrounds. Colors serve function, not decoration.

**Text hierarchy** (dark terminal):

| Element                 | Color                    | Purpose                                            |
| ----------------------- | ------------------------ | -------------------------------------------------- |
| Your input              | White / terminal default | Full brightness — you are the primary actor        |
| Virgil's responses      | Soft white or light gray | Slightly receded — the guide speaks calmly         |
| Pipeline notifications  | Dim gray                 | Background awareness — not competing for attention |
| Errors                  | Muted red                | Alert without alarm                                |
| Success markers         | Muted green              | Confirmation without celebration                   |
| Active/in-progress      | Muted blue or cyan       | Motion, activity                                   |
| Timestamps (when shown) | Dim gray                 | Metadata, not content                              |

**On light terminals**, the palette inverts naturally — dark text on light backgrounds, with the same relative hierarchy. The TUI should detect terminal background and adjust, or provide a `theme` configuration.

Colors are never used as the sole carrier of meaning. Status is always communicated through both symbol and color: `✓` for complete, `✗` for failure, `◉` for active, `○` for pending. This ensures readability in monochrome terminals and for users with color vision differences.

### Symbols

A small, consistent symbol vocabulary replaces verbose status labels.

| Symbol | Meaning                                      |
| ------ | -------------------------------------------- |
| `❯`    | Input prompt — your turn                     |
| `▸`    | Pipeline notification / in-progress sub-step |
| `✓`    | Complete / success                           |
| `✗`    | Failed / error                               |
| `◉`    | Active — currently executing                 |
| `○`    | Pending — not yet started                    |
| `⟳`    | Retrying (loop iteration)                    |
| `↺`    | Cycling (cycle pass)                         |
| `…`    | Truncated — more content available in panel  |

No emoji. No nerd font glyphs. No box-drawing characters beyond the minimal frame. The symbols above are available in virtually every modern terminal font. If a terminal can't render them, the TUI falls back to ASCII equivalents: `>`, `*`, `[ok]`, `[err]`, `(*)`, `( )`, etc.

### Text Formatting

Virgil's responses use minimal formatting in the stream. Bold is available for emphasis. Code is shown in the terminal's monospace font (which is already the ambient font — so code blocks are distinguished by indentation and a subtle background shift, not by a font change).

Markdown-style formatting in responses is rendered:

| Markdown     | Rendered as                                                                         |
| ------------ | ----------------------------------------------------------------------------------- |
| `**bold**`   | Bold (terminal bold attribute)                                                      |
| `` `code` `` | Dim background or alternate color                                                   |
| Code blocks  | Indented, with language-appropriate syntax highlighting                             |
| Lists        | Indented with `·` (middle dot) for bullets — no heavy bullet characters             |
| Headings     | Bold, with a blank line above — no `#` characters shown                             |
| Links        | Shown as text, with URL available via panel or `Ctrl+click` if terminal supports it |

Tables, images, and other rich content that doesn't render well in a terminal are shown as a summary in the stream with full content available in the panel.

---

## Interaction Patterns

### Conversation Flow

The default interaction is conversational. You type, Virgil responds, the stream advances. There is no mode switching, no slash-command prefix required, no need to specify that you're "chatting" versus "commanding."

```
❯ what's on my calendar today

  You have three meetings. Standup at 10, design review at 1,
  and your 1:1 with Sarah at 3.

❯ anything between standup and the design review?

  You're clear from 10:30 to 1. That's two and a half hours.

❯ draft a blog post based on my recent notes about church tech

  ▸ Retrieving recent notes...

  Here's a draft:

  ...
```

Virgil never asks "did you mean X?" unless genuine ambiguity exists that would produce meaningfully different outcomes. If the intent is 80% clear, Virgil acts on the 80% and lets the user correct. This is the guide philosophy — Virgil interprets, doesn't interrogate.

### Refinement

Refinement signals ("make it shorter", "more casual", "add a section about costs") are the most common follow-up. The TUI doesn't need special UI for refinement — it's just another message in the stream. But the TUI does need to handle the visual update gracefully.

When Virgil produces a revised version of content that was already displayed in the stream, the new version replaces the old version in the panel. The stream shows a brief note:

```
❯ make it shorter

  ▸ Revised — check the panel for the updated draft.
```

If the panel is closed, the revision appears in the stream as a new response. If the panel is open and already showing the draft, the content updates in place. The user sees the revision without losing the conversation context.

### Long-Running Pipelines

When a pipeline takes more than a few seconds, the TUI shows an initial acknowledgment in the stream and updates with status lines as steps complete.

```
❯ add OAuth login to Keep

  ▸ Starting dev-feature pipeline.

❯ remind me to review PR 47 after my last meeting

  Done — reminder set for 3:30 PM.

  ▸ dev-feature: spec complete
  ▸ dev-feature: preparing (3 parallel branches)
  ▸ dev-feature: building...
  ▸ dev-feature: verify failed, fixing (attempt 2)...
  ▸ dev-feature: verify passed, publishing PR

  dev-feature complete. PR #47 ready for review.
```

The key behavior: the user can keep talking while pipelines run. The stream interleaves pipeline status with conversation naturally. Pipeline notifications never interrupt a response that's currently streaming — they queue and appear after the response completes.

When multiple pipelines are running, each notification is prefixed with the pipeline name. The panel shows a tab strip (1, 2, 3...) to switch between active pipeline details.

### Interruption and Cancellation

The user can interrupt a running operation.

| Situation                         | Key      | Behavior                                           |
| --------------------------------- | -------- | -------------------------------------------------- |
| Streaming response in progress    | `Esc`    | Stop the stream. Partial response remains visible. |
| Pipeline running                  | `Ctrl+X` | Cancel pipeline. Confirm if destructive.           |
| AI model thinking (no output yet) | `Esc`    | Cancel the invocation.                             |

Cancellation is not destructive to state. A cancelled pipeline's partial results are still available. A cancelled draft's partial content is still in the stream. Nothing is lost — the operation just stops advancing.

### Errors

Errors appear in the stream as normal responses, not as modal dialogs or banner notifications. An error is just Virgil telling you something went wrong.

```
❯ check my calendar

  I couldn't reach the calendar API — looks like the credentials
  have expired. You can re-authenticate with `virgil auth calendar`.
```

The error is conversational. It explains what happened and what the user can do about it. Technical details (HTTP status codes, stack traces) are available in the panel or in logs — not in the stream.

Fatal errors that prevent Virgil from operating at all (server unreachable, database corrupt) are shown on the input line as a status indicator and briefly in the stream:

```
  ✗ Server connection lost. Reconnecting...
```

---

## Commands

Most interaction is natural language. But some operations are better expressed as explicit commands — configuration changes, system operations, things that aren't signals to be routed but direct instructions to the TUI or server.

Commands start with `:` (colon). The colon prefix is intentional — it's the command leader from vi/vim, familiar to terminal users, and clearly distinguishes commands from natural language without the heaviness of a `/` slash-command system.

```
:panel                  Toggle the detail panel
:panel close            Close the panel
:theme dark|light       Switch theme
:log level [pipe]       Set log verbosity (global or per-pipe)
:pipes                  List available pipes and pipelines
:status                 Show server status, active pipelines
:config                 Open configuration in $EDITOR
:auth <service>         Re-authenticate a service
:clear                  Clear the stream (does not affect memory)
:quit                   Exit (:q also works)
```

Commands are not routed through the signal pipeline. They are handled directly by the TUI or forwarded to a server management endpoint. They do not appear as conversational exchanges in the stream — the command executes and the result, if any, appears inline.

Bare `:` with no command opens a command palette — a filterable list of available commands. This serves as discoverability for users who know commands exist but can't remember the exact syntax.

---

## Autocomplete

The input line supports context-aware autocomplete, triggered by `Tab`.

**Pipe and pipeline names** — typing the first few characters of a pipe or pipeline name and pressing Tab completes it. `dra` → `draft`, `dev-f` → `dev-feature`.

**Flags** — after a pipe name, Tab suggests available flags. `draft --` → `--type`, `--tone`, `--length`.

**Flag values** — after a flag, Tab suggests allowed values. `draft --type=` → `blog`, `email`, `pr`, `memo`.

**Commands** — after `:`, Tab completes command names. `:pa` → `:panel`, `:pi` → `:pipes`.

**Recent topics** — for natural language input, Tab can suggest recent topics from memory. This is a lighter touch — it suggests, not completes. A ghost-text preview appears that the user can accept with `Tab` or ignore by continuing to type.

Autocomplete is rendered as ghost text (dimmed, inline) rather than a dropdown menu. One suggestion at a time. Tab cycles through alternatives. This keeps the input line clean and avoids the cognitive overhead of scanning a menu.

---

## Responsive Layout

The TUI adapts to terminal dimensions.

**Full width (120+ columns):** Stream and panel side by side. Stream gets approximately two-thirds. Panel gets one-third.

**Medium width (80–119 columns):** Stream and panel side by side, but panel narrows to minimum useful width (~30 columns). Below minimum, panel becomes an overlay.

**Narrow width (< 80 columns):** Panel is always an overlay. When opened, it replaces the stream temporarily. The input line remains visible at the bottom.

**Height:** The stream takes all available height minus the input line (1-6 lines depending on input). The stream scrolls. Content that would overflow is accessible by scrolling up. The most recent exchange is always visible.

**Minimum viable terminal:** 60 columns × 15 rows. Below this, the TUI warns and suggests one-shot mode.

---

## Accessibility

The TUI respects terminal accessibility conventions:

**Screen reader compatibility.** Output is structured semantic text, not visual-only formatting. Status changes are announced through standard terminal output, not through cursor position tricks.

**No animation dependency.** No spinners, progress bars, or animated transitions carry meaning. All status is communicated through persistent text and symbols. The `▸` prefix on a pipeline notification is a static character, not a spinning indicator.

**Keyboard-only operation.** Every function is accessible via keyboard. No mouse interaction is required. Mouse scrolling in the stream and panel is supported as a convenience, not a requirement.

**Color is redundant.** As noted in the color section, every status communicated by color is also communicated by symbol. The TUI is fully usable in a monochrome terminal.

**Terminal bell.** Virgil never rings the terminal bell. Ambient signals that need attention are shown in the stream. If the user wants audible notifications, they can configure their terminal or OS to watch for specific output patterns — Virgil doesn't impose sound.

---

## Configuration

TUI-specific configuration lives in the user's config file (`~/.config/virgil/tui.yaml`). It is separate from the server configuration (`virgil.yaml`) because the TUI is a client — its preferences are personal, not system-level.

```yaml
# ~/.config/virgil/tui.yaml

theme: auto # auto | dark | light (auto detects terminal background)
panel_position: right # right | bottom
panel_width: 0.33 # fraction of terminal width (when side-by-side)

input:
  max_lines: 6 # maximum lines for multiline input expansion
  history_size: 100 # number of inputs to recall with Up/Down (per session)
  ghost_suggest: true # show autocomplete ghost text

stream:
  scroll_margin: 3 # lines of context kept above the current view when auto-scrolling
  max_buffer: 5000 # maximum lines retained in the stream buffer

notifications:
  pipeline_updates: true # show ▸ pipeline status lines in stream
  ambient_signals: true # show ambient signal responses in stream
  bell: false # never true — Virgil doesn't ring the bell

keybindings:
  panel_toggle: tab
  panel_toggle_alt: ctrl+p
  panel_scroll_down: ctrl+j
  panel_scroll_up: ctrl+k
  cancel_stream: escape
  cancel_pipeline: ctrl+x
  submit: enter
  newline: shift+enter
  exit: ctrl+d
  clear_input: escape
  command_palette: ":"
```

All keybindings are remappable. The defaults are designed for comfort across macOS and Linux terminals without conflicting with common terminal emulator bindings.

---

## Server Connection

The TUI connects to the Virgil server over a local Unix socket (default) or TCP (when configured for remote access). Connection management is invisible to the user.

**Startup sequence:**

1. TUI checks for a running server (pidfile or socket probe).
2. If no server is found, TUI starts the server as a background process.
3. TUI connects to the server.
4. If connection fails after server start, TUI retries for up to 3 seconds, then shows an error.

**Reconnection:** If the connection drops during a session (server crash, restart), the TUI shows a brief status line and attempts reconnection automatically. No user action required. If reconnection succeeds, the session continues. Pipeline state is recovered from the server — any pipelines that were running are queried for current status.

**Server shutdown:** When the last client disconnects and no pipelines are running, the server remains active for a configurable idle timeout (default 10 minutes), then shuts down. This avoids restart latency for quick successive sessions while not leaving a permanent daemon.

---

## Progressive Disclosure

The TUI is built on layers of progressive disclosure. The surface layer is simple. Depth is available for those who want it.

**Layer 0 — Conversation.** Type and talk. This is the only layer most interactions need. No commands, no panels, no configuration.

**Layer 1 — Pipeline awareness.** Status lines in the stream. The panel for detail. Enough to know what's happening and intervene if needed.

**Layer 2 — Inspection.** Commands like `:pipes`, `:status`, `:log debug draft`. Expanding pipeline steps to see envelopes. Viewing metrics summaries. For debugging, optimization, and understanding.

**Layer 3 — Configuration.** `tui.yaml` for the client, `virgil.yaml` for the server, `pipe.yaml` for individual pipes. Direct editing of the system's behavior. For power users and system administrators.

Each layer is invisible until you need it. The TUI never shows Layer 2 or 3 affordances to a user operating at Layer 0. No gear icons, no hamburger menus, no "advanced options" toggles. The layers are there. You find them by asking.

---

## What the TUI Does Not Do

Listing what the TUI deliberately excludes is as important as defining what it includes.

**No chat history management.** There are no conversation lists, no "new chat" button, no thread labels. The stream is one continuous flow. Memory provides continuity. If the user wants to recall something from a past session, they ask Virgil.

**No notification badges or counts.** Pipeline status appears in the stream as it happens. There is no persistent unread count, no badge on a tab, no red dot anywhere.

**No mouse-first interactions.** Everything works with the keyboard. Mouse support (scrolling, clicking links) is a convenience layer, not a requirement. No drag-and-drop, no right-click menus, no hover tooltips.

**No theming beyond dark/light.** Two themes. Both restrained. No custom color schemes, no accent colors, no font size options (the terminal controls font). The TUI's visual identity is its restraint.

**No welcome screen.** Opening Virgil shows the input prompt and the last few lines of the stream (if resuming a recent session) or an empty stream (if starting fresh). No splash screen, no tip of the day, no onboarding wizard. The interface teaches through use, not through instruction.

**No loading screens.** Nothing blocks the input line. Even when Virgil is processing, you can type. Even when the server is reconnecting, the input line is available (submissions queue until connection is restored). The TUI never tells you to wait. You tell it what to do; it works in the background.

---

## Implementation Notes

### bubbletea Architecture

The TUI is built with [bubbletea](https://github.com/charmbracelet/bubbletea), which provides the Elm-architecture foundation: Model → Update → View.

**Top-level model** holds the stream buffer, input state, panel state, connection state, and active pipeline trackers. Sub-models manage the input line (using [bubbles/textarea](https://github.com/charmbracelet/bubbles)), the stream viewport (using [bubbles/viewport](https://github.com/charmbracelet/bubbles)), and the panel viewport.

**Message types** map to the interaction patterns defined above:

| Message               | Source             | Effect                                                 |
| --------------------- | ------------------ | ------------------------------------------------------ |
| `ServerResponseMsg`   | Server connection  | Append to stream, update panel if pipeline             |
| `StreamChunkMsg`      | Streaming response | Append partial text to current response                |
| `PipelineUpdateMsg`   | Server push        | Update pipeline tracker, append notification to stream |
| `PipelineCompleteMsg` | Server push        | Finalize pipeline, append completion to stream         |
| `InputSubmitMsg`      | User keypress      | Send to server, append input to stream                 |
| `PanelToggleMsg`      | User keypress      | Show/hide panel, recalculate layout                    |
| `ResizeMsg`           | Terminal resize    | Recalculate all layout dimensions                      |
| `ConnectionLostMsg`   | Connection monitor | Show status, begin reconnection                        |
| `ErrorMsg`            | Any source         | Display error in stream or status line                 |

**Rendering** uses [lipgloss](https://github.com/charmbracelet/lipgloss) for styling. Styles are defined in a central theme struct that switches between dark and light palettes. No inline style definitions — all visual attributes flow from the theme.

### Text Rendering

Response text from the server may contain markdown formatting. The TUI renders markdown using [glamour](https://github.com/charmbracelet/glamour) with a custom style that matches the TUI's palette. Glamour's default styles are too heavy — the custom style strips unnecessary decoration and aligns with the "earned attention" principle.

Code blocks use syntax highlighting via glamour's built-in chroma integration. Highlighting is subtle — keyword emphasis and string differentiation, not a full rainbow.

### Server Protocol

The TUI communicates with the server over a bidirectional stream. The connection carries:

**Client → Server:**

- Signal submission (user input)
- Pipeline control (cancel, pause)
- Status queries (pipe list, server health)

**Server → Client:**

- Response chunks (streaming text)
- Response complete (final envelope)
- Pipeline updates (step status changes)
- Pipeline complete (final result)
- Ambient notifications (scheduled alerts, self-healing summaries)

The wire protocol is JSON lines over the socket connection. Each message is a single JSON object followed by a newline. This is simple, debuggable, and works with standard Unix tools for inspection.

---

## Checklist

Before shipping the TUI, verify:

```
Modes
  ☐  Interactive mode launches and connects to server
  ☐  One-shot mode prints response and exits
  ☐  Pipe mode reads stdin and writes to stdout
  ☐  Server auto-starts if not running (all modes)

Stream
  ☐  Input appears immediately on submission
  ☐  Responses stream character-by-character
  ☐  Pipeline notifications interleave without disrupting responses
  ☐  Scrollback works (scroll up to see history)
  ☐  Stream buffer respects max_buffer limit

Panel
  ☐  Toggle opens/closes cleanly
  ☐  Pipeline steps update in real time
  ☐  Parallel branches shown with tree structure
  ☐  Loop iterations shown with retry count
  ☐  Multiple active pipelines switchable
  ☐  Panel closes when content is no longer relevant

Input
  ☐  Multiline expansion works (Shift+Enter)
  ☐  Input history works (Up/Down)
  ☐  Autocomplete works for pipe names, flags, flag values
  ☐  Input is never blocked (can type during streaming, pipeline, reconnection)
  ☐  All keybindings work and are remappable

Layout
  ☐  Full width layout (120+ cols) renders correctly
  ☐  Medium width layout (80-119 cols) renders correctly
  ☐  Narrow width layout (< 80 cols) renders correctly
  ☐  Terminal resize is handled without artifacts
  ☐  Minimum terminal size warning shown when too small

Visual
  ☐  Dark theme renders correctly
  ☐  Light theme renders correctly
  ☐  All status symbols render (with ASCII fallback)
  ☐  Colors are never the sole carrier of meaning
  ☐  No animations carry meaning

Connection
  ☐  Auto-reconnection works after server restart
  ☐  Queued inputs are sent after reconnection
  ☐  Pipeline state is recovered after reconnection
  ☐  Server idle timeout works correctly

Commands
  ☐  : prefix triggers command mode
  ☐  Command palette (bare :) shows available commands
  ☐  All documented commands work
  ☐  Unknown commands produce clear errors
```
