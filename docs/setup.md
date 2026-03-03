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

## Claude CLI Setup

The draft and chat pipes require the Claude CLI for AI completions.

```bash
npm install -g @anthropic-ai/claude-code
claude auth login
```

Virgil will warn at startup if the Claude CLI is not found but will continue running. Deterministic pipes (memory, calendar) work without it.
