package lru

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	c := New(100)
	for i := range 4 {
		c.Put(fmt.Sprint(i), make([]byte, 20))
	}
	c.Peek("0") // now the most recent
	c.Put("4", make([]byte, 20))
	c.Put("5", make([]byte, 20))
	for key, want := range map[string]bool{"0": true, "1": false, "2": true, "3": true, "4": true, "5": true} {
		if _, ok := c.Peek(key); ok != want {
			t.Errorf("%s cached = %v", key, ok)
		}
	}
	if u := c.Usage(); u.Items != 5 || u.Bytes != 100 || u.Capacity != 100 {
		t.Errorf("usage %+v", u)
	}
	c.Put("big", make([]byte, 25)) // a quarter of the capacity is too big
	if _, ok := c.Peek("big"); ok {
		t.Error("kept an oversized entry")
	}
}

func TestGetCoalescesAndRetriesCancelled(t *testing.T) {
	c := New(1 << 20)
	started, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// The first caller goes away while rendering.
		_, err := c.Get("k", func() ([]byte, error) {
			close(started)
			<-release
			return nil, context.Canceled
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("first caller: %v", err)
		}
	}()
	<-started
	done := make(chan []byte)
	go func() {
		data, err := c.Get("k", func() ([]byte, error) { return []byte("made"), nil })
		if err != nil {
			t.Error(err)
		}
		done <- data
	}()
	close(release)
	if got := string(<-done); got != "made" {
		t.Errorf("waiting caller got %q", got)
	}
	wg.Wait()
	calls := 0
	data, _ := c.Get("k", func() ([]byte, error) { calls++; return nil, nil })
	if string(data) != "made" || calls != 0 {
		t.Errorf("not cached: %q, %d renders", data, calls)
	}
}
