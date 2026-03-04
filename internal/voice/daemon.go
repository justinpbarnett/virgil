package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

// Daemon listens for hotkey events and routes voice commands to the Virgil server.
type Daemon struct {
	cfg        *config.VoiceConfig
	serverAddr string
	stt        *STTClient
	tts        *TTSClient
	hotkeys    *HotkeyManager
	mode       config.VoiceOutputMode
	logger     *log.Logger

	recordingMu sync.Mutex
	recording   *Recorder

	playingMu  sync.Mutex
	playing    bool
	playingCmd *exec.Cmd

	cacheDir        string
	cacheMu         sync.RWMutex
	announcementCache map[string]string
}

// NewDaemon creates a new voice daemon, validating system dependencies.
func NewDaemon(cfg *config.VoiceConfig, serverAddr string) (*Daemon, error) {
	if err := validateSystemDeps(); err != nil {
		return nil, err
	}

	keys := []string{cfg.PushToTalkKey, cfg.ModeCycleKey}
	hotkeys, err := NewHotkeyManager(keys)
	if err != nil {
		return nil, fmt.Errorf("hotkey registration failed: %w", err)
	}

	cacheDir := filepath.Join(config.DataDir(), "tts-cache")
	d := &Daemon{
		cfg:               cfg,
		serverAddr:        serverAddr,
		stt:               NewSTTClient(cfg.OpenAIKey),
		tts:               NewTTSClient(cfg.ElevenLabsKey, cfg.ElevenLabsVoice, cfg.ElevenLabsModel),
		hotkeys:           hotkeys,
		mode:              cfg.OutputMode,
		logger:            log.New(log.Writer(), "[voice] ", log.LstdFlags),
		cacheDir:          cacheDir,
		announcementCache: make(map[string]string),
	}
	go d.populateCache(context.Background())
	return d, nil
}

func validateSystemDeps() error {
	if _, err := exec.LookPath("rec"); err != nil {
		return fmt.Errorf("sox is required for voice recording: brew install sox")
	}
	if _, err := exec.LookPath("afplay"); err != nil {
		return fmt.Errorf("afplay is required for audio playback (macOS only)")
	}
	return nil
}

// populateCache pre-generates TTS audio for all announcement phrases.
// Files that already exist are skipped (no API call). Runs in a background goroutine.
func (d *Daemon) populateCache(ctx context.Context) {
	if err := os.MkdirAll(d.cacheDir, 0o755); err != nil {
		d.logger.Printf("cache: failed to create dir: %v", err)
		return
	}
	phrases := AllCachePhrases()
	generated := 0
	for _, phrase := range phrases {
		path, err := d.tts.PrecachePhrase(ctx, d.cacheDir, phrase)
		if err != nil {
			d.logger.Printf("cache: failed %q: %v", phrase, err)
			continue
		}
		d.cacheMu.Lock()
		d.announcementCache[phrase] = path
		d.cacheMu.Unlock()
		generated++
	}
	d.logger.Printf("cache: ready (%d/%d phrases)", generated, len(phrases))
}

// playFileOnce plays an audio file without deleting it afterwards.
// Manages the playing mutex so other play calls respect busy state.
func (d *Daemon) playFileOnce(path string) {
	afplay, err := exec.LookPath("afplay")
	if err != nil {
		return
	}
	cmd := exec.Command(afplay, path)
	d.playingMu.Lock()
	d.playing = true
	d.playingCmd = cmd
	d.playingMu.Unlock()
	cmd.Run()
	d.playingMu.Lock()
	d.playing = false
	d.playingCmd = nil
	d.playingMu.Unlock()
}

// playAnnouncementNonBlocking plays a step/thinking announcement.
// Uses pre-cached audio if available (near-instant), falls back to real-time TTS.
// Drops silently if audio is already playing.
func (d *Daemon) playAnnouncementNonBlocking(ctx context.Context, text string) {
	d.playingMu.Lock()
	busy := d.playing
	d.playingMu.Unlock()
	if busy {
		return
	}
	d.cacheMu.RLock()
	path, ok := d.announcementCache[text]
	d.cacheMu.RUnlock()
	if ok {
		go d.playFileOnce(path)
	} else {
		d.speakAsync(ctx, text)
	}
}

// speakInterrupting stops any current audio and speaks text via real-time TTS.
// Used for final responses — always plays regardless of busy state.
func (d *Daemon) speakInterrupting(ctx context.Context, text string) {
	d.stopAudio()
	d.speakAsync(ctx, text)
}

