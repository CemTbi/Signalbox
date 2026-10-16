// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/CemTbi/Signalbox/pkg/specengine"
)

// specCheckTimeout bounds how long one document may take to check.
const specCheckTimeout = 30 * time.Second

// specChecksHandler validates and lints a document sent as the request body
// without storing it. The editor calls this as people type.
//
// The document is untrusted input, so file and remote references stay
// disabled: a $ref must never make the server read its own disk or fetch a
// URL on the caller's behalf.
func specChecksHandler(d Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ruleSets := r.URL.Query().Get("ruleSets")
		if !r.URL.Query().Has("ruleSets") {
			ruleSets = string(specengine.RuleSetRecommended)
		}
		sets, err := specengine.ParseRuleSets(ruleSets)
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "invalid-parameter", "ruleSets: "+err.Error())
			return
		}

		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, d.MaxSpecBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeProblem(w, r, http.StatusRequestEntityTooLarge, "document-too-large",
					fmt.Sprintf("The document is larger than %d MiB.", d.MaxSpecBytes>>20))
				return
			}
			writeProblem(w, r, http.StatusBadRequest, "unreadable-body", "The request body couldn't be read.")
			return
		}
		if len(bytes.TrimSpace(data)) == 0 {
			writeProblem(w, r, http.StatusBadRequest, "empty-body",
				"Send the OpenAPI document, as YAML or JSON, as the request body.")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), specCheckTimeout)
		defer cancel()
		report, err := specengine.Check(ctx, data, specengine.Options{SourceName: "request body"}, sets...)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			writeProblem(w, r, http.StatusUnprocessableEntity, "check-timeout",
				fmt.Sprintf("Checking the document took longer than %s.", specCheckTimeout))
			return
		case errors.Is(err, context.Canceled):
			return // the client went away
		case err != nil:
			d.Logger.ErrorContext(r.Context(), "spec check failed", "error", err, "request_id", RequestID(r.Context()))
			writeProblem(w, r, http.StatusInternalServerError, "internal-error",
				"The document couldn't be checked. Quote the request ID when you report it.")
			return
		}
		writeJSON(w, http.StatusOK, report)
	})
}
