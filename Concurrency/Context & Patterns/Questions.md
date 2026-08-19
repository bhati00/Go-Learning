# Phase 9c — Context & Patterns · Interview Questions

**Target level:** 3-4 YOE Go developer
**Total questions:** 12

> Research sources consulted: Go official blog — [Go Concurrency Patterns: Context](https://go.dev/blog/context)
> (Sameer Ajmani); Go official blog — [Pipelines and Cancellation](https://go.dev/blog/pipelines);
> Go 1.21 `context` package source (`src/context/context.go`);
> Practical Go Lessons Ch. 37 (context internals, goroutine leak examples);
> Ardan Labs Go training — context and concurrency patterns material;
> Go team blog post — [Contexts and structs](https://go.dev/blog/context-and-structs).

---

## Question Distribution

| Category | Questions |
|----------|-----------|
| Runtime & Internals | Q1, Q2, Q7 |
| Language Gotchas | Q3, Q5, Q10 |
| Applied / Design | Q4, Q6, Q8, Q9 |
| Live Coding — Fix It | Q11 |
| Live Coding — Implement It | Q12 |

---

## context.WithCancel & Context Tree

### Q1 · ⭐ How does cancellation propagate through a context tree? Walk through the internals. `[Internals]`

**The question an interviewer actually asks:**
> "I call `cancel()` on a parent context. How does every child and grandchild know to stop? Walk me through what happens."

---

**HOOK:** Every `cancelCtx` maintains a `children` map. When `cancel()` is called on a parent, it closes the parent's `Done` channel, then iterates its `children` map and calls `cancel()` on each child — which does the same to its own children. The entire subtree is cancelled in a single recursive walk.

**INTERNALS:** `context.WithCancel(parent)` calls `propagateCancel(parent, child)`, which finds the nearest cancellable ancestor in the tree and inserts the new child into its `children map[canceler]struct{}`. The `cancelCtx` struct looks like this in the runtime:

```go
type cancelCtx struct {
    Context                      // parent — embedded
    mu       sync.Mutex
    done     atomic.Value        // lazily allocated chan struct{}, closed on cancel
    children map[canceler]struct{} // set to nil after first cancel
    err      error
    cause    error
}
```

When `cancel(removeFromParent, err, cause)` is called:
1. The `done` channel is closed (once, atomically).
2. Every child in `children` has its own `cancel()` called recursively.
3. `children` is set to `nil`.
4. If `removeFromParent=true`, the child removes itself from its parent's `children` map.

The `Done()` channel is created lazily — it is only allocated when `Done()` is first called. This means creating a context that nobody ever selects on is nearly free.

**REAL-WORLD:** In a microservice that receives 50,000 requests per second, every request gets a `WithCancel` context. That's 50,000 child registrations per second into parent maps, and 50,000 deregistrations when each request completes. The `mu` mutex on each `cancelCtx` is the contention point — one mutex per context level, not one global mutex, so contention is bounded per request rather than global. I've seen teams create deeply nested chains (request → middleware → handler → DB → query → retry) and wonder why context operations showed up in their pprof. Flatter context trees are cheaper.

**INSIGHT:** Cancellation is a recursive tree walk, not a broadcast. Its speed is proportional to the number of live children at each level. A context with 10,000 direct children will have `cancel()` iterate 10,000 times when cancelled — keep child counts bounded.

---

⚠️ **Weak answer sounds like:** "When you call cancel, the Done channel closes and all goroutines watching it stop."
That skips how children are registered, how the recursive walk works, and why `defer cancel()` is required for deregistration.

💬 **Likely follow-up:** "What is `propagateCancel`? What does it do when the parent is already cancelled by the time you call `WithCancel`?"

---

### Q2 · ⭐ Why does a goroutine checking `ctx.Done()` not stop automatically when a context is cancelled? `[Conceptual]`

**The question an interviewer actually asks:**
> "I cancelled a context. Why is the goroutine still running? I thought cancelling the context would stop it."

---

**HOOK:** Cancelling a context closes a channel — it sends a signal. The goroutine must actively receive from that channel in a `select` to see the signal. If it's in the middle of a CPU-bound loop or a blocking call that doesn't watch `ctx.Done()`, it has no way to see the cancellation until it next reaches a `select` that includes `<-ctx.Done()`.

**INTERNALS:** `context.WithCancel` returns a `Done()` channel — a `<-chan struct{}`. Cancellation closes this channel. Receiving from a closed channel returns immediately — but only if the goroutine is doing that receive. The runtime has no mechanism to "inject" a stop into an arbitrary goroutine mid-execution. Go does not have goroutine cancellation at the scheduler level. The only mechanism is cooperative: the goroutine itself must periodically yield to a `select` containing `<-ctx.Done()`.

**REAL-WORLD:** The most common manifestation is a database or HTTP client call that takes 10 seconds, while the context's deadline is 500ms. The context is "cancelled" after 500ms, but the underlying TCP connection is still running. The fix is to pass the context into every blocking call — `http.NewRequestWithContext(ctx, ...)`, `db.QueryContext(ctx, ...)` — so the library cancels the I/O at the OS level on your behalf. A goroutine that calls a context-unaware function (`time.Sleep`, raw `syscall.Read`) will not honour the cancellation until that function returns.

**INSIGHT:** Context cancellation is a cooperative signal, not a preemptive interrupt. A goroutine that never calls `<-ctx.Done()` is effectively invisible to cancellation. Pass context into every blocking operation, not just into goroutine launch points.

---

⚠️ **Weak answer sounds like:** "The goroutine stops when context is cancelled."
This is wrong. The context signals a stop; the goroutine decides when to look at the signal.

💬 **Likely follow-up:** "What happens if you call a blocking system call like `io.Read` inside a goroutine with a cancelled context? Does the read return immediately?"

---

## Context Leak

### Q3 · ⭐ What is a context leak? Give a concrete code example and explain what goes wrong at the runtime level. `[Applied]`

**The question an interviewer actually asks:**
> "I've heard 'context leak' but it doesn't seem to cause immediate crashes. Why is it a problem and what actually leaks?"

---

**HOOK:** A context leak is when `cancel()` is never called after `WithCancel`, `WithTimeout`, or `WithDeadline`. The child context stays registered in its parent's `children` map indefinitely, holding memory and (for timed contexts) a live timer goroutine until the parent itself is eventually cancelled.

**INTERNALS:** Every `WithCancel` call does two things: creates a `cancelCtx` and inserts a pointer to it into the parent's `children` map. That pointer keeps the child reachable from the parent — the GC cannot collect it. `cancel()` is the only thing that removes this pointer (by calling `removeChild`, which deletes the entry from the parent's map). Without `cancel()`, the child lives in that map for the lifetime of the parent.

For `WithTimeout` and `WithDeadline`, the leak is worse: a `timerCtx` embeds a `time.Timer`, which schedules a goroutine in the runtime's timer heap. If the work finishes in 10ms but the timeout is 5 seconds, not calling `cancel()` leaves a timer goroutine parked for 5 more seconds, burning a slot in the timer heap.

```go
// BUG: cancel discarded — ctx leaks until parent is cancelled
func handleRequest(parent context.Context) {
    ctx, _ := context.WithTimeout(parent, 5*time.Second)
    fetchFromDB(ctx) // returns in 10ms
    // ctx is still registered in parent.children until parent cancels (5s later)
    // The 5-second timer goroutine is still running
}

// FIX: always defer cancel immediately after creation
func handleRequest(parent context.Context) {
    ctx, cancel := context.WithTimeout(parent, 5*time.Second)
    defer cancel() // fires in 10ms when handleRequest returns
    fetchFromDB(ctx)
}
```

`go vet` detects the discarded cancel: _"the cancel function is not used on all paths (possible context leak)"_.

**REAL-WORLD:** In a service handling 10,000 requests per second with a 5-second timeout, forgetting `defer cancel()` means at peak the parent context holds ~50,000 live child contexts and ~50,000 timer goroutines simultaneously. Each timer goroutine consumes a few hundred bytes. At 50,000 that's tens of MB of unexplained heap growth that appears only under production load — not in local tests where throughput is low. I've seen this surface as "the service gradually slows down and uses more memory over 24 hours without an obvious cause."

**INSIGHT:** The `cancel` function is cleanup, not an early-exit signal. Treat it exactly like `defer f.Close()` on a file — call it on the very next line after creating the context, unconditionally. The deferred call runs for free if the work completes normally; it's not wasted even when the timeout would have fired anyway.

---

⚠️ **Weak answer sounds like:** "You should call cancel() when you want to cancel the context early."
This misses the point — `cancel()` is mandatory even on the happy path. Its purpose is cleanup, not just cancellation.

💬 **Likely follow-up:** "Does the same leak apply to `context.WithValue`? Does calling `WithValue` require any cleanup?"

---

## WithTimeout vs WithDeadline

### Q4 · What is the difference between `context.WithTimeout` and `context.WithDeadline`? When would you choose each? `[Conceptual]`

**The question an interviewer actually asks:**
> "I see both WithTimeout and WithDeadline in our codebase. They seem to do the same thing. Are they interchangeable?"

---

**HOOK:** They are identical at the runtime level — `WithTimeout` is a one-line wrapper that calls `WithDeadline(parent, time.Now().Add(d))`. The difference is purely expressive: `WithTimeout` takes a duration ("finish within 200ms"), `WithDeadline` takes an absolute clock time ("finish before 3:00 AM"). Use whichever makes your intent clearer.

**INTERNALS:** From the Go source:
```go
func WithTimeout(parent Context, timeout time.Duration) (Context, CancelFunc) {
    return WithDeadline(parent, time.Now().Add(timeout))
}
```
Both return a `*timerCtx` — a `cancelCtx` with an embedded `time.Timer`. The timer calls the context's internal cancel function when it fires. The `Deadline()` method on both returns the same absolute `time.Time` value; there is no "timeout" field — everything is stored as a deadline internally.

**REAL-WORLD:** `WithTimeout` is the right choice for per-call latency budgets: "this DB query gets 500ms", "this HTTP call gets 2s". `WithDeadline` is the right choice for batch processing windows: "all jobs in this batch must finish before midnight", "the SLA says we must respond within the hour from request receipt — and the request is already 47 minutes old." In the batch case, computing `time.Now().Add(13 * time.Minute)` manually and passing it to `WithTimeout` is fragile; `WithDeadline(ctx, slaExpiry)` is clear.

**INSIGHT:** A child context's effective deadline is always `min(own deadline, parent deadline)`. `WithDeadline` makes this constraint explicit — if you write `WithDeadline(parent, midnight)` and the parent expires at 11:50 PM, your context expires at 11:50 PM, not midnight. The child can never outlive its parent, regardless of which function you use to create it.

---

⚠️ **Weak answer sounds like:** "WithTimeout takes a duration and WithDeadline takes a time, that's the only difference."
True but incomplete — misses the implementation identity, the deadline inheritance rule, and the idiom for when to prefer each.

💬 **Likely follow-up:** "If I call WithTimeout(parent, 10s) and the parent context already has a deadline of 2s, what is the effective timeout?"

---

### Q5 · ⭐ A parent context expires in 300ms. You call `WithTimeout(parent, 5*time.Second)`. When does the child context expire? `[Gotcha]`

**The question an interviewer actually asks:**
> "Here's a short quiz. Parent deadline is 300ms from now. I derive a child with a 5-second timeout. When does the child cancel? In 300ms or in 5 seconds?"

---

**HOOK:** 300ms. The child's effective deadline is `min(parent deadline, child deadline)`. A child context can never outlive its parent — that would break the tree guarantee that cancelling a parent cancels all descendants.

**INTERNALS:** `WithDeadline` (which `WithTimeout` calls) explicitly checks: if `cur, ok := parent.Deadline(); ok && cur.Before(d)`, the parent's deadline is tighter, so a `cancelCtx` is returned (not a `timerCtx`) — no timer is even started, because the parent will cancel it before the child's timer would fire. The child still gets a proper cancellation when the parent fires; it just doesn't have its own timer.

```go
func WithDeadline(parent Context, d time.Time) (Context, CancelFunc) {
    // ...
    if cur, ok := parent.Deadline(); ok && cur.Before(d) {
        // Parent deadline is before our deadline — child will be cancelled first
        // by parent. No need to start a timer; just return a cancelCtx.
        return WithCancel(parent)
    }
    // ...start timer for d...
}
```

**REAL-WORLD:** This is a common source of confusion in middleware stacks. A framework sets a 30-second request deadline on the root context. An individual RPC call sets a 60-second timeout. The RPC times out in 30 seconds — the framework's deadline wins — and no timer is started for the 60-second child. Engineers see "RPC timed out after 30s" and are confused because they set a 60-second timeout. The solution: always use `ctx.Deadline()` to check what the effective deadline is before starting long-running operations. If the remaining time is too small to do the work, fail fast rather than starting.

**INSIGHT:** Setting a timeout looser than the parent's is not an error — it's silently ignored. The runtime optimises it away. But it can mislead readers who see `WithTimeout(ctx, 60s)` and assume the operation has 60 seconds.

---

⚠️ **Weak answer sounds like:** "5 seconds, because I set 5 seconds."
This misses the fundamental contract of the context tree: children cannot outlive parents.

💬 **Likely follow-up:** "How would you check programmatically how much time is left in a context before starting an expensive operation?"

---

## context.Value

### Q6 · What should and should not be stored in `context.Value`? What is the risk of using a string as a key? `[Applied]`

**The question an interviewer actually asks:**
> "My team is using `context.WithValue` to pass user ID, database connection, config struct, and a logger. Is this OK? What's the right way to use context values?"

---

**HOOK:** `context.Value` is for request-scoped cross-cutting concerns — request IDs, authentication tokens, trace spans. It is **not** for passing function parameters. A database connection or config struct is a function parameter — put it in the function signature. Using a plain string key is dangerous: two packages using the same string silently shadow each other's values.

**INTERNALS:** The risk of string keys:

```go
// Package A
ctx = context.WithValue(ctx, "userID", "alice")

// Package B (independent, in the same binary)
ctx = context.WithValue(ctx, "userID", 42) // overwrites A's value in the chain

// Somewhere in Package A:
id := ctx.Value("userID").(string) // panics: int is not a string
```

Because `context.Value` uses `==` for key comparison, `"userID"` in package A and `"userID"` in package B are the same key. They collide. The correct fix is an unexported custom type:

```go
type contextKey int     // unexported — no other package can construct this type
const userIDKey contextKey = iota
ctx = context.WithValue(ctx, userIDKey, "alice")
```

Now `contextKey(0)` from package A and `contextKey(0)` from package B are **different types** — they compare unequal even with the same underlying value, because Go's `==` for interface values compares both type and value.

**REAL-WORLD:** Appropriate uses: request ID (tracing), authenticated user principal (authorisation middleware), distributed trace span (OpenTelemetry). These are all "who is making this call" metadata — they flow through every layer but are never core business logic inputs. Inappropriate uses: `*sql.DB`, `*redis.Client`, `Config`, `Logger`. These are dependencies; they should be injected through constructors or explicit function parameters so the function signature is honest about what it needs. I've seen codebases where `ctx.Value("db")` was used to retrieve a database connection — and then a unit test silently ran against a nil connection because nobody passed the right context key.

**INSIGHT:** `context.Value` is an escape hatch for metadata you don't want to thread through 10 layers of function signatures. Every usage is a design smell that should prompt the question: "Is this truly cross-cutting request metadata, or is it a dependency I'm hiding from the type system?"

---

⚠️ **Weak answer sounds like:** "Use string keys and avoid collisions with naming conventions like 'mypkg/userID'."
Naming conventions rely on discipline. Unexported type keys rely on the compiler. Use the compiler.

💬 **Likely follow-up:** "What is the lookup cost of `ctx.Value`? Does it matter at scale?"

---

### Q7 · ⭐ What is the time complexity of `ctx.Value(key)`? Why? `[Internals]`

**The question an interviewer actually asks:**
> "I'm calling `ctx.Value` on a hot path — potentially millions of times per second. Should I worry about it?"

---

**HOOK:** `ctx.Value` is O(N) where N is the depth of the context chain from the current node to the root. Each `WithValue` call adds one node; `Value` walks upward through every node until it finds a matching key or reaches the root.

**INTERNALS:** Each `valueCtx` stores exactly one key-value pair and a pointer to its parent:

```go
type valueCtx struct {
    Context       // parent — pointer upward
    key, val any
}

func (c *valueCtx) Value(key any) any {
    if c.key == key {
        return c.val
    }
    return value(c.Context, key) // recurse upward
}
```

If a context chain has depth 10 (background → withCancel → withTimeout → withValue × 7), a lookup for a value stored at depth 1 requires 10 pointer dereferences and 10 equality checks. The walk also crosses `cancelCtx` and `timerCtx` nodes (which have no value), adding to the chain length without contributing to the lookup.

**Real-world worst case:** Middleware-heavy HTTP services. Each middleware layer calls `WithCancel` or `WithTimeout` (adds a non-value node) and possibly `WithValue` (adds a value node). By the time a request handler is reached, the chain might be 8-12 nodes deep. For a simple request-ID lookup, that's 12 pointer dereferences. At 100,000 requests/second with 10 lookups per request, that's 12,000,000 pointer dereferences per second for request-ID alone. This is measurable in a CPU profile — context lookups appearing in hot paths is a known Go performance issue.

**FIX:** For hot-path access to a single value, extract it from context once at the entry point and pass it explicitly:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    reqID := r.Context().Value(requestIDKey).(string) // look up once
    processOrder(r.Context(), reqID, order)           // pass explicitly
}

func processOrder(ctx context.Context, reqID string, order Order) error {
    // use reqID directly — no ctx.Value call in the hot path
}
```

**INSIGHT:** `context.Value` is a linked-list scan, not a hash map lookup. Store one composite struct per concern (`type RequestMetadata struct { ID, UserID, TraceID string }`) and retrieve it once rather than calling `WithValue` and `Value` per field. The chain stays shallower and lookup cost stays bounded.

---

⚠️ **Weak answer sounds like:** "It's a simple map lookup — should be O(1)."
It is emphatically not a map. This is one of the most common misunderstandings about context performance.

💬 **Likely follow-up:** "Is there a way to make context value lookup O(1)? What are the trade-offs?"

---

## Worker Pool

### Q8 · ⭐ Design a worker pool in Go. How do you handle clean shutdown when a context is cancelled? `[Design]`

**The question an interviewer actually asks:**
> "Walk me through how you'd implement a bounded worker pool. I want N workers, a jobs channel, and for everything to shut down cleanly when the context is cancelled."

---

**HOOK:** Launch N goroutines at startup, each ranging over a shared jobs channel. Close the jobs channel to signal no more work. Use `context.Context` inside each worker's `select` to stop early on cancellation. Use `sync.WaitGroup` to know when all workers have exited before closing the results channel.

**INTERNALS:** The critical design decisions and the bugs they prevent:

```go
func workerPool(ctx context.Context, jobs <-chan Job, n int) <-chan Result {
    results := make(chan Result, n)
    var wg sync.WaitGroup

    for i := 0; i < n; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case job, ok := <-jobs:
                    if !ok {
                        return // jobs closed — normal shutdown
                    }
                    r := process(job)
                    select {
                    case results <- r:
                    case <-ctx.Done():
                        return // cancelled while trying to send result
                    }
                case <-ctx.Done():
                    return // cancelled while waiting for job
                }
            }
        }()
    }

    // Closer goroutine: wait for all workers, then close results
    go func() {
        wg.Wait()
        close(results)
    }()

    return results
}
```

**Why two `select` blocks?** The first races between receiving a job and cancellation. The second races between sending the result and cancellation. Without the second, a worker that just finished `process(job)` could block forever on `results <- r` if the context was cancelled and the collector has stopped draining results.

**Why a separate closer goroutine?** The caller returns `results` immediately — it doesn't wait for workers. Only the closer goroutine blocks on `wg.Wait()`. If you tried to close `results` in the function body (after the `for` loop), you'd have to block there — which defeats the whole purpose of returning a channel.

**Why not close jobs in the worker?** Only the producer knows when jobs are exhausted. Closing from multiple goroutines causes a double-close panic. The producer closes jobs; the workers detect close and exit; the WaitGroup fires the closer.

**REAL-WORLD:** The most common bug I see: closing `results` before `wg.Wait()`. This causes any worker that's still running and tries to send its last result to panic with "send on closed channel". The WaitGroup is the gate that prevents this. The second most common bug: not selecting on `ctx.Done()` when sending results — the collector exits (because context cancelled), the results channel fills up, and every worker blocks on `results <- r` forever. Both goroutines are now leaked.

**INSIGHT:** The channel is the throttle; closing is the shutdown signal; `WaitGroup` is the completion barrier; context is the cancellation signal. Each plays a distinct role. Mix them up and you get leaks or panics.

---

⚠️ **Weak answer sounds like:** "Use a buffered channel as a semaphore to limit concurrency."
That is a semaphore, not a worker pool. A semaphore limits concurrent execution but doesn't provide bounded goroutine count, result aggregation, or clean shutdown semantics.

💬 **Likely follow-up:** "Your `process(job)` function doesn't accept a context. How does cancellation reach it? What if it runs for 10 minutes?"

---

## Fan-out / Fan-in

### Q9 · ⭐ What is fan-out/fan-in? When does it create back-pressure problems and how do you diagnose them? `[Design]`

**The question an interviewer actually asks:**
> "I implemented a fan-out pipeline with 10 workers. Under load, everything grinds to a halt. How do I diagnose whether the bottleneck is in the fan-out stage, the fan-in merge, or the collector?"

---

**HOOK:** Fan-out distributes work from one input channel to N workers in parallel. Fan-in merges N output channels back into one. Back-pressure happens when one stage produces faster than the next stage consumes — the channel between them fills up and the faster stage blocks, becoming serialized behind the slower one.

**INTERNALS:** The back-pressure diagnosis tool is channel occupancy:

```go
// Diagnostic: how full is each channel?
fmt.Printf("jobs: %d/%d, results: %d/%d\n",
    len(jobs), cap(jobs),
    len(results), cap(results))
