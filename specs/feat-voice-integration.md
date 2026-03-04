# Feature: Voice Integration

## Metadata

type: `feat`
task_id: `voice-integration`
prompt: `Add voice input via push-to-talk with Whisper transcription and tiered voice output via ElevenLabs TTS, both controlled by global hotkeys.`

## Feature Description

Virgil currently accepts input only through text — typed in the TUI, piped via stdin, or passed as CLI arguments. All three share the same server path (`POST /signal`). Voice adds a fourth client: a daemon that registers global hotkeys, captures audio on push-to-talk, transcribes via OpenAI's Whisper API, and sends the transcription to the same Virgil server.

On the output side, the daemon speaks responses through ElevenLabs TTS at one of four verbosity levels, cycled by a global hotkey:

| Mode | Final response | Pipeline steps | Use case |
|------|---------------|----------------|----------|
| **Silent** | — | — | At desk, TUI visible |
| **Notify** | Brief acknowledgement (few words) | — | Working, want audio confirmation |
| **Steps** | Brief acknowledgement | Announces each pipe as it runs | Want progress awareness |
| **Full** | Speaks the complete response | Announces each pipe as it runs | Driving, hands-free |

The daemon is a separate process from the TUI. It communicates with the Virgil server over the same HTTP interface. The TUI and voice daemon can run simultaneously — voice sends signals, responses appear in both the TUI stream and as spoken audio.

### Why a separate daemon

Terminal key events cannot detect key-up (only key-down). True push-to-talk — hold to record, release to stop — requires OS-level event monitoring (`CGEventTap` on macOS), which needs its own run loop. Running this in the TUI's BubbleTea process would conflict with the terminal event loop. A separate daemon sidesteps this entirely and follows Virgil's existing client-server split.

## User Story

As a Virgil user
I want to speak commands and hear responses at a verbosity level matching my context
So that I can interact with Virgil hands-free while focused on other work — or fully eyes-free while driving

## Relevant Files

### Existing Files (modify)

- `cmd/virgil/main.go` — add `--voice` flag to start voice daemon mode
- `internal/config/config.go` — add `VoiceConfig` struct and loader for `voice.json`
- `internal/envelope/envelope.go` — add `SSEEventStep` constant for pipeline step events
- `internal/server/api.go` — emit `step` SSE events during pipeline execution
- `internal/runtime/runtime.go` — extend `ExecuteStream` sink to carry event type for step transitions
- `docs/setup.md` — add Voice Integration setup section

### New Files

- `internal/voice/daemon.go` — main daemon loop: hotkey registration, event dispatch, signal sending, output mode cycling
- `internal/voice/stt.go` — OpenAI Whisper API client: record audio → POST multipart → return transcript
- `internal/voice/tts.go` — ElevenLabs API client: text → POST → receive audio → play; output mode logic
- `internal/voice/hotkey_darwin.go` — macOS global hotkey registration via CGEventTap (build-tagged)
- `internal/voice/hotkey_stub.go` — no-op stub for non-macOS platforms (build-tagged)
- `internal/voice/audio.go` — microphone recording and audio playback using system commands (`sox`, `afplay`)

### New Config File

- `~/.config/virgil/voice.json` — API keys, voice ID, hotkey configuration, default output mode

## Implementation Plan

### Phase 1: Audio I/O Foundation

Set up microphone capture and audio playback by shelling out to system commands. Recording uses `sox` (`rec` command) to write a temporary WAV file. Playback uses `afplay` on macOS. These are simple, reliable, and avoid CGo audio library dependencies.

### Phase 2: Global Hotkey Registration

Implement macOS global hotkey detection using CGEventTap for push-to-talk (hold/release) and a mode cycle key. This requires accessibility permissions. Provide a build-tagged stub for other platforms.

### Phase 3: Whisper STT Integration

Implement the OpenAI Whisper API client. The daemon records audio while the push-to-talk key is held, sends the recording to Whisper on release, and posts the transcript to the Virgil server as a signal.

### Phase 4: ElevenLabs TTS Integration

