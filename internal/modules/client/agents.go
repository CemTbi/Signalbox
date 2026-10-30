// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/agents"
)

// Agents hands out runners that send through people's agents; the agents
// module is one.
type Agents interface {
	Runner(ctx context.Context, userID, agentID uuid.UUID) (agents.Runner, error)
}

// UseAgents lets sends and runs go through people's agents.
func (m *Module) UseAgents(a Agents) { m.agents = a }

// EngineFor returns the engine that sends for the user: the server's, or
// through one of the user's own agents when agentID isn't uuid.Nil.
func (m *Module) EngineFor(ctx context.Context, userID, agentID uuid.UUID) (*Engine, error) {
	e := m.Engine()
	if agentID == uuid.Nil {
		return e, nil
	}
	if m.agents == nil {
		return nil, ErrNotFound
	}
	r, err := m.agents.Runner(ctx, userID, agentID)
	if errors.Is(err, agents.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e.Through(r, r.Name()), nil
}
