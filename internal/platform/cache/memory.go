package cache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// cacheItem represents an item in the cache
type cacheItem struct {
	key        string
	value      interface{}
	expiration time.Time
}

// MemoryCache implements an in-memory LRU cache with TTL support
type MemoryCache struct {
	maxSize int
	items   map[string]*list.Element
	lru     *list.List
	mu      sync.RWMutex
	stopCh  chan struct{}
}

// NewMemoryCache creates a new in-memory cache
func NewMemoryCache(maxSize int) *MemoryCache {
	if maxSize <= 0 {
		maxSize = 1000 // default max size
	}

	cache := &MemoryCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element),
		lru:     list.New(),
		stopCh:  make(chan struct{}),
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// Get retrieves a value from cache
func (c *MemoryCache) Get(ctx context.Context, key string) (interface{}, error) {
	c.mu.RLock()
	element, exists := c.items[key]
	c.mu.RUnlock()

	if !exists {
		return nil, ErrNotFound
	}

	item := element.Value.(*cacheItem)

	// Check if expired
	if time.Now().After(item.expiration) {
		// Remove expired item
		c.mu.Lock()
		c.remove(key)
		c.mu.Unlock()
		return nil, ErrNotFound
	}

	// Move to front (most recently used)
	c.mu.Lock()
	c.lru.MoveToFront(element)
	c.mu.Unlock()

	return item.value, nil
}

// Set stores a value in cache with TTL
func (c *MemoryCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	expiration := time.Now().Add(ttl)

	// Check if key already exists
	if element, exists := c.items[key]; exists {
		// Update existing item
		item := element.Value.(*cacheItem)
		item.value = value
		item.expiration = expiration
		c.lru.MoveToFront(element)
		return nil
	}

	// Create new item
	item := &cacheItem{
		key:        key,
		value:      value,
		expiration: expiration,
	}

	// Add to front of LRU list
	element := c.lru.PushFront(item)
	c.items[key] = element

	// Evict oldest if over capacity
	if c.lru.Len() > c.maxSize {
		c.evictOldest()
	}

	return nil
}

// Delete removes a key from cache
func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.remove(key)
	return nil
}

// Close closes the cache
func (c *MemoryCache) Close() error {
	close(c.stopCh)
	return nil
}

// remove removes an item (caller must hold lock)
func (c *MemoryCache) remove(key string) {
	if element, exists := c.items[key]; exists {
		c.lru.Remove(element)
		delete(c.items, key)
	}
}

// evictOldest removes the oldest item (caller must hold lock)
func (c *MemoryCache) evictOldest() {
	element := c.lru.Back()
	if element != nil {
		item := element.Value.(*cacheItem)
		c.remove(item.key)
	}
}

// cleanup periodically removes expired items
func (c *MemoryCache) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanupExpired()
		case <-c.stopCh:
			return
		}
	}
}

// cleanupExpired removes all expired items
func (c *MemoryCache) cleanupExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	toRemove := make([]string, 0)

	// Find expired items
	for key, element := range c.items {
		item := element.Value.(*cacheItem)
		if now.After(item.expiration) {
			toRemove = append(toRemove, key)
		}
	}

	// Remove expired items
	for _, key := range toRemove {
		c.remove(key)
	}
}

// Stats returns cache statistics
func (c *MemoryCache) Stats() (size int, maxSize int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items), c.maxSize
}
