// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// Contracts generates contract tests from published versions.
type Contracts interface {
	GenerateContractTests(ctx context.Context, userID, versionID uuid.UUID, opts client.ContractOptions) (client.Collection, client.ContractReport, error)
}

// ContractTestsJSON is what generating contract tests made.
type ContractTestsJSON struct {
	Collection CollectionJSON     `json:"collection"`
	Report     ContractReportJSON `json:"report"`
}

// ContractReportJSON says what generating contract tests did.
type ContractReportJSON struct {
	Created  bool `json:"created"`
	Requests int  `json:"requests"`
	Replaced int  `json:"replaced"`
}

func (a *api) generateContractTests(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	versionID, ok := pathID(w, r, "versionId", id.Version, "version")
	if !ok {
		return
	}
	// The version's hosted mock is the base URL when the spec names no
	// server.
	v, err := a.APIs.GetVersion(r.Context(), s.User.ID, versionID)
	if err != nil {
		a.apisError(w, r, "version", err)
		return
	}
	mock := a.PublicURL.JoinPath("mocks", id.Format(id.Spec, v.SpecID), v.Name).String()
	c, report, err := a.Contracts.GenerateContractTests(r.Context(), s.User.ID, versionID, client.ContractOptions{MockURL: mock})
	var bad *client.ContractError
	switch {
	case errors.As(err, &bad):
		writeProblem(w, r, http.StatusUnprocessableEntity, "not-contract-testable",
			"This version's spec can't make contract tests: only OpenAPI 3.0, 3.1 and 3.2 documents can.")
		return
	case errors.Is(err, client.ErrNoContracts):
		writeProblem(w, r, http.StatusServiceUnavailable, "contracts-unavailable", "This server can't generate contract tests.")
		return
	case errors.Is(err, client.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow generating contract tests. Ask a workspace owner or admin to make you an editor.")
		return
	case err != nil:
		a.collectionsError(w, r, "version", err)
		return
	}
	out := ContractTestsJSON{Collection: collectionJSON(c), Report: ContractReportJSON(report)}
	status := http.StatusOK
	if report.Created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/collections/"+out.Collection.ID)
	}
	writeJSON(w, status, out)
}
