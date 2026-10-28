// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
)

// stepPrinter prints a run's steps as they end, for the cli reporter.
type stepPrinter struct {
	w          io.Writer
	iterations int
	iteration  int
}

func newStepPrinter(w io.Writer, name, environment string, iterations int) *stepPrinter {
	head := name
	if environment != "" {
		head += " · " + environment
	}
	if iterations > 1 {
		head += fmt.Sprintf(" · %d iterations", iterations)
	}
	fmt.Fprintf(w, "%s\n\n", head)
	return &stepPrinter{w: w, iterations: iterations, iteration: -1}
}

// step prints one request, its failure, and its failed tests and scripts.
func (p *stepPrinter) step(s report.Step) {
	if p.iterations > 1 && s.Iteration != p.iteration {
		if p.iteration >= 0 {
			fmt.Fprintln(p.w)
		}
		fmt.Fprintf(p.w, "Iteration %d of %d\n", s.Iteration+1, p.iterations)
	}
	p.iteration = s.Iteration
	name := s.Name
	if s.Path != "" {
		name = s.Path + " / " + name
	}
	mark, detail := "✓", ""
	switch {
	case s.Failed:
		mark = "✗"
	case s.Failure != nil:
		mark = "–"
	}
	switch {
	case s.Response != nil:
		detail = fmt.Sprintf("%d · %s", s.Response.Status, formatDuration(s.Duration))
	case s.Failure != nil && s.Failure.Code == "skipped":
		detail = "not sent"
	default:
		detail = "no response"
	}
	fmt.Fprintf(p.w, "  %s %s %s · %s\n", mark, s.Method, name, detail)
	if f := s.Failure; f != nil && f.Code != "skipped" {
		fmt.Fprintf(p.w, "      %s\n", f.Message)
	}
	if sc := s.Scripts; sc != nil {
		for _, e := range sc.Errors {
			event := "test"
			if e.Event == "prerequest" {
				event = "pre-request"
			}
			fmt.Fprintf(p.w, "      ✗ the %s script of %s stopped: %s: %s\n", event, e.From, e.Name, e.Message)
		}
		for _, t := range sc.Tests {
			if !t.Passed && !t.Skipped {
				fmt.Fprintf(p.w, "      ✗ %s\n", t.Name)
				if t.Error != "" {
					fmt.Fprintf(p.w, "        %s\n", t.Error)
				}
			}
		}
	}
	if s.Note != "" {
		fmt.Fprintf(p.w, "      %s\n", s.Note)
	}
	if len(s.Unresolved) > 0 {
		fmt.Fprintf(p.w, "      no value for {{%s}}\n", strings.Join(s.Unresolved, "}}, {{"))
	}
}

// summary prints the totals and how the run ended.
func (p *stepPrinter) summary(r report.Run, why string) {
	t := r.Totals
	fmt.Fprintf(p.w, "\n%s: %s, %s · %s\n", statusWord(r.Status), count(t.Requests, t.FailedRequests, "request"), count(t.Tests, t.FailedTests, "test"), formatDuration(r.Duration))
	if why != "" {
		fmt.Fprintln(p.w, why)
	}
}

func statusWord(status string) string {
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}

func count(n, failed int, noun string) string {
	s := plural(n, noun)
	if failed > 0 {
		s += fmt.Sprintf(" (%d failed)", failed)
	}
	return s
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// stoppedWhy explains a run that ended before its last step.
func stoppedWhy(stopped string) string {
	switch stopped {
	case "bail":
		return "The run stopped at the first failure (--bail)."
	case "steps":
		return "The run stopped at 10,000 requests, the most a run sends."
	case "time":
		return "The run stopped after an hour, the longest a run lasts."
	case "error":
		return "The run stopped on a server error."
	case "restart":
		return "The server stopped during the run."
	}
	return ""
}

// reporters reads --reporter: the cli reporter and the report files.
func reporters(list string) (cliOut bool, files []string, err error) {
	seen := map[string]bool{}
	for _, r := range strings.Split(list, ",") {
		r = strings.TrimSpace(r)
		switch r {
		case "":
		case "cli":
			cliOut = true
		case report.JUnit, report.JSON, report.HTML:
			if !seen[r] {
				files = append(files, r)
			}
		default:
			return false, nil, fmt.Errorf("unknown reporter %q: use cli, junit, json or html", r)
		}
		seen[r] = true
	}
	return cliOut, files, nil
}

// writeReports writes a report file per format into dir, named after the
// run, and returns their paths.
func writeReports(dir, name string, formats []string, write func(w io.Writer, format string) error) ([]string, error) {
	if len(formats) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	var paths []string
	for _, f := range formats {
		path := filepath.Join(dir, report.Slug(name)+"."+report.Ext(f))
		file, err := os.Create(path)
		if err != nil {
			return paths, err
		}
		err = write(file, f)
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return paths, fmt.Errorf("write %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}
