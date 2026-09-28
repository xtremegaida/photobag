package server

import (
	"container/list"
	"sync"
)

// previewCache is a byte-bounded LRU of rendered previews that also
// coalesces concurrent renders of the same key.
type previewCache struct {
	mu       sync.Mutex
	capacity int
	size     int
	ll       *list.List
	items    map[string]*list.Element
	inflight map[string]*flight
}

type cacheEntry struct {
	key  string
	data []byte
}

type flight struct {
	done chan struct{}
	data []byte
	err  error
}

func newPreviewCache(capacity int) *previewCache {
	return &previewCache{capacity: capacity, ll: list.New(), items: map[string]*list.Element{}, inflight: map[string]*flight{}}
}

func (c *previewCache) get(key string, render func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		data := el.Value.(*cacheEntry).data
		c.mu.Unlock()
		return data, nil
	}
	if f, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.data, f.err
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	f.data, f.err = render()
	close(f.done)

	c.mu.Lock()
	delete(c.inflight, key)
	if f.err == nil && len(f.data) < c.capacity/4 {
		c.items[key] = c.ll.PushFront(&cacheEntry{key, f.data})
		c.size += len(f.data)
		for c.size > c.capacity {
			el := c.ll.Back()
			e := el.Value.(*cacheEntry)
			c.ll.Remove(el)
			delete(c.items, e.key)
			c.size -= len(e.data)
		}
	}
	c.mu.Unlock()
	return f.data, f.err
}
