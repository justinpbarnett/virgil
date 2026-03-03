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
		switch flags["action"] {
		case "store":
			return handleStore(s, input, flags, logger)
		case "working-state":
			return handleWorkingState(s, input, flags, logger)
		default:
			return handleRetrieve(s, input, flags, logger)
		}
	}
}

func handleStore(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("memory", "store")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		out.Error = envelope.FatalError("no content to store")
		logger.Error("store failed", "error", "no content")
		return out
	}

	if err := s.Save(content, nil); err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("failed to save: %v", err))
		logger.Error("store failed", "error", err)
		return out
	}

	logger.Info("stored")
	out.Content = "Remembered: " + content
	out.ContentType = envelope.ContentText
	return out
}

func handleRetrieve(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("memory", "retrieve")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

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
		return out
	}

	limit := 10
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	}

	sortOrder := flags["sort"]
	logger.Debug("retrieving", "query", query, "limit", limit, "sort", sortOrder)
	entries, err := s.Search(query, limit, sortOrder)
	if err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("search failed: %v", err))
		logger.Error("search failed", "error", err)
		return out
	}

	logger.Info("retrieved", "count", len(entries))
	out.Content = entries
	out.ContentType = envelope.ContentList
	return out
}

func handleWorkingState(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("memory", "working-state")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	namespace := flags["namespace"]
	key := flags["key"]
	op := flags["op"]
	if op == "" {
		op = "get"
	}

	// Validate namespace+key for ops that need them.
	needsKey := op == "put" || op == "get" || op == "delete"
	if namespace == "" && (needsKey || op == "list") {
		out.Error = envelope.FatalError(fmt.Sprintf("namespace is required for %s", op))
		return out
	}
	if key == "" && needsKey {
		out.Error = envelope.FatalError(fmt.Sprintf("key is required for %s", op))
		return out
	}

	switch op {
	case "put":
		content := envelope.ContentToText(input.Content, input.ContentType)
		if content == "" {
			out.Error = envelope.FatalError("content is required for put")
			return out
		}
		if err := s.PutState(namespace, key, content); err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("put failed: %v", err))
			logger.Error("working-state put failed", "error", err)
			return out
		}
		logger.Info("working-state put", "namespace", namespace, "key", key)
		out.Content = fmt.Sprintf("Stored state: %s/%s", namespace, key)
		out.ContentType = envelope.ContentText
		return out

	case "get":
		content, found, err := s.GetState(namespace, key)
		if err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("get failed: %v", err))
			logger.Error("working-state get failed", "error", err)
			return out
		}
		if !found {
			out.Content = ""
			out.ContentType = envelope.ContentText
			return out
		}
		logger.Info("working-state get", "namespace", namespace, "key", key)
		out.Content = content
		out.ContentType = envelope.ContentText
		return out

	case "delete":
		if err := s.DeleteState(namespace, key); err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("delete failed: %v", err))
			logger.Error("working-state delete failed", "error", err)
			return out
		}
		logger.Info("working-state delete", "namespace", namespace, "key", key)
		out.Content = fmt.Sprintf("Deleted state: %s/%s", namespace, key)
		out.ContentType = envelope.ContentText
		return out

	case "list":
		entries, err := s.ListState(namespace)
		if err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("list failed: %v", err))
			logger.Error("working-state list failed", "error", err)
			return out
		}
		logger.Info("working-state list", "namespace", namespace, "count", len(entries))
		out.Content = entries
		out.ContentType = envelope.ContentList
		return out

	default:
		out.Error = envelope.FatalError(fmt.Sprintf("unknown working-state op: %s", op))
		return out
	}
}
