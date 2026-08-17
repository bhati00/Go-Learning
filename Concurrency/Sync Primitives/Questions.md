# Sync Primitives & Goroutine Safety — Interview Questions & Answers

Difficulty tags: `[Conceptual]` · `[Internals]` · `[Applied]` · `[Gotcha]` · `[Design]`

---

## sync.Mutex vs Channel

1. ⭐ When should you use a `sync.Mutex` instead of a channel? `[Applied]`

> **Hook:** Use a mutex when multiple goroutines share a piece of state that needs to be read or written safely. Use a channel when goroutines need to communicate — to pass ownership of a value from one to another.
>
> **Internals:** A mutex directly protects memory: it serialises access to a critical section by allowing only one goroutine to hold the lock at a time. A channel transfers a value from a sender goroutine to a receiver goroutine, with a happens-before guarantee attached. Forcing a channel into the role of a mutex (the `make(chan struct{}, 1)` semaphore pattern) works mechanically, but it adds channel overhead — sudog allocations, queue manipulation, potential goroutine parks — for a problem that the mutex was designed to solve in 2-3 atomic operations.
>
> **Real-world:** The most common violation I've seen is using a goroutine as a "state server" — routing all reads and writes through a channel to avoid a mutex. This works, but it serialises all access through a single goroutine's message loop, creates unbounded channel queues under load, and makes stack traces harder to read. A mutex is simpler, faster, and has a well-understood failure mode (deadlock) that the race detector catches.
>
> **Insight:** The Go proverb "don't communicate by sharing memory; share memory by communicating" is about design philosophy, not a ban on mutexes. The `sync` package exists precisely because mutexes are the right tool for protecting shared state.

> ⚠️ **Weak answer sounds like:** "You should always use channels in Go — mutexes are the old way."
> This misreads the proverb and shows no understanding of when each tool is appropriate.

> 💬 **Likely follow-up:** "If a mutex is held during a long operation, what happens to other goroutines waiting to acquire it? Is there anything you can do?"

---

2. What does it mean for a mutex to be "held during a long operation"? What are the consequences? `[Applied]`

> **Hook:** While a goroutine holds a mutex, every other goroutine trying to acquire the same lock is parked — they cannot run their critical section at all. The longer the lock is held, the more goroutines pile up in the wait queue, and the worse your tail latency becomes.
>
> **Internals:** When a goroutine calls `mu.Lock()` and the mutex is already held, it first spins briefly (hoping the holder releases quickly), then parks itself — creating a sudog and sleeping. Each parked goroutine is a scheduler context switch: the current M switches to run other goroutines, and must switch back when the lock is released. Under high contention, this becomes a cascade: lock → park → context switch → resume → lock → park → ... The mutex's internal state machine (starvation mode) exists specifically to prevent some goroutines from waiting indefinitely (Topic 7 in the README).
>
> **Real-world:** The classic mistake is making a network call or a database query while holding a lock. A 100ms database call while holding a mutex means every other goroutine needing that lock waits at least 100ms. The fix is always the same: copy out what you need under a short lock, do the expensive work outside the lock, acquire the lock again to write results.
>
> **Insight:** Lock granularity is a performance dial. The narrower the critical section, the less contention. The extreme — an atomic operation instead of a mutex — is sometimes worth it for simple counters. Measure before assuming.

> ⚠️ **Weak answer sounds like:** "Other goroutines will wait for the lock."
> True but surface-level. Missing the scheduler cost, the tail latency impact, and the remediation pattern.

> 💬 **Likely follow-up:** "How do you detect mutex contention in a running Go service?"

---

## sync.RWMutex

3. ⭐ When does `sync.RWMutex` hurt performance instead of helping it? `[Internals]`