Implement the ElevenLabs TTS client and output mode logic. Four verbosity levels — silent, notify, steps, full — determine what gets spoken. A global hotkey cycles between them.

### Phase 5: Server-Side Step Events

Add a `step` SSE event type so the server can report pipeline step transitions to SSE clients. The voice daemon uses these in `steps` and `full` modes to announce each pipe as it runs.

### Phase 6: Daemon Orchestration & Config

Wire everything together in the daemon loop. Add configuration loading, the `--voice` CLI flag, and setup documentation.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add VoiceConfig to config package

In `internal/config/config.go`, add a `VoiceConfig` struct and a loader:

```go
type VoiceOutputMode string

const (
    VoiceModeSilent VoiceOutputMode = "silent"
    VoiceModeNotify VoiceOutputMode = "notify"
    VoiceModeSteps  VoiceOutputMode = "steps"
    VoiceModeFull   VoiceOutputMode = "full"
)

type VoiceConfig struct {
    OpenAIKey        string          `json:"openai_api_key"`
    ElevenLabsKey    string          `json:"elevenlabs_api_key"`
    ElevenLabsVoice  string          `json:"elevenlabs_voice_id"`
    ElevenLabsModel  string          `json:"elevenlabs_model_id"`
    PushToTalkKey    string          `json:"push_to_talk_key"`
    ModeCycleKey     string          `json:"mode_cycle_key"`
    OutputMode       VoiceOutputMode `json:"output_mode"`
    MaxSpokenChars   int             `json:"max_spoken_chars"`
}
```

Add a `LoadVoiceConfig(configDir string) (*VoiceConfig, error)` function that reads `voice.json` from the config directory. Apply defaults: `ElevenLabsModel` defaults to `"eleven_turbo_v2_5"`, `PushToTalkKey` defaults to `"right_option"`, `ModeCycleKey` defaults to `"f8"`, `OutputMode` defaults to `"notify"`, `MaxSpokenChars` defaults to `200`.

Return `nil, nil` if the file doesn't exist (voice is optional — not an error).

Validate `OutputMode` against the four allowed values. Return an error for unknown modes.

### 2. Implement audio recording

Create `internal/voice/audio.go`. Implement microphone recording by shelling out to `sox`:

```go
type Recorder struct {
    cmd  *exec.Cmd
    path string
}

func StartRecording() (*Recorder, error)
func (r *Recorder) Stop() (string, error) // returns path to WAV file
```

`StartRecording` spawns `rec` (from sox) writing to a temp file: `rec -q -r 16000 -c 1 -b 16 <tmpfile>.wav`. The 16kHz mono 16-bit format is what Whisper expects. `Stop` sends SIGINT to the process (graceful stop for sox) and returns the temp file path.

Also implement audio playback:

```go
func PlayAudio(path string) error
```

On macOS, shell out to `afplay <path>`. Run asynchronously so the daemon isn't blocked during playback. Clean up the temp file after playback completes.

### 3. Implement macOS global hotkey registration

Create `internal/voice/hotkey_darwin.go` with build tag `//go:build darwin`.

Use CGo to access macOS `CGEventTap` for monitoring key-down and key-up events globally. This requires the process to have accessibility permissions (System Preferences > Privacy & Security > Accessibility).

```go
type HotkeyEvent struct {
    Key    string // e.g., "right_option"
    Action string // "down" or "up"
}

type HotkeyManager struct {
    events chan HotkeyEvent
}

func NewHotkeyManager(keys []string) (*HotkeyManager, error)
func (h *HotkeyManager) Events() <-chan HotkeyEvent
func (h *HotkeyManager) Close()
```

The manager creates a `CGEventTap` that monitors `kCGEventFlagsChanged` (for modifier keys like Option) and `kCGEventKeyDown`/`kCGEventKeyUp` (for regular keys like F8). Events matching registered keys are sent on the channel. The tap runs on its own run loop in a dedicated goroutine.

Map key names to macOS keycodes: `right_option` → `kVK_RightOption` (0x3D), `f8` → `kVK_F8` (0x64), etc.

