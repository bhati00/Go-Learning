package main

import (
	"fmt"
	"sync"
)

type Cache struct {
	mu    sync.Mutex
	value string
}

func (c *Cache) Set(value string) {
	c.mu.Lock()
	c.value = value
	c.mu.Unlock()
}

func (c *Cache) Get() string {
	return c.value
}

func main() {
	cache := Cache{}
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		cache.Set("ready")
	}()

	fmt.Println(cache.Get())
	wg.Wait()
}
