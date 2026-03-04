package voice

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// --- StripMarkdown ---

func TestStripMarkdownHeaders(t *testing.T) {
	out := StripMarkdown("# Header\n## Sub\ntext")
	if strings.Contains(out, "#") {
		t.Errorf("expected headers stripped, got: %s", out)
	}
	if !strings.Contains(out, "text") {
		t.Errorf("expected plain text preserved, got: %s", out)
	}
}

func TestStripMarkdownBoldItalic(t *testing.T) {
	out := StripMarkdown("**bold** and *italic*")
	if strings.Contains(out, "*") {
		t.Errorf("expected asterisks stripped, got: %s", out)
	}
	if !strings.Contains(out, "bold") || !strings.Contains(out, "italic") {
		t.Errorf("expected words preserved, got: %s", out)
	}
}

func TestStripMarkdownCodeFence(t *testing.T) {
	out := StripMarkdown("intro\n```\ncode here\n```\nend")
	if strings.Contains(out, "code here") {
		t.Errorf("expected code fence content removed, got: %s", out)
	}
	if !strings.Contains(out, "end") {
		t.Errorf("expected text after code fence preserved, got: %s", out)
	}
}

func TestStripMarkdownInlineCode(t *testing.T) {
	out := StripMarkdown("use `foo` bar")
	if strings.Contains(out, "`") {
		t.Errorf("expected backticks stripped, got: %s", out)
	}
	if !strings.Contains(out, "foo") {
		t.Errorf("expected inline code content preserved, got: %s", out)
	}
}

func TestStripMarkdownLinks(t *testing.T) {
	out := StripMarkdown("see [the docs](https://example.com) for details")
	if strings.Contains(out, "https://") {
		t.Errorf("expected URL stripped, got: %s", out)
	}
	if !strings.Contains(out, "the docs") {
		t.Errorf("expected link text preserved, got: %s", out)
	}
}

// --- NotifySummary ---

func TestNotifySummaryShortText(t *testing.T) {
	text := "Done."
	out := NotifySummary(text, 200)
	if out != "Done." {
		t.Errorf("expected text returned as-is, got: %s", out)
	}
}

func TestNotifySummaryFirstSentence(t *testing.T) {
	text := "You have 3 events today. The first is at 9am. The second is at 2pm."
	out := NotifySummary(text, 50)
	if out != "You have 3 events today." {
		t.Errorf("expected first sentence, got: %s", out)
	}
}

func TestNotifySummaryLongFirstSentence(t *testing.T) {
	// First sentence is itself too long
	long := strings.Repeat("x", 300) + "."
	out := NotifySummary(long, 200)
	if out != "Done." {
		t.Errorf("expected 'Done.' fallback, got: %s", out)
	}
}

func TestNotifySummaryEmpty(t *testing.T) {
	out := NotifySummary("", 200)
	if out != "" {
		t.Errorf("expected empty for empty input, got: %s", out)
	}
}

func TestNotifySummaryUnderThreshold(t *testing.T) {
	text := "Email sent to Alice."
	out := NotifySummary(text, 200)
	if out != text {
		t.Errorf("expected full text, got: %s", out)
	}
}

// --- StepAnnouncement ---

func TestStepAnnouncementKnownPipes(t *testing.T) {
	cases := map[string]string{
		"calendar": "Checking calendar.",
		"draft":    "Drafting.",
		"study":    "Researching.",
		"chat":     "Thinking.",
		"mail":     "Checking mail.",
		"code":     "Writing code.",
		"shell":    "Running command.",
		"build":    "Building.",
	}
	for pipe, want := range cases {
		got := StepAnnouncement(pipe)
		if got != want {
			t.Errorf("StepAnnouncement(%q) = %q, want %q", pipe, got, want)
		}
	}
}

func TestStepAnnouncementUnknownPipe(t *testing.T) {
	out := StepAnnouncement("memory")
	if out != "Memory." {
		t.Errorf("expected 'Memory.', got: %s", out)
	}
}

func TestStepAnnouncementLowercaseCapitalized(t *testing.T) {
	out := StepAnnouncement("weather")
	if out != "Weather." {
		t.Errorf("expected 'Weather.', got: %s", out)
	}
}

// --- TTS client ---

func TestTTSClientSpeakSuccess(t *testing.T) {
	fakeAudio := []byte("MP3_AUDIO_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") == "" {
			t.Error("expected xi-api-key header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected JSON content type, got %s", r.Header.Get("Content-Type"))
		}
		if !strings.Contains(r.URL.Path, "voice123") {
			t.Errorf("expected voice ID in URL, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(fakeAudio)
	}))
	defer srv.Close()

	client := &TTSClient{
		apiKey:     "test-key",
		voiceID:    "voice123",
		modelID:    "eleven_turbo_v2_5",
		httpClient: srv.Client(),
	}
	// Override the URL in the client for testing
	origURL := elevenLabsBaseURL
	_ = origURL // accessed via package-level constant

	// Use a custom HTTP client that redirects to test server
	client.httpClient = &http.Client{
		Transport: &urlRewriteTransport{
			base:   srv.URL,
			inner:  srv.Client().Transport,
		},
	}

	path, err := client.Speak(context.Background(), "Hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading audio file: %v", err)
	}
	if string(data) != string(fakeAudio) {
		t.Errorf("expected audio data written to file")
	}
}

func TestTTSClientSpeakUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := &TTSClient{
		apiKey:  "bad-key",
		voiceID: "voice123",
		modelID: "eleven_turbo_v2_5",
		httpClient: &http.Client{
			Transport: &urlRewriteTransport{base: srv.URL, inner: srv.Client().Transport},
		},
	}

	_, err := client.Speak(context.Background(), "Hello")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got: %v", err)
	}
}

// urlRewriteTransport rewrites requests to point at the test server.
type urlRewriteTransport struct {
	base  string
	inner http.RoundTripper
}

func (t *urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = strings.TrimPrefix(t.base, "http://")
	if t.inner != nil {
		return t.inner.RoundTrip(req2)
	}
	return http.DefaultTransport.RoundTrip(req2)
}

func TestTTSClientRequestBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte("audio"))
	}))
	defer srv.Close()

	client := &TTSClient{
		apiKey:  "key",
		voiceID: "v1",
		modelID: "model1",
		httpClient: &http.Client{
			Transport: &urlRewriteTransport{base: srv.URL, inner: srv.Client().Transport},
		},
	}

	path, err := client.Speak(context.Background(), "test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	os.Remove(path)

	if !strings.Contains(string(gotBody), "test text") {
		t.Errorf("expected text in request body, got: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), "model1") {
		t.Errorf("expected model_id in request body, got: %s", gotBody)
	}
}
