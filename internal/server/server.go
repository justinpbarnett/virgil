package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

// broker is a generic pub/sub hub. Subscribers receive values broadcast by publishers.
type broker[T any] struct {
	mu   sync.Mutex
	subs map[chan T]struct{}
}

func newBroker[T any]() broker[T] {
	return broker[T]{subs: make(map[chan T]struct{})}
}

func (b *broker[T]) subscribe(buf int) chan T {
	ch := make(chan T, buf)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broker[T]) unsubscribe(ch chan T) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *broker[T]) broadcast(val T) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- val:
		default:
		}
	}
}

// serveSSE runs a standard SSE loop: subscribes to a broker, writes events until the client disconnects.
func serveSSE[T any](w http.ResponseWriter, r *http.Request, b *broker[T], buf int, eventName string, marshal func(T) []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}
	ch := b.subscribe(buf)
	defer b.unsubscribe(ch)
	w.Header().Set("Content-Type", envelope.SSEContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case val := <-ch:
			data := marshal(val)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, data)
			flusher.Flush()
		}
	}
}

type Deps struct {
	Config   *config.Config
	Router   *router.Router
	Parser   *parser.Parser
	Planner  *planner.Planner
	Runtime  *runtime.Runtime
	Registry *pipe.Registry
	Logger   *slog.Logger
}

type Server struct {
	config    *config.Config
	router    *router.Router
	parser    *parser.Parser
	planner   *planner.Planner
	runtime   *runtime.Runtime
	registry  *pipe.Registry
	server    *http.Server
	pidPath   string
	logger    *slog.Logger
	startedAt time.Time

	voiceStatus    broker[voiceStatus]
	lastVoiceStatus struct {
		sync.Mutex
		val *voiceStatus
	}
	voiceInput  broker[string]
	voiceSpeak  broker[voiceSpeakMsg]
	voiceCycle  broker[struct{}]
	voiceStop   broker[struct{}]
}

type voiceSpeakMsg struct {
	Text     string
	Priority string
}

func New(d Deps) *Server {
	pidPath := filepath.Join(config.DataDir(), "virgil.pid")

	return &Server{
		config:      d.Config,
		router:      d.Router,
		parser:      d.Parser,
		planner:     d.Planner,
		runtime:     d.Runtime,
		registry:    d.Registry,
		pidPath:     pidPath,
		logger:      d.Logger,
		startedAt:   time.Now(),
		voiceStatus: newBroker[voiceStatus](),
		voiceInput:  newBroker[string](),
		voiceSpeak:  newBroker[voiceSpeakMsg](),
		voiceCycle:  newBroker[struct{}](),
		voiceStop:   newBroker[struct{}](),
	}
}

func (s *Server) Start() error {
	addr := net.JoinHostPort(s.config.Server.Host, strconv.Itoa(s.config.Server.Port))
	s.server = &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if err := s.writePID(); err != nil {
		s.logger.Warn("failed to write PID file", "error", err)
	} else {
		s.logger.Info("pid file written", "path", s.pidPath)
	}

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		s.logger.Info("shutting down server")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	}()

	s.logger.Info("server starting", "addr", addr)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	os.Remove(s.pidPath)
	s.logger.Info("shutdown complete")
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /signal", s.handleSignal)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /pipes", s.handlePipes)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("POST /voice/status", s.handleVoiceStatusPost)
	mux.HandleFunc("GET /voice/status", s.handleVoiceStatusSSE)
	mux.HandleFunc("POST /voice/input", s.handleVoiceInputPost)
	mux.HandleFunc("GET /voice/input", s.handleVoiceInputSSE)
	mux.HandleFunc("POST /voice/speak", s.handleVoiceSpeakPost)
	mux.HandleFunc("GET /voice/speak", s.handleVoiceSpeakSSE)
	mux.HandleFunc("POST /voice/cycle", s.handleVoiceCyclePost)
	mux.HandleFunc("GET /voice/cycle", s.handleVoiceCycleSSE)
	mux.HandleFunc("POST /voice/stop", s.handleVoiceStopPost)
	mux.HandleFunc("GET /voice/stop", s.handleVoiceStopSSE)
	return mux
}

func (s *Server) writePID() error {
	dir := filepath.Dir(s.pidPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
}
