// SPDX-License-Identifier: Apache-2.0

// Command signalbox-server runs the Signalbox platform. One binary contains
// every role; --roles picks which ones this process runs.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/CemTbi/Signalbox/internal/app"
	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/logging"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCommand().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "signalbox-server:", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "signalbox-server",
		Short:         "Run the Signalbox platform",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newServeCommand(), newMigrateCommand(), newHealthcheckCommand(), &cobra.Command{
		Use:   "version",
		Short: "Print the version of signalbox-server",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "signalbox-server", buildinfo.Get())
		},
	})
	return root
}

func newServeCommand() *cobra.Command {
	cfg := app.DefaultConfig()
	var logFormat, logLevel string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the server",
		Long: `Start the server with the selected roles.

Every flag can also be set with an environment variable, shown in brackets.
Flags win over environment variables.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger, err := logging.New(cmd.ErrOrStderr(), logFormat, logLevel)
			if err != nil {
				return err
			}
			srv, err := app.New(cmd.Context(), cfg, logger)
			if err != nil {
				return err
			}
			return srv.Run(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Roles, "roles", env("SIGNALBOX_ROLES", cfg.Roles),
		"roles to run: all, or a comma-separated list of api, mock, runner, scheduler, worker, ingest [SIGNALBOX_ROLES]")
	f.StringVar(&cfg.HTTPAddr, "http-addr", env("SIGNALBOX_HTTP_ADDR", cfg.HTTPAddr),
		"address for the web app and REST API [SIGNALBOX_HTTP_ADDR]")
	f.StringVar(&cfg.OpsAddr, "ops-addr", env("SIGNALBOX_OPS_ADDR", cfg.OpsAddr),
		"address for health endpoints; keep it off the public network [SIGNALBOX_OPS_ADDR]")
	f.StringVar(&cfg.PublicURL, "public-url", env("SIGNALBOX_PUBLIC_URL", cfg.PublicURL),
		"the address people open Signalbox at, such as https://signalbox.example.com [SIGNALBOX_PUBLIC_URL]")
	f.StringVar(&cfg.TrustedProxies, "trusted-proxies", env("SIGNALBOX_TRUSTED_PROXIES", cfg.TrustedProxies),
		"reverse proxies whose X-Forwarded-For header is believed, as addresses or CIDR ranges [SIGNALBOX_TRUSTED_PROXIES]")
	f.StringVar(&cfg.SetupCode, "setup-code", env("SIGNALBOX_SETUP_CODE", cfg.SetupCode),
		"code that unlocks first-run setup; by default a random one is logged [SIGNALBOX_SETUP_CODE]")
	addDatabaseFlags(f, &cfg.DatabaseURL, &cfg.DatabaseWait)
	f.BoolVar(&cfg.Migrate, "migrate", envBool("SIGNALBOX_MIGRATE", cfg.Migrate),
		"apply pending database migrations at startup [SIGNALBOX_MIGRATE]")
	f.DurationVar(&cfg.DrainDelay, "drain-delay", envDuration("SIGNALBOX_DRAIN_DELAY", cfg.DrainDelay),
		"how long readiness fails before shutdown starts [SIGNALBOX_DRAIN_DELAY]")
	f.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", envDuration("SIGNALBOX_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout),
		"how long in-flight requests get to finish [SIGNALBOX_SHUTDOWN_TIMEOUT]")
	f.Int64Var(&cfg.MaxSpecBytes, "max-spec-bytes", envInt("SIGNALBOX_MAX_SPEC_BYTES", cfg.MaxSpecBytes),
		"largest OpenAPI document the API accepts, in bytes [SIGNALBOX_MAX_SPEC_BYTES]")
	addLogFlags(f, &logFormat, &logLevel)
	return cmd
}

// newMigrateCommand applies migrations on demand, for installations that
// start the server with --migrate=false and migrate as a separate step.
func newMigrateCommand() *cobra.Command {
	var (
		databaseURL         string
		wait                time.Duration
		logFormat, logLevel string
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations and exit",
		Long: `Apply pending database migrations and exit.

The server migrates the database at startup unless --migrate=false is set.
Use this command when migrations run as a separate deployment step.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger, err := logging.New(cmd.ErrOrStderr(), logFormat, logLevel)
			if err != nil {
				return err
			}
			if databaseURL == "" {
				return fmt.Errorf("no database configured: set SIGNALBOX_DATABASE_URL or --database-url")
			}
			ctx := cmd.Context()
			pool, err := postgres.Open(ctx, databaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			waitCtx, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			if err := postgres.WaitReady(waitCtx, pool, logger); err != nil {
				return err
			}
			return postgres.Migrate(ctx, pool, logger)
		},
	}
	f := cmd.Flags()
	addDatabaseFlags(f, &databaseURL, &wait)
	addLogFlags(f, &logFormat, &logLevel)
	return cmd
}

func addDatabaseFlags(f *pflag.FlagSet, url *string, wait *time.Duration) {
	f.StringVar(url, "database-url", env("SIGNALBOX_DATABASE_URL", ""),
		"PostgreSQL URL, such as postgres://signalbox:password@localhost:5432/signalbox [SIGNALBOX_DATABASE_URL]")
	f.DurationVar(wait, "database-wait", envDuration("SIGNALBOX_DATABASE_WAIT", time.Minute),
		"how long startup waits for the database to answer [SIGNALBOX_DATABASE_WAIT]")
}

func addLogFlags(f *pflag.FlagSet, format, level *string) {
	f.StringVar(format, "log-format", env("SIGNALBOX_LOG_FORMAT", "json"),
		"log format: json or text [SIGNALBOX_LOG_FORMAT]")
	f.StringVar(level, "log-level", env("SIGNALBOX_LOG_LEVEL", "info"),
		"log level: debug, info, warn or error [SIGNALBOX_LOG_LEVEL]")
}

// newHealthcheckCommand probes the local ops listener. Container images
// without a shell or curl use it as their health check.
func newHealthcheckCommand() *cobra.Command {
	var url string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit with 0 when the local server is ready, for container health checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("%s: %w", url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("%s answered %s", url, resp.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", readyURL(env("SIGNALBOX_OPS_ADDR", app.DefaultConfig().OpsAddr)),
		"readiness URL to probe; defaults to /readyz on the ops address [SIGNALBOX_OPS_ADDR]")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "how long to wait for an answer")
	return cmd
}

// readyURL turns a listen address such as ":9090" into a URL on loopback.
func readyURL(opsAddr string) string {
	host, port, err := net.SplitHostPort(opsAddr)
	if err != nil {
		return "http://127.0.0.1:9090/readyz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/readyz"
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		fmt.Fprintf(os.Stderr, "signalbox-server: ignoring %s=%q: not a duration such as 5s\n", key, v)
	}
	return fallback
}

func envInt(key string, fallback int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
		fmt.Fprintf(os.Stderr, "signalbox-server: ignoring %s=%q: not a whole number\n", key, v)
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		fmt.Fprintf(os.Stderr, "signalbox-server: ignoring %s=%q: not true or false\n", key, v)
	}
	return fallback
}
