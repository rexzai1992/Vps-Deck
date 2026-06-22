package monitoring

import (
	"context"
	"sync"
	"time"
)

type snapshotCache[T any] struct {
	mu        sync.Mutex
	value     T
	hasValue  bool
	expiresAt time.Time
}

func (c *snapshotCache[T]) get(ctx context.Context, ttl time.Duration, collect func(context.Context) T) T {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if c.hasValue && now.Before(c.expiresAt) {
		return c.value
	}
	c.value = collect(ctx)
	c.hasValue = true
	c.expiresAt = now.Add(ttl)
	return c.value
}