```

The **full** channel is the bottleneck. The stage draining it (the consumer) is slower than the stage filling it (the producer). Rules:

| Channel | Usually full | Diagnosis |
|---------|-------------|-----------|
| jobs | Yes | Workers are slower than the generator — add more workers or speed up each worker |
| results | Yes | Collector is slower than workers — the fan-in merge is a single channel; collector can't keep up |
| merge output | Yes | Downstream is slower than all workers combined — fan-out the collection stage too |

**The goroutine leak in naive fan-in:** If the collector stops reading (e.g., because context was cancelled), all workers trying to send their result into the merged channel will block forever. Every forwarder goroutine inside the merge function is also blocked. None of them will ever exit:

```go
// LEAK: merge goroutines block if nobody reads the output
func merge(channels ...<-chan Result) <-chan Result {
    out := make(chan Result) // unbuffered — blocks when collector stops
    var wg sync.WaitGroup
    for _, ch := range channels {
        wg.Add(1)
        go func(c <-chan Result) {
            defer wg.Done()
            for r := range c { // blocks here if out is full and nobody reads
                out <- r       // BLOCKS FOREVER if collector exits
            }
        }(ch)
    }
    // ...
}
```

Fix: every send in the merge must also select on `ctx.Done()`:

```go
for r := range c {
    select {
    case out <- r:
    case <-ctx.Done():
        return // collector is gone — exit cleanly
    }
}
```

**REAL-WORLD:** Back-pressure in a pipeline is not always visible as CPU spike — it often looks like steadily growing memory usage (unbounded channel buffers) or mysterious latency with all goroutines blocked. A goroutine dump (`/debug/pprof/goroutine?debug=2`) shows all blocked goroutine stacks — if you see thousands of goroutines blocked at `out <- r`, the fan-in channel is the bottleneck.

**INSIGHT:** In a pipeline, the slowest stage sets the throughput ceiling for the entire pipeline. Buffers absorb transient spikes but cannot fix a sustained mismatch. The diagnosis question is always: which channel is full? That channel's consumer needs to go faster — either by being made more efficient or by adding more goroutines to drain it.

---

⚠️ **Weak answer sounds like:** "Fan-out is good for parallelism."
This is true but misses the critical back-pressure semantics, the goroutine leak failure mode, and the diagnostic approach.

💬 **Likely follow-up:** "If you have 10 workers and the collector is the bottleneck, can you just add a second collector? What needs to change?"

---

## Applied

### Q10 · What does this code print? Is there a problem? `[Gotcha]`

```go
package main

