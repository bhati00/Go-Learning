# Phase 9b — Sync Primitives & Goroutine Safety · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 6
**Mode:** Work through one at a time. Write your answer, then read the walkthrough.

> Research sources: Go race detector docs, r/golang "found this bug in prod" threads,
> Blind Go interview reports on mutex/WaitGroup misuse, Uber Go Style Guide (mutex
> section), Go blog "Share Memory By Communicating," `sync` package godoc gotchas
> (`WaitGroup.Add` ordering, `sync.Once` re-panic behavior).

---

## Exercise Distribution

| # | Type | Topic |
|---|------|-------|
| 1 | `[BugHunt]` | Concurrent map access without synchronization |
| 2 | `[BugHunt]` | `WaitGroup.Add` called inside the goroutine — race on the counter |
| 3 | `[FixIt]` | Mutex left locked on an early-return error path |
| 4 | `[Implement]` | Counting semaphore via buffered channel to bound concurrent DB connections |
| 5 | `[Output]` | `sync.Once` — panics, re-entrancy, and lazy singleton init |
| 6 | `[Design]` | Concurrent-safe bounded LRU-ish cache |

> Order: two bug hunts on the two most common real-world sync bugs → one fix-it on a
> classic mutex leak → one implement (semaphore pattern, extremely common ask) →
> one output-prediction gotcha on `sync.Once` → one design question tying it all together.

---

---

## Exercise 1 · `[BugHunt]` — Concurrent map access without synchronization

**Context:** Go maps are explicitly **not** safe for concurrent read/write. This is the single most common "why did my program panic randomly in production" bug reported on r/golang, and it's a near-guaranteed interview question: "is a Go map safe for concurrent use?"

```go
func main() {
	cache := make(map[string]int)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n%10)
			cache[key] = cache[key] + 1 // read + write, no lock
		}(i)
	}
	wg.Wait()
	fmt.Println(cache)
}
```

**Find the bug and predict what happens when run with `go run -race`.** Does it crash every time, sometimes, or never?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. Every goroutine performs a read-modify-write on the shared `cache` map: read the current value, compute `+1`, write it back. With up to 100 goroutines mapping to only 10 distinct keys, many goroutines race on the same key concurrently.
2. Go's map implementation actively **detects** concurrent unsynchronized access (it's not just "undefined behavior" silently corrupting data — the runtime has built-in concurrent-write detection) and calls `fatal error: concurrent map writes` or `concurrent map read and map write`, crashing the whole process immediately. This is a `fatal error`, not a `panic` — it **cannot be recovered** with `recover()`.
3. Under `-race`, the race detector additionally reports the exact two goroutines and source lines involved in the race, even on runs where the runtime's own crash detector doesn't trigger.

**The Trap:** Without `-race`, this may run several times without visibly crashing (especially with low goroutine counts or fast machines) — leading people to falsely conclude "it works." The runtime's crash detection is probabilistic timing-dependent, not guaranteed on every run, which is exactly why this bug commonly slips through casual local testing and only surfaces under production load.

**The Fix:** Either a `sync.Mutex` guarding all map access, or `sync.Map` (better only for the specific case of disjoint keys per goroutine / write-once-read-many; for general read-modify-write like this, a plain mutex is usually simpler and often faster):

```go
var mu sync.Mutex
cache := make(map[string]int)
// ...
go func(n int) {
	defer wg.Done()
	key := fmt.Sprintf("key-%d", n%10)
	mu.Lock()
	cache[key] = cache[key] + 1
	mu.Unlock()
}(i)
```

**Interview Signal:** Tests whether you know Go maps have zero built-in concurrency safety, that the runtime crash on detection is a `fatal error` (unrecoverable) not a `panic`, and that `sync.Map` is a specialized tool, not a default replacement for "map + mutex."

**Key Rule to Remember:** Rule: Go maps are never safe for concurrent read/write — guard with a `Mutex`/`RWMutex`, or use `sync.Map` only when your access pattern actually matches its optimization (disjoint keys, append-mostly, rarely re-written).

---

---

## Exercise 2 · `[BugHunt]` — `WaitGroup.Add` called inside the goroutine

**Context:** This is a subtler `WaitGroup` bug than "forgot to call Wait" — it's a genuine race on the WaitGroup's own internal counter, and it's a very common interview trick question because the code "looks" correct at a glance.

```go
func main() {
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		go func(n int) {
			wg.Add(1) // BUG: Add() called from inside the goroutine
			defer wg.Done()
			fmt.Println("working:", n)
		}(i)
	}

	wg.Wait() // may return before some goroutines even call Add
	fmt.Println("all done")
}
```

