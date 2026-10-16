// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/CemTbi/Signalbox/internal/roles"
)

// Meta describes the running installation.
type Meta struct {
	Product    string       `json:"product"`
	Version    string       `json:"version"`
	Commit     string       `json:"commit,omitempty"`
	BuildDate  string       `json:"buildDate,omitempty"`
	APIVersion string       `json:"apiVersion"`
	Roles      []roles.Name `json:"roles"`
}

func metaHandler(d Deps) http.Handler {
	meta := Meta{
		Product:    "Signalbox",
		Version:    d.Build.Version,
		Commit:     d.Build.Commit,
		BuildDate:  d.Build.BuildDate,
		APIVersion: "v1",
		Roles:      d.Roles,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, meta)
	})
}