// Run enters the main event loop, processing hotkey events until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	d.logger.Printf("voice daemon started (mode: %s)", d.mode)
	d.logger.Printf("push-to-talk: %s | mode cycle: %s", d.cfg.PushToTalkKey, d.cfg.ModeCycleKey)
	d.postVoiceStatus(false, string(d.mode))
	go d.runSpeakSubscriber(ctx)
	go d.runCycleSubscriber(ctx)
	go d.runStopSubscriber(ctx)

	for {
		select {
		case <-ctx.Done():
			d.hotkeys.Close()
			d.stopAudio()
			return ctx.Err()
		case event, ok := <-d.hotkeys.Events():
			if !ok {
				return nil
			}
			switch {
			case event.Key == d.cfg.PushToTalkKey && event.Action == "down":
				d.startRecording()
			case event.Key == d.cfg.PushToTalkKey && event.Action == "up":
				go d.stopAndSubmit(ctx)
			case event.Key == d.cfg.ModeCycleKey && event.Action == "down":
				d.cycleMode(ctx)
			}
		}
	}
}

func (d *Daemon) startRecording() {
	d.recordingMu.Lock()
	defer d.recordingMu.Unlock()
	if d.recording != nil {
		return
	}
	r, err := StartRecording()
	if err != nil {
		d.logger.Printf("recording failed: %v", err)
		return
	}
	d.recording = r
	d.logger.Println("Recording...")
	d.postVoiceStatus(true, string(d.mode))
}

func (d *Daemon) stopAndSubmit(ctx context.Context) {
	d.recordingMu.Lock()
	if d.recording == nil {
		d.recordingMu.Unlock()
		return
	}
	rec := d.recording
	d.recording = nil
	d.recordingMu.Unlock()
	d.postVoiceStatus(false, string(d.mode))

	path, err := rec.Stop()
	if err != nil {
		d.logger.Printf("stop recording failed: %v", err)
		return
	}

	transcript, err := d.stt.Transcribe(ctx, path)
	if err != nil {
		d.logger.Printf("transcription failed: %v", err)
		return
	}
	if transcript == "" {
		d.logger.Println("Empty transcription, skipping")
		return
	}
	d.logger.Printf("transcript: %s", transcript)
	d.postVoiceInput(ctx, transcript)
}

func (d *Daemon) postVoiceInput(ctx context.Context, text string) {
	payload, _ := json.Marshal(map[string]string{"text": text})
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "POST",
		fmt.Sprintf("http://%s/voice/input", d.serverAddr), bytes.NewReader(payload))
	if err != nil {
		d.logger.Printf("postVoiceInput: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.logger.Printf("postVoiceInput: %v", err)
		return
	}
	resp.Body.Close()
}