> **Hook:** `RWMutex` hurts performance when writes are frequent — because each `RLock`/`RUnlock` is heavier than a plain `Lock`/`Unlock` (atomic reader-count operations), and a waiting writer blocks all new readers anyway, temporarily converting the RWMutex back into an exclusive lock.
>
> **Internals:** `RWMutex` maintains a reader count with an atomic add/subtract on every `RLock`/`RUnlock`. When a writer calls `Lock()`, it atomically adds a large negative sentinel to the reader count to signal "writer waiting," then waits for the current reader count to drain to zero. While the writer waits, all new readers block — they try to `RLock`, see the writer-waiting sentinel, and park. So the moment a writer arrives, you get writer-wait overhead AND reader-blocking overhead — more total work than a plain mutex would have done.
>
> **Real-world:** I've seen a service switch from `sync.Mutex` to `sync.RWMutex` to "improve read performance" on a config map. The config was refreshed every 30 seconds (rare writes, fine) but the reads happened 100,000 times per second across 16 CPUs. The atomic reader-count increment on every `RLock` became a false sharing hotspot — 16 cores hammering the same cache line. `sync.Mutex` was faster because an uncontended `Lock`/`Unlock` pair has lower memory bus traffic than `RLock`/`RUnlock` on a shared counter.
>
> **Insight:** `RWMutex` wins when reads are long enough that holding two concurrent reads in parallel saves more time than the reader-count bookkeeping costs. For very short critical sections (a map lookup, a field read), the bookkeeping can exceed the protected work. Always benchmark.

> ⚠️ **Weak answer sounds like:** "RWMutex is better than Mutex because multiple readers can hold it simultaneously."
> Describes the mechanism but misses every performance caveat.

> 💬 **Likely follow-up:** "How does Go prevent writer starvation in RWMutex?"

---

## sync.WaitGroup

4. ⭐ What is the correct order for `wg.Add`, goroutine launch, and `wg.Wait`? Why does order matter? `[Gotcha]`

> **Hook:** `Add` must be called before the goroutine is launched. `Wait` must be called after the loop that launches goroutines. Calling `Add` inside the goroutine creates a race: `Wait` may return before `Add` runs.
>
> **Internals:** `WaitGroup` maintains an internal counter. `Wait()` parks the calling goroutine until the counter reaches zero. If `Add(1)` is called inside the goroutine, there is a window between the `go` statement and the `Add` call where the counter is still zero. If `Wait()` runs in that window, it sees a zero counter and returns immediately — before any goroutine has done any work. This is a genuine race condition that the race detector can catch. `Add` called before the `go` statement eliminates this window entirely, because `Add` completes in the same goroutine before the scheduler has any opportunity to run `Wait`.
>
> **Real-world:** I've seen this exact bug in test code where a test function launched goroutines and called `wg.Wait()` without proper ordering. Under low load the test passed because goroutines ran before `Wait` was evaluated. Under high CI load (many parallel tests) the race surfaced and tests completed before goroutines finished, reporting false successes.
>
> **Insight:** The `defer wg.Done()` pattern at the top of every goroutine is not just style — it guarantees the counter is decremented even if the goroutine panics mid-execution. Without it, a panicking goroutine leaves the counter permanently above zero, and `Wait()` blocks forever.

> ⚠️ **Weak answer sounds like:** "You call Add before Wait."
> Missing the specific placement relative to `go` and the race condition that incorrect ordering creates.

> 💬 **Likely follow-up:** "Can you reuse a WaitGroup after Wait returns?"

---

5. What does this code print? Is there a bug? `[Gotcha]`

```go
package main

import (
    "fmt"
    "sync"
)

func main() {
    var wg sync.WaitGroup
    for i := 0; i < 3; i++ {
        go func() {
            wg.Add(1)
            defer wg.Done()
            fmt.Println("working")
        }()
    }
    wg.Wait()
    fmt.Println("done")
}
```

> **Hook:** This program has a race condition. It may print "done" before any "working" line appears, or it may deadlock if `Wait` is called after the goroutines have already run and decremented a counter that was never incremented in time.
>
> **Internals:** `wg.Add(1)` is called inside the goroutine — after the `go` statement. `wg.Wait()` is called immediately after the loop. There is a real race: `Wait` sees the counter at zero (no `Add` has been called yet) and returns immediately. The goroutines may run after `main` has already returned, or they may run partially, printing "working" with no "done" following. Under the race detector (`go run -race`), this reports a data race on the WaitGroup's internal counter. The fix: move `wg.Add(1)` to before the `go` statement.
>
> **Real-world:** This pattern is particularly dangerous in table-driven tests: the loop launches goroutines, the test body calls `wg.Wait()`, and everything looks fine locally because goroutines happen to run before Wait under low load. Add `-count=10 -race` to the test command and the race surfaces immediately.
>
> **Insight:** `WaitGroup` is not internally synchronised against the goroutine launch — it's synchronised against explicit `Add` and `Done` calls. The programmer is responsible for the ordering.

