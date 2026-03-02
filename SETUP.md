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

## Claude CLI Setup

The draft and chat pipes require the Claude CLI for AI completions.

```bash
npm install -g @anthropic-ai/claude-code
claude auth login
```

Virgil will warn at startup if the Claude CLI is not found but will continue running. Deterministic pipes (memory, calendar) work without it.
