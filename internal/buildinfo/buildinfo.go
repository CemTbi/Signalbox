// SPDX-License-Identifier: Apache-2.0

// Package buildinfo reports which build of Signalbox is running.
//
// Release builds set the variables with -ldflags, for example:
//
//	-X github.com/CemTbi/Signalbox/internal/buildinfo.version=0.1.0
//
// Development builds fall back to the VCS information the Go toolchain embeds.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Set by the release build.
var (
	version = "0.0.0-dev"
	commit  = ""
	date    = ""
)

// Info describes the running build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"buildDate,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build information of the running binary.
func Get() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		BuildDate: date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info.Commit != "" {
		return info
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value[:min(12, len(s.Value))]
		case "vcs.time":
			if info.BuildDate == "" {
				info.BuildDate = s.Value
			}
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// String formats the build for version output, such as
// "0.1.0 (commit 1a2b3c4d5e6f, built 2026-10-16T12:00:00Z, go1.27.2 linux/amd64)".
func (i Info) String() string {
	s := i.Version + " ("
	if i.Commit != "" {
		s += "commit " + i.Commit
		if i.Modified {
			s += "-modified"
		}
		s += ", "
	}
	if i.BuildDate != "" {
		s += "built " + i.BuildDate + ", "
	}
	return s + i.GoVersion + " " + i.Platform + ")"
}