> ⚠️ **Weak answer sounds like:** "It prints working three times, then done."
> Ignores the race between Add and Wait entirely.

> 💬 **Likely follow-up:** "How would you fix this with a single-line change?"

---

## sync.Pool

6. ⭐ What is `sync.Pool`'s relationship with the garbage collector? What is the main gotcha? `[Internals]`

> **Hook:** `sync.Pool` is cleared by the GC on every collection cycle. Objects you put into a pool are not guaranteed to survive until the next `Get` — if a GC runs between your `Put` and `Get`, the object is gone and `New` will be called to allocate a fresh one.
>
> **Internals:** The runtime registers a cleanup hook in `sync.Pool` that is called during every GC cycle. On GC, the current per-P local pools are moved to a "victim cache," and the previous victim cache is dropped. An object survives at most two GC cycles: one in the local pool, one in the victim cache. After that it is unreachable and collected. This is intentional — the pool is an *allocation amortiser*, not a memory store. The per-P design (one pool shard per logical processor P) means `Get` and `Put` are lock-free on the fast path: each P only reads from and writes to its own shard, with no contention from other Ps.
>
> **Real-world:** The most common production mistake is storing objects in `sync.Pool` that hold mutable state from a previous use — log buffers, JSON encoders, byte slices — and not resetting them before use. A goroutine calls `Get`, skips the `Reset`, and processes data that includes leftover bytes from a previous request. This produces corrupted output that's nearly impossible to reproduce because it depends on which goroutine last used that pool object. The rule: always `Reset()` before use, never after `Put`.
>
> **Insight:** `sync.Pool` is worth using when profiling shows a specific, frequently-allocated object type creating GC pressure. Use it surgically, not defensively. For connection pools or any resource that must be explicitly released, it is the wrong abstraction entirely — use a purpose-built pool.

> ⚠️ **Weak answer sounds like:** "sync.Pool caches objects so you don't have to allocate them every time."
> True but missing the GC-clears-pool behaviour, which is the entire operational characteristic of the type.

> 💬 **Likely follow-up:** "If Pool.New is nil and the pool is empty, what does Get return?"

---

7. Why must you reset a pooled object before using it? Show a concrete example of what goes wrong if you don't. `[Gotcha]`

> **Hook:** A pooled object retains whatever state it had when the last user called `Put`. If you don't reset it, you're processing stale data from a previous, unrelated operation — which can mean corrupted output, information leakage across requests, or subtle logic bugs.
>
> **Internals:** `sync.Pool` has no concept of object state — it just stores and returns pointers. When goroutine A finishes with a `bytes.Buffer` and calls `pool.Put(buf)`, the buffer still holds A's data in its internal byte slice. When goroutine B calls `pool.Get()`, it receives that same buffer with A's data intact. If B calls `buf.WriteString("hello")` without first calling `buf.Reset()`, it appends to A's leftover data — not to an empty buffer.
>
> **Real-world:**
> ```go
> var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
>
> // Bug: no Reset — leaks data from previous request
> func buildResponse(data string) string {
>     buf := bufPool.Get().(*bytes.Buffer)
>     defer bufPool.Put(buf)
>     buf.WriteString(data)         // may append to leftover data
>     return buf.String()           // returns garbage prefix from previous use
> }
>
> // Fix: Reset before use, not after Put
> func buildResponse(data string) string {
>     buf := bufPool.Get().(*bytes.Buffer)
>     buf.Reset()                   // clear state from previous user
>     defer bufPool.Put(buf)
>     buf.WriteString(data)
>     return buf.String()
> }
> ```
> In a web service, this produces response bodies that intermittently contain data from a previous request — a serious information-disclosure vulnerability under load.
>
> **Insight:** Reset-before-use (not reset-after-put) is the correct pattern because the pool may drop the object between `Put` and the next `Get`. Resetting after `Put` wastes work. Resetting before use is always correct regardless of what happened in between.

