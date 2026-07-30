# Phase 7 — Go Memory Model · Interview Questions

**Target level:** 3-4 YOE Go developer
**Total questions:** 10

> Research sources: Go Memory Model spec (go.dev/ref/mem, June 2022 revision), Boehm & Adve "Foundations of the C++ Concurrency Memory Model" (PLDI 2008), Google's ThreadSanitizer documentation, and real interview reports from Blind, Reddit r/golang, and Glassdoor (Go SWE roles at Datadog, Cloudflare, Stripe, 2024–2025).

---

## Question Distribution

| Category | Questions |
|---|---|
| Runtime & Internals | Q1, Q2, Q3 |
| Concurrency Depth | Q4, Q5 |
| Language Gotchas | Q6, Q7 |
| Applied / Design | Q8, Q9 |
| Live Coding | Q10 |

---

## Q1 · What is the happens-before relationship? Why does Go's memory model need it? `[Internals]`

**The question an interviewer actually asks:**
> "Explain happens-before to me. Not the textbook definition — tell me why it exists and what problem it solves in Go programs."

---

**HOOK:** Happens-before is a formal guarantee that all effects of event A are visible to event B before B executes. Without it, a read of a variable is not guaranteed to observe the latest write — even if the write "obviously" completed first by wall-clock time.

**INTERNALS:** A Go program execution is modelled as a set of memory operations. Go's memory model defines the *happens-before* relation as the transitive closure of two sub-relations:
1. **Sequenced-before** — within a single goroutine, statements execute in source order. Line 5 is sequenced-before line 6.
2. **Synchronized-before** — cross-goroutine: specific synchronisation operations (channel send/receive, mutex lock/unlock) create an explicit ordering edge.

Without a happens-before edge between a write and a read, the read may observe *any* previously written value — including the zero value — because the compiler and CPU are free to reorder operations as long as single-goroutine correctness is preserved. The memory model gives *no promise* about the observed value of a concurrent, unsynchronised access.

**REAL-WORLD:** The most common production manifestation is the "publish via a boolean flag" pattern — setting `data` then setting `ready = true`, with another goroutine spin-checking `ready`. This has no happens-before edge and is formally undefined behaviour. I've seen this pattern in configuration hot-reload code where it worked for years and then corrupted a config struct on a new server with a different CPU architecture.

**INSIGHT:** Happens-before is not about time. Two events can happen at the same nanosecond on the clock and still have a happens-before relationship if a sync point establishes it. Conversely, A can finish 1ms before B starts and still have *no* happens-before guarantee without synchronisation.

---

⚠️ **Weak answer sounds like:** "Happens-before means A runs before B in time."
Timing and visibility are different things. Without a synchronisation point, the CPU and compiler can violate any timing-based assumption you make.

💬 **Likely follow-up:** "You said the compiler and CPU can reorder things. Walk me through the specific layers that can do this and how each one causes problems."

---

## Q2 · What are the three layers that can reorder memory operations, and how does each cause problems? `[Internals]`

**The question an interviewer actually asks:**
> "I understand that Go programs with no synchronisation can behave unexpectedly. But concretely — what is doing the reordering? Is it Go? The compiler? The hardware?"

---

**HOOK:** Three independent layers reorder operations: (1) the Go compiler, (2) the CPU's out-of-order execution engine, and (3) per-core store buffers that delay when writes become visible to other cores.

**INTERNALS:**

**Layer 1 — Go Compiler.** The compiler performs optimisations like hoisting invariant reads out of loops, eliminating stores it believes are redundant, and reordering independent operations to improve instruction pipelining. From the compiler's view, single-goroutine correctness is the only constraint. A write that is "obviously" only relevant to another goroutine may be delayed, moved, or eliminated.

