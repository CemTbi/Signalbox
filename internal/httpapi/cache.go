// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"container/list"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// revisionCache keeps what was most recently built from revisions, such as
// mocks and reference docs. A revision never changes, so nothing built
// from one goes stale.
type revisionCache[T any] struct {
	build func(content string) (T, error)
	mu    sync.Mutex
	max   int
	order *list.List // of uuid.UUID, most recent first
	items map[uuid.UUID]*list.Element
	built map[uuid.UUID]builtOrError[T]
	group singleflight.Group
}

type builtOrError[T any] struct {
	value T
	err   error
}

func newRevisionCache[T any](size int, build func(content string) (T, error)) *revisionCache[T] {
	return &revisionCache[T]{build: build, max: size, order: list.New(), items: map[uuid.UUID]*list.Element{}, built: map[uuid.UUID]builtOrError[T]{}}
}

// get returns what was built from a revision, building it once even when
// many requests ask at the same time. content loads the revision's
// document, and is called only when the revision isn't cached.
func (c *revisionCache[T]) get(revision uuid.UUID, content func() (string, error)) (T, error) {
	c.mu.Lock()
	if el, ok := c.items[revision]; ok {
		c.order.MoveToFront(el)
		b := c.built[revision]
		c.mu.Unlock()
		return b.value, b.err
	}
	c.mu.Unlock()
	v, err, _ := c.group.Do(revision.String(), func() (any, error) {
		text, err := content()
		if err != nil {
			// Loading failed, not building: try again next time.
			return nil, err
		}
		value, err := c.build(text)
		entry := builtOrError[T]{value: value, err: err}
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, ok := c.items[revision]; !ok {
			c.items[revision] = c.order.PushFront(revision)
			c.built[revision] = entry
			for c.order.Len() > c.max {
				oldest := c.order.Back()
				old, _ := oldest.Value.(uuid.UUID)
				c.order.Remove(oldest)
				delete(c.items, old)
				delete(c.built, old)
			}
		}
		return entry, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	entry, _ := v.(builtOrError[T])
	return entry.value, entry.err
}
