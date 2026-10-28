// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
)

// pollEvery is how often a run on a server is asked for its progress.
var pollEvery = time.Second

// apiClient calls a Signalbox server's API with a personal access token.
type apiClient struct {
	base  *url.URL
	token string
	http  *http.Client
}

// apiError is a problem the server answered with.
type apiError struct {
	Status int
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Errors []struct {
		Pointer string `json:"pointer"`
		Detail  string `json:"detail"`
	} `json:"errors"`
}

func (e *apiError) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	for _, f := range e.Errors {
		msg += fmt.Sprintf("\n  %s: %s", f.Pointer, f.Detail)
	}
	return fmt.Sprintf("the server answered %d: %s", e.Status, msg)
}

func (c *apiClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	resp, err := c.send(ctx, method, path, reader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("read the server's answer to %s %s: %w", method, path, err)
	}
	return nil
}

// send sends a request, and turns an answer that isn't 2xx into an
// *apiError.
func (c *apiClient) send(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	u := c.base.JoinPath(strings.Split(path, "?")[0])
	if _, query, ok := strings.Cut(path, "?"); ok {
		u.RawQuery = query
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Signalbox-CLI/"+buildinfo.Get().Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		e := &apiError{Status: resp.StatusCode}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(e)
		return nil, e
	}
	return resp, nil
}

// serverRun is a run as the API returns it.
type serverRun struct {
	ID          string        `json:"id"`
	ProjectID   string        `json:"projectId"`
	Name        string        `json:"name"`
	Environment string        `json:"environment"`
	Status      string        `json:"status"`
	Stopped     string        `json:"stopped"`
	Iterations  int           `json:"iterations"`
	Totals      report.Totals `json:"totals"`
	DurationMs  float64       `json:"durationMs"`
}

func (r serverRun) finished() bool { return r.Status != report.Queued && r.Status != report.Running }

// serverStep is a step as the API returns it.
type serverStep struct {
	report.Step
	DurationMs float64 `json:"durationMs"`
}