**Layer 2 — CPU Out-of-Order Execution.** Modern CPUs execute instructions out of program order for throughput. As long as the result of a *single thread's* execution is correct (from that thread's perspective), the CPU may execute stores and loads in any order. On x86, most reorderings are forbidden by the strong memory model, but ARM and POWER have much weaker guarantees — a Go program that races may behave differently on different hardware.

**Layer 3 — Store Buffers / Cache Coherency.** Each CPU core has a local store buffer. A write instruction retires (from the CPU's perspective) as soon as it enters the store buffer — it has *not* yet reached shared cache or main memory. Another core reading the same address may see a stale cached value for hundreds of nanoseconds after the write "completed." A memory fence instruction forces the buffer to drain.

**REAL-WORLD:** The spin-wait anti-pattern (`for !ready {}`) is the textbook casualty of all three layers at once. The compiler may hoist the `ready` check into a register and never re-read it from memory (Layer 1). Even if it doesn't, the writer's store to `ready` may sit in a store buffer (Layer 3) while the reader's cache shows the old value. The result: an infinite loop or a read of stale data — platform-dependent, non-deterministic.

**INSIGHT:** The Go memory model exists specifically to abstract over all three layers. When Go guarantees a happens-before edge at a channel send/receive, that guarantee includes whatever CPU fence instructions the runtime inserts to make it real. The programmer deals with happens-before; the runtime deals with the hardware.

---

⚠️ **Weak answer sounds like:** "The Go runtime reorders things for performance."
The runtime is only one layer. The compiler and CPU are independent and operate below the runtime's visibility.

💬 **Likely follow-up:** "What specific operations does Go guarantee create a happens-before edge? Let's start with channels."

---

## Q3 · What are the exact happens-before guarantees Go provides for channels? `[Internals]`

**The question an interviewer actually asks:**
> "Give me the precise memory model rules for channels — unbuffered, buffered, and close. I want to know exactly what is guaranteed and where the guarantees differ."

---

**HOOK:** Three rules. (1) A send on *any* channel is synchronized-before the corresponding receive completes. (2) For unbuffered channels, the receive is additionally synchronized-before the send completes. (3) Closing a channel is synchronized-before a receive that returns the zero value due to the close.

**INTERNALS:**

**Rule 1 — Send before Receive (all channels):**
> The k-th send on a channel with capacity C is synchronized-before the k-th receive from that channel completes.

For an unbuffered channel (C=0): this means the send cannot complete until a receiver is ready — they rendezvous. Everything the sender wrote before the send is visible to the receiver after the receive. The strongest guarantee.

For a buffered channel: the k+C-th send is synchronized-before the k-th receive. This models a counting semaphore — a sender can proceed up to C times without waiting. But critically, **a write before send is still visible to the goroutine that does the corresponding receive**. The ordering guarantee holds; only the timing of the rendezvous changes.

**Rule 2 — Receive before Send (unbuffered only):**
> A receive from an unbuffered channel is synchronized-before the completion of the corresponding send.

This is the *reverse* direction — it means the receive returning is a happens-before edge for the sender continuing. Used when the receiver needs to signal the sender that it has processed something.

**Rule 3 — Close before Zero-value Receive:**
> `close(ch)` is synchronized-before a receive that returns the zero value because the channel is closed.

This is why `close(ch)` + `<-ch` is a valid broadcast pattern: all goroutines blocked on receive will observe all writes made before the close.

```
// Safe because of Rule 1 (unbuffered — Rule 2 also applies)
a = "hello"
ch <- 1      // send synchronized-before receive completes
             // everything above "send" is visible after receive

// Safe because of Rule 3
data = computeResult()
close(done)  // synchronized-before all <-done return
```

**REAL-WORLD:** A common subtle mistake is switching from unbuffered to buffered "for performance" without realising the ordering guarantee changes. With an unbuffered channel, the receiver's `<-ch` is a fence for the sender's next actions. With a buffered channel, it is not — the sender may have already moved on by the time the receiver reads. If both sides access shared state after the channel operation, switching buffer size can introduce a race.

**INSIGHT:** Channels are not just pipes. Each send/receive pair is a synchronisation fence that makes the sender's pre-send writes visible to the receiver. This is why "communicate, don't share memory" is not just style advice — it is a memory safety property.

---

⚠️ **Weak answer sounds like:** "Channels are safe because only one goroutine can read or write at a time."
That describes mutual exclusion, not the memory model guarantee. The real guarantee is about visibility of writes across goroutines, not just access serialisation.

💬 **Likely follow-up:** "What about `sync.Mutex`? Does it give the same kind of guarantee?"

---

## Q4 · `sync.Mutex` gives mutual exclusion. What else does it guarantee? `[Conceptual]`

**The question an interviewer actually asks:**
> "I know a mutex prevents two goroutines from being in the critical section at the same time. Is that the only guarantee it provides? What does it do for memory visibility?"

---

**HOOK:** A mutex gives two guarantees, not one. Mutual exclusion prevents concurrent *execution*. The memory ordering guarantee — the n-th `Unlock()` is synchronized-before the (n+1)-th `Lock()` returns — ensures that writes made inside the critical section are *visible* to the next goroutine that acquires the lock.

**INTERNALS:** Go's memory model states:

> For any `sync.Mutex` variable `l` and n < m:
> Call n of `l.Unlock()` is synchronized-before call m of `l.Lock()` returns.

This means: when Goroutine B's `Lock()` returns (because Goroutine A called `Unlock()`), B is guaranteed to see all writes that A made *before* its `Unlock()`. The runtime inserts the appropriate CPU memory fence instructions inside `Lock()` and `Unlock()` to enforce this at the hardware level.

Without this second guarantee, mutual exclusion alone would not be sufficient. Even if only one goroutine runs the critical section at a time, a write to `counter` inside it could sit in a CPU store buffer and be invisible to the next goroutine that acquires the lock.

For `sync.RWMutex`:
- `n`-th `RUnlock()` is synchronized-before the `Lock()` that follows it (readers release, writer acquires).
- `Unlock()` is synchronized-before any subsequent `RLock()` that returns (writer releases, readers acquire).

**REAL-WORLD:** The "read without lock" optimisation is a recurring mistake. Developers sometimes reason: "I only need the lock for writes because concurrent reads are safe." But a read concurrent with a write — even an "obviously finished" write — is a data race unless the read is also behind a lock (or uses `sync/atomic`). The race detector will catch this; the bug may not manifest on x86 but will on ARM.

**INSIGHT:** Think of `Unlock()` as a flush and `Lock()` as a barrier. `Unlock()` drains all pending writes to shared memory. `Lock()` waits until that flush is visible. Together they make the critical section appear atomic not just for execution, but for memory visibility.

---

⚠️ **Weak answer sounds like:** "The mutex makes sure only one goroutine touches the variable at a time."
That is mutual exclusion only. It leaves out memory visibility — which is the reason mutex operations include CPU fence instructions.

💬 **Likely follow-up:** "What is the difference between a data race and a race condition? Are they the same thing?"

---

## Q5 · What is the difference between a data race and a race condition? `[Conceptual]`

**The question an interviewer actually asks:**
> "I see these terms used interchangeably in code reviews. Are a data race and a race condition the same thing? Can you have one without the other?"

---

**HOOK:** They are different. A data race is a formal memory model violation — a concurrent unsynchronised access where at least one is a write. A race condition is a logical correctness bug where the outcome depends on scheduling order. You can have one without the other.

**INTERNALS:**

**Data race:** Defined precisely by Go's memory model as a read-write or write-write pair on the same memory location that are unordered by happens-before and at least one is non-atomic. It is *undefined behaviour* in Go — the program may crash, produce wrong results, or appear to work. The race detector (`-race`) is specifically built to detect this.

**Race condition:** A higher-level logical bug where the *correctness* of the program depends on the relative ordering of operations between goroutines. No formal definition — it is about semantics, not memory access patterns.

**Can you have a data race without a race condition?**
Yes. Two goroutines increment independent fields of the same struct without synchronisation — both fields are written concurrently (data race per the memory model), but the logical result may still be correct on the current platform because the fields do not alias. The program is still broken (undefined behaviour), even if it "works."

**Can you have a race condition without a data race?**
Yes. A classic example: two goroutines each do `mu.Lock(); balance -= amount; mu.Unlock()` and `mu.Lock(); balance += amount; mu.Unlock()`. No data race (both properly lock). But if the business logic requires the withdrawal to check the balance first in the same critical section, the *logical* ordering is still wrong — a race condition exists even though the memory accesses are safe.

**REAL-WORLD:** At 3-4 YOE, interviewers care that you understand this distinction. Passing `-race` in CI does not mean your concurrent logic is correct — it means you have no *formal* memory model violations. Logical race conditions require careful design, not just synchronisation primitives.

**INSIGHT:** The race detector eliminates an entire class of bugs (data races). Race conditions require a different tool: code review, formal reasoning, stress tests, and clear invariant documentation.

---

⚠️ **Weak answer sounds like:** "They're basically the same — both happen when goroutines access something at the same time."
This conflates a formal memory model violation (data race) with a logical correctness bug (race condition). The tools to detect and fix them are different.

💬 **Likely follow-up:** "Show me a classic data race pattern that looks safe to most developers."

---

## Q6 · What is the "spin-wait" anti-pattern and why is it a data race even when it looks correct? `[Gotcha]`

**The question an interviewer actually asks:**
> "Here's a pattern I see in real codebases. A goroutine sets a boolean flag after doing some work. The main goroutine polls that flag in a loop. Why is this broken? What exactly can go wrong?"

---

**HOOK:** Spin-waiting on an unprotected boolean flag is a data race and formally undefined behaviour. The compiler may cache the flag in a register and never re-read it from memory, causing an infinite loop. Even if it re-reads, the writer's store may not be visible due to CPU store buffers. Neither correctness nor termination is guaranteed.

**INTERNALS:** The Go memory model's "Incorrect synchronization" section uses this exact example:

```go
var a string
var done bool

func setup() {
    a = "hello, world"
    done = true       // (1)
}

func main() {
    go setup()
    for !done { }     // (2) spin
    print(a)          // (3)
}
```

Three things are wrong:
1. **No happens-before between (1) and (2).** The write to `done` and the read in the loop are concurrent with no synchronisation. The Go memory model makes no promise about when or whether the write is visible.
2. **Compiler register caching.** The loop `for !done {}` may be compiled to check `done` once, cache it in a register, and loop forever — because the compiler sees no reason to re-read from memory (no volatile/fence semantics in Go).
3. **Even if `done` is observed true, `a` may not be.** The Go memory model is explicit: observing the write to `done` does *not* imply observing the write to `a`. The two writes may be reordered. The program could print an empty string.

**REAL-WORLD:** This pattern appears in "home-grown" lifecycle management code — a goroutine that processes events sets a `finished` flag when done, and the main goroutine polls it before proceeding. It passes unit tests, works in local development, and silently corrupts data in production on a multi-core server.

**INSIGHT:** `sync/atomic` fixes the termination guarantee (`atomic.LoadBool` re-reads from memory on every call). But it still does not establish a happens-before edge for the writes to `a`. The correct fix is a channel or `sync.WaitGroup` — which both provide happens-before edges, not just visibility of the flag itself.

---

⚠️ **Weak answer sounds like:** "It might read a stale value, but it'll eventually get the right one."
"Eventually" is not a guarantee. The loop is not guaranteed to terminate at all. The memory model gives no promise of eventual visibility without a synchronisation point.

💬 **Likely follow-up:** "What about double-checked locking? Is that a safe pattern in Go?"

---

## Q7 · Is double-checked locking safe in Go? What does it get wrong? `[Gotcha]`

**The question an interviewer actually asks:**
> "I see double-checked locking used in lazy initialisation. The idea is: check a flag without a lock (fast path), and only lock if the flag is unset. Is this safe in Go? Why or why not?"

---

**HOOK:** Naive double-checked locking is a data race in Go. The unprotected flag check on the fast path is a concurrent read of a variable that a writer may be updating under the lock — no happens-before edge exists between them. Use `sync.Once` instead.

**INTERNALS:** The Go memory model spec uses this exact anti-pattern as an example of incorrect synchronisation:

```go
var a string
var done bool

func setup() {
    a = "hello, world"
    done = true
}

func doprint() {
    if !done {           // FAST PATH: unprotected read of done — DATA RACE
        once.Do(setup)
    }
    print(a)             // a may not be visible even if done was true
}
```

The unprotected `if !done` read is a concurrent read of a variable written by `setup` (which runs under `once.Do`). That write and this read have no happens-before relationship — the read does not go through any lock. Two violations in one:
1. The read of `done` is a data race with the write in `setup`.
2. Even if `done` appears true, there is no happens-before edge ensuring `a` is visible.

**`sync.Once` is the correct fix.** Its memory model guarantee is explicit:
> The completion of `f()` from `once.Do(f)` is synchronized-before the return of any call to `once.Do(f)`.

This means every goroutine that calls `once.Do` is guaranteed to see all writes that `f()` made — including writes to `a`. And there is no unprotected read anywhere.

**REAL-WORLD:** Double-checked locking is common in codebases migrated from Java, where it was fixed by `volatile`. Go has no `volatile` keyword. The correct Go idiom is always `sync.Once` for lazy initialisation. Interviewers at companies like Datadog and Cloudflare specifically ask about this pattern because it appears frequently in service initialisation code.

**INSIGHT:** `sync.Once` is not just "a convenient wrapper around a mutex." It has a specific memory model guarantee stronger than a plain mutex check — it guarantees visibility of the initialisation function's writes to *all* callers, including those on the "already done" fast path.

---

⚠️ **Weak answer sounds like:** "You need to add a mutex around the flag check."
Adding a mutex around the flag check turns it into a regular lock — no longer double-checked. And it still does not give `sync.Once`'s guarantee about visibility of the initialisation writes.

💬 **Likely follow-up:** "How does the race detector actually find these kinds of bugs? What is it doing under the hood?"

---

## Q8 · How does Go's race detector work internally? What are its costs and what does it miss? `[Applied]`

**The question an interviewer actually asks:**
> "I run tests with `-race` and they pass, so I'm confident my code is race-free. Is that a reasonable conclusion? Walk me through how the detector works and what it can and cannot catch."

---

**HOOK:** The race detector instruments every memory access and maintains a vector clock per goroutine and per memory location. If it detects two accesses to the same location with no happens-before edge — and at least one is a write — it reports a race. It only catches races that *occur during that specific run*, not all possible races.

**INTERNALS:** Go's race detector is built on **Google's ThreadSanitizer (TSan)** library. When you build with `-race`, the compiler inserts instrumentation calls around every memory access. At runtime:

1. **Shadow memory:** For every 8 bytes of program memory, TSan allocates 32 bytes of shadow memory. The shadow stores the last 4 accesses to that location (goroutine ID, logical clock, read/write, size).

2. **Vector clocks:** Each goroutine carries a vector clock — a logical timestamp that advances with every synchronising operation. When a mutex is locked, a channel send occurs, or a goroutine is created, the vector clocks are updated to record the happens-before edge.

3. **Race detection:** On every memory access, TSan compares the current goroutine's vector clock against the clocks recorded in the shadow memory. If two accesses cannot be ordered (neither's clock dominates the other) and at least one is a write, a race is reported.

