// Package cache provides a small in-memory TTL cache with a size limit.
package cache

import (
	"sync"
	"time"
)

type item struct {
	value   any
	expires time.Time
}

// Cache is a concurrency-safe in-memory TTL cache.
type Cache struct {
	mu         sync.Mutex
	items      map[string]item
	maxEntries int
	ttl        time.Duration
	stop       chan struct{}
}

// New creates a cache with the given max entries and default TTL. If maxEntries
// is <= 0 it defaults to 256. A background goroutine periodically evicts
// expired entries; it is stopped via Close.
func New(maxEntries int, ttl time.Duration) *Cache {
	if maxEntries <= 0 {
		maxEntries = 256
	}
	c := &Cache{
		items:      make(map[string]item),
		maxEntries: maxEntries,
		ttl:        ttl,
		stop:       make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

// Get returns the cached value for key and whether it was present and fresh.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(it.expires) {
		delete(c.items, key)
		return nil, false
	}
	return it.value, true
}

// Set stores value for key with the default TTL, evicting expired entries and
// the oldest entry if the cache is full.
func (c *Cache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	// Opportunistically drop expired entries to reclaim space.
	for k, it := range c.items {
		if now.After(it.expires) {
			delete(c.items, k)
		}
	}
	if len(c.items) >= c.maxEntries {
		c.evictOldest(now)
	}
	c.items[key] = item{value: value, expires: now.Add(c.ttl)}
}

// evictOldest removes the entry closest to expiring when the cache is full.
func (c *Cache) evictOldest(now time.Time) {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, it := range c.items {
		if first || it.expires.Before(oldest) {
			oldestKey = k
			oldest = it.expires
			first = false
		}
	}
	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}

func (c *Cache) evictLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-ticker.C:
			c.mu.Lock()
			for k, it := range c.items {
				if now.After(it.expires) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// Len returns the current number of live entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	n := 0
	for _, it := range c.items {
		if now.Before(it.expires) {
			n++
		}
	}
	return n
}

// Close stops the background eviction goroutine.
func (c *Cache) Close() {
	close(c.stop)
}