import (
    "context"
    "fmt"
    "time"
)

func worker(ctx context.Context, id int) {
    for {
        select {
        case <-ctx.Done():
            fmt.Printf("worker %d stopped\n", id)
            return
        default:
            fmt.Printf("worker %d working\n", id)
            time.Sleep(100 * time.Millisecond)
        }
    }
}

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
    defer cancel()

    for i := 0; i < 3; i++ {
        go worker(ctx, i)
    }

    time.Sleep(1 * time.Second)
    fmt.Println("main done")
}
```

---

**HOOK:** The program has a latent problem: the `select` with a `default` case means each worker polls — it never truly parks on `ctx.Done()`. After the timeout fires at 250ms, the `default` branch runs the `time.Sleep(100ms)` path and then the next `select` iteration will see `ctx.Done()` closed. So workers stop within ~350ms. The final output is "main done" after 1 second. But the real bug: this is a CPU-burning busy loop unless each `default` branch does meaningful work.

**INTERNALS:** A `select` with a `default` case never parks the goroutine — if no case is ready, `default` runs immediately. This turns the loop into a poll-wait-poll cycle. The goroutines are always "runnable" from the scheduler's perspective; they never relinquish their P by blocking on a channel. At `time.Sleep(100ms)`, the goroutine does park (sleep is a blocking operation), so this specific example is not a true busy-wait. But if you removed the `time.Sleep`, all three workers would spin at 100% CPU burning the same P in a tight loop.

The correct cancellable-worker pattern when there is no other channel to select on:

```go
func worker(ctx context.Context, id int) {
    for {
        select {
        case <-ctx.Done():
            fmt.Printf("worker %d stopped\n", id)
            return
        case <-time.After(100 * time.Millisecond):
            fmt.Printf("worker %d working\n", id)
        }
    }
}
```

Now both cases cause the goroutine to park — no CPU is burned while waiting.

**REAL-WORLD:** The `select { case <-ctx.Done(): ... default: doWork() }` pattern is intentional and correct when the work in `default` is a real unit of computation (processing a queue item, a calculation step). It becomes a bug when `default` is empty or contains only a `time.Sleep` to slow down a spin loop. The diagnostic: if `runtime.NumCPU()` shows high scheduler activity with very little actual work done, look for goroutines with default-case selects.

**INSIGHT:** `select` without `default` = blocking wait. `select` with `default` = non-blocking poll. These have fundamentally different CPU profiles. Use `default` only when you intend to poll; use a `time.After` case when you want periodic work with clean cancellation.

---

⚠️ **Weak answer sounds like:** "The workers print 'working' and then stop after 250ms."
Correct on outcome, but misses the distinction between blocking and polling — the core point of the question.

💬 **Likely follow-up:** "Replace `time.Sleep` with an actual jobs channel. Now the worker has two things to select on: a job and a cancellation. How does the select statement change?"

---

## Live Coding — Fix It

### Q11 · Fix the context leak and goroutine leak in this code. `[Gotcha]` + `[Coding]`

**The question an interviewer actually asks:**
> "This code works in testing but leaks in production under load. Find the bugs and fix them."

```go
package main

