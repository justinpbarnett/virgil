# Virgil Setup

## Google Calendar API Setup

The calendar pipe requires Google Calendar API credentials.

### 1. Create a GCP Project

1. Go to [Google Cloud Console](https://console.cloud.google.com)
2. Create a new project (or select an existing one)
3. Enable the Google Calendar API:
   - Navigate to **APIs & Services > Library**
   - Search for "Google Calendar API"
   - Click **Enable**

### 2. Create OAuth2 Credentials

1. Navigate to **APIs & Services > Credentials**
2. Click **Create Credentials > OAuth 2.0 Client ID**
3. Select **Desktop app** as the application type
4. Download the credentials JSON file
5. Save it as `~/.config/virgil/google-credentials.json`

### 3. Authorize the Application

Run the token flow to generate `~/.config/virgil/google-token.json`:

```bash
just auth
```

This opens your browser for Google authorization. After you approve, the token is saved automatically.

### Expected File Locations

```
~/.config/virgil/
  google-credentials.json   # OAuth2 client credentials from GCP
  google-token.json          # OAuth2 access/refresh token
```

## Gmail API Setup

The mail pipe requires Gmail API credentials. It uses the same Google OAuth2 credentials as the calendar pipe — if you've already set up calendar, you just need to re-authorize with the additional Gmail scopes.

### 1. Enable the Gmail API

1. Go to [Google Cloud Console](https://console.cloud.google.com)
2. Select the same project used for Calendar (or create a new one)
3. Enable the Gmail API:
   - Navigate to **APIs & Services > Library**
   - Search for "Gmail API"
   - Click **Enable**

### 2. Configure OAuth Consent Screen

If you haven't already, configure the OAuth consent screen:

1. Navigate to **APIs & Services > OAuth consent screen**
2. Add the following scopes:
   - `https://www.googleapis.com/auth/gmail.readonly` — read messages and labels
   - `https://www.googleapis.com/auth/gmail.send` — send messages
   - `https://www.googleapis.com/auth/gmail.modify` — archive, label, and trash messages

If you already have a consent screen configured for Calendar, add the Gmail scopes to the existing configuration.

### 3. Create or Reuse OAuth2 Credentials

If you already have `~/.config/virgil/google-credentials.json` from the calendar setup, skip this step — the same credentials work for both Calendar and Gmail.

If not, follow the same steps as [Google Calendar API Setup](#2-create-oauth2-credentials) above.

### 4. Authorize (or Re-authorize) the Application

Run the auth flow to grant Gmail permissions:

```bash
just auth
```

**If you previously authorized for Calendar only**, you must re-run this command. The auth flow requests all scopes (Calendar + Gmail) and overwrites the existing token. Your calendar access continues to work.

### Expected File Locations

Same files as Calendar — no additional credential files needed:

```
~/.config/virgil/
  google-credentials.json   # OAuth2 client credentials (shared with Calendar)
  google-token.json          # OAuth2 access/refresh token (updated with Gmail scopes)
```

## Jira API Setup

The jira pipe requires a personal access token and a credentials file.

### 1. Generate an API Token

**Jira Cloud** (`yourco.atlassian.net`):

1. Go to [Atlassian account settings](https://id.atlassian.com/manage-profile/security/api-tokens)
2. Click **Create API token**
3. Give it a label (e.g., "virgil") and copy the token

**Jira Server / Data Center** (self-hosted):

1. Log in to your Jira instance
2. Navigate to your profile → **Personal Access Tokens**
3. Click **Create token**, give it a name, and copy the token

### 2. Create the Credentials File

Create `~/.config/virgil/jira.json`:

```json
{
  "base_url": "https://yourco.atlassian.net",
  "email": "you@example.com",
  "token": "your-api-token"
}
```

For Jira Server / Data Center, `email` is still required in the file but is unused — only the Bearer token is sent. You can set it to any non-empty string.

### Expected File Location

```
~/.config/virgil/
  jira.json   # Jira credentials
```

## Claude CLI Setup

The draft and chat pipes require the Claude CLI for AI completions.

```bash
npm install -g @anthropic-ai/claude-code
claude auth login
```

Virgil will warn at startup if the Claude CLI is not found but will continue running. Deterministic pipes (memory, calendar) work without it.

## Voice Integration Setup

The voice daemon enables push-to-talk input (via Whisper) and spoken responses (via ElevenLabs). It runs as a separate process alongside the TUI.

### 1. Prerequisites: sox

Audio recording requires `sox`:

```bash
brew install sox
```

### 2. macOS Accessibility Permissions

The voice daemon uses global hotkeys that require accessibility access. Grant it once:

1. Open **System Settings > Privacy & Security > Accessibility**
2. Add **Terminal** (or the `virgil` binary, if running directly)
3. Enable the toggle

Without this, the daemon will exit with an error pointing here.

### 3. OpenAI API Key (Whisper STT)

The daemon transcribes audio via OpenAI's Whisper API.

1. Create or log into your account at [platform.openai.com](https://platform.openai.com)
2. Navigate to **API keys** and create a new key
3. Copy the key — it starts with `sk-`

### 4. ElevenLabs API Key and Voice ID (TTS)

The daemon speaks responses via ElevenLabs.

1. Create or log into your account at [elevenlabs.io](https://elevenlabs.io)
2. Navigate to **Profile > API Keys** and copy your key
3. Go to **Voices** to find a voice you like — copy its **Voice ID** from the voice card (or use the [GET /v1/voices](https://api.elevenlabs.io/v1/voices) API)

### 5. Create voice.json

Create `~/.config/virgil/voice.json`:

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

All fields except `openai_api_key`, `elevenlabs_api_key`, and `elevenlabs_voice_id` have defaults and are optional.

### 6. Running the Voice Daemon

Start the daemon in a separate terminal:

```bash
virgil --voice
```

The daemon runs in the foreground. The TUI and voice daemon can both be connected to the same server simultaneously.

### Default Hotkeys

| Key | Action |
|-----|--------|
| Right Option (hold) | Push-to-talk: hold to record, release to transcribe and send |
| F8 | Cycle output mode |

### Output Modes

| Mode | Spoken output | Use case |
|------|--------------|----------|
| **Silent** | Nothing | At desk with TUI visible |
| **Notify** | Brief acknowledgement (first sentence or "Done.") | Working, want audio confirmation |
| **Steps** | Step announcements + brief summary | Want pipeline progress awareness |
| **Full** | Step announcements + complete response | Driving, hands-free use |

Press F8 to cycle: Silent → Notify → Steps → Full → Silent. Switching to any active mode announces its name aloud.