// runOnServer runs a collection stored on a Signalbox server, there.
func runOnServer(ctx context.Context, stdout, stderr io.Writer, opts testRunOptions) error {
	rep, err := checkCommon(opts)
	if err != nil {
		return err
	}
	switch {
	case opts.server == "":
		return usage("--collection runs on a server: give --server, or set SIGNALBOX_SERVER")
	case opts.token == "":
		return usage("give a personal access token with --token, or set SIGNALBOX_TOKEN; create one under Access tokens")
	case len(opts.envVars) > 0, len(opts.allow) > 0, len(opts.deny) > 0:
		return usage("--env-var, --allow and --deny are for offline runs; on a server, environments and its egress policy apply")
	}
	base, err := url.Parse(strings.TrimRight(opts.server, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return usage("--server %q isn't an http or https address", opts.server)
	}
	api := &apiClient{base: base, token: opts.token, http: &http.Client{Timeout: time.Minute}}
	fail := func(what string, err error) error {
		if ctx.Err() != nil {
			return &ExitError{Code: ExitFindings, Err: errors.New("interrupted")}
		}
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("%s: %w", what, err)}
	}

	var col struct {
		ProjectID string `json:"projectId"`
	}
	if err := api.do(ctx, http.MethodGet, "/api/v1/collections/"+url.PathEscape(opts.collection), nil, &col); err != nil {
		return fail("find the collection", err)
	}
	body := map[string]any{"bail": opts.bail}
	if opts.iterations > 0 {
		body["iterations"] = opts.iterations
	}
	if opts.delayMs > 0 {
		body["delayMs"] = opts.delayMs
	}
	if opts.folder != "" {
		folderID, err := findFolder(ctx, api, opts.collection, opts.folder)
		if err != nil {
			return fail("find the folder", err)
		}
		body["folderId"] = folderID
	}
	if opts.environment != "" {
		envID, err := findEnvironment(ctx, api, col.ProjectID, opts.environment)
		if err != nil {
			return fail("find the environment", err)
		}
		body["environmentId"] = envID
	}
	if opts.data != "" {
		content, err := readLimited(opts.data, testruns.MaxDataBytes, "data file")
		if err != nil {
			return err
		}
		format := testruns.DataCSV
		if strings.HasSuffix(strings.ToLower(opts.data), ".json") {
			format = testruns.DataJSON
		}
		body["data"] = map[string]string{"format": format, "content": string(content)}
	}

	var run serverRun
	if err := api.do(ctx, http.MethodPost, "/api/v1/collections/"+url.PathEscape(opts.collection)+"/runs", body, &run); err != nil {
		return fail("start the run", err)
	}
	out := io.Discard
	if rep.cli {
		out = stdout
	}
	fmt.Fprintf(stderr, "Run %s: %s\n", run.ID, base.JoinPath("runs", run.ID))
	printer := newStepPrinter(out, run.Name, run.Environment, run.Iterations)
	gh := &githubReporter{w: stdout, iterations: run.Iterations}
	var failed []report.Step
	if err := follow(ctx, api, &run, func(s report.Step) {
		printer.step(s)
		if rep.github {
			gh.step(s)
		}
		if s.Failed {
			failed = append(failed, s)
		}
	}); err != nil {
		if ctx.Err() != nil {
			// Ctrl-C cancels the run on the server too.
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := api.do(cancelCtx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(run.ID)+"/cancel", nil, nil); err != nil {
				fmt.Fprintf(stderr, "signalbox: couldn't cancel the run: %v\n", err)
			} else {
				fmt.Fprintln(stderr, "Cancelled the run.")
			}
			return &ExitError{Code: ExitFindings, Err: errors.New("interrupted")}
		}
		return fail("follow the run", err)
	}
	done := report.Run{Name: run.Name, Status: run.Status, Totals: run.Totals, Iterations: run.Iterations, Stopped: run.Stopped,
		Duration: time.Duration(run.DurationMs * float64(time.Millisecond))}
	printer.summary(done, stoppedWhy(run.Stopped))
	if rep.github {
		if err := appendSummary(githubSummary(done, failed)); err != nil {
			fmt.Fprintf(stderr, "signalbox: couldn't write the job summary: %v\n", err)
		}
	}
	paths, werr := writeReports(opts.out, run.Name, rep.files, func(w io.Writer, format string) error {
		resp, err := api.send(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(run.ID)+"/report?format="+format, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(w, resp.Body)
		return err
	})
	for _, p := range paths {
		fmt.Fprintf(out, "Wrote %s\n", p)
	}
	if werr != nil {
		return fail("download the reports", werr)
	}
	if run.Status != report.Passed {
		return errRunFailed
	}
	return nil
}

// follow prints a run's steps as they end, until it finishes, and leaves
// the finished run in run.
func follow(ctx context.Context, api *apiClient, run *serverRun, step func(report.Step)) error {
	last := 0
	for {
		if err := api.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(run.ID), nil, run); err != nil {
			return err
		}
		// Every step that ended before the run was asked for is listed now.
		for more := true; more; {
			var page struct {
				Data []serverStep `json:"data"`
				More bool         `json:"more"`
			}
			if err := api.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(run.ID)+"/steps?limit=500&after="+strconv.Itoa(last), nil, &page); err != nil {
				return err
			}
			for _, s := range page.Data {
				s.Duration = time.Duration(s.DurationMs * float64(time.Millisecond))
				step(s.Step)
				last = s.Seq
			}
			more = page.More
		}
		if run.finished() {
			return nil
		}
		select {
		case <-time.After(pollEvery):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// findFolder returns a folder's ID from its ID or name.
func findFolder(ctx context.Context, api *apiClient, collectionID, folder string) (string, error) {
	var items struct {
		Data []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := api.do(ctx, http.MethodGet, "/api/v1/collections/"+url.PathEscape(collectionID)+"/items", nil, &items); err != nil {
		return "", err
	}
	for _, it := range items.Data {
		if it.Kind == "folder" && (it.ID == folder || it.Name == folder) {
			return it.ID, nil
		}
	}
	return "", fmt.Errorf("the collection has no folder %q", folder)
}

// findEnvironment returns an environment's ID from its ID, name or slug.
func findEnvironment(ctx context.Context, api *apiClient, projectID, env string) (string, error) {
	var envs struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"data"`
	}
	if err := api.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/environments", nil, &envs); err != nil {
		return "", err
	}
	var names []string
	for _, e := range envs.Data {
		if e.ID == env || strings.EqualFold(e.Name, env) || strings.EqualFold(e.Slug, env) {
			return e.ID, nil
		}
		names = append(names, e.Slug)
	}
	return "", fmt.Errorf("the project has no environment %q; it has %s", env, strings.Join(names, ", "))
}
