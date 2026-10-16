// SPDX-License-Identifier: Apache-2.0

// Package app assembles signalbox-server: it starts the selected roles and
// the ops listener, and shuts them down in order.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/roles"
	"github.com/CemTbi/Signalbox/internal/webui"
)

// Config is the server's configuration. Every field has a flag and an
// environment variable; see cmd/signalbox-server.
type Config struct {
	// Roles is "all" or a comma-separated list of roles to run.
	Roles string
	// HTTPAddr is where the api role serves the web app and the REST API.
	HTTPAddr string
	// OpsAddr serves /livez and /readyz for every role. Keep it off the
	// public network.
	OpsAddr string
	// DrainDelay is how long readiness fails before listeners close, so
	// load balancers stop routing traffic first. Use about 5s on Kubernetes.
	DrainDelay time.Duration
	// ShutdownTimeout bounds how long in-flight requests get to finish.
	ShutdownTimeout time.Duration
	// MaxSpecBytes bounds OpenAPI documents sent to the API.
	MaxSpecBytes int64
}

// DefaultConfig returns the configuration for a local single-node install.
func DefaultConfig() Config {
	return Config{
		Roles:           "all",
		HTTPAddr:        ":8080",
		OpsAddr:         ":9090",
		ShutdownTimeout: 20 * time.Second,
		MaxSpecBytes:    httpapi.DefaultMaxSpecBytes,
	}
}

// Server is a configured signalbox-server with its listeners bound.
type Server struct {
	cfg     Config
	logger  *slog.Logger
	roles   []roles.Name
	health  *health.Checker
	servers []*http.Server
	lns     []net.Listener
}

// New validates the configuration and binds every listener, so a busy port
// fails here, before anything reports ready.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Server, error) {
	names, err := roles.Parse(cfg.Roles)
	if err != nil {
		return nil, err
	}
	build := buildinfo.Get()
	checker := health.New("signalbox-server", build.Version)
	checker.Add(health.Uptime(time.Now()))

	s := &Server{cfg: cfg, logger: logger, roles: names, health: checker}

	ops := http.NewServeMux()
	ops.Handle("GET /livez", checker.LiveHandler())
	ops.Handle("GET /readyz", checker.ReadyHandler())
	if err := s.listen(ctx, cfg.OpsAddr, ops); err != nil {
		return nil, err
	}

	for _, n := range names {
		switch n {
		case roles.API:
			handler := httpapi.NewHandler(httpapi.Deps{
				Logger:       logger,
				Health:       checker,
				Build:        build,
				Roles:        names,
				UI:           webui.Handler(),
				MaxSpecBytes: cfg.MaxSpecBytes,
			})
			if err := s.listen(ctx, cfg.HTTPAddr, handler); err != nil {
				s.closeListeners()
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Server) listen(ctx context.Context, addr string, handler http.Handler) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.lns = append(s.lns, ln)
	s.servers = append(s.servers, &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	})
	return nil
}

func (s *Server) closeListeners() {
	for _, ln := range s.lns {
		_ = ln.Close()
	}
}

// HTTPAddr returns the address the api role listens on, or "" when the api
// role isn't running.
func (s *Server) HTTPAddr() string {
	if len(s.lns) < 2 {
		return ""
	}
	return s.lns[1].Addr().String()
}

// OpsAddr returns the address the ops listener is bound to.
func (s *Server) OpsAddr() string { return s.lns[0].Addr().String() }

// Run serves until ctx is cancelled or a listener fails, then drains and
// shuts down gracefully. A clean shutdown returns nil.
func (s *Server) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for i, srv := range s.servers {
		ln := s.lns[i]
		g.Go(func() error {
			if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve on %s: %w", ln.Addr(), err)
			}
			return nil
		})
	}
	g.Go(func() error {
		<-gctx.Done()
		return s.shutdown()
	})

	build := buildinfo.Get()
	s.logger.Info("signalbox-server started",
		"version", build.Version, "roles", s.roles, "http_addr", s.HTTPAddr(), "ops_addr", s.OpsAddr())
	return g.Wait()
}

func (s *Server) shutdown() error {
	s.health.SetDraining()
	if s.cfg.DrainDelay > 0 {
		s.logger.Info("draining before shutdown", "delay", s.cfg.DrainDelay)
		time.Sleep(s.cfg.DrainDelay)
	}
	s.logger.Info("shutting down", "timeout", s.cfg.ShutdownTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	var errs []error
	for _, srv := range s.servers {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	s.logger.Info("stopped")
	return nil
}
