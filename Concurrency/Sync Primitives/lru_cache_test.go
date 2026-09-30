package primitves

import (
	"sync"
	"testing"
)

// Stress-tests get/put from many goroutines under `go test -race` and checks
// the capacity invariant still holds once everything settles.
func TestLruCacheConcurrentAccess(t *testing.T) {
	capacity := 3
	lru := NewLruCache(capacity)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := i % 5
			if i%2 == 0 {
				lru.put(key, "v")
			} else {
				lru.get(key)
			}
		}(i)
	}
	wg.Wait()

	if len(lru.cacheMap) > capacity {
		t.Fatalf("cache exceeded capacity: got %d entries, want <= %d", len(lru.cacheMap), capacity)
	}
}
