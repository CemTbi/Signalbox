// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed specs/*.yaml
var specs embed.FS

// The shop's services, as the seed puts them on Signalbox's canvas.
var shopServices = []struct {
	name, slug, spec, description string
	x, y                          float64
}{
	{"Storefront", "storefront-web", "storefront.yaml", "The shop's web API, which browsers call.", 0, 160},
	{"Orders", "orders-api", "orders.yaml", "Places orders and keeps them in PostgreSQL.", 320, 60},
	{"Inventory", "inventory-api", "inventory.yaml", "Products and their stock.", 640, 0},
	{"Payments", "payments-api", "payments.yaml", "Charges orders through the payment provider.", 640, 220},
}

// seeder talks to Signalbox's API as a signed-in person, the way the web
// app does: with the session cookie, the public URL as Origin, and the
// session's CSRF token.
type seeder struct {
	base, origin string
	http         *http.Client
	csrf         string
}

// runSeed sets the demo shop up in Signalbox: a workspace and project,
// the shop's services with their published specs, and an ingest key for
// production, which it writes to SIGNALBOX_INGEST_KEY_FILE for the
// services. It signs in as DEMO_EMAIL; on a new installation it completes
// setup with SIGNALBOX_SETUP_CODE if that's set, and otherwise waits for
// someone to. Running it again reuses what's there and makes a new key.
func runSeed(ctx context.Context) error {
	jar, _ := cookiejar.New(nil)
	s := &seeder{
		base:   strings.TrimRight(env("SIGNALBOX_URL", "http://signalbox:8080"), "/"),
		origin: strings.TrimRight(env("SIGNALBOX_PUBLIC_URL", "http://localhost:8080"), "/"),
		http:   &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
	email, password := env("DEMO_EMAIL", "ada@example.com"), env("DEMO_PASSWORD", "correct horse battery staple")
	keyFile := env("SIGNALBOX_INGEST_KEY_FILE", "/run/demo/ingest-key")

	if err := s.waitReady(ctx); err != nil {
		return err
	}
	if err := s.setUpOrWait(ctx, email, password); err != nil {
		return err
	}
	if err := s.signIn(ctx, email, password); err != nil {
		return err
	}
	ws, err := s.find(ctx, "/api/v1/workspaces", "slug", "demo-shop", map[string]any{"name": "Demo shop", "slug": "demo-shop"})
	if err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	project, err := s.find(ctx, "/api/v1/workspaces/"+ws+"/projects", "name", "Shop",
		map[string]any{"name": "Shop", "description": "A small web shop that sends real traces: the demo of Signalbox."})
	if err != nil {
		return fmt.Errorf("the project: %w", err)
	}
	for _, svc := range shopServices {
		if err := s.service(ctx, project, svc.name, svc.slug, svc.spec, svc.description, svc.x, svc.y); err != nil {
			return fmt.Errorf("the %s service: %w", svc.slug, err)
		}
	}
	key, err := s.ingestKey(ctx, project)
	if err != nil {
		return fmt.Errorf("the ingest key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o755); err != nil { //nolint:gosec // G301: a shared volume
		return err
	}
	if err := os.WriteFile(keyFile, []byte(key+"\n"), 0o644); err != nil { //nolint:gosec // G306: the services read it as another user
		return err
	}
	slog.Info("the demo shop is set up", "workspace", "demo-shop", "project", "Shop", "key_file", keyFile)
	return nil
}

// do sends a request and decodes a JSON answer into out. An answer other
// than want is an *errStatus. 429s are waited out, as Retry-After says.
func (s *seeder) do(ctx context.Context, method, path string, in, out any, want int) error {
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if in != nil {
			b, err := json.Marshal(in)
			if err != nil {
				return err
			}
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, s.base+path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Origin", s.origin)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if s.csrf != "" {
			req.Header.Set("X-CSRF-Token", s.csrf)
		}
		resp, err := s.http.Do(req)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 10 {
			wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(max(wait, 1)) * time.Second):
			}
			continue
		}
		if resp.StatusCode != want {
			return &errStatus{status: resp.StatusCode, body: string(b)}
		}
		if out != nil {
			return json.Unmarshal(b, out)
		}
		return nil
	}
}