> ⚠️ **Weak answer sounds like:** "You should reset it after putting it back."
> Wrong order. If the GC drops the object between Put and Get, the reset-after-put was wasted and you still get a dirty object.

> 💬 **Likely follow-up:** "Is sync.Pool safe to use for HTTP request/response objects that contain net.Conn references?"

---

## sync.Map

8. ⭐ When should you prefer `sync.Map` over a plain `map` protected by `sync.RWMutex`? `[Applied]`

> **Hook:** Prefer `sync.Map` when keys are written once (at startup or during initial load) and read many more times thereafter, and when the key set is mostly stable. For maps with frequent writes, new key insertions, or mixed read/write patterns, `map + sync.RWMutex` is simpler and usually faster.
>
> **Internals:** `sync.Map` maintains two internal maps: an atomic "read map" (a pointer to a snapshot, read without any lock) and a "dirty map" (protected by a mutex, receives all writes and new key insertions). A `Load` on an existing key hits only the atomic read map — zero lock acquisition. A `Store` of a new key hits the dirty map under a mutex. When the number of dirty-map misses on the read map crosses a threshold, the dirty map is promoted atomically to become the new read map. This promotion is the design assumption: write once, promote once, read lock-free forever after.
>
> **Real-world:** The textbook use case is a service registry or plugin handler map: at startup, all handlers are registered via `Store` (writes, goes through dirty map with lock). During request handling, millions of `Load` calls per second retrieve handlers (reads, hit the atomic read map, no lock). A `map + RWMutex` would work but every `RLock`/`RUnlock` adds atomic read-count overhead. `sync.Map` eliminates it entirely for stable key sets. Where I've seen `sync.Map` used badly: per-request metrics counters — new keys added constantly, values updated on every request. Every write goes through the mutex path, and the promotion never fires because the dirty map is always being modified. Worse than `map + sync.Mutex`.
>
> **Insight:** `sync.Map` is not a drop-in replacement for `map + mutex` — it's a specialised data structure with a specific access pattern assumption baked into its design. Read the name: it's a map optimised for reads. If your workload is write-heavy, it actively works against you.

> ⚠️ **Weak answer sounds like:** "sync.Map is the thread-safe version of map."
> Technically true but misses every performance characteristic. An interviewer wants to know when each is appropriate.

> 💬 **Likely follow-up:** "What does sync.Map's Range method guarantee about consistency?"

---

## sync.Once

9. ⭐ How does `sync.Once` achieve zero overhead on all calls after the first? `[Internals]`

> **Hook:** After the first call to `Do` completes, `sync.Once` sets a `done` flag atomically. Every subsequent call performs a single atomic load of that flag, sees it is set, and returns immediately — no lock, no function call, effectively just one CPU instruction.
>
> **Internals:** `sync.Once` uses a two-phase fast/slow path. The fast path is an atomic load of the `done` uint32 field. If `done == 1`, the call returns immediately. If `done == 0`, the goroutine enters the slow path: it acquires a `Mutex`, double-checks `done` (another goroutine may have won the race), runs `f()` if still zero, atomically stores `done = 1`, and unlocks. The double-check inside the mutex prevents two goroutines that both fail the fast path from both running `f`. After the first completion, every future call is just an atomic load — one instruction, no memory contention, no kernel interaction.
>
> **Real-world:** This is the Go equivalent of the double-checked locking pattern from Java — but correct, because Go's memory model guarantees that the atomic store of `done=1` happens-before the atomic load that subsequent callers see. In Java, the naive version of double-checked locking without `volatile` is broken due to reordering. In Go, the runtime's memory model via the atomic operations ensures correctness.
>
> **Insight:** `sync.Once` is the canonical way to implement lazy initialisation in Go — init-on-first-use with no overhead on the hot path. It's particularly valuable for initialising package-level resources (database connections, config parsers) where you want zero cost if the resource is never used, and safe concurrent initialisation if it is.

> ⚠️ **Weak answer sounds like:** "sync.Once uses a mutex to make sure only one goroutine runs the function."
> Partially true — but missing the fast-path atomic load that makes it efficient, and the double-check pattern that makes it correct.

