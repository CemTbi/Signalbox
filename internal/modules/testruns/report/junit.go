// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	ID        int         `xml:"id,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr,omitempty"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure"`
	Error     *junitProblem `xml:"error"`
	Skipped   *junitProblem `xml:"skipped"`
}

type junitProblem struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Text    string `xml:",chardata"`
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

// suiteName names a step for CI: its folders, its name, and its iteration
// when the run has more than one.
func suiteName(r Run, s Step) string {
	name := s.Name
	if s.Path != "" {
		name = s.Path + " / " + name
	}
	if r.Iterations > 1 {
		name += fmt.Sprintf(" (iteration %d)", s.Iteration+1)
	}
	return name
}

// writeJUnit writes a testsuite per step and a testcase per test. A
// request that got no response, or a script that failed, is an error; a
// test that failed is a failure.
func writeJUnit(w io.Writer, r Run) error {
	out := junitSuites{Name: r.Name, Time: seconds(r.Duration)}
	for _, s := range r.Steps {
		suite := junitSuite{Name: suiteName(r, s), ID: s.Seq, Time: seconds(s.Duration)}
		class := strings.ReplaceAll(suite.Name, " / ", ".")
		add := func(c junitCase) {
			c.ClassName = class
			if c.Time == "" {
				c.Time = "0.000"
			}
			suite.Cases = append(suite.Cases, c)
			suite.Tests++
			switch {
			case c.Error != nil:
				suite.Errors++
			case c.Failure != nil:
				suite.Failures++
			case c.Skipped != nil:
				suite.Skipped++
			}
		}
		if f := s.Failure; f != nil {
			c := junitCase{Name: s.Method + " " + s.URL, Time: seconds(s.Duration)}
			if f.Code == "skipped" {
				c.Skipped = &junitProblem{Message: f.Message}
			} else {
				c.Error = &junitProblem{Message: f.Message, Type: f.Code, Text: f.Message}
			}
			add(c)
		}
		if sc := s.Scripts; sc != nil {
			for _, e := range sc.Errors {
				msg := e.Name + ": " + e.Message
				add(junitCase{Name: fmt.Sprintf("%s script of %s", e.Event, e.From), Error: &junitProblem{Message: msg, Type: "script", Text: msg}})
			}
			for _, t := range sc.Tests {
				c := junitCase{Name: t.Name}
				switch {
				case t.Skipped:
					c.Skipped = &junitProblem{}
				case !t.Passed:
					c.Failure = &junitProblem{Message: t.Error, Type: "AssertionError", Text: t.Error}
				}
				add(c)
			}
		}
		if suite.Tests == 0 {
			// A request without tests still shows that it ran.
			add(junitCase{Name: s.Method + " " + s.URL, Time: seconds(s.Duration)})
		}
		out.Suites = append(out.Suites, suite)
		out.Tests += suite.Tests
		out.Failures += suite.Failures
		out.Errors += suite.Errors
		out.Skipped += suite.Skipped
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
