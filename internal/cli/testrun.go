// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/scripts"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
	"github.com/CemTbi/Signalbox/internal/platform/egress"
)

type testRunOptions struct {
	environment string
	data        string
	folder      string
	iterations  int
	delayMs     int
	bail        bool
	envVars     []string
	reporters   string
	out         string
	timeoutMs   int
	allow       []string
	deny        []string
	server      string
	token       string
	collection  string
}

func newTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run API test suites",
	}
	cmd.AddCommand(newTestRunCommand())
	return cmd
}

func newTestRunCommand() *cobra.Command {
	opts := testRunOptions{}
	cmd := &cobra.Command{
		Use:   "run [collection.json]",
		Short: "Run a collection's requests and tests, offline or on a Signalbox server",
		Long: `Run a collection as a test suite: its requests in order, with their
pre-request and test scripts, any number of iterations, optionally once
per row of a data file.

Offline, it runs a Postman collection file (v2.1 or v2.0) with an optional
Postman environment file, and needs no Signalbox server. Requests may reach
this machine and private networks; cloud metadata addresses stay refused
unless --allow opens them.

With --server and --collection, it runs a collection stored on a Signalbox
server, there, and prints its requests as they end. SIGNALBOX_SERVER and
SIGNALBOX_TOKEN can stand in for --server and --token.

Exit codes: 0 when every request got a response and every test passed, 1
when something failed or the run was cancelled, 2 when the run couldn't
start: a bad flag, an unreadable file or a server that refused.`,
		Example: `  signalbox test run shop.postman_collection.json -e staging.postman_environment.json
  signalbox test run shop.json -d users.csv --reporter cli,junit --out reports
  signalbox test run shop.json --folder Orders --env-var baseUrl=http://localhost:8080 --bail
  SIGNALBOX_TOKEN=sbx_pat_… signalbox test run --server https://signalbox.example.com \
    --collection col_01j9z3w5y7a9c1e3g5j7m9p1r3 --environment staging -r cli,junit`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && cmd.Flags().Changed("server") {
				return usage("--server runs a stored collection: use it with --collection, not a file")
			}
			if opts.server == "" {
				opts.server = os.Getenv("SIGNALBOX_SERVER")
			}
			if opts.token == "" {
				opts.token = os.Getenv("SIGNALBOX_TOKEN")
			}
			switch {
			case len(args) == 1 && opts.collection != "":
				return usage("give a collection file to run offline, or --collection to run on a server, not both")
			case len(args) == 1:
				return runOffline(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], opts)
			case opts.collection != "":
				return runOnServer(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
			default:
				return usage("give a Postman collection file, or --collection with --server")
			}
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.environment, "environment", "e", "", "offline: a Postman environment file; on a server: an environment's ID, name or slug")
	f.StringVarP(&opts.data, "data", "d", "", "a CSV or JSON data file: one iteration per row")
	f.StringVar(&opts.folder, "folder", "", "run only this folder: its name, or on a server its ID")
	f.IntVarP(&opts.iterations, "iterations", "n", 0, "how many times to run the requests (default: once per data row, or once)")
	f.IntVar(&opts.delayMs, "delay-request", 0, "milliseconds to wait between requests")
	f.BoolVar(&opts.bail, "bail", false, "stop at the first request that fails or has a failing test")
	f.StringArrayVar(&opts.envVars, "env-var", nil, "offline: set an environment variable, as key=value (repeatable)")
	f.StringVarP(&opts.reporters, "reporter", "r", "cli", "reporters: cli, junit, json and html, comma-separated")
	f.StringVarP(&opts.out, "out", "o", ".", "the directory for junit, json and html reports")
	f.IntVar(&opts.timeoutMs, "timeout-request", 30000, "offline: milliseconds each request may take")
	f.StringArrayVar(&opts.allow, "allow", nil, "offline: let requests reach this address range, such as 169.254.0.0/16 (repeatable)")
	f.StringArrayVar(&opts.deny, "deny", nil, "offline: keep requests from this address range, such as 10.0.0.0/8 (repeatable)")
	f.StringVar(&opts.server, "server", "", "the Signalbox server to run on (or SIGNALBOX_SERVER)")
	f.StringVar(&opts.token, "token", "", "a personal access token for the server (or SIGNALBOX_TOKEN)")
	f.StringVar(&opts.collection, "collection", "", "on a server: the collection's ID, such as col_01j9z3w5y7a9c1e3g5j7m9p1r3")
	return cmd
}

func usage(format string, args ...any) error {
	return &ExitError{Code: ExitUsage, Err: fmt.Errorf(format, args...)}
}

// errRunFailed ends a run that ran but didn't pass. Its message is empty:
// the run's output already says what failed.
var errRunFailed = &ExitError{Code: ExitFindings, Err: errors.New("")}

func readLimited(path string, limit int64, what string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, usage("read the %s: %v", what, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, usage("read the %s: %v", what, err)
	}
	if int64(len(data)) > limit {
		return nil, usage("the %s %s is larger than %d MiB", what, path, limit>>20)
	}
	return data, nil
}

func readData(path string) ([]map[string]any, error) {
	if path == "" {
		return nil, nil
	}
	content, err := readLimited(path, testruns.MaxDataBytes, "data file")
	if err != nil {
		return nil, err
	}
	format := testruns.DataCSV
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		format = testruns.DataJSON
	}
	rows, err := testruns.ParseData(format, content)
	if err != nil {
		return nil, usage("%s: %v", path, err)
	}
	return rows, nil
}

