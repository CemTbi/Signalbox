// SPDX-License-Identifier: Apache-2.0

// Package logging builds the structured logger every role writes to.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New returns a logger that writes JSON or human-readable text at the given
// level. JSON is the default for servers because log pipelines parse it.
func New(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q: use debug, info, warn or error", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(format) {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("log format %q: use json or text", format)
}