> 💬 **Likely follow-up:** "What happens if the function passed to Once panics? Is the Once 'used up'?"

---

10. What happens if the function passed to `sync.Once.Do` panics? `[Gotcha]`

> **Hook:** If `f` panics, `Do` still marks the Once as done — `done` is set to 1. The panic propagates to the calling goroutine, but no future call to `Do` will ever run `f` again. The Once is permanently consumed.
>
> **Internals:** The `done = 1` store happens in a `defer` inside the slow path, before the panic unwinds the stack. This is intentional: the alternative — leaving `done = 0` on panic — would mean every caller retrying after the first failure, which creates its own correctness problems if `f` has partial side effects (half-initialised state) that make retrying unsafe. The runtime designers chose "fail once, fail permanently" as the safer invariant.
>
> **Real-world:** This means if you initialise a database connection inside `Once.Do` and the connection string is invalid, the connection is never established and every future call returns whatever value `db.conn` had (nil). The fix is to panic loudly on unrecoverable init failure (signalling to ops that the service is misconfigured) or to not use `sync.Once` for anything that might legitimately fail and need to be retried — use a mutex and a state machine instead.
>
> **Insight:** `sync.Once` is the right tool for "initialise once and it will definitely succeed" (parsing a static config, registering handlers). It is the wrong tool for "try to connect to a remote service at startup" — because remote connectivity can fail transiently, and you want retry logic, which Once cannot provide.

> ⚠️ **Weak answer sounds like:** "The Once will try again on the next call."
> Factually wrong. Once consumed is Once consumed, regardless of how the first call ended.

> 💬 **Likely follow-up:** "How would you implement retry-on-failure initialisation without sync.Once?"

---

## sync.Mutex Starvation Mode

11. ⭐ What is `sync.Mutex` starvation mode? What triggers it and what changes when it's active? `[Internals]`

> **Hook:** Starvation mode is a fairness mechanism added in Go 1.9. When a goroutine has been waiting for a mutex for more than 1 millisecond, the mutex switches to FIFO handoff mode — the lock is handed directly to the head of the wait queue, and no new goroutine is allowed to acquire it by spinning.
>
> **Internals:** In normal mode, when a goroutine calls `Unlock()`, it wakes the head waiter but does not hand off the lock. The lock is available to be snatched by any goroutine currently spinning — including brand-new arrivals. This "barging" is cache-friendly (the CPU running the current goroutine already has the relevant cache lines) but unfair to parked waiters. Starvation mode flips this: the `Unlock` writes the lock ownership directly to the head waiter's goroutine ID. Any new goroutine calling `Lock()` sees the mutex as taken and goes straight to the wait queue — no spinning, no barging. Starvation mode exits when the wait queue is empty or when the goroutine at the head waited less than 1ms (indicating contention has subsided).
>
> **Real-world:** You won't configure this directly — it's automatic. But it matters for reasoning about tail latency under contention. In a service that shows a long tail of slow requests, starvation mode is why those requests eventually complete rather than waiting indefinitely. Without it, a goroutine with bad scheduling luck could wait seconds on a heavily contended mutex while faster goroutines keep barging. With starvation mode, the maximum wait is bounded.
>
> **Insight:** Normal mode optimises for throughput (fewer goroutine switches, better cache locality from barging). Starvation mode optimises for tail latency (every waiter is guaranteed forward progress). The runtime switches dynamically, giving you both: fast in the common case, fair when contention is severe.

> ⚠️ **Weak answer sounds like:** "sync.Mutex is always FIFO."
> It is only FIFO in starvation mode. In normal mode, new goroutines can barge ahead of parked waiters.

> 💬 **Likely follow-up:** "Is there any way to force starvation mode, or prevent the mutex from entering it?"

---

## Goroutine Leak

12. ⭐ What are the two most common causes of goroutine leaks? How do you detect them in production? `[Applied]`