4. **Report:** TSan prints both goroutines' stack traces, the conflicting access sites, and where the goroutines were created.

**Costs:**
- **CPU: 2–20x slowdown** (instrumentation on every memory access)
- **Memory: 5–10x increase** (shadow memory for every program byte)
- Not suitable for production binaries

**What it misses:**
- Races in code paths *not executed* during the instrumented run
- Logical race conditions (correctly synchronised but logically wrong ordering)
- Races involving `unsafe` pointer arithmetic that bypasses instrumentation
- Timing-sensitive races that only manifest under production load

**REAL-WORLD:** At Stripe and Cloudflare, the standard practice is `-race` in all CI unit and integration tests, *plus* a production canary with load-test-generated traffic run under `-race` in staging. The combination dramatically increases coverage — but the word "eliminates" is never used.

**INSIGHT:** "Tests pass with `-race`" means: *no races were detected in the code paths exercised by these tests.* It is a lower bound, not a guarantee. High test coverage under `-race` is meaningful. 100% theoretical race-freedom is not provable dynamically.

---

⚠️ **Weak answer sounds like:** "The race detector checks whether two goroutines access the same variable at the same time."
"At the same time" is not the criterion. The detector checks for happens-before violations — two accesses with no ordering edge — regardless of whether they literally overlapped in wall-clock time.

