# Phase 9a — Channels · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 6
**Mode:** Work through one at a time. Write your answer, then read the walkthrough.

> Research sources: r/golang recurring threads on channel gotchas, Blind Go interview reports,
> LeetCode's Concurrency category (Print in Order, Print FooBar Alternately, Building H2O —
> these are real Go/Java interview staples for channel-based synchronisation), Go blog
> "Pipelines and cancellation", Go FAQ on channels, Rob Pike's "Go Concurrency Patterns" talk.

---

## Exercise Distribution

| # | Type | Topic |
|---|------|-------|
| 1 | `[Output]` | nil channel + `select` — the "disable a case" trick |
| 2 | `[Output]` | Closed channel semantics — send panics, receive drains then zero value |
| 3 | `[BugHunt]` | Goroutine leak — forgotten receiver on an unbuffered channel |
| 4 | `[Implement]` | Fan-out / fan-in pipeline |
| 5 | `[Implement]` | Worker pool with bounded concurrency |
| 6 | `[Coding]` | Print in order — classic multithreaded interview problem (LeetCode 1114-style) using channels |

> Order: two output predictions to lock in channel mechanics → one bug hunt (real production
> failure mode) → two implement problems (the two patterns every Go interview asks about) →
> one classic coding problem that shows up verbatim on LeetCode's concurrency track and in
> onsite interviews at companies that ask "implement synchronization with channels."

---

---

## Exercise 1 · `[Output]` — nil channel + `select` — the "disable a case" trick

**Context:** A nil channel blocks forever on both send and receive. This sounds useless until you see it's the standard trick for dynamically disabling a `select` case. Interviewers use this to check if you actually understand channel state, not just syntax.

```go
package main

import "fmt"

func main() {
	ch := make(chan int, 1)
	ch <- 42

	var done chan struct{} // nil channel, never assigned

	for i := 0; i < 2; i++ {
		select {
		case v := <-ch:
			fmt.Println("got:", v)
			ch = nil // disable this case after first receive
		case <-done:
			fmt.Println("done fired") // this should never print
		default:
			fmt.Println("default")
		}
	}
}
```

**What does this print, in order?** Trace what happens to the `select` on each loop iteration as `ch` transitions from a real channel to `nil`.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `ch` is buffered with capacity 1 and already holds `42`. `done` is declared but never initialized — its zero value is `nil`.
2. **Iteration 1:** `select` evaluates all non-default cases. `<-ch` is ready (buffer has a value) → that case fires. Prints `got: 42`. Then `ch = nil` runs inside the case body.
3. **Iteration 2:** Now both `ch` and `done` are `nil`. A `nil` channel is never ready for send or receive — the runtime never queues a `nil` channel's operations at all. Since neither case can proceed, and a `default` exists, `select` falls through to `default` immediately (no blocking). Prints `default`.

**Output:**
```
got: 42
default
```

**The Trap:** People assume a `nil` channel causes a panic or an immediate error. It does not — it just blocks forever, silently. Inside a bare `<-ch` with a `nil` channel and no `select`, your goroutine would hang permanently (this is a real deadlock cause: `fatal error: all goroutines are asleep - deadlock!`). Inside a `select`, a `nil` channel case is simply never chosen — which is exactly why setting a channel variable to `nil` is the idiomatic way to "turn off" a case without restructuring the `select`.

**Interview Signal:** Tests whether you know that a `nil` channel is a legal, well-defined value (not an error state) whose defined behavior — blocking forever — is exploited deliberately in real pipeline code to disable completed channels.

**Key Rule to Remember:** Rule: nil channel → blocks forever on send/receive, and is never selected in a `select`. Set a channel to `nil` to deliberately remove it from `select` consideration.

---

---

## Exercise 2 · `[Output]` — Closed channel semantics

**Context:** "What happens when you send/receive on a closed channel" is asked in nearly every Go interview that touches concurrency. The two-value receive form is the part people forget under pressure.

```go
package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	close(ch)

	for i := 0; i < 4; i++ {
		v, ok := <-ch
		fmt.Println(v, ok)
	}
}
```

