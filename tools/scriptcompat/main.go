// SPDX-License-Identifier: Apache-2.0

// Command scriptcompat runs Postman scripts taken from public collections in
// Signalbox's script sandbox and reports which would work. It is the tool
// behind spike 4 and ADR-0017, and isn't shipped.
//
//	scriptcompat cases.json
//
// cases.json is a list of {collection, event, name, source, request,
// response, collectionVariables}. Scripts whose collection saved an example
// response see it; the rest see a 200 response with an empty JSON object.
//
// Each script ends in one of these outcomes:
//
//	works        it ran to the end; its tests may pass or fail
//	data         it stopped on the stand-in response's data, such as a
//	             missing field; Postman would stop the same way
//	unsupported  it used an API or module the sandbox doesn't have
//	syntax       the engine couldn't parse it
//	timeout      it ran out of time
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/grafana/sobek"

	"github.com/CemTbi/Signalbox/internal/modules/scripts"
)

type testCase struct {
	Collection          string            `json:"collection"`
	Event               string            `json:"event"`
	Name                string            `json:"name"`
	Source              string            `json:"source"`
	Request             scripts.Request   `json:"request"`
	Response            *scripts.Response `json:"response"`
	CollectionVariables map[string]any    `json:"collectionVariables"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "scriptcompat:", err)
		os.Exit(1)
	}
}

// unsupportedPattern finds errors that mean the sandbox lacks something.
var unsupportedPattern = regexp.MustCompile(`isn't available|is not a function|is not defined|not supported|Object has no member`)

// classify sorts a finished script into an outcome. A ReferenceError is
// "unsupported?": it can name a global the sandbox lacks, or a helper the
// collection defines in another script, which only a person can tell apart.
func classify(res *scripts.Result) (outcome, detail string) {
	if res.Error == nil {
		return "works", ""
	}
	e := res.Error
	msg := e.Name + ": " + e.Message
	switch {
	case e.Name == "ReferenceError":
		return "unsupported?", msg
	case unsupportedPattern.MatchString(e.Message) && (strings.Contains(e.Message, "pm.") || strings.Contains(e.Message, "isn't available") || strings.Contains(e.Message, "postman")):
		return "unsupported", msg
	}
	return "data", msg
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: scriptcompat cases.json")
	}
	raw, err := os.ReadFile(os.Args[1]) //nolint:gosec // G703: the person running the tool names the file.
	if err != nil {
		return err
	}
	var cases []testCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}

	send := func(_ context.Context, req scripts.Request) (*scripts.Response, error) {
		return &scripts.Response{Code: 200, Status: "OK", Body: "{}"}, nil
	}
	pool := scripts.NewPool(2, scripts.Options{Timeout: 5 * time.Second, Send: send})
	defer pool.Close()

	// What a warm session costs in memory.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var warm []*scripts.Session
	for range 10 {
		s, err := scripts.NewSession(scripts.Options{})
		if err != nil {
			return err
		}
		if _, err := s.Run(context.Background(), scripts.Input{Event: "test", Source: "pm.expect(1).to.equal(1)",
			Response: &scripts.Response{Code: 200, Body: "{}"}}); err != nil {
			return err
		}
		warm = append(warm, s)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	fmt.Printf("memory per warm session: %.1f MB\n", float64(after.HeapAlloc-before.HeapAlloc)/10/1e6)
	runtime.KeepAlive(warm)

	counts := map[string]int{}
	// One session per collection, as in a collection run.
	sessions := map[string]*scripts.Session{}
	details := map[string]map[string]int{}
	var durations []time.Duration
	for _, c := range cases {
		outcome, detail := "", ""
		// Syntax first, so syntax errors aren't mixed with runtime ones.
		if _, err := sobek.Compile("script", "(function (__g) { with (__g) { "+c.Source+"\n} })", false); err != nil {
			outcome, detail = "syntax", firstLine(err.Error())
		} else {
			s := sessions[c.Collection]
			if s == nil {
				if s, err = pool.Session(); err != nil {
					return err
				}
				sessions[c.Collection] = s
			}
			resp := c.Response
			if resp == nil && c.Event == "test" {
				resp = &scripts.Response{Code: 200, Status: "OK", Body: "{}", ResponseTime: 120,
					Header: []scripts.Header{{Key: "Content-Type", Value: "application/json"}}}
			}
			start := time.Now()
			res, err := s.Run(context.Background(), scripts.Input{
				Event: c.Event, Source: c.Source, Request: c.Request, Response: resp,
				Variables: scripts.Variables{Collection: c.CollectionVariables},
				Info:      scripts.Info{EventName: c.Event, RequestName: c.Name, IterationCount: 1},
			})
			durations = append(durations, time.Since(start))
			switch {
			case errors.Is(err, scripts.ErrTimeout):
				outcome = "timeout"
			case err != nil:
				outcome, detail = "unsupported", firstLine(err.Error())
			default:
				outcome, detail = classify(res)
			}
		}
		counts[outcome]++
		if detail != "" {
			if details[outcome] == nil {
				details[outcome] = map[string]int{}
			}
			details[outcome][normalize(detail)]++
		}
	}

	fmt.Printf("%d scripts\n", len(cases))
	for _, o := range []string{"works", "data", "unsupported?", "unsupported", "syntax", "timeout"} {
		fmt.Printf("  %-13s %5d  %5.1f%%\n", o, counts[o], 100*float64(counts[o])/float64(len(cases)))
	}
	slices.Sort(durations)
	if n := len(durations); n > 0 {
		fmt.Printf("time per script in its collection's session: p50 %v, p95 %v, p99 %v, max %v\n",
			durations[n/2].Round(10*time.Microsecond), durations[n*95/100].Round(10*time.Microsecond),
			durations[n*99/100].Round(10*time.Microsecond), durations[n-1].Round(10*time.Microsecond))
	}
	for _, o := range []string{"unsupported?", "unsupported", "syntax", "data"} {
		if len(details[o]) == 0 {
			continue
		}
		fmt.Printf("\n%s, most common:\n", o)
		type kv struct {
			k string
			v int
		}
		var list []kv
		for k, v := range details[o] {
			list = append(list, kv{k, v})
		}
		slices.SortFunc(list, func(a, b kv) int { return cmp.Or(b.v-a.v, strings.Compare(a.k, b.k)) })
		for _, e := range list[:min(15, len(list))] {
			fmt.Printf("  %4d  %s\n", e.v, e.k)
		}
	}
	return nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

var (
	quoted = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	where  = regexp.MustCompile(` at .*$|\(line \d+ col \d+\)|script:\d+:\d+`)
)

// normalize groups errors that differ only in names or positions.
func normalize(s string) string {
	s = where.ReplaceAllString(s, "")
	if strings.HasPrefix(s, "ReferenceError") || strings.Contains(s, "isn't available") {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(quoted.ReplaceAllString(s, "…"))
}