Create `internal/voice/hotkey_stub.go` with build tag `//go:build !darwin`:

```go
func NewHotkeyManager(keys []string) (*HotkeyManager, error) {
    return nil, fmt.Errorf("global hotkeys not supported on this platform")
}
```

### 4. Implement Whisper STT client

Create `internal/voice/stt.go`:

```go
type STTClient struct {
    apiKey     string
    httpClient *http.Client
}

func NewSTTClient(apiKey string) *STTClient
func (s *STTClient) Transcribe(ctx context.Context, audioPath string) (string, error)
```

`Transcribe` sends a multipart POST to `https://api.openai.com/v1/audio/transcriptions`:
- Field `file`: the WAV audio file
- Field `model`: `"whisper-1"`
- Field `response_format`: `"text"` (plain text, not JSON)
- Header `Authorization: Bearer <api_key>`

The response body is the raw transcript text. Return it trimmed. Clean up the temp WAV file after transcription.

Handle errors: 401 (invalid key), 413 (file too large — Whisper has a 25MB limit), network errors. Wrap errors with context for the daemon to log.

### 5. Implement ElevenLabs TTS client

Create `internal/voice/tts.go`:

```go
type TTSClient struct {
    apiKey     string
    voiceID    string
    modelID    string
    httpClient *http.Client
}

func NewTTSClient(apiKey, voiceID, modelID string) *TTSClient
func (t *TTSClient) Speak(ctx context.Context, text string) (string, error) // returns path to audio file
```

`Speak` sends a POST to `https://api.elevenlabs.io/v1/text-to-speech/<voice_id>?output_format=mp3_44100_128`:
- Header `xi-api-key: <api_key>`
- Header `Content-Type: application/json`
- Body: `{"text": "<text>", "model_id": "<model_id>"}`

The response body is raw MP3 audio. Write it to a temp file and return the path. The caller is responsible for playback and cleanup.

Handle errors: 401 (invalid key), 422 (validation error), rate limits (429 — respect `Retry-After` header).

### 6. Implement output mode speech logic

Add functions in `internal/voice/tts.go` for mode-aware speech preparation:

```go
func StripMarkdown(text string) string
func NotifySummary(text string, maxChars int) string
func StepAnnouncement(pipe string) string
```

