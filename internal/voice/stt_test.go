package voice

import (
	"context"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSTTClientTranscribeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("expected Bearer auth, got %s", r.Header.Get("Authorization"))
		}

		ct := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(ct)
		if err != nil || mediaType != "multipart/form-data" {
			t.Errorf("expected multipart/form-data, got %s", ct)
		}

		r.ParseMultipartForm(10 << 20)
		if r.FormValue("model") != "whisper-1" {
			t.Errorf("expected model=whisper-1, got %s", r.FormValue("model"))
		}
		if r.FormValue("response_format") != "text" {
			t.Errorf("expected response_format=text, got %s", r.FormValue("response_format"))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("  hello world  "))
	}))
	defer srv.Close()

	// Create a temp WAV file to transcribe
	f, err := os.CreateTemp("", "test-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("fake WAV data"))
	f.Close()
	wavPath := f.Name()

	client := &STTClient{
		apiKey:     "sk-test",
		httpClient: srv.Client(),
	}
	// Redirect to test server
	client.httpClient = &http.Client{
		Transport: &urlRewriteTransport{base: srv.URL, inner: srv.Client().Transport},
	}

	transcript, err := client.Transcribe(context.Background(), wavPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if transcript != "hello world" {
		t.Errorf("expected trimmed transcript, got: %q", transcript)
	}

	// File should be removed after transcription
	if _, err := os.Stat(wavPath); !os.IsNotExist(err) {
		t.Error("expected WAV file to be cleaned up after transcription")
		os.Remove(wavPath)
	}
}

func TestSTTClientTranscribeUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid key"))
	}))
	defer srv.Close()

	f, err := os.CreateTemp("", "test-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	client := &STTClient{
		apiKey: "bad-key",
		httpClient: &http.Client{
			Transport: &urlRewriteTransport{base: srv.URL, inner: srv.Client().Transport},
		},
	}

	_, err = client.Transcribe(context.Background(), f.Name())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got: %v", err)
	}
}

func TestSTTClientTranscribeTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()

	f, err := os.CreateTemp("", "test-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	client := &STTClient{
		apiKey: "key",
		httpClient: &http.Client{
			Transport: &urlRewriteTransport{base: srv.URL, inner: srv.Client().Transport},
		},
	}

	_, err = client.Transcribe(context.Background(), f.Name())
	if err == nil || !strings.Contains(err.Error(), "413") {
		t.Errorf("expected 413 error, got: %v", err)
	}
}

func TestSTTClientCleansUpFileOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f, err := os.CreateTemp("", "test-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	wavPath := f.Name()

	client := &STTClient{
		apiKey: "key",
		httpClient: &http.Client{
			Transport: &urlRewriteTransport{base: srv.URL, inner: srv.Client().Transport},
		},
	}

	client.Transcribe(context.Background(), wavPath)

	// File should be cleaned up even on error
	if _, err := os.Stat(wavPath); !os.IsNotExist(err) {
		t.Error("expected WAV file to be cleaned up after error")
		os.Remove(wavPath)
	}
}