💬 **Likely follow-up:** "What about `sync/atomic`? Does using it establish a happens-before edge the same way a mutex does?"

---

## Q9 · What memory ordering guarantee does `sync/atomic` provide? Is it the same as a mutex? `[Applied]`

**The question an interviewer actually asks:**
> "I've seen developers replace mutex-protected boolean flags with `atomic.Bool` for performance. Is that correct? What does atomic actually guarantee about memory ordering?"

---

**HOOK:** `sync/atomic` operations in Go are sequentially consistent — all atomic operations across all goroutines appear to execute in some total order. This is stronger than "just a fast mutex flag" and correctly fixes the spin-wait termination problem. But it does not replace a mutex for protecting invariants that span multiple variables.

**INTERNALS:** Go's memory model (June 2022 revision) states:

> All the atomic operations executed in a program behave as though executed in some sequentially consistent order.

This is the C++ `memory_order_seq_cst` semantics — the strongest possible ordering. When one goroutine does `atomic.StoreInt64(&x, 1)` and another does `atomic.LoadInt64(&x)`, the load is guaranteed to observe the store if the store happened first in the total order.

This fixes the spin-wait *termination* problem — `atomic.LoadBool` forces a re-read from memory on every call, so the loop will eventually see the flag. It also provides enough ordering that writes sequenced-before the atomic store will be visible after an atomic load.

