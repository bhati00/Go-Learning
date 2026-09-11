# Phase 7 - Go Memory Model · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 5
**Mode:** Work through one at a time. Predict or fix the code first, then use `-race` to verify the memory-model reasoning.

> Research sources: [Go Memory Model](https://go.dev/ref/mem), [Go Race Detector](https://go.dev/doc/articles/race_detector), and [`sync`](https://pkg.go.dev/sync) package documentation.

---

## Exercise Distribution

| # | Type | Topic |
|---|---|---|
| 1 | `[Trace]` | Channel close publishes prior writes |
| 2 | `[BugHunt]` | A read must synchronize too |
| 3 | `[FixIt]` | Mutex protects correctness and visibility |
| 4 | `[FixIt]` | Safe lazy initialization with `sync.Once` |
| 5 | `[RaceReport]` | Read and fix a race-detector report |

> Order: establish a happens-before chain -> find a missing edge -> repair it with a mutex -> use the standard lazy-init primitive -> interpret the tool evidence.

---

## Exercise 1 · `[Trace]` - Does the receiver see the result?

**Context:** A channel can send a value, but it can also publish writes made before the send or close. Trace the happens-before chain instead of relying on scheduling luck.

```go
package main

import (
	"fmt"
	"time"
)

type Config struct {
	Port int
}

func main() {
	var config Config
	ready := make(chan struct{})

	go func() {
		config.Port = 8080
		close(ready)
	}()

	<-ready
	fmt.Println(config.Port)
	time.Sleep(time.Millisecond)
}
```

**Answer all three:**
1. Is `fmt.Println(config.Port)` guaranteed to print `8080`?
2. Write the full happens-before chain from the assignment to the read.
3. Would replacing `<-ready` with `time.Sleep(time.Millisecond)` be a correct fix? Why?

Verify with:

```bash
go run -race main.go
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `config.Port = 8080` is sequenced-before `close(ready)` because both run in the same goroutine.
2. `close(ready)` is synchronized-before `<-ready` returning because the receive observes the closed channel.
3. The receive is sequenced-before `fmt.Println(config.Port)` in `main`.
4. Happens-before is transitive, so the assignment happens-before the read. The program is race-free and always prints `8080`.

**The Trap:** The goroutine often finishes before `main` reads even without `<-ready`, but elapsed time is not synchronization. A sleep gives no memory visibility guarantee and makes the test timing-dependent.

**Answer:**

```text
config.Port = 8080 -> close(ready) -> <-ready returns -> read config.Port
```

**Interview Signal:** Tests whether you treat channel close as a broadcast synchronization event, not merely a way to stop receivers.

**Key Rule to Remember:** Rule: write -> `close(done)` -> receive from `done` gives every awakened receiver visibility of the earlier write.

---

## Exercise 2 · `[BugHunt]` - Locking only writers

**Context:** A mutex is not optional for readers when a writer can run concurrently. Find the one primary bug in this cache.

```go
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
```

**Find the bug and explain both missing guarantees.** Why can `Get` neither safely read `value` nor reliably observe the value written by `Set`?

Verify with:

```bash
go run -race main.go
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `Set` writes `value` while holding `mu`.
2. `Get` reads `value` without acquiring `mu`.
3. The read and write can be concurrent, so they form a data race.
4. There is also no `Unlock()` -> `Lock()` edge for `Get`; therefore the write's visibility is not guaranteed to the reader.

**The Trap:** A lock around writes prevents writers from overlapping each other, but it does nothing for a reader that bypasses the lock. Correct synchronization must cover every conflicting access.

**The Fix:**

```go
func (c *Cache) Get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}
```

With this change, `Set`'s `Unlock()` happens-before a later successful `Get` `Lock()`. The result may still be the initial empty string if `Get` wins the scheduling race, but it is memory-safe. To require `"ready"`, call `Get` after `wg.Wait()`.

**Interview Signal:** Tests whether you know a mutex provides memory visibility as well as mutual exclusion.

**Key Rule to Remember:** Rule: if one goroutine writes under a mutex, every goroutine reading that value must use the same mutex or another explicit synchronization mechanism.

---

## Exercise 3 · `[FixIt]` - Publish a completed calculation

**Context:** A test intermittently reads a partial result. Repair the code so the caller cannot read until the worker has completed the calculation.

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.Mutex
	result := 0
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		mu.Lock()
		result = 6 * 7
		mu.Unlock()
	}()

	mu.Lock()
	fmt.Println(result)
	mu.Unlock()
	wg.Wait()
}
```

**Fix the program so it is both race-free and guaranteed to print `42`.** Explain why making both accesses lock-protected is necessary but not sufficient for the required result.

Verify with:

```bash
go run -race main.go
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:** The mutex makes individual reads and writes safe, but it does not decide which goroutine acquires it first. `main` can lock first, read the valid initial value `0`, and only then let the worker calculate the result.

