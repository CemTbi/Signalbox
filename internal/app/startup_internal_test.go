// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
)

func TestStartupExplainsRiskySettings(t *testing.T) {
	tests := []struct {
		name      string
		publicURL string
		sso       identity.SSOConfig
		want      []string
		dontWant  []string
	}{
		{
			name:      "plain HTTP everywhere and no allowed domains",
			publicURL: "http://192.0.2.10:8080",
			sso:       identity.SSOConfig{Issuer: "http://dex.internal.example/dex"},
			want: []string{
				"Signalbox is served over plain HTTP",
				`"redirect_url":"http://192.0.2.10:8080/api/v1/sso/callback"`,
				"SIGNALBOX_OIDC_ALLOWED_DOMAINS",
				"issuer URL uses plain HTTP",
			},
		},
		{
			name:      "HTTPS and allowed domains",
			publicURL: "https://signalbox.example.com",
			sso:       identity.SSOConfig{Issuer: "https://idp.example.com", AllowedDomains: []string{"example.com"}},
			want:      []string{`"redirect_url":"https://signalbox.example.com/api/v1/sso/callback"`},
			dontWant:  []string{"plain HTTP", "SIGNALBOX_OIDC_ALLOWED_DOMAINS"},
		},
		{
			name:      "a provider on this machine",
			publicURL: "http://localhost:8080",
			sso:       identity.SSOConfig{Issuer: "http://127.0.0.1:5556/dex", AllowedDomains: []string{"example.com"}},
			dontWant:  []string{"plain HTTP"},
		},
		{
			name:      "no single sign-on",
			publicURL: "https://signalbox.example.com",
			dontWant:  []string{"single sign-on", "redirect_url"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			public, err := url.Parse(tt.publicURL)
			if err != nil {
				t.Fatal(err)
			}
			s := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil)), publicURL: public, api: &endpoint{}}
			if tt.sso.Issuer != "" {
				s.cfg.Identity.SSO = tt.sso
				s.cfg.Identity.SSO.RedirectURL = public.JoinPath("api/v1/sso/callback").String()
			}
			s.warnAboutPlainHTTP()
			s.describeSSO()
			out := logs.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("the startup log doesn't mention %q:\n%s", w, out)
				}
			}
			for _, w := range tt.dontWant {
				if strings.Contains(out, w) {
					t.Errorf("the startup log mentions %q:\n%s", w, out)
				}
			}
		})
	}
}
