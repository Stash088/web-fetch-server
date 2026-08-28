package cache

import (
	"sync"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	c := New(10, time.Minute)
	defer c.Close()
	c.Set("k", "v")
	got, ok := c.Get("k")
	if !ok || got != "v" {
		t.Fatalf("Get after Set: got %v, ok %v", got, ok)
	}
}

func TestGetMiss(t *testing.T) {
	c := New(10, time.Minute)
	defer c.Close()
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss for unknown key")
	}
}

func TestExpiry(t *testing.T) {
	c := New(10, 10*time.Millisecond)
	defer c.Close()
	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("expected hit before expiry")
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expected miss after expiry")
	}
}

func TestMaxEntriesEvictsOldest(t *testing.T) {
	c := New(2, time.Minute)
	defer c.Close()
	c.Set("a", 1)
	time.Sleep(2 * time.Millisecond)
	c.Set("b", 2)
	// Force "a" to be oldest by re-setting b.
	time.Sleep(2 * time.Millisecond)
	c.Set("c", 3) // should evict "a" (oldest expiry)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("expected 'b' present")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected 'c' present")
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := New(100, time.Minute)
	defer c.Close()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "k"
			c.Set(key, n)
			_, _ = c.Get(key)
		}(i)
	}
	wg.Wait()
	if c.Len() == 0 {
		t.Fatal("expected at least one entry")
	}
}
