// SPDX-License-Identifier: Apache-2.0

package convert

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrNotCurl means the text isn't a curl command Signalbox reads.
var ErrNotCurl = errors.New("convert: not a curl command")

// MaxCurl bounds a curl command.
const MaxCurl = 1 << 20

// splitShell splits a command line as a POSIX shell does: single quotes,
// double quotes with backslash escapes, $'…' with C escapes, backslashes,
// and backslash-newline continuations. A command copied from a browser as
// "cURL (cmd)", with ^ escapes, is read too.
func splitShell(s string) ([]string, error) {
	if strings.Contains(s, `^"`) || strings.Contains(s, "^\n") || strings.Contains(s, "^\r\n") {
		s = uncaret(s)
	}
	var (
		words []string
		cur   strings.Builder
		in    bool // in a word
	)
	end := func() {
		if in {
			words = append(words, cur.String())
			cur.Reset()
			in = false
		}
	}
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\':
			if i+1 < len(r) && r[i+1] == '\n' {
				i++
				continue
			}
			if i+2 < len(r) && r[i+1] == '\r' && r[i+2] == '\n' {
				i += 2
				continue
			}
			if i+1 < len(r) {
				i++
				cur.WriteRune(r[i])
				in = true
			}
		case c == '\'':
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			if j >= len(r) {
				return nil, fmt.Errorf("%w: a single quote isn't closed", ErrNotCurl)
			}
			cur.WriteString(string(r[i+1 : j]))
			in, i = true, j
		case c == '$' && i+1 < len(r) && r[i+1] == '\'':
			j := i + 2
			for j < len(r) && r[j] != '\'' {
				if r[j] == '\\' && j+1 < len(r) {
					var s string
					s, j = cEscape(r, j+1)
					cur.WriteString(s)
					continue
				}
				cur.WriteRune(r[j])
				j++
			}
			if j >= len(r) {
				return nil, fmt.Errorf("%w: a $' quote isn't closed", ErrNotCurl)
			}
			in, i = true, j
		case c == '"':
			j := i + 1
			for j < len(r) && r[j] != '"' {
				if r[j] == '\\' && j+1 < len(r) && strings.ContainsRune("\"\\$`\n", r[j+1]) {
					j++
					if r[j] != '\n' {
						cur.WriteRune(r[j])
					}
				} else {
					cur.WriteRune(r[j])
				}
				j++
			}
			if j >= len(r) {
				return nil, fmt.Errorf("%w: a double quote isn't closed", ErrNotCurl)
			}
			in, i = true, j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			end()
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	end()
	return words, nil
}

// uncaret undoes cmd.exe's ^ escapes and ^ line continuations.
func uncaret(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] == '^' && i+1 < len(r) {
			i++
			if r[i] == '\r' && i+1 < len(r) && r[i+1] == '\n' {
				i++
			}
			if r[i] == '\n' {
				b.WriteRune(' ')
				continue
			}
		}
		b.WriteRune(r[i])
	}
	return b.String()
}

