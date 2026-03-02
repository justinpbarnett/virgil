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
	"syscall"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

type Server struct {
	config   *config.Config
	router   *router.Router
	parser   *parser.Parser
	planner  *planner.Planner
	runtime  *runtime.Runtime
	registry *pipe.Registry
	server   *http.Server
	pidPath  string
	logger   *slog.Logger
}

func New(
	cfg *config.Config,
	rt *router.Router,
	p *parser.Parser,
	pl *planner.Planner,
	run *runtime.Runtime,
	reg *pipe.Registry,
	logger *slog.Logger,
) *Server {
	pidPath := filepath.Join(config.DataDir(), "virgil.pid")

	return &Server{
		config:   cfg,
		router:   rt,
		parser:   p,
		planner:  pl,
		runtime:  run,
		registry: reg,
		pidPath:  pidPath,
		logger:   logger,
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
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /signal", s.handleSignal)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

func (s *Server) writePID() error {
	dir := filepath.Dir(s.pidPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
}
