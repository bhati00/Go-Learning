package primitves

import (
	"sync"
)

func FetchAll(ids []string, maxConns int, fetch func(id string) (string, error)) (map[string]string, error) {
	sem := make(chan struct{}, maxConns)
	results := make(map[string]string, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for _, val := range ids {
		mu.Lock()
		stop := firstErr != nil
		mu.Unlock()
		if stop {
			break // a failure already happened, stop launching new work
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(v string) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := fetch(v)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			results[v] = result
		}(val)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