// cEscape reads the escape at r[i], just after a backslash in $'…', as
// bash does, and returns what it stands for and the index after it. A
// browser's Copy as cURL writes characters such as ! as !.
func cEscape(r []rune, i int) (string, int) {
	simple := map[rune]string{
		'a': "\a", 'b': "\b", 'e': "\x1b", 'E': "\x1b", 'f': "\f", 'n': "\n", 'r': "\r", 't': "\t", 'v': "\v",
		'\\': `\`, '\'': "'", '"': `"`, '?': "?",
	}
	if s, ok := simple[r[i]]; ok {
		return s, i + 1
	}
	// digits reads up to limit digits of base from r[from:].
	digits := func(from, limit int, base rune) (rune, int) {
		var v rune
		j := from
		for j < len(r) && j-from < limit {
			d := hexValue(r[j])
			if d < 0 || d >= base {
				break
			}
			v = v*base + d
			j++
		}
		return v, j
	}
	switch c := r[i]; {
	case c >= '0' && c <= '7':
		v, j := digits(i, 3, 8)
		return string([]byte{byte(v & 0xff)}), j //nolint:gosec // G115: masked to a byte, as bash does with \777
	case c == 'x' || c == 'u' || c == 'U':
		limit := map[rune]int{'x': 2, 'u': 4, 'U': 8}[c]
		v, j := digits(i+1, limit, 16)
		switch {
		case j == i+1:
			return `\` + string(c), i + 1
		case c == 'x':
			return string([]byte{byte(v)}), j //nolint:gosec // G115: two hex digits fit a byte
		default:
			return string(v), j
		}
	case c == 'c' && i+1 < len(r):
		return string(r[i+1] & 0x1f), i + 2
	default:
		// Bash keeps the backslash of an escape it doesn't know.
		return `\` + string(c), i + 1
	}
}

func hexValue(c rune) rune {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

// Options curl reads that change nothing about the request Signalbox
// sends, and whether each takes a value.
var ignoredOptions = map[string]bool{
	"-s": false, "--silent": false, "-S": false, "--show-error": false, "-v": false, "--verbose": false, "-i": false, "--include": false,
	"-L": false, "--location": false, "--compressed": false, "-f": false, "--fail": false, "--fail-with-body": false, "-N": false,
	"--no-buffer": false, "-g": false, "--globoff": false, "--http1.1": false, "--http2": false, "--http2-prior-knowledge": false,
	"--path-as-is": false, "-#": false, "--progress-bar": false, "-o": true, "--output": true, "-O": false, "--remote-name": false,
	"-w": true, "--write-out": true, "--retry": true, "--retry-delay": true, "--retry-max-time": true, "-m": true, "--max-time": true,
	"--connect-timeout": true, "--max-redirs": true, "-D": true, "--dump-header": true, "--trace": true, "--trace-ascii": true,
}

// Options curl reads that Signalbox can't do; the request is sent without
// them.
var unsupportedOptions = map[string]bool{
	"-k": false, "--insecure": false, "-x": true, "--proxy": true, "-U": true, "--proxy-user": true, "--cacert": true, "--capath": true,
	"-E": true, "--cert": true, "--key": true, "--cert-type": true, "--resolve": true, "--connect-to": true, "--interface": true,
	"--unix-socket": true, "--digest": false, "--ntlm": false, "--negotiate": false, "--aws-sigv4": true, "-c": true, "--cookie-jar": true,
	"-T": true, "--upload-file": true, "-4": false, "-6": false, "--ipv4": false, "--ipv6": false,
}

// curlRequest collects what a command says before it becomes a request.
type curlRequest struct {
	method   string
	url      string
	headers  []Header
	data     []string
	form     []formField
	get      bool
	head     bool
	user     string
	hasUser  bool
	bearer   string
	jsonBody bool
}

// ReadCurl reads a curl command, such as one a browser's "Copy as cURL"
// gives, as a request.
func ReadCurl(command string) (Request, Report, error) {
	if len(command) > MaxCurl {
		return Request{}, Report{}, fmt.Errorf("%w: it is longer than 1 MiB", ErrNotCurl)
	}
	words, err := splitShell(strings.TrimSpace(command))
	if err != nil {
		return Request{}, Report{}, err
	}
	if len(words) > 0 && (words[0] == "curl" || strings.HasSuffix(words[0], "/curl") || strings.EqualFold(words[0], "curl.exe")) {
		words = words[1:]
	}
	words = normalize(words)
	var (
		r  Report
		cr curlRequest
	)
	value := func(i *int, opt string) (string, error) {
		if *i+1 >= len(words) {
			return "", fmt.Errorf("%w: %s needs a value", ErrNotCurl, opt)
		}
		*i++
		return words[*i], nil
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		opt := ""
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			opt = w
		}
		if opt == "" {
			if cr.url == "" {
				cr.url = w
			} else {
				r.add("", Differs, "Only the first URL is imported; %q is left out.", w)
			}
			continue
		}
		get := func() (string, error) { return value(&i, opt) }
		var v string
		switch opt {
		case "-X", "--request":
			if v, err = get(); err == nil {
				cr.method = strings.ToUpper(v)
			}
		case "-H", "--header":
			if v, err = get(); err == nil {
				cr.header(&r, v)
			}
		case "-d", "--data", "--data-ascii", "--data-binary", "--data-raw":
			if v, err = get(); err == nil {
				if strings.HasPrefix(v, "@") && opt != "--data-raw" {
					r.add("", Omitted, "The data from the file %s wasn't imported; files aren't read.", v[1:])
					continue
				}
				cr.data = append(cr.data, v)
			}
		case "--data-urlencode":
			if v, err = get(); err == nil {
				cr.data = append(cr.data, urlencodeData(&r, v))
			}
		case "--json":
			if v, err = get(); err == nil {
				cr.data, cr.jsonBody = append(cr.data, v), true
			}
		case "-F", "--form", "--form-string":
			if v, err = get(); err == nil {
				name, val, _ := strings.Cut(v, "=")
				if opt != "--form-string" && (strings.HasPrefix(val, "@") || strings.HasPrefix(val, "<")) {
					r.add("", Omitted, "The form field %q from a file wasn't imported; files aren't read.", name)
					continue
				}
				cr.form = append(cr.form, formField{name: name, value: val})
			}
		case "-u", "--user":
			if v, err = get(); err == nil {
				cr.user, cr.hasUser = v, true
			}
		case "--oauth2-bearer":
			cr.bearer, err = get()
		case "-A", "--user-agent":
			if v, err = get(); err == nil {
				cr.headers = append(cr.headers, Header{Key: "User-Agent", Value: v, Enabled: true})
			}
		case "-e", "--referer":
			if v, err = get(); err == nil {
				cr.headers = append(cr.headers, Header{Key: "Referer", Value: v, Enabled: true})
			}
		case "-b", "--cookie":
			if v, err = get(); err == nil {
				if strings.Contains(v, "=") {
					cr.headers = append(cr.headers, Header{Key: "Cookie", Value: v, Enabled: true})
				} else {
					r.add("", Omitted, "The cookies from the file %s weren't imported; files aren't read.", v)
				}
			}
		case "-G", "--get":
			cr.get = true
		case "-I", "--head":
			cr.head = true
		case "--url":
			if v, err = get(); err == nil {
				cr.url = v
			}
		default:
			if takes, ok := ignoredOptions[opt]; ok {
				if takes {
					i++
				}
				continue
			}
			if takes, ok := unsupportedOptions[opt]; ok {
				if takes {
					i++
				}
				r.add("", Differs, "curl's %s option isn't supported, so the request is sent without it.", opt)
				continue
			}
			r.add("", Differs, "curl's %s option isn't known to Signalbox, so it was ignored.", opt)
		}
		if err != nil {
			return Request{}, Report{}, err
		}
	}
	if cr.url == "" {
		return Request{}, Report{}, fmt.Errorf("%w: it has no URL", ErrNotCurl)
	}
	req := cr.build(&r)
	if len(req.URL) > MaxURL || len(req.Body) > MaxBody || len(req.Headers) > MaxHeaders {
		return Request{}, Report{}, fmt.Errorf("%w: the request is larger than Signalbox saves", ErrNotCurl)
	}
	r.Requests = 1
	return req, r, nil
}

// shortWithValue are curl's one-letter options that take a value.
const shortWithValue = "XHdFuAebowmDxUETc"

// valueOptions are curl's long options that take a value.
var valueOptions = map[string]bool{
	"--request": true, "--header": true, "--data": true, "--data-ascii": true, "--data-binary": true, "--data-raw": true,
	"--data-urlencode": true, "--json": true, "--form": true, "--form-string": true, "--user": true, "--oauth2-bearer": true,
	"--user-agent": true, "--referer": true, "--cookie": true, "--url": true,
}

func takesValue(opt string) bool {
	if len(opt) == 2 && opt[0] == '-' {
		return strings.IndexByte(shortWithValue, opt[1]) >= 0
	}
	return valueOptions[opt] || ignoredOptions[opt] || unsupportedOptions[opt]
}

// normalize splits options so each word is one option or one value:
// -sSL becomes -s -S -L, -XPOST becomes -X POST, and --request=POST
// becomes --request POST.
func normalize(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		// A value, such as -d -1, isn't an option.
		if n := len(out); n > 0 && takesValue(out[n-1]) && (n < 2 || !takesValue(out[n-2])) {
			out = append(out, w)
			continue
		}
		switch {
		case w == "--" || w == "-":
			out = append(out, w)
		case strings.HasPrefix(w, "--"):
			if name, v, ok := strings.Cut(w, "="); ok {
				out = append(out, name, v)
			} else {
				out = append(out, w)
			}
		case strings.HasPrefix(w, "-") && len(w) > 2:
			for j := 1; j < len(w); j++ {
				c := w[j]
				out = append(out, "-"+string(c))
				if strings.IndexByte(shortWithValue, c) >= 0 {
					if j+1 < len(w) {
						out = append(out, w[j+1:])
					}
					break
				}
			}
		default:
			out = append(out, w)
		}
	}
	return out
}

func (cr *curlRequest) header(r *Report, v string) {
	if strings.HasPrefix(v, "@") {
		r.add("", Omitted, "The headers from the file %s weren't imported; files aren't read.", v[1:])
		return
	}
	key, val, ok := strings.Cut(v, ":")
	key = strings.TrimSpace(key)
	switch {
	case ok:
		val = strings.TrimSpace(val)
		if val == "" {
			// "Name:" removes a header curl would send; Signalbox sends none
			// of curl's.
			return
		}
		cr.headers = append(cr.headers, Header{Key: key, Value: val, Enabled: true})
	case strings.HasSuffix(key, ";"):
		cr.headers = append(cr.headers, Header{Key: strings.TrimSuffix(key, ";"), Value: "", Enabled: true})
	}
}

func urlencodeData(r *Report, v string) string {
	name, content, hasEq := strings.Cut(v, "=")
	switch {
	case !hasEq && strings.HasPrefix(v, "@"), hasEq && strings.HasPrefix(content, "@") && name != "":
		r.add("", Omitted, "The data from a file in --data-urlencode %s wasn't imported; files aren't read.", v)
		return ""
	case !hasEq:
		return url.QueryEscape(v)
	case name == "":
		return url.QueryEscape(content)
	default:
		return name + "=" + url.QueryEscape(content)
	}
}

func (cr *curlRequest) build(r *Report) Request {
	req := Request{Method: cr.method, URL: cr.url, Headers: cr.headers}
	if req.Headers == nil {
		req.Headers = []Header{}
	}
	if !strings.Contains(req.URL, "://") && !strings.HasPrefix(req.URL, "{{") {
		req.URL = "http://" + req.URL
		r.add("", Info, "The URL has no scheme, so it uses http://, as curl does.")
	}
	data := strings.Join(cr.data, "&")
	switch {
	case cr.get && len(cr.data) > 0:
		sep := "?"
		if strings.Contains(req.URL, "?") {
			sep = "&"
		}
		req.URL += sep + data
	case len(cr.form) > 0:
		body, contentType := multipartBody(cr.form)
		req.Body = body
		setDefault(&req, "Content-Type", contentType, true)
	case len(cr.data) > 0:
		req.Body = data
		if cr.jsonBody {
			setDefault(&req, "Content-Type", "application/json", false)
			setDefault(&req, "Accept", "application/json", false)
		} else {
			setDefault(&req, "Content-Type", "application/x-www-form-urlencoded", false)
		}
	}
	if req.Method == "" {
		switch {
		case cr.head:
			req.Method = "HEAD"
		case req.Body != "":
			req.Method = "POST"
		default:
			req.Method = "GET"
		}
	}
	if cr.hasUser {
		user, pass, ok := strings.Cut(cr.user, ":")
		if !ok {
			r.add("", Differs, "curl would ask for the password of %s; the request has none, so add it under Auth.", user)
		}
		req.Auth = &Auth{Type: "basic", Username: user, Password: pass}
	}
	if cr.bearer != "" {
		req.Auth = &Auth{Type: "bearer", Token: cr.bearer}
	}
	if a := req.Auth; a != nil {
		// Auth replaces an Authorization header, so keep the explicit one
		// only when there's no auth.
		for i, h := range req.Headers {
			if strings.EqualFold(h.Key, "Authorization") {
				req.Headers = append(req.Headers[:i:i], req.Headers[i+1:]...)
				break
			}
		}
	}
	return req
}

// setDefault adds a header unless the request has it. replace says to
// replace it instead: a multipart body needs its own boundary.
func setDefault(req *Request, name, value string, replace bool) {
	for i, h := range req.Headers {
		if strings.EqualFold(h.Key, name) {
			if replace && !strings.Contains(h.Value, "boundary=") {
				req.Headers[i].Value = value
			}
			return
		}
	}
	req.Headers = append(req.Headers, Header{Key: name, Value: value, Enabled: true})
}