**What does this print for all four iterations?** Then answer separately: what happens if, after `close(ch)`, you additionally run `ch <- 3`?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. The channel is buffered with 2 values already sitting in it, then closed. Closing a channel does **not** discard buffered values — it only prevents further sends.
2. **Receive 1 & 2:** Drain the buffer normally. `ok` is `true` because a real value was delivered: prints `1 true`, then `2 true`.
3. **Receive 3 & 4:** The buffer is now empty and the channel is closed. A receive on a closed, empty channel returns immediately (never blocks) with the **zero value** of the channel's type and `ok = false`.

**Output:**
```
1 true
2 true
0 false
0 false
```

**Second part — `ch <- 3` after close:** This panics immediately with `panic: send on closed channel`. This is not recoverable by checking a return value (channels have no error-return form for send) — the only ways to survive it are to never let two goroutines race on "is this closed" or to use `recover()` defensively (a code smell, not a fix).

**The Trap:** People conflate "closed" with "empty" — they assume a receive on a closed channel always returns the zero value, forgetting that buffered/in-flight values are still delivered first. The other trap is assuming a receive on a closed channel blocks or errors — it does neither; it returns immediately, which is why forgetting to check `ok` is a classic source of a `for v := range ch` loop silently processing a stream of zero values forever if not written as a `range` (which handles this correctly by exiting the loop on close).

**Interview Signal:** Tests the full close/receive contract: buffered values still flow, exhausted+closed gives zero value with `ok=false`, and send-after-close panics.

**Key Rule to Remember:** Rule: close(ch) → sends panic, receives on empty channel return `(zero value, false)` — never blocks. Only the sender should close a channel, never the receiver.

---

---

## Exercise 3 · `[BugHunt]` — Goroutine leak: forgotten receiver

**Context:** This is the single most common goroutine leak pattern in real production Go code — it shows up in code review constantly and is a favorite "spot the bug" interview question.

```go
func process(items []int) []int {
	results := make(chan int)

	for _, item := range items {
		go func(n int) {
			results <- n * n // unbuffered send
		}(item)
	}

	var out []int
	for i := 0; i < len(items)/2; i++ { // BUG: only reads half
		out = append(out, <-results)
	}
	return out
}
```

**Find the bug.** What happens to the goroutines that never get their value received? Does the program crash, hang, or "just" leak?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `results` is unbuffered. Every launched goroutine tries to send its computed value — but a send on an unbuffered channel only completes once a receiver is ready to take it at that exact moment.
2. The loop only receives `len(items)/2` values, then `process` returns.
3. The remaining goroutines (half of them) are permanently parked in `results <- n*n`, waiting for a receiver that will never come — `results` goes out of scope from the caller's perspective, but the goroutines still hold a reference to it (they close over the channel), so the channel itself is never garbage collected either, and neither are the parked goroutines.