**`StripMarkdown`** removes formatting (`#`, `*`, `` ` ``, `[]()`-style links, code fences) for cleaner speech. Used by all speaking modes.

**`NotifySummary`** produces a brief spoken acknowledgement from a response:
1. Strip markdown
2. If the stripped text length ≤ `maxChars`, return the full stripped text
3. If longer, extract the first sentence (up to the first `.`, `!`, or `?` followed by a space or end of string). If that sentence ≤ `maxChars`, return it
4. If still too long, return `"Done."`

This gives `notify` and `steps` modes a natural spoken summary — the first sentence often captures the key result ("You have 3 events today.", "Email sent.", "Build succeeded."). Falls back to "Done." only for responses whose first sentence is itself long.

**`StepAnnouncement`** produces a brief spoken phrase for a pipeline step transition. Given the pipe name, return a natural phrase:
- Map known pipe names to spoken phrases: `"calendar"` → `"Checking calendar."`, `"draft"` → `"Drafting."`, `"study"` → `"Researching."`, `"chat"` → `"Thinking."`, `"mail"` → `"Checking mail."`, `"code"` → `"Writing code."`, `"shell"` → `"Running command."`, `"build"` → `"Building."`
- Unknown pipes: capitalize the name and append a period: `"memory"` → `"Memory."`

These are short, conversational, and give the user a sense of progress without being verbose.

The daemon calls these functions based on the current output mode:

| Mode | On step event | On final response |
|------|--------------|-------------------|
| `silent` | — | — |
| `notify` | — | `NotifySummary(response, maxChars)` |
| `steps` | `StepAnnouncement(pipe)` | `NotifySummary(response, maxChars)` |
| `full` | `StepAnnouncement(pipe)` | `StripMarkdown(response)` (complete text) |

### 7. Add step SSE event to server

In `internal/envelope/envelope.go`, add a new SSE event constant:

```go
const SSEEventStep = "step"
```

In `internal/runtime/runtime.go`, extend `ExecuteStream` so that when the observer's `OnTransition` fires for non-terminal steps, a step event is sent through the SSE sink. The simplest approach: change the existing sink callback signature to accept an event type alongside the data:

```go
type StreamEvent struct {
    Type string // "chunk" or "step"
    Data string
}
```

Change `ExecuteStream`'s sink parameter from `func(chunk string)` to `func(StreamEvent)`. Update the existing chunk sends to use `StreamEvent{Type: "chunk", Data: text}`. Add step event sends after each non-terminal step completes:

```go
sink(StreamEvent{
    Type: "step",
    Data: fmt.Sprintf(`{"pipe":"%s","duration":"%s"}`, step.Pipe, duration),
})
```

In `internal/server/api.go`, update the SSE handler to emit the new event type. When the sink receives a `StreamEvent` with `Type: "step"`, write it as `event: step\ndata: ...\n\n`. Chunk events continue as `event: chunk\ndata: ...\n\n`.

The TUI's SSE reader in `internal/tui/tui.go` should ignore unknown event types (it already has a `default` case in the event switch). Step events are only consumed by the voice daemon.

### 8. Implement the voice daemon

Create `internal/voice/daemon.go`:

```go
type Daemon struct {
    config     *config.VoiceConfig
    serverAddr string
    stt        *STTClient
    tts        *TTSClient
    hotkeys    *HotkeyManager
    mode       config.VoiceOutputMode
    recording  *Recorder
    logger     *log.Logger
}

func NewDaemon(cfg *config.VoiceConfig, serverAddr string) (*Daemon, error)
func (d *Daemon) Run(ctx context.Context) error
```

`Run` enters the main event loop:

```
for event := range d.hotkeys.Events() {
    switch {
    case event.Key == d.config.PushToTalkKey && event.Action == "down":
        d.startRecording()
    case event.Key == d.config.PushToTalkKey && event.Action == "up":
        d.stopAndSubmit(ctx)
    case event.Key == d.config.ModeCycleKey && event.Action == "down":
        d.cycleMode(ctx)
    }
}
```

**`startRecording`** calls `StartRecording()` and logs "Recording..." to stderr.

**`stopAndSubmit`**:
1. Calls `d.recording.Stop()` → WAV file path
2. Calls `d.stt.Transcribe(ctx, path)` → transcript text
3. Logs the transcript to stderr
4. If mode is `silent` — send sync `POST /signal`, discard response
5. If mode is `notify` — send sync `POST /signal`, speak `NotifySummary(response, maxChars)`
6. If mode is `steps` or `full` — open SSE stream to `POST /signal`:
   - On `step` events: speak `StepAnnouncement(pipe)` (non-blocking — fire and forget, let playback overlap with pipeline execution)
   - On `chunk` events: accumulate text
   - On `done` event: speak `NotifySummary(response, maxChars)` for `steps` mode, or `StripMarkdown(response)` for `full` mode

For `steps`/`full`, step announcements should not queue up behind each other. If a new step event arrives while the previous announcement is still playing, skip the new one (the pipeline moved faster than speech). This keeps audio responsive rather than falling behind.

**`cycleMode`** advances through the modes in order and announces the new mode:
```
silent → notify → steps → full → silent
```
The daemon speaks the new mode name (e.g., "Notify.", "Steps.", "Full.") when cycling into an active mode. Cycling to `silent` plays nothing (obviously). Log the mode change to stderr.

**Signal sending**: Reuse the same HTTP pattern from `internal/tui/client.go`. For sync mode, `POST /signal` with `Content-Type: application/json` body `{"text": "..."}`. For SSE mode, add `Accept: text/event-stream` header.

### 9. Wire the daemon into main.go

In `cmd/virgil/main.go`, add a `--voice` flag:

```go
voice := flag.Bool("voice", false, "run voice daemon")
```

When `--voice` is set:
1. Load `VoiceConfig` from the config directory
2. If nil (file doesn't exist), exit with an error pointing to setup instructions
3. Validate required fields (OpenAI key, ElevenLabs key, voice ID)
4. Ensure the server is running (reuse `tui.EnsureServer`)
5. Create and run the daemon

The voice daemon runs as a foreground process (not backgrounded). The user starts it in a separate terminal or via a launch agent.

### 10. Add setup documentation

Append a "Voice Integration Setup" section to `docs/setup.md`. Cover:

1. **Prerequisites**: `sox` installation (`brew install sox`)
2. **macOS accessibility permissions** for global hotkeys — System Settings > Privacy & Security > Accessibility, add Terminal (or the `virgil` binary)
3. **OpenAI API key** for Whisper transcription — where to get it, link to OpenAI dashboard
4. **ElevenLabs API key and voice ID** — where to get them, link to ElevenLabs dashboard, how to find voice IDs (Voices section or `GET /v1/voices` API)
5. **Creating `~/.config/virgil/voice.json`** with example:

```json
{
  "openai_api_key": "sk-...",
  "elevenlabs_api_key": "...",
  "elevenlabs_voice_id": "JBFqnCBsd6RMkjVDRZzb",
  "elevenlabs_model_id": "eleven_turbo_v2_5",
  "push_to_talk_key": "right_option",
  "mode_cycle_key": "f8",
  "output_mode": "notify",
  "max_spoken_chars": 200
}
```

6. **Running**: `virgil --voice` in a separate terminal
7. **Default hotkeys**: Right Option (push-to-talk), F8 (cycle output mode)
8. **Output modes**: Table describing silent, notify, steps, full

### 11. Add voice status to the TUI

In the keybinding summary (`internal/tui/tui.go`, `keybindingSummary` function), note that voice mode is available via `virgil --voice`. This is purely informational — no TUI changes are needed since the daemon is a separate process.

### 12. Add dependency: CGo for macOS hotkeys

Update `go.mod` — the CGEventTap implementation uses CGo with system frameworks. No third-party Go dependencies are needed for this since we use CGo directly for CoreGraphics and direct HTTP calls for Whisper/ElevenLabs APIs.

Ensure the `justfile` `build` target still works with CGo enabled (it should by default on macOS).

### 13. Validate system dependencies at daemon startup

In the daemon's `NewDaemon` or `Run`, check for required system tools:
- Verify `rec` (sox) is in PATH — if not, exit with: `"sox is required for voice recording: brew install sox"`
- Verify `afplay` is in PATH (macOS) — if not, exit with error
- Check accessibility permissions by attempting to create a CGEventTap — if it fails, exit with: `"Accessibility permission required: System Settings > Privacy & Security > Accessibility"`

Fail fast with clear instructions rather than cryptic errors mid-session.

## Testing Strategy

### Unit Tests

- **VoiceConfig loading**: Test `LoadVoiceConfig` with valid JSON, missing file (returns nil), malformed JSON (returns error), partial JSON (defaults applied), invalid output mode (returns error)
- **StripMarkdown**: Test with headers, bold, italic, code blocks, inline code, links. Verify clean plain text output
- **NotifySummary**: Test with text under threshold (returned as-is), text over threshold with short first sentence (first sentence returned), text with long first sentence (returns "Done."), empty text (returns empty)
- **StepAnnouncement**: Test with known pipe names (maps to friendly phrases), unknown pipe names (capitalized with period)
- **Mode cycling**: Test `silent → notify → steps → full → silent` cycle wraps correctly
- **STT client**: Test request construction (multipart form, correct headers, model field). Use `httptest.Server` to mock the Whisper API
- **TTS client**: Test request construction (correct URL with voice ID, headers, body). Use `httptest.Server` to mock ElevenLabs API. Verify audio bytes are written to temp file

### Integration Tests

- **Daemon signal flow (notify mode)**: Mock Whisper API, ElevenLabs API, and Virgil server with `httptest.Server`. Provide a pre-recorded WAV → transcribe → send signal → receive response → verify `NotifySummary` is spoken
- **Daemon signal flow (steps mode)**: Mock server returns step SSE events before done. Verify `StepAnnouncement` is called for each step, then `NotifySummary` for the final response
- **Daemon signal flow (full mode)**: Verify complete `StripMarkdown` response is spoken after step announcements
- **Silent mode**: Verify no TTS calls at all — signal still sent, response still received, but `Speak` is never called
- **Mode cycling**: Simulate mode cycle key events, verify mode advances and announcement is spoken (except silent)
- **Step announcement overlap**: Verify that if a step event arrives while previous audio is playing, the new announcement is skipped rather than queued
- **Error handling**: Test behavior when Whisper API returns 401, when ElevenLabs returns 429, when sox is not installed

### Manual Testing

- Verify push-to-talk records audio while held and stops on release
- Verify transcription accuracy with clear speech
- Cycle through all four modes with F8 — verify mode announcement is spoken
- **Silent**: verify no audio output after response
- **Notify**: verify short responses spoken fully, long responses produce brief summary
- **Steps**: verify each pipeline step is announced, then final summary
- **Full**: verify step announcements followed by complete spoken response
- Verify daemon works alongside TUI session (both connected to same server)

## Risk Assessment

**CGEventTap requires accessibility permissions.** Users must grant the terminal (or the `virgil` binary) accessibility access in System Settings. The daemon validates this at startup and exits with clear instructions if permission is missing. This is a one-time setup step.

**Sox dependency.** Sox is an external system dependency. If not installed, the daemon fails fast with `brew install sox` instructions. Sox is mature, widely available, and the simplest way to capture audio without CGo audio library complexity.

**macOS-only for v1.** Global hotkey registration via CGEventTap is macOS-specific. Linux support (via X11/Wayland event monitoring) and Windows support are left for future work. The build-tagged stub ensures the project compiles on all platforms — it just can't run the voice daemon.

**API costs.** Both Whisper and ElevenLabs are paid APIs. Whisper charges per minute of audio; ElevenLabs charges per character. The tiered output modes help manage costs — `notify` mode speaks only a few words per response, while `full` mode speaks everything. Default to `notify` to minimize costs unless the user explicitly opts into higher verbosity.

**Latency.** The full path (stop recording → upload to Whisper → get transcript → send signal → get response → upload to ElevenLabs → get audio → play) involves multiple API round-trips. Expected latency: 2-5 seconds for voice input, 1-3 seconds for voice output. The `eleven_turbo_v2_5` model is ElevenLabs' lowest-latency option. In `steps` mode, step announcements provide perceived responsiveness while the pipeline runs.

**Step announcement overlap.** If the pipeline runs faster than speech, step announcements could queue up and play long after the pipeline finishes. Mitigated by skipping announcements when the previous one is still playing. The daemon prioritizes the final response over intermediate step audio.

**`ExecuteStream` sink signature change.** Changing the sink from `func(string)` to `func(StreamEvent)` touches the runtime, server, and any test code that creates stream sinks. This is internal code with a small surface area. Update all call sites in one pass.

## Validation Commands

```bash
go build ./...
go vet ./...
go test ./internal/config/... -v
go test ./internal/voice/... -v
go test ./internal/runtime/... -v
go test ./internal/server/... -v
go test ./... -count=1
```

Manual validation:
```bash
# Start server
just start

# In another terminal, start voice daemon
./bin/virgil --voice

# Default mode is "notify"
# Hold right Option key, say "what time is it", release
# Verify transcription appears in daemon logs
# Verify brief summary is spoken

# Press F8 → "Steps." is announced
# Hold right Option, say "draft an email to Alice about the meeting", release
# Verify step announcements ("Studying.", "Drafting.") are spoken
# Verify final summary is spoken

# Press F8 → "Full." is announced
# Hold right Option, say "what are my calendar events today", release
# Verify step announcements are spoken
# Verify complete response is spoken

# Press F8 → cycles to silent (no announcement)
# Hold right Option, say "hello", release
# Verify signal is sent but no audio output

# Press F8 → "Notify." is announced (back to start)
```

## Open Questions (Unresolved)

1. **Should the voice daemon show a visual indicator (menu bar icon, notification)?** Currently it only logs to stderr. A menu bar icon showing recording state and current output mode would improve UX but adds complexity (requires a Cocoa app bundle or similar). **Recommendation: Defer to a follow-up. Stderr logging is sufficient for v1.**

2. **Should voice input be processed differently than text input?** The current design sends the raw transcript as a signal, identical to typed text. Whisper output sometimes includes filler words or punctuation artifacts. A post-processing step could clean these up. **Recommendation: Skip for v1. Whisper's output is generally clean enough. If quality issues arise, add a cleanup step later.**

3. **ElevenLabs voice selection.** The user must provide a voice ID in the config. ElevenLabs has many pre-made voices. **Recommendation: Document how to find voice IDs (ElevenLabs dashboard or API) in the setup guide. Let the user choose.**

4. **Should `full` mode also summarize very long responses (e.g., multi-page explanations)?** The current design speaks everything in full mode. For a 2000-character response, this could take 30+ seconds of speech. **Recommendation: Speak everything in v1. The user chose `full` mode explicitly. If it's too verbose, they can cycle to `notify` or `steps`.**

## Resolved Decisions

- **Separate daemon, not TUI integration.** Terminals can't detect key-up events, so true push-to-talk requires OS-level event monitoring. A separate daemon avoids conflicts with BubbleTea's event loop and follows Virgil's client-server architecture.
- **Shell out for audio I/O.** Using `sox` for recording and `afplay` for playback avoids complex CGo audio library dependencies (PortAudio, etc.). These tools are mature and reliable.
- **Direct HTTP for APIs.** No SDK dependencies for Whisper or ElevenLabs. Both APIs are simple REST endpoints. Direct `net/http` calls keep the dependency tree clean.
- **macOS-only for v1.** CGEventTap is the most reliable way to get push-to-talk on macOS. Linux/Windows can be added later with platform-specific implementations behind build tags.
- **Four output modes, not a boolean.** A simple on/off toggle doesn't capture the range of voice output needs. `silent` for desk work, `notify` for brief confirmations, `steps` for pipeline awareness, `full` for hands-free. Cycling via hotkey is fast — four presses to return to the starting mode.
- **Cycle hotkey, not separate hotkeys per mode.** Four modes need only one hotkey to cycle through them. Announcing the mode name on switch gives clear feedback. Avoids consuming multiple global hotkeys.
- **SSE for steps/full modes, sync for silent/notify.** Step events require SSE streaming. Silent and notify modes only need the final response, so sync is simpler and lower overhead. The daemon picks the request style based on the current mode.
- **Skip overlapping step announcements.** If the pipeline moves faster than speech, the daemon drops step announcements rather than queuing them. The final response is always spoken — intermediate awareness is best-effort.
- **First-sentence extraction for notify summaries.** "Done." is too terse when the first sentence naturally captures the result. First-sentence extraction gives better summaries ("You have 3 events today.") with "Done." as a safe fallback for long-winded first sentences.

## Sub-Tasks

1. **Audio & Config Foundation** (Steps 1-2) — VoiceConfig with output modes, audio recording/playback via sox/afplay
2. **Hotkey Registration** (Step 3) — macOS CGEventTap implementation, build-tagged stub
3. **STT Integration** (Step 4) — Whisper API client, multipart upload
4. **TTS Integration** (Steps 5-6) — ElevenLabs API client, output mode speech logic (StripMarkdown, NotifySummary, StepAnnouncement)
5. **Server Step Events** (Step 7) — SSEEventStep constant, StreamEvent type, ExecuteStream sink change, server SSE handler update
6. **Daemon & Wiring** (Steps 8-9, 11-13) — Daemon loop with mode cycling, CLI flag, dependency checks
7. **Documentation** (Step 10) — Setup instructions in docs/setup.md

Sub-task 3 depends on 1. Sub-task 4 is independent. Sub-task 5 is independent. Sub-task 6 depends on 1, 2, 3, 4, and 5.
