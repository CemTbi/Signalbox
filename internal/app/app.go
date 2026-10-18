// SPDX-License-Identifier: Apache-2.0

// Package app assembles signalbox-server: it connects to the database,
// starts the selected roles and the ops listener, and shuts them down in
// order.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/egress"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
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

	// DatabaseURL is the PostgreSQL connection URL. Required.
	DatabaseURL string
	// DatabaseWait bounds how long startup waits for PostgreSQL to answer.
	DatabaseWait time.Duration
	// Migrate applies pending database migrations at startup. Turn it off
	// to run them separately with "signalbox-server migrate".
	Migrate bool
	// PublicURL is the address people open Signalbox at, such as
	// https://signalbox.example.com.
	PublicURL string
	// TrustedProxies lists the reverse proxies, as addresses or CIDR ranges,
	// whose X-Forwarded-For header is believed.
	TrustedProxies string
	// SetupCode, when set, unlocks first-run setup instead of a random code
	// written to the log.
	SetupCode string
	// OIDC connects an OpenID Connect identity provider for single sign-on.
	// Its redirect URL is derived from PublicURL.
	OIDC identity.SSOConfig
	// Identity tunes accounts and sessions. Tests lower its hashing cost.
	Identity identity.Config
	// EgressAllow and EgressDeny adjust where the runner may send requests:
	// comma-separated address ranges, addresses and hostnames such as
	// 10.0.0.0/8 or *.internal.example.com. See docs/adr/0016-runner-egress.md.
	EgressAllow string
	EgressDeny  string
}

// DefaultConfig returns the configuration for a local single-node install.
func DefaultConfig() Config {
	return Config{
		Roles:           "all",
		HTTPAddr:        ":8080",
		OpsAddr:         ":9090",
		ShutdownTimeout: 20 * time.Second,
		MaxSpecBytes:    httpapi.DefaultMaxSpecBytes,
		DatabaseWait:    time.Minute,
		Migrate:         true,
		PublicURL:       "http://localhost:8080",
	}
}

// Server is a configured signalbox-server with its listeners bound.
type Server struct {
	cfg        Config
	logger     *slog.Logger
	roles      []roles.Name
	health     *health.Checker
	publicURL  *url.URL
	db         *pgxpool.Pool
	identity   *identity.Service
	workspaces *workspaces.Service
	client     *client.Module // nil when the api role isn't running

	ops *endpoint
	api *endpoint // nil when the api role isn't running
}

type endpoint struct {
	srv *http.Server
	ln  net.Listener
}

// New validates the configuration and binds every listener, so a busy port
// or a bad setting fails here, before anything reports ready. It doesn't
// connect to the database yet; Run does.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Server, error) {
	names, err := roles.Parse(cfg.Roles)
	if err != nil {
		return nil, err
	}
	publicURL, err := ParsePublicURL(cfg.PublicURL)
	if err != nil {
		return nil, err
	}
	trusted, err := httpapi.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return nil, errors.New("no database configured: set SIGNALBOX_DATABASE_URL or --database-url to a PostgreSQL URL, " +
			"such as postgres://signalbox:password@localhost:5432/signalbox")
	}
	if cfg.OIDC.Issuer != "" && cfg.OIDC.ClientID == "" {
		return nil, errors.New("single sign-on needs a client ID: set SIGNALBOX_OIDC_CLIENT_ID or --oidc-client-id " +
			"to the client registered at the identity provider")
	}
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	cfg.Identity.SetupCode = cfg.SetupCode
	if cfg.OIDC.Issuer != "" {
		cfg.OIDC.RedirectURL = publicURL.JoinPath("api/v1/sso/callback").String()
		cfg.Identity.SSO = cfg.OIDC
	}
	ids, err := identity.New(db, logger, cfg.Identity)
	if err != nil {
		db.Close()
		return nil, err
	}

	build := buildinfo.Get()
	checker := health.New("signalbox-server", build.Version)
	checker.Add(health.Uptime(time.Now()))
	checker.Add(postgres.Check(db, logger))
	checker.SetStarting("waiting for the database")

	s := &Server{
		cfg: cfg, logger: logger, roles: names, health: checker, publicURL: publicURL,
		db: db, identity: ids, workspaces: workspaces.New(db, logger),
	}

	ops := http.NewServeMux()
	ops.Handle("GET /livez", checker.LiveHandler())
	ops.Handle("GET /readyz", checker.ReadyHandler())
	if s.ops, err = s.bind(ctx, cfg.OpsAddr, ops); err != nil {
		db.Close()
		return nil, err
	}

	// The api role hands requests to a runner in the same process. Runners
	// in their own processes need NATS, which comes with high availability.
	var run client.Runner
	if slices.Contains(names, roles.Runner) {
		if !slices.Contains(names, roles.API) {
			db.Close()
			_ = s.ops.ln.Close()
			return nil, errors.New("the runner role runs with the api role for now: use --roles=all, or api,runner")
		}
		policy, err := egressPolicy(ctx, cfg.DatabaseURL, cfg.EgressAllow, cfg.EgressDeny, net.DefaultResolver)
		if err != nil {
			db.Close()
			_ = s.ops.ln.Close()
			return nil, err
		}
		run = runner.New(&egress.Dialer{Policy: policy}, runner.Limits{}, "Signalbox/"+build.Version)
	}

	for _, n := range names {
		if n != roles.API {
			continue
		}
		mods := apis.New(db, logger, s.workspaces)
		s.client = client.New(db, logger, s.workspaces, run)
		handler := httpapi.NewHandler(httpapi.Deps{
			Logger:         logger,
			Health:         checker,
			Build:          build,
			Roles:          names,
			UI:             webui.Handler(),
			MaxSpecBytes:   cfg.MaxSpecBytes,
			Identity:       ids,
			Workspaces:     s.workspaces,
			APIs:           mods,
			Mocks:          mods,
			Client:         s.client,
			PublicURL:      publicURL,
			TrustedProxies: trusted,
		})
		if s.api, err = s.bind(ctx, cfg.HTTPAddr, handler); err != nil {
			_ = s.ops.ln.Close()
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

// ParsePublicURL checks the address Signalbox is reached at. Signalbox must
// be served from the root of its host.
func ParsePublicURL(s string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("public URL %q: use an absolute http or https URL, such as https://signalbox.example.com", s)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("public URL %q: remove the path, query and credentials; Signalbox is served from the root of its host", s)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

func (s *Server) bind(ctx context.Context, addr string, handler http.Handler) (*endpoint, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return &endpoint{ln: ln, srv: &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	}}, nil
}

// HTTPAddr returns the address the api role listens on, or "" when the api
// role isn't running.
func (s *Server) HTTPAddr() string {
	if s.api == nil {
		return ""
	}
	return s.api.ln.Addr().String()
}

// OpsAddr returns the address the ops listener is bound to.
func (s *Server) OpsAddr() string { return s.ops.ln.Addr().String() }

// Run serves until ctx is cancelled or something fails, then drains and
// shuts down gracefully. A clean shutdown returns nil.
//
// The ops listener answers at once, so liveness passes and readiness
// explains the wait while the server connects to the database and migrates
// it. The api listener serves only once that is done.
func (s *Server) Run(ctx context.Context) error {
	defer s.db.Close()
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serve(s.ops) })
	g.Go(func() error {
		<-gctx.Done()
		return s.shutdown()
	})

	if err := s.start(gctx); err != nil {
		if ctx.Err() != nil {
			return g.Wait() // stopped during startup
		}
		// Ending the group's context makes the goroutine above shut down.
		g.Go(func() error { return err })
		return g.Wait()
	}

	if s.api != nil {
		g.Go(func() error { return serve(s.api) })
	}
	g.Go(func() error {
		s.identity.Run(gctx)
		return nil
	})
	g.Go(func() error {
		s.workspaces.Run(gctx)
		return nil
	})
	if s.client != nil {
		g.Go(func() error {
			s.client.Run(gctx)
			return nil
		})
	}
	s.health.SetStarted()
	build := buildinfo.Get()
	s.logger.Info("signalbox-server started",
		"version", build.Version, "roles", s.roles, "http_addr", s.HTTPAddr(), "ops_addr", s.OpsAddr(),
		"public_url", s.publicURL.String())
	return g.Wait()
}