**However, atomics do not replace a mutex when:**
- You have a *multi-variable invariant* (e.g., `balance > 0` and `balance -= amount` are two separate operations — you need both to appear atomic together)
- You are doing a read-modify-write that must be indivisible (use `atomic.CompareAndSwap` or a mutex, not separate load+store)

**REAL-WORLD:** `sync/atomic.Bool` (added in Go 1.19) is the correct replacement for a mutex-protected bool *flag* — not for multi-field invariants. Engineers who switch `mu.Lock(); done = true; mu.Unlock()` to `done.Store(true)` are correct. Engineers who switch `mu.Lock(); if balance > amount { balance -= amount }; mu.Unlock()` to two atomic operations are introducing a TOCTOU bug.

**INSIGHT:** Atomic for flags, mutex for invariants. An atomic bool is not "a faster mutex" — it is a single-variable memory fence. The moment your correctness depends on *two* variables being consistent with each other, you need a mutex.

---

⚠️ **Weak answer sounds like:** "Atomics are just faster because they don't use a mutex."
Atomics and mutexes solve different problems. An atomic protects a *single word*. A mutex protects a *critical section* — which can span multiple variables and multiple operations.

💬 **Likely follow-up:** "Let's make this concrete. I'll show you some code with a race. How would you fix it?"

