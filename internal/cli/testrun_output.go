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

// reporting is what --reporter asks for.
type reporting struct {
	cli, github bool
	// files are the report files' formats.
	files []string
}

// reporters reads --reporter: the cli and github reporters, and the
// report files.
func reporters(list string) (reporting, error) {
	var r reporting
	seen := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "":
		case "cli":
			r.cli = true
		case "github":
			r.github = true
		case report.JUnit, report.JSON, report.HTML:
			if !seen[name] {
				r.files = append(r.files, name)
			}
		default:
			return reporting{}, fmt.Errorf("unknown reporter %q: use cli, github, junit, json or html", name)
		}
		seen[name] = true
	}
	return r, nil
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

// maxAnnotations bounds the annotations a run writes; GitHub shows only
// the first few of a step anyway.
const maxAnnotations = 50

// githubReporter writes GitHub Actions annotations for failed requests as
// they end, and a summary for the job's page.
type githubReporter struct {
	w           io.Writer
	iterations  int
	annotations int
}

// step annotates a failed request: why it got no response, and each
// script error and failed test.
func (g *githubReporter) step(s report.Step) {
	if !s.Failed {
		return
	}
	title := stepTitle(s, g.iterations)
	note := func(msg string) {
		if g.annotations >= maxAnnotations {
			return
		}
		g.annotations++
		fmt.Fprintf(g.w, "::error title=%s::%s\n", escapeProperty(title), escapeData(msg))
	}
	if f := s.Failure; f != nil && f.Code != "skipped" {
		note(f.Message)
	}
	if sc := s.Scripts; sc != nil {
		for _, e := range sc.Errors {
			note(fmt.Sprintf("The %s script of %s stopped: %s: %s", eventName(e.Event), e.From, e.Name, e.Message))
		}
		for _, t := range sc.Tests {
			if !t.Passed && !t.Skipped {
				msg := t.Name
				if t.Error != "" {
					msg += ": " + t.Error
				}
				note(msg)
			}
		}
	}
}

func eventName(event string) string {
	if event == "prerequest" {
		return "pre-request"
	}
	return "test"
}

func stepTitle(s report.Step, iterations int) string {
	name := s.Method + " " + s.Name
	if s.Path != "" {
		name = s.Method + " " + s.Path + " / " + s.Name
	}
	if iterations > 1 {
		name += fmt.Sprintf(" (iteration %d)", s.Iteration+1)
	}
	return name
}

// maxSummarySteps bounds the failed requests a summary lists.
const maxSummarySteps = 50

// githubSummary is the Markdown a run adds to its job's summary: the
// outcome, the totals, and the failed requests with their failed tests.
func githubSummary(r report.Run, failed []report.Step) string {
	var b strings.Builder
	mark := "✅"
	if r.Status != report.Passed {
		mark = "❌"
	}
	fmt.Fprintf(&b, "### %s %s: %s\n\n", mark, markdownText(r.Name), statusWord(r.Status))
	t := r.Totals
	b.WriteString("| Requests | Failed requests | Tests | Failed tests | Time |\n|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&b, "| %d | %d | %d | %d | %s |\n\n", t.Requests, t.FailedRequests, t.Tests, t.FailedTests, formatDuration(r.Duration))
	if why := stoppedWhy(r.Stopped); why != "" {
		b.WriteString(why + "\n\n")
	}
	for i, s := range failed {
		if i == maxSummarySteps {
			fmt.Fprintf(&b, "…and %d more failed requests.\n", len(failed)-maxSummarySteps)
			break
		}
		fmt.Fprintf(&b, "- **%s**", markdownText(stepTitle(s, r.Iterations)))
		if s.Response != nil {
			fmt.Fprintf(&b, " · %d", s.Response.Status)
		}
		b.WriteString("\n")
		if f := s.Failure; f != nil && f.Code != "skipped" {
			fmt.Fprintf(&b, "  - %s\n", markdownText(f.Message))
		}
		if sc := s.Scripts; sc != nil {
			for _, e := range sc.Errors {
				fmt.Fprintf(&b, "  - The %s script of %s stopped: %s\n", eventName(e.Event), markdownText(e.From), markdownText(e.Name+": "+e.Message))
			}
			for _, x := range sc.Tests {
				if !x.Passed && !x.Skipped {
					line := markdownText(x.Name)
					if x.Error != "" {
						line += ": " + markdownText(x.Error)
					}
					fmt.Fprintf(&b, "  - ✗ %s\n", line)
				}
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

var markdownEscaper = strings.NewReplacer(`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`, `|`, `\|`, "<", "&lt;", ">", "&gt;", "\r", " ", "\n", " ")

// markdownText keeps text from a run, such as a test's name, from being
// read as Markdown or HTML.
func markdownText(s string) string {
	if len(s) > 1000 {
		s = s[:1000] + "…"
	}
	return markdownEscaper.Replace(s)
}

// appendSummary adds a run's summary to the file GitHub Actions shows on
// the job's page, when there is one.
func appendSummary(summary string) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G703: the Actions runner names the file, for us to write.
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, summary)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