**Find the bug.** Why is this a race, and what's the realistic worst-case symptom?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `wg.Wait()` in the main goroutine returns as soon as the internal counter reaches zero. If it's called before *any* `Add(1)` has happened, the counter is still `0` — `Wait()` returns **immediately**, without waiting for any goroutine at all.
2. Because goroutines are scheduled independently, there's no guarantee any of the five goroutines have reached their `wg.Add(1)` line before the main goroutine reaches `wg.Wait()`. This is a genuine race between "main calls Wait" and "goroutines call Add" — the Go documentation for `WaitGroup` explicitly warns about this.
3. Realistic symptom: `"all done"` prints before some or all of the `"working: N"` lines — or, in the worst case, `main()` returns and the process exits while goroutines are still starting up, silently dropping their work entirely.

**The Trap:** This bug is insidious because it "usually" seems to work — in practice the main goroutine often loses the scheduling race and the spawned goroutines get to run their `Add` first, especially with `GOMAXPROCS > 1` and cheap goroutine bodies. It becomes a visible failure only under specific timing (heavy system load, `GOMAXPROCS=1`, or when the goroutine body does something slower before `Add`), making it a classic flaky-test / flaky-prod bug that's hard to reproduce on a developer's laptop.

**The Fix:** Always call `Add()` in the same goroutine that launches the child — synchronously, before `go func(){...}()` — never inside the spawned goroutine itself:

```go
for i := 0; i < 5; i++ {
	wg.Add(1) // called synchronously in the loop, guaranteed before Wait()
	go func(n int) {
		defer wg.Done()
		fmt.Println("working:", n)
	}(i)
}
wg.Wait()
```

**Interview Signal:** Tests whether you know the documented invariant: all calls to `Add` must happen-before the corresponding `Wait` call returns, which in practice means `Add` must be called by the parent, synchronously, before spawning — never from inside the goroutine being tracked.

**Key Rule to Remember:** Rule: always call `wg.Add(1)` in the parent goroutine, immediately before `go func(){...}()` — never inside the goroutine itself. `Add` must happen-before `Wait` can safely rely on it.

---

---

## Exercise 3 · `[FixIt]` — Mutex left locked on an early-return error path

**Context:** This is one of the most common real production deadlock causes — a function acquires a lock, hits an early `return` on an error branch, and forgets to release it. Every subsequent caller of that lock blocks forever.

```go
type Store struct {
	mu   sync.Mutex
	data map[string]string
}

func (s *Store) Update(key, value string) error {
	s.mu.Lock()
	if value == "" {
		return errors.New("value cannot be empty") // BUG: mutex never unlocked
	}
	s.data[key] = value
	s.mu.Unlock()
	return nil
}
```

**Fix the bug.** Write the corrected version, and explain what happens to every other goroutine that calls `Update` or reads `s.data` after this bug is triggered once.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `s.mu.Lock()` acquires the lock. On the empty-value branch, the function returns immediately — the code path to `s.mu.Unlock()` is never reached.
2. The lock is now held forever with no owner able to release it. Every future call to `Update` (or any other method that calls `s.mu.Lock()`) blocks permanently the moment this happens once, for the lifetime of the process — a full deadlock of every consumer of `s.data`, triggered by a single bad input (`value == ""`).
3. This is worse than a crash: the process stays "alive" (no fatal error, no panic) but is functionally dead for anything touching this store — the kind of bug that shows up as "the service went unresponsive" with no error logs pointing at the cause.

**The Fix:** Use `defer s.mu.Unlock()` immediately after `Lock()` — it guarantees release on every return path, including panics:

```go
func (s *Store) Update(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		return errors.New("value cannot be empty")
	}
	s.data[key] = value
	return nil
}
```

**The Trap:** Manual `Unlock()` calls placed "at the end" of a function look correct in the happy path and pass code review — the bug only manifests on error branches that are easy to miss, especially as functions grow more branches over time. This is exactly why `defer mu.Unlock()` immediately after `Lock()` is a near-universal Go idiom, not just a style preference.

**Interview Signal:** Tests whether you default to `defer Unlock()` as a reflex, understanding that manual unlock placement is fragile against every future edit that adds a new return path.

**Key Rule to Remember:** Rule: always pair `mu.Lock()` with `defer mu.Unlock()` on the very next line — never rely on unlocking manually "before every return," because someone will add a new return path later and forget.

---

---

## Exercise 4 · `[Implement]` — Counting semaphore to bound concurrent DB connections

