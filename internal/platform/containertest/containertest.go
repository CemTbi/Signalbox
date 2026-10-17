// SPDX-License-Identifier: Apache-2.0

// Package containertest holds what the database test helpers share about
// starting containers with Testcontainers.
package containertest

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// RequireEnv names the environment variable that makes database tests fail
// instead of skipping when they can't run, as CI sets it.
const RequireEnv = "SIGNALBOX_REQUIRE_DATABASE_TESTS"

// DockerUnavailable returns why Testcontainers can't reach Docker, or "".
func DockerUnavailable(ctx context.Context) (reason string) {
	defer func() {
		if p := recover(); p != nil {
			reason = fmt.Sprint(p)
		}
	}()
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		return err.Error()
	}
	if err := provider.Health(ctx); err != nil {
		return err.Error()
	}
	return ""
}

// Run starts a container with run, and tries again when that fails.
// Starting fails now and then for reasons that don't last: the test
// binaries of one `go test` run share Testcontainers' reaper container,
// which one binary can find before it runs, and Docker Desktop on Windows
// sometimes can't bind the random port it picked. A short wait fixes both,
// and a container that can't start at all fails after four tries.
func Run[C testcontainers.Container](ctx context.Context, run func(context.Context) (C, error)) (C, error) {
	var (
		ctr C
		err error
	)
	for attempt := 1; attempt <= 4; attempt++ {
		ctr, err = run(ctx)
		if err == nil {
			return ctr, nil
		}
		_ = testcontainers.TerminateContainer(ctr) // a no-op for a nil container
		select {
		case <-ctx.Done():
			return ctr, err
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return ctr, err
}
