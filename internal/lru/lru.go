// Package lru is a byte-bounded, in-memory cache of rendered images
// (previews, thumbnails made on demand) that evicts the least recently used
// and coalesces concurrent renders of the same key.
package lru

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

// Cache is safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	capacity int
	size     int
	ll       *list.List
	items    map[string]*list.Element
	inflight map[string]*flight
}

type entry struct {
	key  string
	data []byte
}

type flight struct {
	done chan struct{}
	data []byte
	err  error
}

// New makes a cache holding up to capacity bytes.
func New(capacity int) *Cache {
	return &Cache{capacity: capacity, ll: list.New(), items: map[string]*list.Element{}, inflight: map[string]*flight{}}
}

// Get returns the cached bytes for key, or renders, caches and returns
// them. Callers asking for a key already being rendered wait for that
// render; if it was cancelled (its caller went away) they render it
// themselves.
func (c *Cache) Get(key string, render func() ([]byte, error)) ([]byte, error) {
	for {
		c.mu.Lock()
		if el, ok := c.items[key]; ok {
			c.ll.MoveToFront(el)
			data := el.Value.(*entry).data
			c.mu.Unlock()
			return data, nil
		}
		f, ok := c.inflight[key]
		if !ok {
			break // with c.mu held
		}
		c.mu.Unlock()
		<-f.done
		if errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded) {
			continue
		}
		return f.data, f.err
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	f.data, f.err = render()
	close(f.done)

	c.mu.Lock()
	delete(c.inflight, key)
	if f.err == nil {
		c.add(key, f.data)
	}
	c.mu.Unlock()
	return f.data, f.err
}

// Peek returns the cached bytes for key, if any.
func (c *Cache) Peek(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*entry).data, true
	}
	return nil, false
}

// Put caches data under key (made elsewhere, for example while importing).
func (c *Cache) Put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return
	}
	c.add(key, data)
}

// add stores a new entry and evicts the oldest beyond capacity. Entries
// over a quarter of the capacity are not kept. c.mu must be held.
func (c *Cache) add(key string, data []byte) {
	if len(data) >= c.capacity/4 {
		return
	}
	c.items[key] = c.ll.PushFront(&entry{key, data})
	c.size += len(data)
	for c.size > c.capacity {
		el := c.ll.Back()
		e := el.Value.(*entry)
		c.ll.Remove(el)
		delete(c.items, e.key)
		c.size -= len(e.data)
	}
}

// Usage reports what the cache holds.
type Usage struct {
	Items    int   `json:"items"`
	Bytes    int64 `json:"bytes"`
	Capacity int64 `json:"capacity"`
}

// Usage returns the number of entries and bytes held.
func (c *Cache) Usage() Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Usage{Items: len(c.items), Bytes: int64(c.size), Capacity: int64(c.capacity)}
}
