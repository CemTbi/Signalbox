// SPDX-License-Identifier: Apache-2.0

package egress

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrResponseTooLarge is returned while reading a response body that is
// longer than Limits.MaxResponseBytes.
var ErrResponseTooLarge = errors.New("egress: the response is larger than the limit")

// Limits bound what one request may cost.
type Limits struct {
	// Timeout covers the whole request: connecting, every redirect, and
	// reading the body. Default 30 seconds.
	Timeout time.Duration
	// MaxResponseBytes caps each response body. Default 10 MB.
	MaxResponseBytes int64
	// MaxRedirects caps the redirects followed. Default 10; -1 follows
	// none and returns the redirect itself.
	MaxRedirects int
}

func (l Limits) withDefaults() Limits {
	if l.Timeout <= 0 {
		l.Timeout = 30 * time.Second
	}
	if l.MaxResponseBytes <= 0 {
		l.MaxResponseBytes = 10 << 20
	}
	if l.MaxRedirects == 0 {
		l.MaxRedirects = 10
	}
	return l
}

// Client returns an HTTP client that connects only through d and stays
// within l. It ignores the HTTP_PROXY family of environment variables:
// through a proxy, the policy would check the proxy instead of the
// destination. Every redirect is checked like the first request, because
// it dials again.
func (d *Dialer) Client(l Limits) *http.Client {
	l = l.withDefaults()
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            d.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
	return &http.Client{
		Transport: limitBody{next: transport, max: l.MaxResponseBytes},
		Timeout:   l.Timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if l.MaxRedirects < 0 {
				return http.ErrUseLastResponse
			}
			if len(via) > l.MaxRedirects {
				return fmt.Errorf("egress: stopped after %d redirects", l.MaxRedirects)
			}
			return nil
		},
	}
}

// limitBody caps response bodies.
type limitBody struct {
	next http.RoundTripper
	max  int64
}

func (t limitBody) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: %d bytes, the limit is %d", ErrResponseTooLarge, resp.ContentLength, t.max)
	}
	resp.Body = &cappedBody{rc: resp.Body, left: t.max}
	return resp, nil
}

type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// One more byte would exceed the limit; check whether there is one.
		var one [1]byte
		if n, _ := b.rc.Read(one[:]); n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }
