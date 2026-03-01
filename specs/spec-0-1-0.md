# Virgil v0.1.0 Specification

**Author:** Justin  
**Date:** 2026-02-28  
**Status:** Final Draft  
**Codename:** _Foundation_

---

## 1. Vision

Virgil is a personal operating system for human intent. It is the one tool you open to do anything — research, development, communication, scheduling, thinking. Every time you leave Virgil to accomplish something is a signal that Virgil needs to grow.

Virgil is not an execution engine. Claude Code and OpenCode already do that well. Virgil is the **interface layer** — the adaptive TUI, the voice I/O, the pipe composability, the contextual presentation, and the routing intelligence that decides whether a task needs AI at all or can be handled with a direct CLI tool call.

### Principles

1. **Don't reinvent the wheel.** Claude Code and OpenCode handle AI-assisted execution. Skills are `.md` files in the standard format those tools already support. Virgil wraps them, it doesn't replace them.
2. **Deterministic when possible.** If a task can be handled by calling `gcalcli`, `curl`, or `jq` directly, don't route it through the AI engine. AI is for judgment. CLI tools are for commands.
3. **Unix native.** Virgil reads stdin, writes stdout, and uses a plain-text envelope format. Standard CLI tools work on Virgil's output. Virgil works on standard CLI output.
4. **Proportional complexity.** The interface is only as complex as the current task. Simple question, clean chat. Parallel dev workload, the full process manager appears.

---

## 2. Scope

### What Virgil Is

- An adaptive TUI that presents AI and tool results with contextual panels
- A voice I/O layer (output in v0.1.0, input in v0.2.0)
- A pipe/envelope system that makes workflows composable with Unix tools
- A router that triages between direct CLI tools and the AI execution engine
- A process manager for parallel AI tasks (inherited from agtop)

### What Virgil Is Not