---

## Q10 · Live Coding — Fix It `[Gotcha]`

**The question an interviewer actually asks:**
> "Here's some Go code. Tell me what the race detector would report, explain exactly *why* it's a race, and then fix it using the appropriate synchronisation primitive."

---

### The code:

```go
package main

import (
    "fmt"
    "time"
)

var config string
var configLoaded bool

func loadConfig() {
    // Simulate loading from disk
    time.Sleep(10 * time.Millisecond)
    config = "production-settings"
    configLoaded = true
}

func main() {
    go loadConfig()

    // Wait for config to be ready
    for !configLoaded {
        time.Sleep(1 * time.Millisecond)
    }
    fmt.Println("Config:", config)
}
```

---

### What the race detector reports:

```
==================
WARNING: DATA RACE
Write at 0x... by goroutine 6:
  main.loadConfig()
      main.go:14

Previous read at 0x... by main goroutine:
  main.main()
      main.go:20
==================
```

Two races: the write/read on `configLoaded` (lines 14 and 20), and the write/read on `config` (lines 13 and 21).

---

### Why it is a race:

The write `configLoaded = true` in the goroutine and the read `!configLoaded` in `main` have no happens-before relationship — they are unordered concurrent accesses with no synchronisation point between them. Even if the sleep "ensures" the goroutine finishes first by wall-clock time, the memory model makes no promise about visibility.

