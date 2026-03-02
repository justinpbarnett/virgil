package runtime

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

type Observer interface {
	OnTransition(pipe string, env envelope.Envelope, duration time.Duration)
}

type noopObserver struct{}

func (n *noopObserver) OnTransition(string, envelope.Envelope, time.Duration) {}

type LogObserver struct {
	logger *slog.Logger
	level  string
}

func NewLogObserver(logger *slog.Logger, level string) *LogObserver {
	return &LogObserver{logger: logger, level: level}
}

func (o *LogObserver) OnTransition(pipe string, env envelope.Envelope, duration time.Duration) {
	status := "ok"
	if env.Error != nil {
		status = env.Error.Severity
	}

	o.logger.Info("pipe executed",
		"pipe", pipe,
		"action", env.Action,
		"duration", duration.String(),
		"status", status,
	)

	if o.level == "debug" {
		data, err := json.Marshal(env)
		if err == nil {
			o.logger.Debug("envelope", "pipe", pipe, "data", string(data))
		}
	}
}