func checkCommon(opts testRunOptions) (cliOut bool, files []string, err error) {
	if opts.iterations < 0 || opts.iterations > client.MaxIterations {
		return false, nil, usage("--iterations must be from 1 to %d", client.MaxIterations)
	}
	if opts.delayMs < 0 || opts.delayMs > int(client.MaxRunDelay.Milliseconds()) {
		return false, nil, usage("--delay-request must be from 0 to 60000 milliseconds")
	}
	cliOut, files, err = reporters(opts.reporters)
	if err != nil {
		return false, nil, usage("%v", err)
	}
	return cliOut, files, nil
}

// runOffline runs a Postman collection file on this machine.
func runOffline(ctx context.Context, stdout, stderr io.Writer, path string, opts testRunOptions) error {
	cliOut, files, err := checkCommon(opts)
	if err != nil {
		return err
	}
	if opts.timeoutMs < 1 || opts.timeoutMs > 300_000 {
		return usage("--timeout-request must be from 1 to 300000 milliseconds")
	}
	policy := egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}}
	for _, a := range opts.allow {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return usage("--allow %q isn't an address range such as 169.254.0.0/16", a)
		}
		policy.Allow = append(policy.Allow, p)
	}
	for _, d := range opts.deny {
		p, err := netip.ParsePrefix(d)
		if err != nil {
			return usage("--deny %q isn't an address range such as 10.0.0.0/8", d)
		}
		policy.Deny = append(policy.Deny, p)
	}

	content, err := readLimited(path, convert.MaxFileSize, "collection")
	if err != nil {
		return err
	}
	col, rep, err := convert.ReadPostmanCollection(content)
	if err != nil {
		return usage("%s: %v", path, err)
	}
	for _, n := range rep.Notes {
		if n.Level != convert.Info {
			where := ""
			if n.Item != "" {
				where = n.Item + ": "
			}
			fmt.Fprintf(stderr, "note: %s%s\n", where, n.Message)
		}
	}
	plan, err := client.PlanCollection(col, opts.folder)
	if errors.Is(err, client.ErrNoFolder) {
		return usage("the collection has no folder named %q", opts.folder)
	}
	if err != nil {
		return err
	}
	if len(plan.Steps) == 0 {
		return usage("there are no requests to run")
	}

	env := map[string]string{}
	var secrets []string
	envName := ""
	if opts.environment != "" {
		data, err := readLimited(opts.environment, convert.MaxFileSize, "environment")
		if err != nil {
			return err
		}
		e, _, err := convert.ReadPostmanEnvironment(data)
		if err != nil {
			return usage("%s: %v", opts.environment, err)
		}
		envName = e.Name
		for _, v := range e.Variables {
			env[v.Key] = v.Value
			if v.Secret {
				secrets = append(secrets, v.Key)
			}
		}
	}
	for _, kv := range opts.envVars {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return usage("--env-var %q isn't key=value", kv)
		}
		env[k] = v
	}
	data, err := readData(opts.data)
	if err != nil {
		return err
	}
	iterations := opts.iterations
	if iterations == 0 {
		iterations = max(len(data), 1)
	}
	if len(plan.Steps)*iterations > client.MaxRunSteps {
		return usage("a run sends at most %d requests: %d requests × %d iterations is more", client.MaxRunSteps, len(plan.Steps), iterations)
	}

	run := runner.New(&egress.Dialer{Policy: policy}, runner.Limits{Timeout: time.Duration(opts.timeoutMs) * time.Millisecond}, "Signalbox-CLI/"+buildinfo.Get().Version)
	var engine *client.Engine
	pool := scripts.NewPool(1, scripts.Options{Send: func(ctx context.Context, req scripts.Request) (*scripts.Response, error) {
		return engine.ScriptSender()(ctx, req)
	}})
	defer pool.Close()
	engine = client.NewEngine(run, pool)

	out := io.Discard
	if cliOut {
		out = stdout
	}
	printer := newStepPrinter(out, plan.Name, envName, iterations)
	r := report.Run{Name: plan.Name, Environment: envName, Iterations: iterations, StartedAt: time.Now()}
	stopped, err := engine.Run(ctx, plan, client.RunOptions{
		Iterations: iterations, Data: data, Environment: env, Secrets: secrets,
		Delay: time.Duration(opts.delayMs) * time.Millisecond, Bail: opts.bail,
	}, func(res client.StepResult) error {
		s := report.FromResult(len(r.Steps)+1, res, plan)
		r.Steps = append(r.Steps, s)
		r.Totals.Add(s)
		printer.step(s)
		return nil
	})
	r.Duration = time.Since(r.StartedAt)
	r.Stopped = stopped
	switch {
	case err == nil && r.Totals.FailedRequests == 0:
		r.Status = report.Passed
	case err == nil:
		r.Status = report.Failed
	case ctx.Err() != nil:
		r.Status = report.Canceled
	default:
		return err
	}
	if r.Steps == nil {
		r.Steps = []report.Step{}
	}
	printer.summary(r, stoppedWhy(stopped))
	paths, werr := writeReports(opts.out, plan.Name, files, func(w io.Writer, format string) error { return report.Write(w, format, r) })
	for _, p := range paths {
		fmt.Fprintf(out, "Wrote %s\n", p)
	}
	if werr != nil {
		return usage("%v", werr)
	}
	if r.Status != report.Passed {
		return errRunFailed
	}
	return nil
}