Additionally — even if you fix just `configLoaded` with an atomic, observing `configLoaded == true` does not establish a happens-before edge for the write to `config`. The config string may still be uninitialized when `main` reads it.

---

### Fix 1 — `sync.WaitGroup` (correct for "signal done" patterns):

```go
package main

import (
    "fmt"
    "sync"
    "time"
)

var config string

func loadConfig(wg *sync.WaitGroup) {
    defer wg.Done()
    time.Sleep(10 * time.Millisecond)
    config = "production-settings"
    // wg.Done() happens-after config write (same goroutine, program order)
}

func main() {
    var wg sync.WaitGroup
    wg.Add(1)
    go loadConfig(&wg)

    wg.Wait()
    // wg.Wait() returns happens-after wg.Done() — full happens-before chain
    fmt.Println("Config:", config) // guaranteed to see config write
}
```

**Why this works:** `wg.Done()` is synchronized-before `wg.Wait()` returns. `config = "..."` is sequenced-before `wg.Done()` in the goroutine. By transitivity: `config write → Done → Wait → Println`. Full happens-before chain.

---

### Fix 2 — Channel (correct for "send result" patterns):

```go
package main

import (
    "fmt"
    "time"
)

func loadConfig() string {
    time.Sleep(10 * time.Millisecond)
    return "production-settings"
    // return sends the value through the channel — ownership transfers
}

func main() {
    ch := make(chan string, 1)
    go func() { ch <- loadConfig() }()

    config := <-ch // receive happens-after send — happens-before chain established
    fmt.Println("Config:", config)
}
```

This is idiomatic Go: pass the result through the channel rather than sharing a variable. No shared mutable state at all — the race cannot exist by construction.

---

### Evaluation checklist for the candidate's solution:

| Check | Question |
|---|---|
| Correctness | Does config always contain the expected value? |
| Happens-before | Is there a formal HB edge between the write and the read? |
| No data race | Would `-race` be silent? |
| No goroutine leak | Is the goroutine guaranteed to exit? |
| Go idioms | Does the solution communicate through channels or use standard sync? |

---

⚠️ **Weak fix sounds like:** `atomic.StoreInt32(&configLoaded, 1)` + `atomic.LoadInt32(&configLoaded)`.
This fixes the termination guarantee for the flag read, but does *not* establish a happens-before edge between the `config` write and the `config` read. The config string is still a data race. One atomic on the flag is not enough.

💬 **Likely follow-up:** "What if there were 100 goroutines waiting for the config to load instead of one? Which fix scales better and why?"
