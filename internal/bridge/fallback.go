package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/observe"
)

type FallbackBridge struct {
	providers map[string]Bridge
	embedder  Bridge
	events    *observe.EventLog
}

func NewFallbackBridge(providers map[string]Bridge, embedder Bridge, events *observe.EventLog) *FallbackBridge {
	return &FallbackBridge{
		providers: providers,
		embedder:  embedder,
		events:    events,
	}
}

func (fb *FallbackBridge) Complete(ctx context.Context, cfg ModelConfig, messages []Message, tools []*internal.Tool, fallbacks []ModelConfig) (*Response, error) {
	chain := append([]ModelConfig{cfg}, fallbacks...)

	var lastErr error
	for i, mc := range chain {
		provider, ok := fb.providers[mc.Provider]
		if !ok {
			slog.Warn("bridge: skipping unregistered provider", "provider", mc.Provider, "model", mc.Model)
			lastErr = fmt.Errorf("unknown provider %q", mc.Provider)
			continue
		}

		start := time.Now()
		resp, err := provider.Complete(ctx, mc, messages, tools)
		duration := time.Since(start)

		ev := &internal.Event{
			Component:  "bridge:" + mc.Provider,
			Action:     "inference",
			Model:      mc.Model,
			DurationMs: duration.Milliseconds(),
		}

		if err != nil {
			ev.Error = err.Error()
			fb.logEvent(ev)

			if IsRetryable(err) && i < len(chain)-1 {
				slog.Warn("bridge fallback", "from", mc.Provider+"/"+mc.Model, "to", chain[i+1].Provider+"/"+chain[i+1].Model, "err", err)
				lastErr = err
				continue
			}
			return nil, err
		}

		ev.TokensIn = resp.TokensIn
		ev.TokensOut = resp.TokensOut
		fb.logEvent(ev)

		return resp, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return nil, ErrNoProviders
}

func (fb *FallbackBridge) Embed(ctx context.Context, text string) ([]float32, error) {
	if fb.embedder == nil {
		return nil, fmt.Errorf("no embedding provider configured")
	}

	start := time.Now()
	result, err := fb.embedder.Embed(ctx, text)
	duration := time.Since(start)

	ev := &internal.Event{
		Component:  "bridge:embeddings",
		Action:     "embed",
		DurationMs: duration.Milliseconds(),
	}
	if err != nil {
		ev.Error = err.Error()
	}
	fb.logEvent(ev)

	return result, err
}

func (fb *FallbackBridge) logEvent(ev *internal.Event) {
	if fb.events == nil {
		return
	}
	if err := fb.events.Log(ev); err != nil {
		slog.Error("failed to log bridge event", "component", ev.Component, "action", ev.Action, "model", ev.Model, "err", err)
	}
}