import (
    "context"
    "fmt"
    "net/http"
    "time"
)

var client = &http.Client{}

func fetchUser(parentCtx context.Context, userID string) (string, error) {
    ctx, _ := context.WithTimeout(parentCtx, 3*time.Second)

    req, err := http.NewRequest("GET", "https://api.example.com/users/"+userID, nil)
    if err != nil {
        return "", err
    }

    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    return fmt.Sprintf("user:%s", userID), nil
}

func handler(w http.ResponseWriter, r *http.Request) {
    result, err := fetchUser(r.Context(), r.URL.Query().Get("id"))
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    fmt.Fprintln(w, result)
}
```

---

**Bug 1 — Context leak:** `context.WithTimeout` returns a cancel function that is discarded (`_`). The 3-second timer goroutine is never cleaned up when the function returns early (in < 3 seconds). Under high load this leaks thousands of timer goroutines.

**Bug 2 — HTTP request not context-aware:** `http.NewRequest` creates a request without the context. If the parent context (`r.Context()`) is cancelled (e.g., client disconnects), the HTTP call to `api.example.com` continues running — it does not inherit the cancellation. Use `http.NewRequestWithContext`.

**Fixed version:**

```go
func fetchUser(parentCtx context.Context, userID string) (string, error) {
    ctx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
    defer cancel() // fix 1: always call cancel

    req, err := http.NewRequestWithContext(ctx, "GET", "https://api.example.com/users/"+userID, nil)
    // fix 2: use NewRequestWithContext so the HTTP call respects ctx
    if err != nil {
        return "", err
    }

    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    return fmt.Sprintf("user:%s", userID), nil
}
```

**Evaluation checklist:**
1. Did they identify the discarded cancel (`_`) as the leak?
2. Did they know `http.NewRequest` vs `http.NewRequestWithContext` is a cancellation concern, not just a style choice?
3. Did they place `defer cancel()` on the line immediately following `WithTimeout`?
4. Did they check whether the error from the context-cancelled HTTP call is distinguishable (wraps `context.Canceled` or `context.DeadlineExceeded`)?

---

⚠️ **Weak answer sounds like:** "Just add `defer cancel()`."
Correct for Bug 1 but misses Bug 2. The HTTP call still runs to completion on client disconnect even with `defer cancel()` if `NewRequest` is used.

💬 **Likely follow-up:** "After the fix, if the context times out during the HTTP call, what error does `client.Do` return? How do you check if the error was caused by a context timeout vs a network failure?"

---

## Live Coding — Implement It

### Q12 · Implement a fan-out pipeline that processes a slice of URLs concurrently (max 5 workers), respects context cancellation, and collects all results. `[Design]` + `[Coding]`

**The question an interviewer actually asks:**
> "Write the core of a URL scraper: take a slice of URLs, process at most 5 at a time, collect results, and stop cleanly if the context is cancelled. Show the full wiring."

---

**Reference solution:**

```go
package main

