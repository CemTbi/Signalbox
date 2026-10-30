// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"time"

	"github.com/google/uuid"
)

// SetPickUp shortens how long jobs wait for their agent, for tests.
func SetPickUp(m *Module, d time.Duration) { m.pickUp = d }

// SetGrace shortens how long past its timeout a job's result may take.
func SetGrace(m *Module, d time.Duration) { m.grace = d }

// Queued is how many jobs an agent may have waiting.
const Queued = queued

// Waiting counts the jobs in an agent's queue, given up or not.
func Waiting(m *Module, agentID uuid.UUID) int { return len(m.queue(agentID)) }
