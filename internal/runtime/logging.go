package runtime

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

type Observer interface {
	OnTransition(pipe string, env envelope.Envelope, duration time.Duration)
}

// logEnvelopeJSON marshals an envelope to JSON and logs it at debug level.
// Shared by LogObserver and Runtime for verbose/debug envelope dumps.
func logEnvelopeJSON(logger *slog.Logger, label string, env envelope.Envelope, attrs ...any) {
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	args := append([]any{"data", string(data)}, attrs...)
	logger.Debug(label, args...)
}

type noopObserver struct{}

func (n *noopObserver) OnTransition(string, envelope.Envelope, time.Duration) {}

type LogObserver struct {
	logger *slog.Logger
	level  config.LogLevel
}

func NewLogObserver(logger *slog.Logger, level config.LogLevel) *LogObserver {
	return &LogObserver{logger: logger, level: level}
}

func (o *LogObserver) OnTransition(pipe string, env envelope.Envelope, duration time.Duration) {
	if o.level == config.Silent {
		return
	}

	hasError := env.Error != nil

	// At error/warn: only log if the envelope has an error
	if o.level <= config.Warn && !hasError {
		return
	}

	// Info: short log
	if hasError {
		o.logger.Error("pipe error", "pipe", pipe, "duration", duration.String(), "error", env.Error.Message)
	} else {
		o.logger.Info("pipe ok", "pipe", pipe, "duration", duration.String())
	}

	// Debug: full envelope JSON
	if o.level >= config.Debug {
		logEnvelopeJSON(o.logger, "envelope", env, "pipe", pipe)
	}
}
