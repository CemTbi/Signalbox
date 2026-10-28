// SPDX-License-Identifier: Apache-2.0

package apis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/pkg/contract"
)

// ContractVersion is a published version as contract tests come from it.
type ContractVersion struct {
	ProjectID uuid.UUID
	SpecID    uuid.UUID
	VersionID uuid.UUID
	SpecName  string
	// Name is the version's name, such as 1.4.0.
	Name    string
	Content []byte
}

// ContractVersion returns a published version and its document, for
// generating contract tests. Every member of the project may.
func (m *Module) ContractVersion(ctx context.Context, userID, versionID uuid.UUID) (ContractVersion, error) {
	if err := m.authorizeFor(ctx, userID, projectOfVersion, versionID, read); err != nil {
		return ContractVersion{}, err
	}
	cv := ContractVersion{VersionID: versionID}
	var content string
	err := m.db.QueryRow(ctx, `
		SELECT s.project_id, p.id, p.name, v.name, r.content
		FROM spec_versions v JOIN specs p ON p.id = v.spec_id JOIN services s ON s.id = p.service_id JOIN spec_revisions r ON r.id = v.revision_id
		WHERE v.id = $1`, versionID).Scan(&cv.ProjectID, &cv.SpecID, &cv.SpecName, &cv.Name, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractVersion{}, ErrNotFound
	}
	if err != nil {
		return ContractVersion{}, fmt.Errorf("get contract version: %w", err)
	}
	cv.Content = []byte(content)
	return cv, nil
}

// checkersKept is how many versions' checkers stay built.
const checkersKept = 16

// checkers keeps the checkers of the versions used last, since building
// one is much slower than checking a response.
type checkers struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*checkerEntry
	// order lists the versions, used last at the end.
	order []uuid.UUID
}

type checkerEntry struct {
	once    sync.Once
	project uuid.UUID
	checker *contract.Checker
	err     error
}

func (c *checkers) entry(versionID uuid.UUID) *checkerEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[uuid.UUID]*checkerEntry{}
	}
	for i, v := range c.order {
		if v == versionID {
			c.order = append(append(c.order[:i:i], c.order[i+1:]...), versionID)
			break
		}
	}
	e, ok := c.entries[versionID]
	if !ok {
		e = &checkerEntry{}
		c.entries[versionID] = e
		c.order = append(c.order, versionID)
		if len(c.order) > checkersKept {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
	}
	return e
}

// CheckContract checks a response to an operation of a published version
// of the project. The run that sent the request was authorized already;
// a version of another project, or one deleted since, fails the check.
func (m *Module) CheckContract(ctx context.Context, projectID, versionID uuid.UUID, op contract.Operation, req *http.Request, resp *http.Response) []contract.Result {
	e := m.contracts.entry(versionID)
	e.once.Do(func() {
		var content string
		err := m.db.QueryRow(ctx, `
			SELECT s.project_id, r.content
			FROM spec_versions v JOIN specs p ON p.id = v.spec_id JOIN services s ON s.id = p.service_id JOIN spec_revisions r ON r.id = v.revision_id
			WHERE v.id = $1`, versionID).Scan(&e.project, &content)
		if err != nil {
			e.err = err
			return
		}
		e.checker, e.err = contract.NewChecker([]byte(content))
	})
	name := op.String() + " follows the spec"
	switch {
	case errors.Is(e.err, pgx.ErrNoRows), e.err == nil && e.project != projectID:
		return []contract.Result{{Name: name, Error: "The spec version these contract tests come from is gone. Generate them again from a published version."}}
	case errors.Is(e.err, contract.ErrTooLarge):
		return []contract.Result{{Name: name, Error: "The spec's schemas expand to too much to check responses against."}}
	case e.err != nil:
		m.logger.WarnContext(ctx, "couldn't prepare contract checks", "error", e.err)
		// Don't keep a failure that may pass next time, such as a
		// cancelled query.
		m.contracts.forget(versionID, e)
		return []contract.Result{{Name: name, Error: "The response couldn't be checked against the spec. The server's log says why."}}
	}
	return e.checker.Check(op, req, resp)
}

func (c *checkers) forget(versionID uuid.UUID, e *checkerEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[versionID] == e {
		delete(c.entries, versionID)
		for i, v := range c.order {
			if v == versionID {
				c.order = append(c.order[:i:i], c.order[i+1:]...)
				break
			}
		}
	}
}
