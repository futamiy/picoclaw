package main

import "sync"

// runConcurrent invokes fn(0..n-1) with at most `concurrency` goroutines in
// flight. Each index is invoked exactly once and writes to its own slot.
func runConcurrent(n, concurrency int, fn func(i int)) {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency == 1 || n <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