// start connects to the database, migrates it and opens first-run setup.
func (s *Server) start(ctx context.Context) error {
	s.warnAboutPlainHTTP()
	s.describeSSO()

	waitCtx, cancel := context.WithTimeout(ctx, s.cfg.DatabaseWait)
	defer cancel()
	if err := postgres.WaitReady(waitCtx, s.db, s.logger); err != nil {
		return err
	}
	if s.cfg.Migrate {
		s.health.SetStarting("running database migrations")
		if err := postgres.Migrate(ctx, s.db, s.logger); err != nil {
			return err
		}
	}
	if s.api != nil {
		if err := s.identity.PrepareSetup(ctx, s.publicURL.JoinPath("setup").String()); err != nil {
			return fmt.Errorf("prepare first-run setup: %w", err)
		}
	}
	return nil
}

// warnAboutPlainHTTP warns when people will reach Signalbox over plain HTTP
// from another machine, where passwords and cookies travel unencrypted.
func (s *Server) warnAboutPlainHTTP() {
	if s.publicURL.Scheme != "http" || s.api == nil || isLoopback(s.publicURL.Hostname()) {
		return
	}
	s.logger.Warn("Signalbox is served over plain HTTP. Passwords and session cookies travel unencrypted; "+
		"put Signalbox behind HTTPS and set SIGNALBOX_PUBLIC_URL to its https:// address.",
		"public_url", s.publicURL.String())
}

// describeSSO logs how single sign-on is set up, so operators can check the
// redirect URL to register at their identity provider.
func (s *Server) describeSSO() {
	o := s.cfg.Identity.SSO
	if o.Issuer == "" || s.api == nil {
		return
	}
	s.logger.Info("single sign-on is on; register this redirect URL with the identity provider",
		"issuer", o.Issuer, "redirect_url", o.RedirectURL)
	if len(o.AllowedDomains) == 0 {
		s.logger.Info("single sign-on creates an account for anyone the identity provider vouches for; " +
			"set SIGNALBOX_OIDC_ALLOWED_DOMAINS to limit it, especially with a public provider such as Google")
	}
	if u, err := url.Parse(o.Issuer); err == nil && u.Scheme == "http" && !isLoopback(u.Hostname()) {
		s.logger.Warn("the identity provider's issuer URL uses plain HTTP, so the client secret and tokens travel unencrypted; "+
			"use its https:// address outside a test setup", "issuer", o.Issuer)
	}
}

func isLoopback(host string) bool {
	addr, err := netip.ParseAddr(host)
	return host == "localhost" || err == nil && addr.IsLoopback()
}

func serve(e *endpoint) error {
	if err := e.srv.Serve(e.ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve on %s: %w", e.ln.Addr(), err)
	}
	return nil
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
	for _, e := range []*endpoint{s.api, s.ops} {
		if e == nil {
			continue
		}
		if err := e.srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		// Shutdown doesn't close a listener that never started serving.
		_ = e.ln.Close()
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	s.logger.Info("stopped")
	return nil
}
