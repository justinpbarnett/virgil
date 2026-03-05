package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/cenkalti/backoff/v4"
)

const elevenLabsBaseURL = "https://api.elevenlabs.io/v1/text-to-speech"

// TTSClient converts text to speech via ElevenLabs.
type TTSClient struct {
	apiKey     string
	voiceID    string
	modelID    string
	httpClient *http.Client
}

// NewTTSClient creates an ElevenLabs TTS client.
func NewTTSClient(apiKey, voiceID, modelID string) *TTSClient {
	return &TTSClient{
		apiKey:  apiKey,
		voiceID: voiceID,
		modelID: modelID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Speak sends text to ElevenLabs TTS and returns a path to the MP3 audio file.
// The caller is responsible for playback and cleanup.
func (t *TTSClient) Speak(ctx context.Context, text string) (string, error) {
	url := fmt.Sprintf("%s/%s?output_format=mp3_44100_128", elevenLabsBaseURL, t.voiceID)

	payload := map[string]string{
		"text":     text,
		"model_id": t.modelID,
	}
	body, _ := json.Marshal(payload)

	bo := backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3), ctx)

	var audioPath string
	err := backoff.RetryNotify(func() error {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return backoff.Permanent(fmt.Errorf("creating TTS request: %w", err))
		}
		req.Header.Set("xi-api-key", t.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := t.httpClient.Do(req)
		if err != nil {
			return backoff.Permanent(fmt.Errorf("TTS request failed: %w", err))
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				return fmt.Errorf("rate limited (retry-after: %s)", retryAfter)
			}
			return fmt.Errorf("rate limited (429)")
		case http.StatusOK:
			// continue
		case http.StatusUnauthorized:
			return backoff.Permanent(fmt.Errorf("invalid ElevenLabs API key (401)"))
		case http.StatusUnprocessableEntity:
			data, _ := io.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("ElevenLabs validation error (422): %s", string(data)))
		default:
			data, _ := io.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("ElevenLabs API error (%d): %s", resp.StatusCode, string(data)))
		}

		f, err := os.CreateTemp("", "virgil-tts-*.mp3")
		if err != nil {
			return backoff.Permanent(fmt.Errorf("creating temp file: %w", err))
		}
		defer f.Close()

		if _, err := io.Copy(f, resp.Body); err != nil {
			os.Remove(f.Name())
			return backoff.Permanent(fmt.Errorf("writing audio: %w", err))
		}

		audioPath = f.Name()
		return nil
	}, bo, nil)

	if err != nil {
		return "", err
	}
	return audioPath, nil
}

var (
	reMDHeader     = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reMDCodeFence  = regexp.MustCompile("(?s)```[^`]*```")
	reMDInlineCode = regexp.MustCompile("`([^`]+)`")
	reMDBold       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reMDItalic     = regexp.MustCompile(`\*([^*]+)\*`)
	reMDLink       = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
)

// StripMarkdown removes markdown formatting for cleaner speech.
func StripMarkdown(text string) string {
	text = reMDCodeFence.ReplaceAllString(text, "")
	text = reMDHeader.ReplaceAllString(text, "")
	text = reMDInlineCode.ReplaceAllString(text, "$1")
	text = reMDBold.ReplaceAllString(text, "$1")
	text = reMDItalic.ReplaceAllString(text, "$1")
	text = reMDLink.ReplaceAllString(text, "$1")
	// Clean up extra whitespace
	text = strings.TrimSpace(text)
	return text
}

// NotifySummary produces a brief spoken acknowledgement from a response.
func NotifySummary(text string, maxChars int) string {
	stripped := StripMarkdown(text)
	if stripped == "" {
		return ""
	}
	if len(stripped) <= maxChars {
		return stripped
	}

	// Try extracting the first sentence
	sentence := firstSentence(stripped)
	if sentence != "" && len(sentence) <= maxChars {
		return sentence
	}

	return "Done."
}

// firstSentence extracts the first sentence ending in ., !, or ?.
func firstSentence(text string) string {
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' {
			// Check if end of string or followed by space/end
			rest := text[i+1:]
			if rest == "" || (len(rest) > 0 && unicode.IsSpace(rune(rest[0]))) {
				return strings.TrimSpace(text[:i+1])
			}
		}
	}
	return ""
}

// ThinkingPhrases are used when the router falls back to layer 4 (AI classification).
// One is picked at random for immediate audio acknowledgment before the AI responds.
var ThinkingPhrases = []string{
	"One second.",
	"On it.",
	"Sure.",
	"Let me think.",
	"Give me a moment.",
	"Working on it.",
}

// ThinkingPhrase returns a random thinking phrase.
func ThinkingPhrase() string {
	return ThinkingPhrases[rand.Intn(len(ThinkingPhrases))]
}

var stepPhrases = map[string]string{
	"calendar": "Checking calendar.",
	"draft":    "Drafting.",
	"study":    "Researching.",
	"chat":     "Thinking.",
	"mail":     "Checking mail.",
	"code":     "Writing code.",
	"shell":    "Running command.",
	"build":    "Building.",
}

// StepAnnouncement produces a brief spoken phrase for a pipeline step transition.
func StepAnnouncement(pipe string) string {
	if phrase, ok := stepPhrases[pipe]; ok {
		return phrase
	}
	if len(pipe) == 0 {
		return "Processing."
	}
	return strings.ToUpper(pipe[:1]) + pipe[1:] + "."
}

// AllCachePhrases returns all phrases that should be pre-cached at startup.
func AllCachePhrases() []string {
	seen := make(map[string]bool)
	var phrases []string
	for _, v := range stepPhrases {
		if !seen[v] {
			seen[v] = true
			phrases = append(phrases, v)
		}
	}
	for _, v := range ThinkingPhrases {
		if !seen[v] {
			seen[v] = true
			phrases = append(phrases, v)
		}
	}
	return phrases
}

// phraseToFilename converts a phrase to a stable cache filename.
func phraseToFilename(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String() + ".mp3"
}

// PrecachePhrase generates TTS audio for text and stores it persistently in cacheDir.
// If the file already exists it returns the path immediately without an API call.
func (t *TTSClient) PrecachePhrase(ctx context.Context, cacheDir, text string) (string, error) {
	path := filepath.Join(cacheDir, phraseToFilename(text))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	tmp, err := t.Speak(ctx, text)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		data, rerr := os.ReadFile(tmp)
		os.Remove(tmp)
		if rerr != nil {
			return "", fmt.Errorf("reading temp file: %w", rerr)
		}
		if werr := os.WriteFile(path, data, 0o644); werr != nil {
			return "", fmt.Errorf("writing cache file: %w", werr)
		}
	}
	return path, nil
}
