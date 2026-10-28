// SPDX-License-Identifier: Apache-2.0

package cli

import "time"

// PollFast makes runs on a server be asked for their progress often, and
// returns a function that undoes it.
func PollFast() func() {
	was := pollEvery
	pollEvery = 10 * time.Millisecond
	return func() { pollEvery = was }
}
