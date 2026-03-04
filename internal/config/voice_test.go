package config

import (
	"testing"
)

func TestLoadVoiceConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadVoiceConfig(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil config for missing file")
	}
}

func TestLoadVoiceConfigValid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "voice.json", `{
		"openai_api_key": "sk-test",
		"elevenlabs_api_key": "el-test",
		"elevenlabs_voice_id": "voice123",
		"output_mode": "steps"
	}`)

	cfg, err := LoadVoiceConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.OpenAIKey != "sk-test" {
		t.Errorf("expected openai key sk-test, got %s", cfg.OpenAIKey)
	}
	if cfg.OutputMode != VoiceModeSteps {
		t.Errorf("expected steps mode, got %s", cfg.OutputMode)
	}
}

func TestLoadVoiceConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "voice.json", `{
		"openai_api_key": "sk-test",
		"elevenlabs_api_key": "el-test",
		"elevenlabs_voice_id": "voice123"
	}`)

	cfg, err := LoadVoiceConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ElevenLabsModel != "eleven_turbo_v2_5" {
		t.Errorf("expected default model, got %s", cfg.ElevenLabsModel)
	}
	if cfg.PushToTalkKey != "right_option" {
		t.Errorf("expected default push-to-talk key, got %s", cfg.PushToTalkKey)
	}
	if cfg.ModeCycleKey != "f8" {
		t.Errorf("expected default mode cycle key, got %s", cfg.ModeCycleKey)
	}
	if cfg.OutputMode != VoiceModeNotify {
		t.Errorf("expected default notify mode, got %s", cfg.OutputMode)
	}
	if cfg.MaxSpokenChars != 200 {
		t.Errorf("expected default 200 max spoken chars, got %d", cfg.MaxSpokenChars)
	}
}

func TestLoadVoiceConfigMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "voice.json", `{not valid json}`)

	_, err := LoadVoiceConfig(dir)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadVoiceConfigInvalidMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "voice.json", `{
		"openai_api_key": "sk-test",
		"output_mode": "loudly"
	}`)

	_, err := LoadVoiceConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid output mode")
	}
}
