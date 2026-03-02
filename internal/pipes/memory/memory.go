package memory

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/store"
)

func NewHandler(s *store.Store, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		switch action {
		case "store":
			return handleStore(s, input, flags, logger)
		case "retrieve":
			return handleRetrieve(s, input, flags, logger)
		default:
			return handleRetrieve(s, input, flags, logger)
		}
	}
}

func handleStore(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("memory", "store")
	out.Args = flags

	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		out.Error = envelope.FatalError("no content to store")
		out.Duration = time.Since(out.Timestamp)
		logger.Error("store failed", "error", "no content")
		return out
	}

	if err := s.Save(content, nil); err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("failed to save: %v", err))
		out.Duration = time.Since(out.Timestamp)
		logger.Error("store failed", "error", err)
		return out
	}

	logger.Info("stored")
	out.Content = "Remembered: " + content
	out.ContentType = envelope.ContentText
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleRetrieve(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("memory", "retrieve")
	out.Args = flags

	query := flags["query"]
	if query == "" {
		query = flags["topic"]
	}
	if query == "" {
		query = envelope.ContentToText(input.Content, input.ContentType)
	}
	if query == "" {
		out.Content = []store.Entry{}
		out.ContentType = envelope.ContentList
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	limit := 10
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	}

	sort := flags["sort"]
	logger.Debug("retrieving", "query", query, "limit", limit, "sort", sort)
	entries, err := s.Search(query, limit, sort)
	if err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("search failed: %v", err))
		out.Duration = time.Since(out.Timestamp)
		logger.Error("search failed", "error", err)
		return out
	}

	logger.Info("retrieved", "count", len(entries))
	out.Content = entries
	out.ContentType = envelope.ContentList
	out.Duration = time.Since(out.Timestamp)
	return out
}