- Not an AI execution engine (Claude Code does this)
- Not a skill runtime (skills are standard `.md` files for Claude Code/OpenCode)
- Not a framework (it's a tool)

### In Scope for v0.1.0

- Core runtime: router, envelope format, mode detection
- Three execution modes: interactive TUI, one-shot, pipe
- Adaptive TUI with contextual panels (bubbletea/lipgloss)
- Direct tool integrations: Google Calendar, local memory store
- AI execution via Claude Code subprocess for soft skills
- Voice output (TTS)
- Command palette
- Process manager (agtop lineage) for parallel AI tasks
- Global configuration at `~/.virgil/`

### Out of Scope

- Speech input (v0.2.0)
- Slack, email integrations (v0.2.0)
- Research skill (v0.2.0)
- Per-project config overrides (v0.3.0)
- Planning Center / Rock ChMS skills (v0.3.0)
- Multi-model routing (v0.4.0)
- Remote access / mobile (v1.0.0)

---

## 3. Architecture

### 3.1 System Diagram

```
┌──────────────────────────────────────────────────────┐
│                     VIRGIL                           │
│                                                      │
│  ┌─────────┐  ┌───────────┐  ┌────────┐  ┌───────┐ │
│  │  TUI    │  │  Envelope │  │ Voice  │  │ Pipe  │ │
│  │  Engine │  │  Layer    │  │ (TTS)  │  │ I/O   │ │
│  └────┬────┘  └─────┬─────┘  └───┬────┘  └───┬───┘ │
│       │             │             │            │     │
│       └─────────────┼─────────────┼────────────┘     │
│                     │             │                   │
│              ┌──────▼──────┐      │                   │
│              │   Router    │      │                   │
│              └──────┬──────┘      │                   │
│                     │             │                   │
│         ┌───────────┼────────┐    │                   │
│         ▼                    ▼    │                   │
│  ┌──────────────┐  ┌─────────────▼──┐                │
│  │ Direct Tools │  │ Claude Code /  │                │
│  │ (gcalcli,    │  │ OpenCode       │                │
│  │  curl, jq,   │  │ (subprocess)   │                │
│  │  filesystem) │  │                │                │
│  └──────┬───────┘  │ ┌────────────┐ │                │
│         │          │ │ Skills     │ │                │
│         │          │ │ (.md files)│ │                │
│         │          │ └────────────┘ │                │
│         │          └───────┬────────┘                │
│         │                  │                         │
│         └──────────┬───────┘                         │
│                    ▼                                 │
│              ┌───────────┐                           │
│              │ Envelope  │ → stdout / TUI / voice    │
│              └───────────┘                           │
└──────────────────────────────────────────────────────┘
```

### 3.2 Component Inventory

| Component        | Responsibility                                                                                                      |
| ---------------- | ------------------------------------------------------------------------------------------------------------------- |
| Router           | Decides: direct tool call or AI engine? Dispatches accordingly.                                                     |
| TUI Engine       | Adaptive bubbletea interface. Conversation stream, contextual panels, status bar, command palette, process manager. |
| Envelope Layer   | Serializes/deserializes the universal message format. Handles pipe I/O.                                             |
| Voice            | TTS output via edge-tts or ElevenLabs. Async, non-blocking.                                                         |
| Direct Tools     | Thin Go wrappers around CLI tools (gcalcli, curl, filesystem ops). No AI involved.                                  |
| AI Engine Bridge | Spawns `claude -p` as subprocess. Prepends skill context. Captures output. Wraps in envelope.                       |
| Process Manager  | Manages parallel AI tasks. Inherited from agtop. Shows status, progress, diffs in TUI.                              |

### 3.3 The Router Decision

The router is the only piece of intelligence Virgil owns. It makes one decision per user input:

```
User input
    │
    ▼
Can this be handled by a direct tool call
with no judgment required?
    │
    ├── YES → Call the tool directly (fast, deterministic)
    │         Examples: "what's on my calendar today"
    │                   "remember this" (with explicit content)
    │                   "list my memories tagged 'keep'"
    │
    └── NO  → Hand to Claude Code with appropriate skill context
              Examples: "review this code"
                        "research ECS architecture patterns"
                        "draft an email about the Keep launch"
                        "help me think through the plugin interface"
```

The router uses a lightweight intent classifier. This can be:

- **Rule-based first:** Pattern matching on keywords and structure. "what's on my calendar" always hits the calendar tool. "remember X" always hits memory. No AI call needed for obvious routes.
- **AI fallback:** If pattern matching is ambiguous, a fast Claude Haiku call classifies the intent. This should be rare for common tasks.

The goal is that the most frequent interactions (calendar, memory, simple conversation) never touch the AI engine for routing. They go direct.

---

## 4. Envelope Format

The envelope is Virgil's universal message format. It flows between Virgil, skills, and standard Unix tools.

### 4.1 Format

```
---
virgil-type: calendar_events
virgil-source: calendar
virgil-timestamp: 2026-02-28T14:30:00Z
virgil-metadata: {"count":3,"events":[...]}
---
Sat Mar 01  9:00 AM — T-ball practice @ Cherokee Fields
Sat Mar 01  2:00 PM — Keep planning session
Sun Mar 02  9:30 AM — Church — New Life Canton
```

### 4.2 Rules

1. Frontmatter is optional. If absent, content is treated as `type: text, source: stdin`.
2. Content is always human-readable plain text or markdown.
3. Metadata is single-line JSON for structured data that skills and the TUI can use.
4. A tool that doesn't understand the format just sees text.
5. `virgil-type` tells the TUI which panel renderer to use.

### 4.3 Stripping Frontmatter

For piping to tools that don't want it:

```bash
virgil calendar today | sed '1,/^---$/d' | sed '1,/^---$/d'
# or provide a built-in flag:
virgil calendar today --raw
```

---

## 5. Skills

Skills are **standard markdown files with YAML frontmatter**, identical to the format used by Claude Code and OpenCode. Virgil does not invent a new skill format.

### 5.1 Where Skills Live

```
~/.virgil/skills/          # Global skills
.virgil/skills/            # Per-project skills (v0.3.0)
```

Virgil reads skill manifests at startup to populate the router's knowledge of what's available. When the AI engine is invoked, the relevant skill file is passed as context.

### 5.2 Example Skill

```markdown
---
name: code-review
description: Review code for quality, bugs, and style issues
triggers:
  - review
  - code review
  - look at this code
  - check this for bugs
tools:
  - shell
  - file_read
---

When asked to review code:

1. Read the specified files or directory
2. Analyze for:
   - Logic errors and potential bugs
   - Performance issues
   - Style inconsistencies
   - Missing error handling
   - Security concerns
3. Format findings as a structured list with severity levels
4. Suggest specific fixes with code examples
5. Summarize the overall code health
```

When a user says "review the SMS categorizer," Virgil's router sees this matches the `code-review` skill triggers, spawns Claude Code with this skill loaded as context, and the AI engine does the work. Virgil captures the output, wraps it in an envelope, renders it in the TUI, and optionally speaks a voice summary.

### 5.3 Skill Discovery

The router builds its routing table from:

1. Skill frontmatter `triggers` fields (primary)
2. Skill `name` and `description` fields (fallback for AI-based routing)
3. Direct tool registrations (calendar, memory — these aren't skills, they're built-in tools)

---

## 6. Direct Tool Integrations

These are built into Virgil as Go code. They call external APIs or CLI tools directly. No AI involved in execution.

### 6.1 Calendar

**Backend:** Google Calendar API via OAuth 2.0  
**Panel type:** Table

| Route Pattern                                        | Action                   |
| ---------------------------------------------------- | ------------------------ |
| "calendar", "schedule", "what's on" + time reference | List events in range     |
| "calendar search" + keyword                          | Search events            |
| "today" / "tomorrow" (in calendar context)           | List events for that day |

**Setup:** OAuth credentials at `~/.virgil/credentials.json`. Token cached at `~/.virgil/tokens/calendar.json`. First use opens browser for consent.

**Envelope output:**

```
---
virgil-type: calendar_events
virgil-source: calendar
virgil-metadata: {"count":3,"events":[{"summary":"T-ball","start":"...","location":"..."},...]}}
---
Sat Mar 01  9:00 AM — T-ball practice @ Cherokee Fields
...
```

### 6.2 Memory

**Backend:** Local filesystem at `~/.virgil/memory/`  
**Panel type:** List

| Route Pattern                                | Action |
| -------------------------------------------- | ------ |
| "remember" + content (or piped stdin)        | Store  |
| "recall" / "what did I save about" + keyword | Search |
| "list memories" / "show memories"            | List   |
| "forget" + identifier                        | Delete |

**Storage:** Individual markdown files with YAML frontmatter. One file per entry.

```
~/.virgil/memory/
├── 20260228-143000-keep-launch-target.md
├── 20260225-091500-pi5-camera-research.md
└── ...
```

Each file:

```markdown
---
title: Keep launch target is March 15
tag: keep
timestamp: 2026-02-28T14:30:00Z
source: user
---

Pilot with New Life Canton. Need SMS categorization
and analytics dashboards ready.
```

**Search:** Substring match across content and frontmatter. Simple, fast, no dependencies.

**Scaling path (documented, not implemented in v0.1.0):**

1. **v0.1.0:** Substring search. Works for hundreds of entries.
2. **Future:** SQLite FTS5 when substring gets slow (~1000+ entries).
3. **Future:** Embeddings + vector search (RAG) when semantic recall matters — "find notes related to church retention" should match entries that don't contain those exact words.

**Pipe support:**

```bash
echo "Keep launch is March 15" | virgil remember --tag keep
virgil recall "Keep" | grep "launch"
cat meeting-notes.md | virgil remember --tag meetings
```

---

## 7. AI Engine Bridge

When the router decides a task needs AI-assisted execution, Virgil spawns Claude Code as a subprocess via `claude -p`.

### 7.1 Invocation

```go
func (bridge *AIBridge) Execute(task AITask) (*Envelope, error) {
    prompt := task.Query
    if task.SkillFile != "" {
        skillContent, _ := os.ReadFile(task.SkillFile)
        prompt = string(skillContent) + "\n\n" + task.Query
    }

    cmd := exec.Command("claude", "-p", prompt)
    cmd.Dir = task.WorkDir

    // If pipe input exists, write to stdin
    if task.Input != nil {
        cmd.Stdin = strings.NewReader(task.Input.Content)
    }

    output, err := cmd.Output()
    // Wrap in envelope and return
}
```

That's the entire bridge. `claude -p <prompt>` runs non-interactively and writes to stdout. If a skill file matches the request, its content is prepended to the prompt as context. No API wrappers, no SDKs, no session management.

### 7.2 Process Management

For parallel AI tasks (the agtop use case):

- Each task runs as an independent `claude -p` subprocess.
- Virgil tracks PID, status, elapsed time, and output buffer per task.
- The TUI process panel renders live status for all active tasks.
- Tasks can be spawned explicitly ("run these three things in parallel") or by the router when it decomposes a multi-step request.

### 7.3 Streaming Output

Claude Code output streams to a **collapsible accordion** in the conversation pane. While the task is running, the user can watch intermediate output in real time. When the task completes, the accordion auto-collapses to a one-line summary. The user can expand it again to review the full output.

This is a reusable TUI component — any long-running task (AI or otherwise) gets an accordion.

### 7.4 Output Envelope

Claude Code output is unstructured text. Virgil wraps it:

```
---
virgil-type: ai_response
virgil-source: claude-code
virgil-timestamp: 2026-02-28T15:00:00Z
virgil-metadata: {"skill":"code-review","duration_ms":4200}
---
<raw Claude Code output>
```

The TUI renders `ai_response` type in the conversation stream as markdown. If the skill's frontmatter declared a specific output structure, a future version could parse and panel-ify it.

---

## 8. Three Execution Modes

### 8.1 Mode Detection

```go
func detectMode(args []string) Mode {
    if !term.IsTerminal(os.Stdin.Fd()) {
        return ModePipe
    }
    if len(args) > 0 {
        return ModeOneShot
    }
    return ModeInteractive
}
```

### 8.2 Pipe Mode

```bash
echo "idea for Keep onboarding" | virgil remember --tag keep
virgil recall "Keep" | grep "launch"
virgil calendar today | wc -l
cat notes.md | virgil summarize
```

- Reads envelope (or raw text) from stdin.
- Writes envelope to stdout.
- No TUI. No voice. No color (unless `--color=always`).
- Exit code reflects success/failure.

### 8.3 One-Shot Mode

```bash
virgil "what's on my calendar this weekend?"
virgil recall "ECS architecture"
```

- If stdout is TTY: rich output (panels, color, tables).
- If stdout is pipe: envelope format.
- Voice output if enabled and stdout is TTY.
- Exits after response.

### 8.4 Interactive Mode

```bash
virgil
```

- Full adaptive TUI.
- REPL loop with persistent command history.
- Contextual panels, voice, process manager.
- Slash commands, command palette.
- Exits on `/quit`, `Ctrl+C`, or `Ctrl+D`.

---

## 9. TUI Specification

### 9.1 Design Principle

The TUI is a **window manager**, not a fixed layout. Components appear and disappear based on the active task. Complexity is proportional to what's happening right now.

### 9.2 Layouts

**Conversation only (default):**

```
┌─────────────────────────────────────────────┐
│                                             │
│  Conversation Stream                        │
│  (full width, clean, focused)               │
│                                             │
├─────────────────────────────────────────────┤
│  virgil ❯ _                                 │
├─────────────────────────────────────────────┤
│  conversation · voice on                 PM │
└─────────────────────────────────────────────┘
```

**Conversation + context panel:**

```
┌───────────────────────┬─────────────────────┐
│                       │                     │
│  Conversation Stream  │  Context Panel      │
│  (50%)                │  (table, list, etc) │
│                       │                     │
├───────────────────────┴─────────────────────┤
│  virgil ❯ _                                 │
├─────────────────────────────────────────────┤
│  calendar · voice on · 2:30 PM              │
└─────────────────────────────────────────────┘
```

**Dev / parallel process mode:**

```
┌──────────┬──────────────┬───────────────────┐
│ Process  │ Conversation │  Diff / Output    │
│ List     │ (narrow)     │                   │
│          │              │                   │
│ Details  │              │                   │
├──────────┴──────────────┴───────────────────┤
│  virgil ❯ _                                 │
├─────────────────────────────────────────────┤
│  dev · 2/3 complete · voice off      2:32PM │
└─────────────────────────────────────────────┘
```

### 9.3 Panel Behavior

- Panels **slide in** from the right (~200ms ease-out).
- Conversation stream **contracts** to accommodate.
- `Esc` dismisses the active panel. Conversation expands back.
- New skill output **replaces** the current panel.
- Panel state is stacked — `Esc` pops to previous panel or no panel.
- Each panel scrolls independently from the conversation.

### 9.4 Panel Types

| Type    | Used By                  | Renders                             |
| ------- | ------------------------ | ----------------------------------- |
| Table   | Calendar, search results | Rows and columns with header        |
| List    | Memory, tasks            | Ordered entries with metadata       |
| Process | Parallel AI tasks        | Progress bars, status, branch names |
| Diff    | Dev work, code review    | Syntax-colored diff with +/- lines  |
| Log     | Long-running tasks       | Streaming text output               |
| None    | Conversation             | No panel, full-width chat           |

Skills declare their preferred panel type in frontmatter. Direct tools have panel types hardcoded.

### 9.5 Conversation Stream

- Newest at bottom. Auto-scrolls on new content.
- Manual scroll up pauses auto-scroll, shows "↓ new" indicator.
- User messages: `virgil ❯ <text>` in accent color.
- AI responses: streamed into a **collapsible accordion** while running, auto-collapses to one-line summary on completion. User can expand to review full output.
- System messages: dimmed.
- Debug messages: skill routing info, dimmed amber (when `/debug` is on).
- Thinking indicator: `⟳ invoking <skill>...` with pulse animation.
- The accordion is a reusable component — any long-running task gets one.

### 9.6 Input Area

- Single-line by default.
- `Shift+Enter` expands to multi-line (5-10 lines).
- `Enter` submits in single-line. `Ctrl+Enter` submits in multi-line.
- Up/down arrows for command history.
- Tab completion for `/commands`.

### 9.7 Status Bar

One line, three sections:

```
  <active_context> · <flags>          <tasks>                <time>
```

- Left: Active skill/tool name (colored), voice on/off, debug on/off.
- Center: Running background task count.
- Right: Notification count, current time.

### 9.8 Notification Bar

- Hidden when empty.
- Slides down from top on new notification.
- Auto-dismisses after 5 seconds.
- Types: success (green), info (blue), warning (amber), error (red).

### 9.9 Command Palette

- `Ctrl+K` to open.
- Fuzzy search across: skills, direct tools, recent queries, running tasks, settings.
- `Enter` selects. `Esc` dismisses.

### 9.10 Color System

| Domain       | Color   | Hex       |
| ------------ | ------- | --------- |
| Calendar     | Blue    | `#60a5fa` |
| Memory       | Purple  | `#c084fc` |
| Dev          | Amber   | `#f59e0b` |
| Research     | Green   | `#34d399` |
| Comms        | Teal    | `#2dd4bf` |
| Error        | Red     | `#f87171` |
| System       | Gray    | `#6b7280` |
| Conversation | Neutral | `#cbd5e1` |

Background: `#0b0d14`. Surface: `#141620`. Border: `#1e2130`.

Monospace font throughout. JetBrains Mono or system monospace.

---

## 10. Voice

### 10.1 Philosophy

Voice is an **ambient awareness channel**, not an information delivery mechanism. The TUI is for information. Voice is for attention. Its purpose is to alert the user that something happened worth looking at. If the user wants details, they look at the screen.

All voice output is **one sentence or shorter.** Never more.

### 10.2 Output (v0.1.0)

**Engine:** edge-tts (free, high quality) as default. ElevenLabs as optional upgrade.

**Flow:**

1. Direct tool or AI engine returns an envelope.
2. If voice is enabled, a fast Claude Haiku call generates a one-sentence alert.
3. TTS renders audio in a background goroutine.
4. Audio plays while TUI renders the full result.

**Good voice output:**

- "Three events this weekend."
- "Saved to memory under keep."
- "Code review complete, four issues found."
- "Build passed on the SMS branch."
- "Research finished."

**Bad voice output (too long, too detailed):**

- "You've got three things this weekend. Saturday morning is t-ball at 9 at Cherokee Fields, then a Keep planning session at 2."
- "I found four issues in the code review: a missing null check in the handler, two unused imports..."

**Voice generation prompt:**

```
Generate a one-sentence spoken alert (under 10 words if possible)
summarizing this result. Be terse. The user will read details on
screen — voice just tells them something happened.
```

### 10.3 Input (v0.2.0 — out of scope)

Future: Whisper.cpp for local speech-to-text, hotkey toggle, real-time transcription in input area.

---

## 11. Configuration

### 11.1 Directory Structure

```
~/.virgil/
├── config.yaml              # Global configuration
├── credentials.json         # Google OAuth credentials
├── tokens/
│   └── calendar.json        # Cached OAuth token
├── history                  # Command history
├── memory/                  # Memory storage
│   └── *.md
└── skills/                  # Skill files (.md)
    ├── code-review.md
    ├── research.md
    └── deploy.md
```

### 11.2 config.yaml

```yaml
# Virgil v0.1.0

# Identity
name: "Virgil"
user_name: "Justin"

# AI engine
engine: "claude" # Path to claude binary
# ANTHROPIC_API_KEY from env var

# Voice
voice:
  enabled: true
  engine: "edge-tts"
  voice_id: "en-US-GuyNeural"

# Router
router:
  # Intent classification: rules-first, AI fallback
  ai_fallback_model: "claude-haiku-4-5-20251001"

# TUI
tui:
  show_debug: false
  notification_timeout: 5

# Direct tools
tools:
  calendar:
    enabled: true
    default_calendar: "primary"
  memory:
    enabled: true
    search_limit: 20
```

---

## 12. Technology Stack

| Component | Technology                             | Rationale                                                |
| --------- | -------------------------------------- | -------------------------------------------------------- |
| Language  | Go                                     | Matches agtop. Single binary. Fast startup.              |
| TUI       | bubbletea + lipgloss + bubbles         | Same stack as lazygit, agtop.                            |
| AI engine | `claude -p` (subprocess)               | Already installed. Non-interactive mode. Don't reinvent. |
| Calendar  | Google Calendar API (Go client)        | Direct integration, no AI needed.                        |
| Memory    | Local filesystem (markdown)            | No dependencies. Human-readable. Git-friendly.           |
| TTS       | edge-tts (subprocess) / ElevenLabs API | Free tier, good quality.                                 |
| Config    | YAML                                   | Standard, supports comments.                             |
| Build     | goreleaser                             | Cross-platform binary builds.                            |

---

## 13. Project Structure

```
virgil/
├── cmd/
│   └── virgil/
│       └── main.go              # Entry point, mode detection
├── internal/
│   ├── router/
│   │   ├── router.go            # Route decision logic
│   │   ├── patterns.go          # Rule-based pattern matching
│   │   └── classify.go          # AI fallback classification (Haiku)
│   ├── envelope/
│   │   └── envelope.go          # Format, serialize, parse
│   ├── tools/
│   │   ├── calendar.go          # Google Calendar direct integration
│   │   ├── memory.go            # Filesystem memory store
│   │   └── tools.go             # Tool registry interface
│   ├── bridge/
│   │   ├── bridge.go            # Claude Code subprocess management
│   │   └── process.go           # Parallel task tracking
│   ├── tui/
│   │   ├── app.go               # Bubbletea app model
│   │   ├── conversation.go      # Chat stream component
│   │   ├── panel.go             # Context panel manager
│   │   ├── panels/
│   │   │   ├── table.go         # Table renderer (calendar, search)
│   │   │   ├── list.go          # List renderer (memory, tasks)
│   │   │   ├── process.go       # Process manager (dev mode)
│   │   │   ├── diff.go          # Diff viewer
│   │   │   └── log.go           # Log stream
│   │   ├── statusbar.go
│   │   ├── input.go
│   │   ├── palette.go           # Command palette
│   │   ├── notification.go
│   │   └── styles.go            # Lipgloss styles
│   └── voice/
│       └── tts.go               # TTS output, async playback
├── go.mod
├── go.sum
├── README.md
├── SPEC.md
└── Makefile
```

---

## 14. Acceptance Criteria

### Core

- [ ] `virgil` launches interactive TUI
- [ ] `virgil "query"` runs one-shot and exits
- [ ] `echo "text" | virgil remember --tag x` reads stdin, stores, writes envelope to stdout
- [ ] `virgil recall "topic" | grep "keyword"` works with standard Unix tools
- [ ] Envelope frontmatter is ignored by `grep`/`awk`/`sed` on the content portion
- [ ] Chaining works: `virgil recall "notes" | virgil summarize`

### Router

- [ ] "What's on my calendar today" routes to direct calendar tool (no AI engine call)
- [ ] "Remember this: X" routes to direct memory tool (no AI engine call)
- [ ] "Review this code" routes to Claude Code with code-review skill loaded
- [ ] "Help me think about X" routes to Claude Code for conversation
- [ ] Ambiguous inputs fall back to AI classification, then dispatch correctly

### Direct Tools

- [ ] Calendar lists events for natural language time ranges
- [ ] Calendar renders a table in the TUI context panel
- [ ] Memory stores from pipe input and from interactive input
- [ ] Memory recalls by keyword search
- [ ] Memory lists entries with tag filtering

### AI Engine Bridge

- [ ] `claude -p` subprocess spawns correctly with skill context prepended
- [ ] Output is captured and wrapped in envelope
- [ ] Parallel tasks display in process manager panel
- [ ] Subprocess failures produce clear error envelopes, not crashes

### TUI

- [ ] Full-width conversation when no panel is active
- [ ] Panel slides in when structured data is returned
- [ ] `Esc` dismisses panel, conversation expands
- [ ] Dev mode: three-pane layout with process list, conversation, diff
- [ ] AI output streams into collapsible accordion, auto-collapses on completion
- [ ] Accordion can be expanded to review full output
- [ ] Status bar shows active context, voice status, time
- [ ] Command palette opens with `Ctrl+K`, supports fuzzy search
- [ ] Thinking indicator during tool/AI execution
- [ ] Command history persists across sessions
- [ ] Independent scroll per pane

### Voice

- [ ] TTS speaks a one-sentence alert when a task completes
- [ ] Voice output is always under 10 words when possible
- [ ] Audio plays asynchronously (doesn't block TUI)
- [ ] `/voice` toggles on/off
- [ ] TUI renders data immediately; voice alert follows

### Quality

- [ ] Single binary, no runtime dependencies (except optional Claude Code)
- [ ] Startup < 200ms to interactive prompt
- [ ] Direct tool response < 500ms (network-dependent for calendar)
- [ ] Graceful degradation: if calendar not configured, other features work
- [ ] Network errors display as a simple error state in the status bar, not a crash or modal
- [ ] Config errors produce clear messages
- [ ] `Ctrl+C` exits cleanly from any state

---

## 15. Milestones

| Milestone             | Target | Deliverable                                                   |
| --------------------- | ------ | ------------------------------------------------------------- |
| M1: Skeleton          | Week 1 | Go project, bubbletea boots, basic REPL, mode detection       |
| M2: Envelope + Router | Week 1 | Envelope format, router with pattern matching, pipe I/O       |
| M3: Direct Tools      | Week 2 | Calendar integration, memory store, TUI table + list panels   |
| M4: AI Bridge         | Week 3 | Claude Code subprocess, skill loading, output capture         |
| M5: Process Manager   | Week 3 | Parallel task tracking, process panel (agtop port)            |
| M6: TUI Polish        | Week 4 | Adaptive layouts, transitions, command palette, notifications |
| M7: Voice             | Week 4 | TTS output, async playback, voice summaries                   |
| M8: Testing + Release | Week 5 | Acceptance criteria pass, README, binary builds               |

---

## 16. Resolved Decisions

| Question                 | Decision                                                                                                                                                                                                                                                                                                                                                                      |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Claude Code invocation   | `claude -p <prompt>`. Non-interactive subprocess. Skill content prepended to prompt.                                                                                                                                                                                                                                                                                          |
| Streaming AI output      | Stream into a collapsible accordion in the conversation pane. Auto-collapse on completion. Expand to review. Reusable component for any long-running task.                                                                                                                                                                                                                    |
| Memory search scaling    | v0.1.0: substring. Future: SQLite FTS5 at ~1000 entries. Later: RAG with embeddings for semantic recall. Each step only when the previous one demonstrably fails.                                                                                                                                                                                                             |
| Offline / network errors | Simple error state in the status bar. No special offline mode. Direct tools that don't need network (memory) continue working.                                                                                                                                                                                                                                                |
| Voice output length      | One sentence or shorter. Always. Voice is an ambient awareness alert, not information delivery. Under 10 words when possible.                                                                                                                                                                                                                                                 |
| Build vs. adopt PAI      | Build Virgil. PAI solves different problems (identity/goal systems, personality frameworks). It has no adaptive TUI, no pipe composability, no envelope format, and is TypeScript/Bun instead of Go. Steal ideas from PAI where useful: skill priority hierarchy (code → CLI → prompt → skill), memory tier architecture, hook patterns for notifications, security approach. |

---

## 17. Open Questions

1. **`claude -p` flags.** Does `claude -p` support passing a system prompt or skill context via a flag, or does it need to be prepended to the prompt string? Spike this in M4.

2. **Multi-account calendar.** v0.1.0 targets a single Google account. If both personal and Passion City calendars are needed, does the tool need a `--profile` flag? Defer unless it's immediately painful.

---

## 18. Influences

Systems studied during design. Take what's useful, leave the rest.

| System             | What to steal                                                                                                                   | What to skip                                                                                  |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| **PAI** (Miessler) | Skill priority hierarchy: code → CLI → prompt → skill. Memory tiers. Hook patterns for notifications. Security policy approach. | TELOS identity system. Personality traits. "Euphoric Surprise" metrics. TypeScript/Bun stack. |
| **OpenClaw**       | Plugin architecture. Persistent memory across sessions.                                                                         | WhatsApp-centric. Not CLI-native.                                                             |
| **lazygit**        | Bubbletea TUI patterns. Panel management. Keyboard-driven workflow.                                                             | Git-specific, not generalizable.                                                              |
| **Unix**           | Everything is a file/stream. Pipes. Small composable tools. `stdin`/`stdout` as universal interface.                            | —                                                                                             |

---

_End of specification._
