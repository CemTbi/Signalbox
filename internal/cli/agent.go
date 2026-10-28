// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/egress"
)

type agentOptions struct {
	server      string
	token       string
	name        string
	concurrency int
	deny        []string
}

func newAgentCommand() *cobra.Command {
	opts := agentOptions{}
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Send your requests from this machine, for a Signalbox server",
		Long: `Run an agent: the Signalbox server hands it the requests you send with
"Run on" set to it, and it sends them from this machine, so localhost, your
private networks and your VPN work. It sends only your requests, opens no
port, and asks the server for work over HTTPS.

Requests may reach any address this machine can; --deny keeps them from
address ranges. SIGNALBOX_SERVER and SIGNALBOX_TOKEN can stand in for
--server and --token.

It runs until stopped with Ctrl-C. Exit codes: 0 when stopped, 2 when it
can't run: a bad flag, or a server that refuses the token.`,
		Example: `  SIGNALBOX_TOKEN=sbx_pat_… signalbox agent --server https://signalbox.example.com
  signalbox agent --server https://signalbox.example.com --token sbx_pat_… --name work-laptop`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.server == "" {
				opts.server = os.Getenv("SIGNALBOX_SERVER")
			}
			if opts.token == "" {
				opts.token = os.Getenv("SIGNALBOX_TOKEN")
			}
			return runAgent(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.server, "server", "", "the Signalbox server (or SIGNALBOX_SERVER)")
	f.StringVar(&opts.token, "token", "", "a personal access token (or SIGNALBOX_TOKEN)")
	f.StringVar(&opts.name, "name", "", "the agent's name, as Run on shows it (default: this machine's name)")
	f.IntVar(&opts.concurrency, "concurrency", 8, "how many requests to send at once")
	f.StringArrayVar(&opts.deny, "deny", nil, "keep requests from this address range, such as 10.0.0.0/8 (repeatable)")
	return cmd
}