**The Trap:** The program doesn't crash and doesn't hang — `process` returns normally with a partial (and non-deterministic, since goroutine scheduling order isn't guaranteed) result. This makes the bug invisible in local testing: it "works." The leaked goroutines silently accumulate. Under sustained load (e.g., this function called in a hot path handling thousands of requests/sec), the goroutine count and memory footprint climb until performance degrades or the process is OOM-killed — a class of bug that usually only shows up in production days later, discovered via `runtime.NumGoroutine()` metrics trending upward or a pprof goroutine dump.

**The Fix:** Either receive exactly as many values as were sent, or make the channel buffered to the exact number of items so sends never block regardless of whether all are received:

```go
func process(items []int) []int {
	results := make(chan int, len(items)) // buffered — sends never block
	for _, item := range items {
		go func(n int) { results <- n * n }(item)
	}
	out := make([]int, 0, len(items))
	for i := 0; i < len(items); i++ { // receive ALL of them
		out = append(out, <-results)
	}
	return out
}
```

**Interview Signal:** Tests whether you understand that an unbuffered send genuinely blocks the sending goroutine until received — and that "the caller doesn't need the result" does not mean "the goroutine gets cleaned up." Nothing frees a goroutine except it returning on its own.

**Key Rule to Remember:** Rule: every goroutine that sends on an unbuffered channel needs a guaranteed receiver, or it leaks forever. Size buffers to the known number of producers, or use a `context` / `done` channel to guarantee an exit path (see Context & Patterns exercises).

---

---

## Exercise 4 · `[Implement]` — Fan-out / fan-in pipeline

**Context:** This is one of the two patterns (with worker pools) that appears in nearly every "design something concurrent in Go" interview. It comes directly from the Go blog's "Pipelines and cancellation" post and is a very common system-design-in-Go question.

**Specification:**
- Write a function `Pipeline(nums []int, workers int) []int` that squares every number in `nums`.
- **Fan-out:** distribute the numbers across `workers` goroutines that each compute squares concurrently.
- **Fan-in:** merge all the workers' outputs into a single result slice.
- Do not care about preserving input order in the output.
- No goroutine should leak — the function must return only once all workers have finished.

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. **Source stage:** a single goroutine feeds all input numbers into an input channel, then closes it — closing signals "no more work" to every downstream stage.
2. **Fan-out stage:** `workers` goroutines all range over the *same* input channel. The runtime's channel receive naturally load-balances — whichever worker is free grabs the next value. No manual work assignment needed.
3. **Fan-in stage:** each worker writes its result to a shared output channel. A `sync.WaitGroup` tracks when all workers are done; a dedicated goroutine waits on the group and then closes the output channel — this is the standard idiom, because only the WaitGroup knows when the *last* writer has finished.
4. The main goroutine ranges over the output channel until it's closed, collecting results.

```go
func Pipeline(nums []int, workers int) []int {
	in := make(chan int)
	go func() {
		defer close(in)
		for _, n := range nums {
			in <- n
		}
	}()

	out := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for n := range in { // fan-out: workers compete for values from `in`
				out <- n * n
			}
		}()
	}

	go func() {
		wg.Wait()  // wait for every worker to finish writing
		close(out) // only close after the LAST writer is done
	}()

	results := make([]int, 0, len(nums))
	for v := range out { // fan-in: collect until out is closed
		results = append(results, v)
	}
	return results
}
```

**The Trap:** The most common mistake is closing `out` too early — e.g., closing it right after `wg.Add`/launching workers instead of after `wg.Wait()` returns. That causes a `panic: send on closed channel` the instant a worker still has a value to write. The second common mistake is having every worker call `close(out)` individually — closing an already-closed channel panics.

**Interview Signal:** Tests whether you can correctly compose multiple channel-based stages and, specifically, whether you know that "only the last writer may close a channel" — enforced here via `WaitGroup.Wait()` in a dedicated closer goroutine.

**Key Rule to Remember:** Rule: in fan-in, only close the merged output channel after a `WaitGroup` confirms every fan-out worker has finished — never let a worker close a channel other goroutines still write to.

---

---

## Exercise 5 · `[Implement]` — Worker pool with bounded concurrency

**Context:** "Implement a worker pool" is asked at nearly every mid-to-senior Go interview — it is the direct, practical application of buffered channels as a concurrency limiter.

**Specification:**
- Write `RunJobs(jobs []func() error, maxConcurrency int) []error`.
- At most `maxConcurrency` jobs run at the same time, regardless of how many jobs there are.
- Return a slice of errors, one per job, in the **same order as the input** `jobs` slice.
- Do not care about optimizing beyond correctness and boundedness — a simple, readable solution is preferred.

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. Preserving order while running concurrently means each job needs to write its result to a **pre-sized results slice at its own index** — not to a channel (which would lose ordering) and not append (which would race).
2. Bounding concurrency is done with a **buffered channel of capacity `maxConcurrency`** acting as a counting semaphore: acquire a slot by sending into it (blocks once full), release by receiving from it.
3. A `sync.WaitGroup` tracks all launched goroutines so the function doesn't return before every job finishes.

```go
func RunJobs(jobs []func() error, maxConcurrency int) []error {
	results := make([]error, len(jobs))
	sem := make(chan struct{}, maxConcurrency) // counting semaphore
	var wg sync.WaitGroup

	for i, job := range jobs {
		wg.Add(1)
		sem <- struct{}{} // acquire — blocks if maxConcurrency slots are taken
		go func(i int, job func() error) {
			defer wg.Done()
			defer func() { <-sem }() // release
			results[i] = job()
		}(i, job)
	}

	wg.Wait()
	return results
}
```

**The Trap:** Two mistakes are common. First, sending into `sem` *inside* the goroutine instead of before launching it — that defeats the purpose, because launching the goroutine itself is cheap and unbounded; you must block the **launching loop**, not the goroutine, to actually cap concurrency. Second, writing results via `append` from multiple goroutines — a data race, and it also destroys ordering since goroutines finish out of order.

**Interview Signal:** Tests whether you know a buffered channel of capacity N is the idiomatic Go counting semaphore, and whether you can reason about *where* the blocking needs to happen to actually bound concurrency (at acquisition time, before the goroutine spawns further work).

**Key Rule to Remember:** Rule: a buffered channel of size N is a semaphore — `ch <- struct{}{}` to acquire, `<-ch` to release. Acquire **before** spawning the goroutine that does the bounded work, not inside it.

---

---

## Exercise 6 · `[Coding]` — Print in Order (classic multithreaded interview problem)

**Context:** This is a direct adaptation of LeetCode 1114 "Print in Order" / 1115 "Print FooBar Alternately" — real problems asked in onsite rounds when the interviewer wants to see channel-based synchronization from scratch, not just theory. It also appears in disguise as "implement a strict ping-pong between two goroutines."

**Specification:**
- You have three functions `first()`, `second()`, `third()`, each just printing their own name.
- Three goroutines call `first()`, `second()`, `third()` respectively, started in an unpredictable order (simulate this by launching them in reverse order: third, second, first).
- Guarantee that regardless of goroutine scheduling, the output is always `first`, then `second`, then `third` — in that exact order, every run.
- Use only channels (no `sync.Mutex`, no `sync.WaitGroup` for the ordering logic itself — a `WaitGroup` is fine just to keep `main` alive until all three finish).

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. Ordering three independent goroutines requires two synchronization points: "second must not run until first is done" and "third must not run until second is done."
2. Two unbuffered channels model exactly this: `firstDone` signals first→second, `secondDone` signals second→third. Each channel is a one-shot rendezvous — perfect for "wait for a single prior event," which is a strict happens-before relationship (Phase 7).

```go
type Foo struct {
	firstDone  chan struct{}
	secondDone chan struct{}
}

func NewFoo() *Foo {
	return &Foo{
		firstDone:  make(chan struct{}),
		secondDone: make(chan struct{}),
	}
}

func (f *Foo) First() {
	fmt.Print("first")
	close(f.firstDone) // signal: first is done — broadcasts to any receiver
}

func (f *Foo) Second() {
	<-f.firstDone // block until First() has run
	fmt.Print("second")
	close(f.secondDone)
}

func (f *Foo) Third() {
	<-f.secondDone // block until Second() has run
	fmt.Print("third")
}

func main() {
	f := NewFoo()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); f.Third() }()
	go func() { defer wg.Done(); f.Second() }()
	go func() { defer wg.Done(); f.First() }()
	wg.Wait()
}
```

**The Trap:** Using a buffered channel with a dummy value (`ch <- struct{}{}`) instead of `close()` works too, but `close()` is strictly better here: `close()` unblocks **all current and future receivers** at once (broadcast semantics), while a single send only unblocks exactly one receiver. If `Second()` were ever called twice by mistake, a single-value channel send approach would deadlock the second call; `close()` degrades gracefully. This is why "signal an event to possibly-many waiters" almost always means `close(ch)`, not `ch <- struct{}{}`.

**Interview Signal:** Tests whether you can build a correct happens-before chain from scratch using only channels — the exact skill being tested by LeetCode's "Print in Order" family of problems, and a direct proxy for whether you can reason about goroutine ordering in real pipeline code.

**Key Rule to Remember:** Rule: to signal "an event happened" to one-or-more waiters, prefer `close(ch)` over `ch <- struct{}{}` — close broadcasts to every current and future receiver; a send only ever wakes one.

---