> **Hook:** The two most common causes are: (1) a goroutine blocked on a channel that will never be ready — because the sender or receiver has exited, and (2) a goroutine in an infinite loop with no exit condition — no context cancellation, no quit channel, nothing to stop it. Detection: expose `GET /debug/pprof/goroutine?debug=2` via `net/http/pprof` and look for goroutines with suspiciously deep stacks blocked in channel operations, or watch `runtime.NumGoroutine()` grow without bound under steady load.
>
> **Internals:** A goroutine blocked on a channel holds a reference to the channel's `hchan` struct via its sudog. The channel holds a reference back to the goroutine via its sendq/recvq. Neither can be collected by the GC because both are reachable from live memory — the scheduler's internal goroutine list keeps the goroutine alive. The goroutine is not Running or Runnable, so it uses no CPU, but its stack (minimum ~2KB, potentially much more) is permanently allocated. A goroutine running a tight infinite loop is worse: it's consuming CPU on every tick and holds all its stack references alive.
>
> **Real-world:** The fan-out leak is the most common pattern I've seen in production:
> ```go
> func fetchAll(urls []string) []Result {
>     results := make(chan Result) // unbuffered
>     for _, url := range urls {
>         go func(u string) {
>             results <- fetch(u) // blocks if caller stops reading
>         }(url)
>     }
>     return []Result{<-results} // only reads ONE result — other senders park forever
> }
> ```
> Under load, these accumulate silently. The first symptom is memory growing without obvious cause. The second is the goroutine dump showing hundreds of goroutines all blocked at `results <- fetch(u)` with the same call stack.
>
> **Insight:** Every goroutine you launch must have a clearly-defined exit path. Before writing `go func()`, ask: "what stops this goroutine?" If the answer is "nothing," you have a leak. The two reliable exit mechanisms are: a `context.Context` whose `Done()` the goroutine selects on, and a channel sized so goroutines can send without blocking indefinitely.

> ⚠️ **Weak answer sounds like:** "A goroutine leak is when a goroutine doesn't exit."
> That's the definition, not the cause or the fix. An interviewer wants the mechanism, the common patterns, and how to find them.

> 💬 **Likely follow-up:** "How does the goleak library work in tests? What does it actually check?"

---

13. What does this code do? Does it leak? How would you fix it? `[Design]`

```go
func process(ctx context.Context, jobs <-chan Job) <-chan Result {
    results := make(chan Result)
    go func() {
        for job := range jobs {
            results <- doWork(job)
        }
    }()
    return results
}
```

> **Hook:** This leaks if the context is cancelled before `jobs` is closed. The goroutine is blocked on `results <- doWork(job)` waiting for someone to read results — but if the caller has abandoned the context and stopped reading, the goroutine parks permanently.
>
> **Internals:** The goroutine has two exit conditions as written: (1) the `jobs` channel is closed (range exits), or (2) it can't send on `results` — which blocks it forever if nobody reads. The `ctx context.Context` parameter is accepted but never used. When the context is cancelled, the caller typically stops reading from the returned `results` channel. Now the goroutine is blocked on `results <-` with nobody on the other end, and the `jobs` channel may still have values. It parks in `results`'s sendq and never exits.
>
> **Fix:**
> ```go
> func process(ctx context.Context, jobs <-chan Job) <-chan Result {
>     results := make(chan Result)
>     go func() {
>         defer close(results) // signals downstream that no more results are coming
>         for job := range jobs {
>             select {
>             case results <- doWork(job):
>             case <-ctx.Done():
>                 return // exit cleanly on cancellation
>             }
>         }
>     }()
>     return results
> }
> ```
> Now the goroutine exits on context cancellation regardless of whether anyone is reading results. `close(results)` in the defer notifies downstream consumers that the pipeline is done.
>
> **Insight:** The `defer close(results)` + `select { case results <- v: case <-ctx.Done(): return }` pattern is the idiomatic Go pipeline stage. It handles three cases correctly: normal completion (range exits, defer closes), context cancellation (select fires, defer closes), and downstream abandonment (same — context is how you signal that).

> ⚠️ **Weak answer sounds like:** "It's fine because the goroutine will exit when jobs is closed."
> Only if someone also keeps reading results. If the context is cancelled and the caller stops reading, the goroutine parks on the send and never sees the jobs channel close.

> 💬 **Likely follow-up:** "If doWork takes 30 seconds and the context is cancelled after 5 seconds, does this fix fully solve the problem?"
