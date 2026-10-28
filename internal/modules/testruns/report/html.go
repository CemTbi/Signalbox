// SPDX-License-Identifier: Apache-2.0

package report

import (
	"fmt"
	"html/template"
	"io"
	"time"
)

// writeHTML writes one page with no scripts and nothing to fetch, so it
// opens from a CI artifact. Failed steps start open.
func writeHTML(w io.Writer, r Run) error {
	return page.Execute(w, r)
}

func duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

var page = template.Must(template.New("report").Funcs(template.FuncMap{
	"duration": duration,
	"time":     func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") },
	"plus1":    func(i int) int { return i + 1 },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>{{.Name}}: {{.Status}}</title>
<style>
:root { --bg: #fff; --fg: #1b1f24; --muted: #59636e; --line: #d1d9e0; --card: #f6f8fa; --pass: #1a7f37; --fail: #cf222e; --skip: #9a6700; }
@media (prefers-color-scheme: dark) { :root { --bg: #0d1117; --fg: #e6edf3; --muted: #9198a1; --line: #30363d; --card: #151b23; --pass: #3fb950; --fail: #f85149; --skip: #d29922; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 1100px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 22px; margin: 0 0 4px; overflow-wrap: anywhere; }
.meta { color: var(--muted); margin: 0 0 20px; }
.status { display: inline-block; padding: 1px 10px; border-radius: 999px; font-weight: 600; font-size: 13px; border: 1px solid currentColor; text-transform: capitalize; }
.passed { color: var(--pass); } .failed, .canceled, .interrupted { color: var(--fail); } .skipped, .queued, .running { color: var(--skip); }
.totals { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; margin: 0 0 24px; }
.totals div { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 12px; }
.totals b { display: block; font-size: 22px; font-variant-numeric: tabular-nums; }
.totals span { color: var(--muted); font-size: 13px; }
details { border: 1px solid var(--line); border-radius: 8px; margin: 0 0 8px; background: var(--bg); }
summary { cursor: pointer; padding: 10px 12px; display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; }
summary .name { font-weight: 600; overflow-wrap: anywhere; }
summary .path, summary .right { color: var(--muted); }
summary .right { margin-left: auto; font-variant-numeric: tabular-nums; }
.body { padding: 0 12px 12px; border-top: 1px solid var(--line); }
.url { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 13px; overflow-wrap: anywhere; margin: 10px 0; }
ul { list-style: none; padding: 0; margin: 8px 0; }
li { padding: 2px 0; overflow-wrap: anywhere; }
li .why { color: var(--muted); display: block; padding-left: 22px; font-size: 13px; }
pre { background: var(--card); border: 1px solid var(--line); border-radius: 6px; padding: 8px; overflow: auto; max-height: 320px; font-size: 12px; white-space: pre-wrap; overflow-wrap: anywhere; }
h3 { font-size: 13px; margin: 12px 0 4px; color: var(--muted); font-weight: 600; }
footer { color: var(--muted); font-size: 12px; margin-top: 32px; }
</style>
</head>
<body>
<main>
<h1>{{.Name}}</h1>
<p class="meta"><span class="status {{.Status}}">{{.Status}}</span>
 Started {{time .StartedAt}} · {{duration .Duration}}{{if .Environment}} · Environment: {{.Environment}}{{end}} · {{.Iterations}} iteration{{if ne .Iterations 1}}s{{end}}{{if .Stopped}} · Stopped: {{.Stopped}}{{end}}</p>
<section class="totals">
<div><b>{{.Totals.Requests}}</b><span>requests</span></div>
<div><b class="{{if .Totals.FailedRequests}}failed{{else}}passed{{end}}">{{.Totals.FailedRequests}}</b><span>failed requests</span></div>
<div><b>{{.Totals.Tests}}</b><span>tests</span></div>
<div><b class="{{if .Totals.FailedTests}}failed{{else}}passed{{end}}">{{.Totals.FailedTests}}</b><span>failed tests</span></div>
</section>
{{$iterations := .Iterations}}
{{range .Steps}}
<details{{if .Failed}} open{{end}}>
<summary><span class="status {{if .Failed}}failed{{else if .Failure}}skipped{{else}}passed{{end}}">{{if .Failed}}failed{{else if .Failure}}skipped{{else}}passed{{end}}</span>
<span class="name">{{.Name}}</span>{{if .Path}}<span class="path">{{.Path}}</span>{{end}}
<span class="right">{{if gt $iterations 1}}iteration {{plus1 .Iteration}} · {{end}}{{if .Response}}{{.Response.Status}} · {{end}}{{duration .Duration}}</span></summary>
<div class="body">
<p class="url">{{.Method}} {{.URL}}</p>
{{with .Failure}}<p class="{{if eq .Code "skipped"}}skipped{{else}}failed{{end}}">{{.Message}}</p>{{end}}
{{with .Note}}<p class="skipped">{{.}}</p>{{end}}
{{with .Unresolved}}<p class="skipped">No value for: {{range $i, $n := .}}{{if $i}}, {{end}}{{$n}}{{end}}</p>{{end}}
{{with .Scripts}}
{{if .Tests}}<h3>Tests</h3><ul>{{range .Tests}}<li class="{{if .Skipped}}skipped{{else if .Passed}}passed{{else}}failed{{end}}">{{if .Skipped}}–{{else if .Passed}}✓{{else}}✗{{end}} {{.Name}}{{if .Error}}<span class="why">{{.Error}}</span>{{end}}</li>{{end}}</ul>{{end}}
{{if .Errors}}<h3>Script errors</h3><ul>{{range .Errors}}<li class="failed">{{.Event}} script of {{.From}}: {{.Name}}: {{.Message}}</li>{{end}}</ul>{{end}}
{{if .Logs}}<h3>Console</h3><pre>{{range .Logs}}[{{.Level}}] {{.Message}}
{{end}}</pre>{{end}}
{{end}}
{{with .Response}}{{if .Body}}<h3>Response body (start)</h3><pre>{{.Body}}</pre>{{end}}{{end}}
</div>
</details>
{{end}}
<footer>Signalbox test run {{.ID}}. Secret values are shown as •••.</footer>
</main>
</body>
</html>
`))