import (
    "context"
    "fmt"
    "sync"
)

type Result struct {
    URL  string
    Body string
    Err  error
}

// generate feeds URLs into a channel, stopping on cancellation.
func generate(ctx context.Context, urls []string) <-chan string {
    out := make(chan string)
    go func() {
        defer close(out)
        for _, u := range urls {
            select {
            case out <- u:
            case <-ctx.Done():
                return
            }
        }
    }()
    return out
}

// fetch processes one URL. Real implementation would do http.Get(ctx, url).
func fetch(ctx context.Context, url string) Result {
    // simulate work; in real code: http.NewRequestWithContext(ctx, ...)
    select {
    case <-ctx.Done():
        return Result{URL: url, Err: ctx.Err()}
    default:
        return Result{URL: url, Body: "content of " + url}
    }
}

// fanOut launches n workers, each reading from jobs and writing to results.
// Returns results channel; closes it when all workers are done.
func fanOut(ctx context.Context, jobs <-chan string, n int) <-chan Result {
    results := make(chan Result, n)
    var wg sync.WaitGroup

    for i := 0; i < n; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case url, ok := <-jobs:
                    if !ok {
                        return
                    }
                    r := fetch(ctx, url)
                    select {
                    case results <- r:
                    case <-ctx.Done():
                        return
                    }
                case <-ctx.Done():
                    return
                }
            }
        }()
    }

    go func() {
        wg.Wait()
        close(results)
    }()

    return results
}

