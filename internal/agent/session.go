package agent

import (
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
)

const maxTurns = 10
const sessionTimeout = 5 * time.Minute

// SessionBuffer holds recent turns for multi-turn conversations, scoped per channel.
type SessionBuffer struct {
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	turns  []turn
	lastAt time.Time
}

type turn struct {
	role    string
	content string
}

// NewSessionBuffer creates an empty session buffer.
func NewSessionBuffer() *SessionBuffer {
	return &SessionBuffer{
		sessions: make(map[string]*session),
	}
}

// Get returns recent turns for a channel as bridge Messages.
// Returns nil if the session is expired or doesn't exist.
func (sb *SessionBuffer) Get(channel string) []bridge.Message {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	sess, ok := sb.sessions[channel]
	if !ok {
		return nil
	}
	if time.Since(sess.lastAt) > sessionTimeout {
		delete(sb.sessions, channel)
		return nil
	}

	messages := make([]bridge.Message, 0, len(sess.turns))
	for _, t := range sess.turns {
		messages = append(messages, bridge.Message{
			Role:    t.role,
			Content: t.content,
		})
	}
	return messages
}

// Add records a user message and assistant response.
func (sb *SessionBuffer) Add(channel, userMsg, assistantMsg string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	now := time.Now()
	sess, ok := sb.sessions[channel]
	if !ok || time.Since(sess.lastAt) > sessionTimeout {
		sess = &session{}
		sb.sessions[channel] = sess
	}

	sess.turns = append(sess.turns,
		turn{role: "user", content: userMsg},
		turn{role: "assistant", content: assistantMsg},
	)
	sess.lastAt = now

	if len(sess.turns) > maxTurns {
		sess.turns = sess.turns[len(sess.turns)-maxTurns:]
	}

	sb.sweepExpired(now)
}

// sweepExpired removes sessions that have timed out. Must be called with mu held.
func (sb *SessionBuffer) sweepExpired(now time.Time) {
	for ch, sess := range sb.sessions {
		if now.Sub(sess.lastAt) > sessionTimeout {
			delete(sb.sessions, ch)
		}
	}
}

// Clear removes a channel's session.
func (sb *SessionBuffer) Clear(channel string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	delete(sb.sessions, channel)
}

// TurnCount returns the number of turns in a channel's session.
func (sb *SessionBuffer) TurnCount(channel string) int {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	sess, ok := sb.sessions[channel]
	if !ok || time.Since(sess.lastAt) > sessionTimeout {
		return 0
	}
	return len(sess.turns)
}
