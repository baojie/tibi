package render

import (
	"container/list"
	"sync"
)

// Cache is a size-bounded LRU for rendered HTML. Stored content is
// immutable, so entries never go stale.
type Cache struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	ll       *list.List
	m        map[string]*list.Element
}

type cacheEntry struct {
	key  string
	val  any
	size int
}

// NewCache returns a cache holding up to maxBytes of values.
func NewCache(maxBytes int) *Cache {
	return &Cache{maxBytes: maxBytes, ll: list.New(), m: map[string]*list.Element{}}
}

// Get returns the cached value for key.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*cacheEntry).val, true
	}
	return nil, false
}

// Put stores val (of approximate size bytes) under key.
func (c *Cache) Put(key string, val any, size int) {
	if size > c.maxBytes/4 {
		return // too big to be worth evicting everything else for
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		c.bytes -= e.Value.(*cacheEntry).size
		c.ll.Remove(e)
	}
	c.m[key] = c.ll.PushFront(&cacheEntry{key, val, size})
	c.bytes += size
	for c.bytes > c.maxBytes {
		e := c.ll.Back()
		ce := e.Value.(*cacheEntry)
		c.ll.Remove(e)
		delete(c.m, ce.key)
		c.bytes -= ce.size
	}
}
