// SPDX-License-Identifier: Apache-2.0

package oidctest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeSendsPeopleOnlyToTheRegisteredAddress(t *testing.T) {
	p := New(t)
	const registered = "https://signalbox.test/api/v1/sso/callback"
	authorize := func(redirectURI string) *http.Response {
		t.Helper()
		q := url.Values{
			"client_id": {p.ClientID}, "response_type": {"code"}, "scope": {"openid email"}, "state": {"st"},
			"code_challenge_method": {"S256"}, "code_challenge": {"challenge"}, "redirect_uri": {redirectURI},
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.Issuer+"/authorize?"+q.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}

	if resp := authorize(registered); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("before a redirect URI is registered, authorize answered %s", resp.Status)
	}
	p.RegisterRedirectURI(registered)
	resp := authorize(registered)
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), registered+"?") {
		t.Errorf("the registered address got %s to %q", resp.Status, resp.Header.Get("Location"))
	}
	for _, other := range []string{
		"https://attacker.test/api/v1/sso/callback",
		"https://signalbox.test/api/v1/sso/callback?next=https://attacker.test",
		"https://signalbox.test/api/v1/sso/callback/",
		"",
	} {
		if resp := authorize(other); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("redirect_uri %q got %s to %q", other, resp.Status, resp.Header.Get("Location"))
		}
	}
}