**The Trap:** Race-free is weaker than logically correct. Synchronization removes undefined memory access; it does not automatically encode the business ordering you need.

**The Fix:** Wait for the worker before reading its result.

```go
wg.Wait()

mu.Lock()
fmt.Println(result)
mu.Unlock()
```

`result = 42` is sequenced-before `wg.Done()`, and `Done()` is synchronized-before `wg.Wait()` returns. After `Wait`, the caller can safely observe the completed work. The mutex remains necessary if future code permits reads and writes to overlap.

**Interview Signal:** Tests whether you distinguish a data race from a race condition in program logic.

**Key Rule to Remember:** Rule: a mutex protects shared access; a completion signal such as `WaitGroup` or a channel establishes required workflow order.

---

## Exercise 4 · `[FixIt]` - Lazy initialization without double-checked locking

**Context:** A service tries to avoid locking after its shared client has been initialized. Its fast path reads shared state without synchronization.

```go
package main

import (
	"fmt"
	"sync"
)

type Client struct {
	endpoint string
}

var (
	client *Client
	mu     sync.Mutex
)

func getClient() *Client {
	if client != nil {
		return client
	}

	mu.Lock()
	defer mu.Unlock()
	if client == nil {
		client = &Client{endpoint: "https://api.example"}
	}
	return client
}

func main() {
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println(getClient().endpoint)
		}()
	}
	wg.Wait()
}
```

**Replace the initialization strategy with the idiomatic Go primitive.** Why is the first `if client != nil` a data race even though the assignment occurs under `mu`?

Verify with:

```bash
go run -race main.go
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:** One goroutine can assign to `client` while another evaluates the unlocked fast path. Those are conflicting accesses with no happens-before relationship. The write holding `mu` does not protect a read that never locks it.

**The Trap:** This pattern is familiar from languages with a `volatile` keyword. Go's idiom is not to hand-build that fast path; `sync.Once` supplies the needed synchronization guarantee.

**The Fix:**

```go
var (
	client     *Client
	clientOnce sync.Once
)

func getClient() *Client {
	clientOnce.Do(func() {
		client = &Client{endpoint: "https://api.example"}
	})
	return client
}
```

The return from the initialization function happens-before the return from every `clientOnce.Do` call. Each caller therefore sees the fully initialized client.

**Interview Signal:** Tests whether you recognize safe publication, not merely duplicate initialization.

**Key Rule to Remember:** Rule: use `sync.Once` for one-time shared initialization; do not read an initialization flag or pointer outside its synchronization scheme.

---

## Exercise 5 · `[RaceReport]` - Turn tool evidence into a fix

**Context:** The race detector does not prove all paths are safe, but when it reports a race, treat the two reported accesses as a missing happens-before edge.

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	count := 0
	var wg sync.WaitGroup

	for range 1_000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			count++
		}()
	}

	wg.Wait()
	fmt.Println(count)
}
```

**Run this, then answer:**
1. Which two operations in `count++` conflict?
2. Why does `wg.Wait()` not make the increments safe?
3. Fix the code with `sync.Mutex`.
4. What does a clean `-race` run still *not* prove?

Verify with:

```bash
go run -race main.go
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:** `count++` is a read-modify-write sequence. Multiple goroutines can read the same old count, then each write a new count based on it. The accesses conflict and have no happens-before edge. `Wait` only orders the final read after every `Done`; it does not order the goroutines relative to each other while they increment.

**The Trap:** A `WaitGroup` is a completion mechanism, not a general-purpose lock. It tells `main` when workers have finished; it does not serialize work performed by workers.

**The Fix:**

```go
var mu sync.Mutex

for range 1_000 {
	wg.Add(1)
	go func() {
		defer wg.Done()
		mu.Lock()
		count++
		mu.Unlock()
	}()
}
```

Now every increment occurs in a critical section, and each `Unlock()` publishes its write to the next goroutine that acquires `mu`. The program prints `1000` without a race report.

**Interview Signal:** Tests whether you can read a race report as a concrete synchronization failure rather than treating `-race` as a mysterious warning.

**Key Rule to Remember:** Rule: `go test -race` and `go run -race` find races exercised in that run; a clean result is evidence, not a proof that unexecuted paths are race-free.

---

## Phase 7 Exercise Checklist

- Can you draw a full happens-before chain for a channel, mutex, or `WaitGroup`?
- Can you explain why a timing delay is not synchronization?
- Can you separate memory safety (no data race) from logical ordering (the correct result)?
- Do you run concurrent code with `-race` before trusting a passing test?

> Next: apply these rules to Phase 8's garbage collector and Phase 9's channels, mutexes, cancellation, and worker patterns.