func (d *Daemon) runSpeakSubscriber(ctx context.Context) {
	for {
		if err := d.connectAndSpeak(ctx); err != nil && ctx.Err() == nil {
			d.logger.Printf("speak subscriber: %v, reconnecting...", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *Daemon) connectAndSpeak(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://%s/voice/speak", d.serverAddr), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	var eventType, data string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			data = after
		} else if line == "" && eventType == "voice_speak" {
			var payload struct {
				Text     string `json:"text"`
				Priority string `json:"priority"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err == nil && payload.Text != "" {
				if payload.Priority == "response" {
					d.speakInterrupting(ctx, payload.Text)
				} else {
					d.playAnnouncementNonBlocking(ctx, payload.Text)
				}
			}
			eventType, data = "", ""
		}
	}
	return scanner.Err()
}

func (d *Daemon) runCycleSubscriber(ctx context.Context) {
	for {
		if err := d.connectAndCycle(ctx); err != nil && ctx.Err() == nil {
			d.logger.Printf("cycle subscriber: %v, reconnecting...", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *Daemon) connectAndCycle(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://%s/voice/cycle", d.serverAddr), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 4096)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if line == "" && eventType == "voice_cycle" {
			d.cycleMode(ctx)
			eventType = ""
		}
	}
	return scanner.Err()
}

func (d *Daemon) postSignalSync(ctx context.Context, text string) (envelope.Envelope, error) {
	url := fmt.Sprintf("http://%s/signal", d.serverAddr)
	body, _ := json.Marshal(map[string]string{"text": text})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return envelope.Envelope{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("signal request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var env envelope.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		d.logger.Printf("unmarshal signal response: %v", err)
		return envelope.Envelope{}, fmt.Errorf("unmarshal signal response: %w", err)
	}
	return env, nil
}

func (d *Daemon) postSignalSSE(ctx context.Context, text string) {
	url := fmt.Sprintf("http://%s/signal", d.serverAddr)
	body, _ := json.Marshal(map[string]string{"text": text})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		d.logger.Printf("SSE request setup failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", envelope.SSEContentType)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		d.logger.Printf("SSE connect failed: %v", err)
		return
	}
	defer resp.Body.Close()

	var accumulated strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)

	var eventType, data string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			data = after
		} else if line == "" && eventType != "" {
			switch eventType {
			case envelope.SSEEventRoute:
				var route struct {
					Pipe string `json:"pipe"`
				}
				if err := json.Unmarshal([]byte(data), &route); err == nil {
					if d.mode != config.VoiceModeSilent {
						announcement := StepAnnouncement(route.Pipe)
						d.speakNonBlocking(ctx, announcement)
					}
				}
			case envelope.SSEEventChunk:
				var chunk struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal([]byte(data), &chunk); err == nil {
					accumulated.WriteString(chunk.Text)
				}
			case envelope.SSEEventStep:
				var step struct {
					Pipe string `json:"pipe"`
				}
				if err := json.Unmarshal([]byte(data), &step); err == nil {
					if d.mode == config.VoiceModeSteps || d.mode == config.VoiceModeFull {
						announcement := StepAnnouncement(step.Pipe)
						d.speakNonBlocking(ctx, announcement)
					}
				}
			case envelope.SSEEventDone:
				if d.mode == config.VoiceModeSilent {
					return
				}
				response := accumulated.String()
				if response == "" {
					var env envelope.Envelope
					if err := json.Unmarshal([]byte(data), &env); err == nil {
						response = envelope.ContentToText(env.Content, env.ContentType)
					}
				}
				if response != "" {
					var spoken string
					if d.mode == config.VoiceModeFull {
						spoken = StripMarkdown(response)
					} else {
						spoken = NotifySummary(response, d.cfg.MaxSpokenChars)
					}
					if spoken != "" {
						d.speakAsync(ctx, spoken)
					}
				}
				return
			}
			eventType, data = "", ""
		}
	}
}

// speakAsync speaks text asynchronously in a goroutine.
func (d *Daemon) speakAsync(ctx context.Context, text string) {
	go func() {
		path, err := d.tts.Speak(ctx, text)
		if err != nil {
			d.logger.Printf("TTS error: %v", err)
			return
		}

		afplay, err := exec.LookPath("afplay")
		if err != nil {
			os.Remove(path)
			return
		}
		cmd := exec.Command(afplay, path)

		d.playingMu.Lock()
		d.playing = true
		d.playingCmd = cmd
		d.playingMu.Unlock()

		cmd.Run()
		os.Remove(path)

		d.playingMu.Lock()
		d.playing = false
		d.playingCmd = nil
		d.playingMu.Unlock()
	}()
}

// stopAudio kills any currently playing audio immediately.
func (d *Daemon) stopAudio() {
	d.playingMu.Lock()
	cmd := d.playingCmd
	d.playingMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
}

// speakNonBlocking speaks text only if nothing is currently playing (drops if busy).
func (d *Daemon) speakNonBlocking(ctx context.Context, text string) {
	d.playingMu.Lock()
	busy := d.playing
	d.playingMu.Unlock()
	if busy {
		return
	}
	d.speakAsync(ctx, text)
}

// abortRecording stops and discards any in-progress recording, and kills audio.
func (d *Daemon) abortRecording() {
	d.recordingMu.Lock()
	rec := d.recording
	d.recording = nil
	d.recordingMu.Unlock()
	if rec != nil {
		rec.Stop()
		d.postVoiceStatus(false, "")
		d.logger.Println("Recording aborted")
	}
	d.stopAudio()
}

func (d *Daemon) runStopSubscriber(ctx context.Context) {
	for {
		if err := d.connectAndStop(ctx); err != nil && ctx.Err() == nil {
			d.logger.Printf("stop subscriber: %v, reconnecting...", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *Daemon) connectAndStop(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://%s/voice/stop", d.serverAddr), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 4096)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if line == "" && eventType == "voice_stop" {
			d.abortRecording()
			eventType = ""
		}
	}
	return scanner.Err()
}

func (d *Daemon) postVoiceStatus(recording bool, mode string) {
	payload, _ := json.Marshal(map[string]any{
		"recording": recording,
		"mode":      mode,
		"model":     d.cfg.VoiceModel,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("http://%s/voice/status", d.serverAddr), bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

var modeOrder = []config.VoiceOutputMode{
	config.VoiceModeSilent,
	config.VoiceModeNotify,
	config.VoiceModeSteps,
	config.VoiceModeFull,
}

func (d *Daemon) cycleMode(ctx context.Context) {
	idx := 0
	for i, m := range modeOrder {
		if m == d.mode {
			idx = i
			break
		}
	}
	d.mode = modeOrder[(idx+1)%len(modeOrder)]
	d.logger.Printf("mode: %s", d.mode)
	d.postVoiceStatus(false, string(d.mode))

	if d.mode != config.VoiceModeSilent {
		announcement := strings.ToUpper(string(d.mode[:1])) + string(d.mode[1:]) + "."
		d.speakAsync(ctx, announcement)
	}
}
