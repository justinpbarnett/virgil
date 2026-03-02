package memory

import (
	"fmt"
	"strconv"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/store"
)

func NewHandler(s *store.Store) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		switch action {
		case "store":
			return handleStore(s, input, flags)
		case "retrieve":
			return handleRetrieve(s, input, flags)
		default:
			return handleRetrieve(s, input, flags)
		}
	}
}

func handleStore(s *store.Store, input envelope.Envelope, flags map[string]string) envelope.Envelope {
	out := envelope.New("memory", "store")
	out.Args = flags

	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		out.Error = envelope.FatalError("no content to store")
		return out
	}

	if err := s.Save(content, nil); err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("failed to save: %v", err))
		return out
	}

	out.Content = "Remembered: " + content
	out.ContentType = envelope.ContentText
	return out
}

func handleRetrieve(s *store.Store, input envelope.Envelope, flags map[string]string) envelope.Envelope {
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
		return out
	}

	limit := 10
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	}

	sort := flags["sort"]
	entries, err := s.Search(query, limit, sort)
	if err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("search failed: %v", err))
		return out
	}

	out.Content = entries
	out.ContentType = envelope.ContentList
	return out
}
