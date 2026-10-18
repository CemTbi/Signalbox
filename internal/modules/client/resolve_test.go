// SPDX-License-Identifier: Apache-2.0

package client

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
)

func TestResolverFillsInVariables(t *testing.T) {
	values := map[string]string{
		"baseUrl": "https://{{host}}/v1",
		"host":    "api.example.com",
		"token":   "s3cret-{{host}}",
		"loop":    "{{loop}}",
	}
	r := newResolver(values, []string{"token"}, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	got, err := r.request(runner.Request{
		Method: "GET", URL: "{{baseUrl}}/orders/{{ id }}",
		Headers: []runner.Header{{Key: "Authorization", Value: "Bearer {{token}}"}, {Key: "X-{{host}}", Value: "{{$timestamp}} {{$isoTimestamp}}"}},
		Body:    []byte(`{"n":{{$randomInt}},"id":"{{$guid}}","l":"{{loop}}","open":"{{"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://api.example.com/v1/orders/{{ id }}" {
		t.Errorf("url = %q", got.URL)
	}
	// Secrets are used as they are, even when they look like a template.
	if got.Headers[0].Value != "Bearer s3cret-{{host}}" {
		t.Errorf("authorization = %q", got.Headers[0].Value)
	}
	if got.Headers[1].Key != "X-api.example.com" || got.Headers[1].Value != "1792324800 2026-10-18T12:00:00.000Z" {
		t.Errorf("header = %+v", got.Headers[1])
	}
	body := string(got.Body)
	if !regexp.MustCompile(`^\{"n":\d+,"id":"[0-9a-f-]{36}","l":"\{\{loop\}\}","open":"\{\{"\}$`).MatchString(body) {
		t.Errorf("body = %s", body)
	}
	n, _ := strconv.Atoi(body[5:strings.IndexByte(body, ',')])
	if n < 0 || n > 1000 {
		t.Errorf("$randomInt = %d", n)
	}
	if !slices.Equal(r.Unresolved(), []string{"id", "loop"}) {
		t.Errorf("unresolved = %v", r.Unresolved())
	}
}

func TestResolverBoundsGrowth(t *testing.T) {
	// Each variable names the next twice: {{v0}} is 2^7 copies of a 64 KiB
	// value, 8 MiB.
	values := map[string]string{"v7": strings.Repeat("x", 64<<10)}
	for i := 6; i >= 0; i-- {
		next := "{{v" + strconv.Itoa(i+1) + "}}"
		values["v"+strconv.Itoa(i)] = next + next
	}
	r := newResolver(values, nil, time.Now())
	if _, err := r.request(runner.Request{Method: "POST", URL: "https://example.com/", Body: []byte("{{v0}}{{v0}}")}); !errors.Is(err, ErrResolvedTooLarge) {
		t.Errorf("err = %v", err)
	}
	// Short of the bound, it fills in.
	r = newResolver(values, nil, time.Now())
	got, err := r.request(runner.Request{Method: "POST", URL: "https://example.com/", Body: []byte("{{v3}}")})
	if err != nil || len(got.Body) != 16*64<<10 {
		t.Errorf("len = %d, %v", len(got.Body), err)
	}
}

func TestMaskerHidesSecrets(t *testing.T) {
	m := newMasker(map[string]string{"short": "abc", "long": "abcdef", "empty": "", "plain": "visible"}, []string{"short", "long", "empty"})
	if got := m.string("x=abcdef&y=abc&z=visible"); got != "x=•••&y=•••&z=visible" {
		t.Errorf("masked = %q", got)
	}
	none := newMasker(map[string]string{"plain": "visible"}, nil)
	if got := none.string("visible"); got != "visible" {
		t.Errorf("masked without secrets = %q", got)
	}
}
