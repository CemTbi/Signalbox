// SPDX-License-Identifier: Apache-2.0

package agents

import "time"

// SetPickUp shortens how long jobs wait for their agent, for tests.
func SetPickUp(m *Module, d time.Duration) { m.pickUp = d }