func (s *seeder) waitReady(ctx context.Context) error {
	for {
		if err := s.do(ctx, http.MethodGet, "/readyz", nil, nil, http.StatusOK); err == nil {
			return nil
		}
		slog.Info("waiting for Signalbox", "url", s.base)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// setUpOrWait completes first-run setup with the setup code, or waits for
// someone else to.
func (s *seeder) setUpOrWait(ctx context.Context, email, password string) error {
	code := os.Getenv("SIGNALBOX_SETUP_CODE")
	for logged := false; ; {
		var status struct {
			Required bool `json:"required"`
		}
		if err := s.do(ctx, http.MethodGet, "/api/v1/setup", nil, &status, http.StatusOK); err != nil {
			return fmt.Errorf("read whether setup is done: %w", err)
		}
		if !status.Required {
			return nil
		}
		if code != "" {
			err := s.do(ctx, http.MethodPost, "/api/v1/setup", map[string]string{
				"setupCode": code, "name": env("DEMO_NAME", "Ada Lovelace"), "email": email, "password": password,
			}, nil, http.StatusCreated)
			if err != nil {
				return fmt.Errorf("complete setup: %w", err)
			}
			slog.Info("completed first-run setup", "email", email)
			return nil
		}
		if !logged {
			slog.Info("waiting for someone to complete Signalbox's setup", "email", email)
			logged = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *seeder) signIn(ctx context.Context, email, password string) error {
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := s.do(ctx, http.MethodPost, "/api/v1/sessions", map[string]string{"email": email, "password": password}, &session, http.StatusCreated); err != nil {
		return fmt.Errorf("sign in as %s: %w", email, err)
	}
	s.csrf = session.CSRFToken
	return nil
}

// find returns the ID of the item in a list whose field has value, making
// it from create when there is none.
func (s *seeder) find(ctx context.Context, path, field, value string, create map[string]any) (string, error) {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet, path+"?limit=100", nil, &list, http.StatusOK); err != nil {
		return "", err
	}
	for _, item := range list.Data {
		if item[field] == value {
			return fmt.Sprint(item["id"]), nil
		}
	}
	var made map[string]any
	if err := s.do(ctx, http.MethodPost, path, create, &made, http.StatusCreated); err != nil {
		return "", err
	}
	return fmt.Sprint(made["id"]), nil
}

// service makes a service with its spec published as a version, unless
// it's there already.
func (s *seeder) service(ctx context.Context, project, name, slug, spec, description string, x, y float64) error {
	id, err := s.find(ctx, "/api/v1/projects/"+project+"/services", "slug", slug, map[string]any{
		"name": name, "slug": slug, "description": description, "telemetryServiceName": slug,
		"position": map[string]float64{"x": x, "y": y},
	})
	if err != nil {
		return err
	}
	var specs struct {
		Data []map[string]any `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet, "/api/v1/services/"+id+"/specs", nil, &specs, http.StatusOK); err != nil {
		return err
	}
	if len(specs.Data) > 0 {
		return nil
	}
	content, err := specFile(spec)
	if err != nil {
		return err
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := s.do(ctx, http.MethodPost, "/api/v1/services/"+id+"/specs", map[string]any{"name": name + " API", "content": content}, &made, http.StatusCreated); err != nil {
		return err
	}
	return s.do(ctx, http.MethodPost, "/api/v1/specs/"+made.ID+"/versions", map[string]any{"notes": "The demo shop's first release."}, nil, http.StatusCreated)
}

func specFile(name string) (string, error) {
	b, err := specs.ReadFile("specs/" + name)
	return string(b), err
}

// ingestKey makes a new key for production, revoking the ones earlier
// runs made, so only the running shop's key works.
func (s *seeder) ingestKey(ctx context.Context, project string) (string, error) {
	var envs struct {
		Data []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet, "/api/v1/projects/"+project+"/environments", nil, &envs, http.StatusOK); err != nil {
		return "", err
	}
	var production string
	for _, e := range envs.Data {
		if e.Slug == "production" {
			production = e.ID
		}
	}
	if production == "" {
		return "", errors.New("the project has no production environment")
	}
	keys := "/api/v1/environments/" + production + "/ingest-keys"
	var list struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet, keys, nil, &list, http.StatusOK); err != nil {
		return "", err
	}
	for _, k := range list.Data {
		if k.Name == "demo-shop" {
			if err := s.do(ctx, http.MethodDelete, "/api/v1/ingest-keys/"+k.ID, nil, nil, http.StatusNoContent); err != nil {
				return "", err
			}
		}
	}
	var made struct {
		Key string `json:"key"`
	}
	if err := s.do(ctx, http.MethodPost, keys, map[string]string{"name": "demo-shop"}, &made, http.StatusCreated); err != nil {
		return "", err
	}
	return made.Key, nil
}
