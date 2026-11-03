// SPDX-License-Identifier: Apache-2.0

package telemetry

import "time"

// SetGraphNow sets the job's clock, for tests.
func SetGraphNow(j *GraphJob, now func() time.Time) { j.now = now }