// agentJob is a request the server hands the agent.
type agentJob struct {
	ID        string `json:"id"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	Headers   []runner.Header `json:"headers"`
	Body      string          `json:"body"`
	TimeoutMs int64           `json:"timeoutMs"`
}

// Waits between attempts when the server can't be reached.
const (
	agentRetryMin = time.Second
	agentRetryMax = 30 * time.Second
)

func runAgent(ctx context.Context, stdout, stderr io.Writer, opts agentOptions) error {
	switch {
	case opts.server == "":
		return usage("give the server with --server, or set SIGNALBOX_SERVER")
	case opts.token == "":
		return usage("give a personal access token with --token, or set SIGNALBOX_TOKEN; create one under Access tokens")
	case opts.concurrency < 1 || opts.concurrency > 64:
		return usage("--concurrency must be from 1 to 64")
	}
	base, err := url.Parse(strings.TrimRight(opts.server, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return usage("--server %q isn't an http or https address", opts.server)
	}
	// It's the owner's machine: requests may go wherever it can reach,
	// except the ranges they deny.
	policy := egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}
	for _, d := range opts.deny {
		p, err := netip.ParsePrefix(d)
		if err != nil {
			return usage("--deny %q isn't an address range such as 10.0.0.0/8", d)
		}
		policy.Deny = append(policy.Deny, p)
	}
	name := opts.name
	host, _ := os.Hostname()
	if name == "" {
		name = host
	}
	if name == "" {
		name = "agent"
	}
	api := &apiClient{base: base, token: opts.token, http: &http.Client{Timeout: agentPollTimeout}}
	run := runner.New(&egress.Dialer{Policy: policy}, runner.Limits{Timeout: 5 * time.Minute}, "Signalbox-Agent/"+buildinfo.Get().Version)
	a := &agent{api: api, run: run, stdout: stdout, stderr: stderr, slots: make(chan struct{}, opts.concurrency)}
	in := map[string]string{"name": name, "hostname": host, "os": runtime.GOOS + "/" + runtime.GOARCH, "version": buildinfo.Get().Version}
	if err := a.connect(ctx, in); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Agent %s is connected to %s.\nRequests you send with Run on: %s go from this machine. Ctrl-C stops it.\n", name, base, name)
	err = a.serve(ctx, in)
	a.wg.Wait()
	if ctx.Err() != nil {
		fmt.Fprintln(stdout, "Stopped.")
		return nil
	}
	return err
}

// agentPollTimeout bounds one request for work: the server holds it for
// up to 25 seconds.
const agentPollTimeout = 40 * time.Second

type agent struct {
	api            *apiClient
	run            *runner.Runner
	stdout, stderr io.Writer
	id             string
	slots          chan struct{}
	wg             sync.WaitGroup
	mu             sync.Mutex
}

func (a *agent) connect(ctx context.Context, in map[string]string) error {
	var out struct {
		ID string `json:"id"`
	}
	if err := a.api.do(ctx, http.MethodPost, "/api/v1/agents", in, &out); err != nil {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("connect the agent: %w", err)}
	}
	a.id = out.ID
	return nil
}

// serve asks for work until ctx ends, and sends each request it gets.
func (a *agent) serve(ctx context.Context, in map[string]string) error {
	wait := agentRetryMin
	for ctx.Err() == nil {
		job, err := a.next(ctx)
		var ae *apiError
		switch {
		case err == nil:
			wait = agentRetryMin
			if job != nil {
				a.start(ctx, *job)
			}
			continue
		case ctx.Err() != nil:
			return nil
		case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("the server refused the token: %w", err)}
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			// Someone deleted the agent; it comes back as a new one.
			fmt.Fprintln(a.stderr, "The agent was deleted on the server; connecting again.")
			if err := a.connect(ctx, in); err != nil && ctx.Err() == nil {
				fmt.Fprintf(a.stderr, "signalbox: %v\n", err)
			}
		default:
			fmt.Fprintf(a.stderr, "signalbox: can't reach the server, trying again in %s: %v\n", wait, err)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil
		}
		wait = min(wait*2, agentRetryMax)
	}
	return nil
}

func (a *agent) next(ctx context.Context) (*agentJob, error) {
	resp, err := a.api.send(ctx, http.MethodPost, "/api/v1/agents/"+url.PathEscape(a.id)+"/jobs/next?waitMs=25000", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	var j agentJob
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return nil, fmt.Errorf("read a job: %w", err)
	}
	return &j, nil
}

// start sends a job's request in the background, when a slot is free.
func (a *agent) start(ctx context.Context, j agentJob) {
	select {
	case a.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() { <-a.slots }()
		a.handle(ctx, j)
	}()
}

func (a *agent) handle(ctx context.Context, j agentJob) {
	req := runner.Request{Method: j.Method, URL: j.URL, Headers: j.Headers, Timeout: time.Duration(j.TimeoutMs) * time.Millisecond}
	var result map[string]any
	if body, err := base64.StdEncoding.DecodeString(j.Body); err != nil {
		result = map[string]any{"failure": map[string]any{"code": "invalid-request", "message": "The agent couldn't read the request's body."}}
	} else {
		req.Body = body
		result = resultJSON(a.run.Run(ctx, req))
	}
	a.log(j, result)
	// The result goes back even if the agent is stopping, so the sender
	// isn't left waiting.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := a.api.do(rctx, http.MethodPut, "/api/v1/agents/"+url.PathEscape(a.id)+"/jobs/"+url.PathEscape(j.ID), result, nil); err != nil {
		fmt.Fprintf(a.stderr, "signalbox: couldn't return a result: %v\n", err)
	}
}

// log prints a request it sent, without its query, which may hold
// credentials.
func (a *agent) log(j agentJob, result map[string]any) {
	where := j.URL
	if u, err := url.Parse(j.URL); err == nil {
		u.RawQuery, u.User, u.Fragment = "", nil, ""
		where = u.String()
	}
	outcome := "no response"
	if r, ok := result["response"].(map[string]any); ok {
		outcome = fmt.Sprint(r["status"])
	} else if f, ok := result["failure"].(map[string]any); ok {
		outcome = fmt.Sprint(f["code"])
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprintf(a.stdout, "%s %s %s → %s\n", time.Now().Format("15:04:05"), j.Method, where, outcome)
}

func resultJSON(res runner.Result) map[string]any {
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	t := res.Timings
	out := map[string]any{"timings": map[string]float64{
		"dnsMs": ms(t.DNS), "connectMs": ms(t.Connect), "tlsMs": ms(t.TLS), "firstByteMs": ms(t.FirstByte), "downloadMs": ms(t.Download), "totalMs": ms(t.Total),
	}}
	if f := res.Failure; f != nil {
		out["failure"] = map[string]any{"code": f.Code, "message": f.Message}
	}
	if r := res.Response; r != nil {
		headers := make([]runner.Header, len(r.Headers))
		copy(headers, r.Headers)
		out["response"] = map[string]any{
			"status": r.Status, "statusText": r.StatusText, "proto": r.Proto, "headers": headers,
			"body": base64.StdEncoding.EncodeToString(r.Body), "url": r.URL, "redirects": r.Redirects,
			"remoteAddress": r.RemoteAddr, "truncated": r.Truncated,
		}
	}
	return out
}