func scrape(ctx context.Context, urls []string) []Result {
    jobs := generate(ctx, urls)
    resultCh := fanOut(ctx, jobs, 5) // max 5 concurrent workers

    var all []Result
    for r := range resultCh { // range exits when resultCh is closed
        all = append(all, r)
    }
    return all
}
```

**Evaluation checklist:**
1. **Correctness:** Does it process all URLs when no cancellation occurs?
2. **Bounded concurrency:** Are exactly N workers created — not one per URL?
3. **Goroutine lifecycle:** Do all goroutines exit when `ctx` is cancelled? Do any block waiting to send?
4. **Context/cancellation:** Is `ctx` passed into the work function (`fetch`)? Is `ctx.Done()` selected on both receives and sends?
5. **Results closer:** Is `close(results)` gated behind `wg.Wait()`, not called before all workers finish?
6. **Generator cleanup:** Does the generator goroutine return on cancellation, or does it block trying to send to `jobs`?

---

⚠️ **Weak answer sounds like:** Launching one goroutine per URL with a semaphore channel.
`make(chan struct{}, 5)` limits concurrency but creates one goroutine per URL — with 10,000 URLs you get 10,000 goroutines. The worker pool pattern with N goroutines is the correct bounded approach.

💬 **Likely follow-up:** "Your `fanOut` returns as soon as workers exit. What if the caller wants to know when all work is done AND see any errors that occurred? How does the API change?"