**Context:** "Bound concurrency to protect a downstream resource" (DB, external API rate limits) is one of the most-asked applied concurrency questions — it's the semaphore pattern from a different angle than the worker-pool exercise, testing whether you generalize the pattern rather than memorize one shape.

**Specification:**
- Write `FetchAll(ids []string, maxConns int, fetch func(id string) (string, error)) (map[string]string, error)`.
- At most `maxConns` calls to `fetch` may be in flight at once, simulating a limited DB connection pool.
- If **any** call to `fetch` returns an error, stop launching new work as soon as practical and return that error (the first one encountered) — partial results do not need to be returned in the error case.
- Safe for concurrent map writes.

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. A buffered channel of size `maxConns` is the semaphore — identical pattern to the worker-pool exercise, applied here to protect a shared external resource instead of just capping goroutine count.
2. "Stop on first error" needs a way for goroutines to observe "someone already failed" without polling — a `context.WithCancel` (or a simple `sync.Once`-guarded flag) shared across goroutines does this: the first failing goroutine cancels a shared context; other goroutines check `ctx.Err()` before doing work and skip if already canceled.
3. Since multiple goroutines write to the results map, and only the *first* error should be recorded, `sync.Once` is the correct idiomatic tool for the "record only the first error" part — it is safe for concurrent calls, executing its function exactly once no matter how many goroutines race to invoke it.

```go
func FetchAll(ids []string, maxConns int, fetch func(id string) (string, error)) (map[string]string, error) {
	sem := make(chan struct{}, maxConns)
	results := make(map[string]string, len(ids))
	var mu sync.Mutex
	var firstErr error
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for _, id := range ids {
		if ctx.Err() != nil {
			break // stop launching new work once cancelled
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()

			val, err := fetch(id)
			if err != nil {
				once.Do(func() {
					firstErr = err
					cancel() // signal all other goroutines to stop launching
				})
				return
			}
			mu.Lock()
			results[id] = val
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
```

**The Trap:** Without `sync.Once`, multiple goroutines racing to set `firstErr` is itself a data race (unsynchronized write to a shared variable) — a subtle bug that's easy to overlook because "just assigning an error" doesn't look dangerous. Without the `context` cancellation check in the launching loop, already-queued goroutines still complete (acceptable), but you'd keep *launching new ones* even after a failure, wasting the bounded resource on work you already know to discard.

**Interview Signal:** Tests composition of three primitives at once — semaphore (channel), first-error-wins (`sync.Once`), and cooperative early-exit (`context`) — which is exactly the shape of real production fan-out code hitting a database or third-party API.

**Key Rule to Remember:** Rule: to record only the "first" occurrence of a concurrent event (first error, first success), use `sync.Once` — a plain `if firstErr == nil { firstErr = err }` is a data race.

---

---

## Exercise 5 · `[Output]` — `sync.Once` panics, re-entrancy, and lazy init

