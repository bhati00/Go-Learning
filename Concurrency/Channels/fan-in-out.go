package main

import (
	"context"
	"fmt"
	"sync"
)

func Pipeline(ctx context.Context, nums []int, workers int) []int {
	jobs := make(chan int)
	results := make(chan int)

	var wg sync.WaitGroup

	// FAN-OUT: create workers
	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				select {

				// Cancellation
				case <-ctx.Done():
					return

				// Receive a job
				case val, ok := <-jobs:
					if !ok {
						return
					}

					// Simple task
					result := val * val

					// Send result, but don't get stuck
					// if the pipeline has been cancelled.
					select {
					case results <- result:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	// Send jobs
	go func() {
		defer close(jobs)

		for _, val := range nums {
			select {
			case jobs <- val:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Close results after ALL workers finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// FAN-IN: collect results
	var output []int

	for {
		select {
		case <-ctx.Done():
			return output

		case val, ok := <-results:
			if !ok {
				return output
			}

			output = append(output, val)
		}
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nums := []int{1, 2, 3, 4, 5, 6}

	result := Pipeline(ctx, nums, 2)

	fmt.Println(result)
}