**Context:** `sync.Once` is used everywhere for lazy singleton initialization, but its panic behavior surprises almost everyone the first time they hit it. This is a genuinely popular "what does this print" interview question.

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var once sync.Once

	safeCall := func(n int) {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("recovered:", r)
			}
		}()
		once.Do(func() {
			fmt.Println("initializing, n =", n)
			panic("init failed")
		})
		fmt.Println("after Do, n =", n)
	}

	safeCall(1)
	safeCall(2)
}
```

**What does this print for both calls?** Specifically: does `once.Do`'s function run again on the second call, since it panicked the first time?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `safeCall(1)` calls `once.Do(f)`. `sync.Once` marks itself as "done" **before** it's fully certain the function completed successfully — actually, the Go implementation considers the `Do` call "consumed" even if `f` panics. It prints `initializing, n = 1`, then panics.
2. The deferred `recover()` in `safeCall(1)` catches the panic, prints `recovered: init failed`. Note `"after Do, n = 1"` is **never printed** — the panic aborted that line, and `recover()` only stops the panic from propagating further, it doesn't resume execution after the point of panic.
3. `safeCall(2)` calls `once.Do(f)` again. Because `sync.Once` already recorded this `Once` as "done" (regardless of the panic), the function `f` is **not** run a second time — `Do` returns immediately without executing anything. It prints `after Do, n = 2` directly, with no `"initializing"` line.

**Output:**
```
initializing, n = 1
recovered: init failed
after Do, n = 2
```

**The Trap:** People assume a panic during `once.Do(f)` means initialization "didn't happen" and expect the next call to retry it — it does not. `sync.Once` guarantees the function runs **at most once**, not "runs until it succeeds." If your `Once`-guarded initializer can fail, a panicking (or error-returning-but-ignored) first call permanently poisons the singleton — every subsequent caller silently proceeds as if init succeeded, which is a nasty, hard-to-detect production bug (e.g., a nil global client that init failed to set up).

**Interview Signal:** Tests whether you know `sync.Once.Do` semantics are "exactly once, ever" — not "retry until success" — and understand the real-world consequence: fallible one-time initializers need their own success/failure tracking, `sync.Once` alone is not enough for retryable init.

**Key Rule to Remember:** Rule: `sync.Once.Do(f)` runs `f` at most once — even if `f` panics, it will never run again. Never use `sync.Once` alone for initialization that can fail and needs a retry.

---

---

## Exercise 6 · `[Design]` — Concurrent-safe bounded cache

**Context:** "Design a thread-safe LRU-ish cache" is a very common system-design-in-Go / applied-concurrency interview question, combining `RWMutex`, map safety, and eviction logic in one problem.

**Specification:**
- Design a type `Cache` with `Get(key string) (string, bool)` and `Put(key, value string)`.
- Capped at `maxSize` entries. When full, evict the **least recently used** entry on the next `Put`.
- Must be safe for many concurrent readers and occasional concurrent writers.
- You do not need a perfectly O(1) LRU (a doubly linked list + map) — a simplified approach (e.g., tracking last-access timestamps and scanning on eviction) is acceptable if you can state its complexity trade-off honestly.

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. Reads (`Get`) will vastly outnumber writes (`Put`) in a typical cache workload — this is the textbook case for `sync.RWMutex` over a plain `Mutex`, from Topic 2 of the README: many concurrent `RLock`s for reads, exclusive `Lock` only for the rarer `Put`/eviction path.
2. `Get` needs to update "last used" metadata to support LRU — but that's a **write** to bookkeeping state even though it's conceptually a "read" of the cache. This is the standard trade-off question interviewers probe: do you take a write lock on every `Get` (simple, correct, but defeats the purpose of `RWMutex` since every "read" now needs exclusive access), or accept an approximate LRU (e.g., only update the timestamp under a cheaper path, or use `atomic` for the timestamp field, or tolerate the eviction being approximately-LRU rather than exactly-LRU)?
3. A honest, mid-level-appropriate answer: use `RWMutex`, and for LRU bookkeeping either (a) take the exclusive lock even on `Get` and admit reads are exclusive here — simplest, correct, and defensible if candidate states the tradeoff — or (b) maintain a separate atomically-updated access counter/timestamp per entry so `Get` only needs `RLock`.

```go
type entry struct {
	value      string
	lastUsed   int64 // unix nano, updated via atomic
}

type Cache struct {
	mu      sync.RWMutex
	data    map[string]*entry
	maxSize int
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}
	atomic.StoreInt64(&e.lastUsed, time.Now().UnixNano()) // no full lock needed
	return e.value, true
}

func (c *Cache) Put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.data[key]; !exists && len(c.data) >= c.maxSize {
		c.evictLocked() // caller already holds the write lock
	}
	c.data[key] = &entry{value: value, lastUsed: time.Now().UnixNano()}
}

func (c *Cache) evictLocked() {
	var oldestKey string
	var oldestTime int64 = math.MaxInt64
	for k, e := range c.data {
		t := atomic.LoadInt64(&e.lastUsed)
		if t < oldestTime {
			oldestTime, oldestKey = t, k
		}
	}
	delete(c.data, oldestKey)
}
```

**The Trap:** The naive version updates `lastUsed` on `Get` while only holding `RLock` (shared with other readers) using a plain field write — that's a data race, because multiple readers could write `lastUsed` concurrently. Using `atomic.StoreInt64` fixes this cheaply without needing the exclusive lock just for bookkeeping. The eviction scan is O(n) — an honest answer states this trade-off explicitly rather than pretending it's O(1); a true O(1) LRU needs an intrusive doubly linked list kept in sync with the map, which is reasonable to mention as "the production version" without necessarily coding it live.

**Interview Signal:** Tests whether you can reason about the read/write ratio to choose `RWMutex` over `Mutex`, whether you notice that "just reading" for LRU purposes actually mutates shared state, and whether you can name the trade-off of your simplified eviction strategy instead of hiding it.

**Key Rule to Remember:** Rule: `RWMutex` only pays off when reads significantly outnumber writes AND reads don't secretly need to mutate shared state — if they do, either use `atomic` for that narrow piece of state or admit you need a full write lock.

---
