# Channels — Interview Questions & Answers

Difficulty tags: `[Conceptual]` · `[Internals]` · `[Applied]` · `[Gotcha]` · `[Design]`

---

## Channel Internals

1. ⭐ What is the internal structure of a Go channel? What are `sendq` and `recvq`? `[Internals]`

> **Hook:** A channel is an `hchan` struct containing a ring buffer (for buffered channels), two goroutine wait queues (`sendq` and `recvq`), a mutex, and a closed flag. `sendq` holds goroutines blocked trying to send; `recvq` holds goroutines blocked trying to receive.
>
> **Internals:** The wait queues are linked lists of `sudog` ("suspended goroutine") nodes. Each `sudog` holds a pointer back to the goroutine G and either the value being sent or a pointer to the destination variable for a receive. When a goroutine blocks on a channel, the runtime creates a sudog, enqueues it, and parks the goroutine — removing it from the scheduler's run queues entirely. When the other side arrives, it finds the sudog, performs a direct copy into the destination pointer, and marks the goroutine Runnable again.
>
> **Real-world:** The mutex on `hchan` protects all channel state. On high-throughput pipelines, this lock can become a bottleneck — it's why `sync.Pool` and ringbuffer-based SPSC queues appear in performance-critical Go codebases. Understanding the lock means you know why `chan int` inside a tight loop at millions of ops/sec will eventually show up in your pprof mutex contention profile.
>
> **Insight:** The channel's lock is held very briefly — just long enough to manipulate the queue or copy a value. The cost is not the lock itself but the goroutine park/unpark cycle when the other side isn't ready.

> ⚠️ **Weak answer sounds like:** "A channel is a typed pipe for communicating between goroutines."
> That is a description of the interface, not the mechanism. An interviewer testing runtime knowledge won't find it useful.

> 💬 **Likely follow-up:** "What is a sudog? Where is it allocated?"

---

2. What is the `sudog` struct? Who allocates it and when is it freed? `[Internals]`

> **Hook:** A `sudog` is the node that represents a goroutine suspended on a channel operation. It is allocated when a goroutine parks (channel full/empty) and freed — or pooled — when the goroutine is woken.
>
> **Internals:** `sudog` is pooled by the runtime via a per-P cache (`p.sudogcache`) and a global pool (`sched.sudogcache`) to avoid per-operation heap allocation. When a goroutine blocks on a channel, the runtime calls `acquireSudog()` which checks the P-local cache first. On wake, `releaseSudog()` returns it to the cache. The `sudog.elem` field points to the value being transferred — for a send it points to the sender's local variable; for a receive it points to the destination variable on the receiver's stack.
>
> **Real-world:** This pooling means that even in high-frequency blocking scenarios, the sudog allocation pressure is low. However, if you're seeing unexpected GC pressure in a channel-heavy service, check whether goroutines are parking and unparking at a very high rate — the sudog pool has a maximum depth, and objects spilling out of it do hit the heap.
>
> **Insight:** The runtime goes out of its way to make goroutine parking cheap — sudog pooling, P-local caches, direct stack copies. The cost of "a goroutine blocked on a channel" is much lower than the cost of "an OS thread blocked on a mutex."

> ⚠️ **Weak answer sounds like:** "It's a struct for blocked goroutines."
> Missing the pooling, the elem pointer, and the allocation strategy.

> 💬 **Likely follow-up:** "What is the direct send optimisation and how does sudog.elem enable it?"

---

## Buffered vs Unbuffered

3. ⭐ What is the scheduler-level difference between a buffered and an unbuffered channel? `[Internals]`

> **Hook:** An unbuffered channel always causes a goroutine to park if the other side isn't already waiting — every send/receive is a potential goroutine switch. A buffered channel only parks when the buffer is full (send) or empty (receive).
>
> **Internals:** For an unbuffered channel (`dataqsiz=0`), every send checks `recvq` first. If it's empty, the sender creates a sudog and parks. For a buffered channel, the send first checks whether the buffer has space (`qcount < dataqsiz`). If it does, it copies the value into `buf[sendx]`, increments `qcount`, and returns — no goroutine switch. The scheduler only intervenes when the buffer is at capacity.
>
> **Real-world:** In a worker pool pattern, choosing the wrong buffer size causes senders to park unexpectedly. A common mistake: `make(chan Job, 1)` and 10 producers — 9 producers park immediately. Sizing the channel to the number of producers, or to the consumer throughput rate, is a measurement exercise not a guess. A zero-size buffer during a high-throughput producer is one of the most common goroutine leak causes I've seen in production: the sender parks, the consumer is shut down, and the sender is stuck forever.
>
> **Insight:** Buffered channels are queues with backpressure. The buffer fills up and then producers stall — this is not a bug, it's the design. If you don't want backpressure, you need to decide what to drop instead of blocking.

> ⚠️ **Weak answer sounds like:** "Buffered channels don't block."
> They do block — just less often. This answer shows a surface-level understanding.

> 💬 **Likely follow-up:** "If I use a buffered channel as a 'done' signal, what can go wrong?"

---

4. A teammate uses a buffered channel as a "work completion" signal: `done <- struct{}{}` with capacity 1. What is the bug? `[Gotcha]`

> **Hook:** The send completes as soon as the value enters the buffer — not when the receiver actually reads it. The receiver may not have processed the signal yet. This is not a completion guarantee; it is a queued notification.
>
> **Internals:** With an unbuffered channel, the send blocks until the receiver calls `<-done`. That means by the time the sender's send returns, the receiver has executed its receive — a true synchronisation point. With `make(chan struct{}, 1)`, the send returns the instant the value sits in the buffer. The receiver could be asleep, preempted, or not yet scheduled. The sender has no way to know.
>
> **Real-world:** I've seen this pattern in integration tests where a goroutine "signals" done through a buffered channel and the test proceeds to check assertions before the goroutine has actually finished cleanup. The test passes 99% of the time and fails randomly under load — classic race condition hiding behind a channel.
>
> **Insight:** If synchronisation is the requirement, use an unbuffered channel or a `sync.WaitGroup`. The capacity of a channel changes its semantics, not just its performance.

> ⚠️ **Weak answer sounds like:** "Buffered channels are fine for signalling."
> This ignores the happens-before semantics difference entirely.

> 💬 **Likely follow-up:** "When would you use a buffered channel of capacity 1 intentionally?"

---

## Closed Channel Behaviour

5. ⭐ What happens when you send to a closed channel? What about receiving from one? `[Gotcha]`

> **Hook:** Sending to a closed channel causes a **runtime panic** immediately. Receiving from a closed channel **never blocks** — it returns the zero value of the channel's element type and `false` for the `ok` flag.
>
> **Internals:** On `close(ch)`, the runtime sets `hchan.closed=1`, drains `recvq` (waking all blocked receivers with zero values and `ok=false`), and drains `sendq` (waking blocked senders with a panic). After that, any new send hits the closed check at the top of the `chansend` path and panics immediately. Any new receive hits `chanrecv`, drains the remaining buffer first, and returns zero + false once the buffer is empty.
>
> **Real-world:** The most common production mistake is a double-close — two goroutines independently deciding to close the same channel. This panics. The rule is: only the sender closes, and if there are multiple senders, a single coordinator (often using `sync.WaitGroup`) closes after all senders are done. I've seen this crash a service during a graceful shutdown sequence where both the timeout handler and the completion handler tried to close the same results channel.
>
> **Insight:** Closing a channel is a broadcast — it wakes every blocked receiver simultaneously. This is the idiomatic way to signal shutdown to many goroutines: `close(done)` unblocks all `<-done` calls across all goroutines instantly, which a single value send (`done <- struct{}{}`) cannot do.

> ⚠️ **Weak answer sounds like:** "Closing a channel signals that no more data will be sent."
> True but incomplete — it doesn't address the panic on send, the drain behaviour for buffered channels, or the broadcast semantics.

> 💬 **Likely follow-up:** "If a buffered channel has 3 values in it and you close it, what happens to those values?"

---

6. How does `for v := range ch` know when to stop? `[Conceptual]`

> **Hook:** `range` on a channel desugars to a loop that calls `v, ok := <-ch` on each iteration. The loop exits when `ok` is `false` — which only happens when the channel is both **closed** and **empty**.
>
> **Internals:** The compiler translates `for v := range ch` into roughly: `for { v, ok := <-ch; if !ok { break }; body }`. Buffered values that were in the channel before `close()` are drained first — each returns `ok=true`. Only after the buffer is empty does the next receive return `ok=false` and break the loop. This guarantees that all sent values are consumed even if close happens before the receiver processes all of them.
>
> **Real-world:** A common mistake is closing the channel before all producers have sent their values — in a multi-producer scenario where producers are goroutines. The receiver drains only what's in the buffer at close time. Values sent after close panic, and values still being written by concurrent producers when close fires cause a panic. Use `sync.WaitGroup` to wait for all producers to finish, then close once.
>
> **Insight:** `range ch` is the idiomatic way to consume all values from a channel until shutdown — but it requires exactly one close, from exactly one goroutine, after all sends are complete.

> ⚠️ **Weak answer sounds like:** "Range stops when the channel is closed."
> Missing the critical detail: it also waits for the buffer to drain. The channel must be closed *and* empty.

> 💬 **Likely follow-up:** "What happens if you `range` over a channel that is never closed?"

---

## select Semantics

7. ⭐ When multiple cases in a `select` are ready simultaneously, which one executes? `[Internals]`

> **Hook:** Go picks one uniformly at random using `fastrandn`. There is no FIFO order, no priority, and no source-code order bias. Every ready case has an equal probability of selection.
>
> **Internals:** The runtime iterates through all cases during the scan phase and builds a list of ready ones. It then calls `fastrandn(nready)` to pick an index into that list. This is intentional: deterministic selection would cause starvation — the first case in source order would always win when both are ready. The randomisation is not cryptographically secure, but it is fair across cases over many iterations.
>
> **Real-world:** I've seen code that relied on `select` case ordering for prioritised message handling — the author assumed the first case would "win" when both channels had data. It worked in testing (channels rarely had data simultaneously) and failed silently in production. If you need priority (e.g., always drain a control channel before a data channel), use nested selects or check the high-priority channel first with a non-blocking select:
> ```go
> select {
> case cmd := <-controlCh:
>     handle(cmd)
> default:
> }
> select {
> case data := <-dataCh:
>     process(data)
> default:
> }
> ```
>
> **Insight:** If you need deterministic channel priority in a `select`, you have to implement it yourself. The language deliberately does not provide it.

> ⚠️ **Weak answer sounds like:** "The first ready case runs."
> Factually wrong. This mistake breaks real programs.

> 💬 **Likely follow-up:** "How do you implement priority between two channels if `select` is random?"

---

8. What does a `select` with a `default` case do differently from one without? When does `default` become a performance problem? `[Applied]`

> **Hook:** A `select` with a `default` case never parks the goroutine — if no channel case is ready, `default` runs immediately. Without `default`, the goroutine parks until at least one case is ready.
>
> **Internals:** During the readiness scan, if no case is ready and a `default` exists, the runtime skips the park path entirely: it just executes the default block and returns. No sudog allocations, no queue manipulation. This makes the `select` a non-blocking poll — O(1) and cheap per iteration, but it means the goroutine stays Running (or Runnable) at all times rather than yielding the CPU.
>
> **Real-world:** The classic mistake is:
> ```go
> for {
>     select {
>     case msg := <-ch:
>         handle(msg)
>     default:
>         // "idle" — but this is a busy-wait at 100% CPU
>     }
> }
> ```
> This goroutine never parks. It loops at full CPU speed, burning a core checking an empty channel millions of times per second. In production, this shows up as a goroutine stuck at the top of a `pprof` CPU profile with a meaningless callsite. Fix: remove `default` to let the goroutine park, or add a timer case to yield periodically.
>
> **Insight:** `default` is the right tool for "check if something is ready, but don't wait" — a single non-blocking probe. It is the wrong tool for "keep doing this until something arrives" — that's what a blocking `select` without `default` is for.

> ⚠️ **Weak answer sounds like:** "Default runs when no channel has data."
> True but incomplete — missing the CPU impact of using it in a loop.

> 💬 **Likely follow-up:** "How would you implement a non-blocking send on a channel?"

---

9. What happens internally when a goroutine blocks on a `select` with three channel cases? `[Internals]`

> **Hook:** The goroutine allocates a `sudog` for each channel case and enqueues itself into all three channels' send/receive queues simultaneously. It then parks. When any one channel satisfies a case, that channel wakes the goroutine, which then dequeues its sudog from the other two channels before continuing.
>
> **Internals:** All channels are locked in address order first (consistent ordering prevents deadlock from two goroutines each running a select involving the same channels). A sudog is created per case — pointing to the same goroutine G but to different value pointers (elem). All sudogs are enqueued. G is marked Blocked and removed from the scheduler. On wake, the channel that satisfied the case has already performed the copy via the sudog's elem pointer. G's wake-up code then sweeps through the remaining channels and removes its sudogs from their queues, releasing them back to the sudog pool.
>
> **Real-world:** In `select` with many cases on high-frequency paths, the sudog allocation and cleanup pass across all cases adds up. A `select` with 10 cases that fires a million times per second is doing 10 million sudog enqueue+dequeue operations per second. This is usually fine — the runtime sudog pool keeps allocation off the heap — but it's worth profiling before assuming a large `select` is free.
>
> **Insight:** The cleanup pass (removing sudogs from non-winning channels) is what makes multi-channel `select` safe — without it, a woken goroutine would appear as a permanent waiter on channels it's no longer blocked on, corrupting future channel operations.

> ⚠️ **Weak answer sounds like:** "The goroutine waits on all channels."
> Missing the multi-sudog enqueue, the consistent lock ordering for deadlock prevention, and the cleanup sweep on wake.

> 💬 **Likely follow-up:** "Why do all channels in a select get locked before the readiness scan?"

---

## Direct Send Optimisation

10. ⭐ What is the direct send optimisation? When does it fire? `[Internals]`

> **Hook:** When a sender finds a receiver already waiting in `recvq` (or vice versa), the runtime copies the value directly into the receiver's destination variable via the sudog's `elem` pointer — skipping the ring buffer entirely. This reduces a two-copy operation to a single copy.
>
> **Internals:** Every `chansend` call checks `recvq` before touching the buffer. If `recvq` is non-empty, `chansend` dequeues the waiting sudog, calls `typedmemmove(sudog.elem, senderValue)` to copy directly into the receiver's variable, wakes the receiver, and returns — the buffer is never touched. The same applies in reverse: a receiver finding a sender in `sendq` copies directly from the sender's sudog. This works even for buffered channels — if a receiver is parked waiting because the buffer was empty, and a new value arrives, it can go directly to the receiver instead of entering the buffer.
>
> **Real-world:** On an unbuffered channel used as a hot pipeline (e.g., a request object flowing through processing stages), the direct send means every value crosses the channel in exactly one memory copy. Benchmarks of tight ping-pong goroutines show this clearly — the throughput is limited by scheduling overhead, not copy cost, even for moderately large values. For very large structs (>1KB), the difference between one copy and two copies on a buffered path starts showing up in CPU profiles.
>
> **Insight:** Unbuffered channels with synchronised senders and receivers can be more efficient than buffered channels — they use one copy versus two. The common intuition that "buffered = faster" is only true when one side runs significantly ahead of the other.

> ⚠️ **Weak answer sounds like:** "Unbuffered channels are slower because they always cause goroutine switches."
> Partially true (they do cause switches when sides are out of sync) but misses the direct send path that makes the copy itself cheaper.

> 💬 **Likely follow-up:** "For a channel of type `[1024]byte`, is it worth using a pointer channel instead? What's the trade-off?"

---

## Design & Applied

11. Design a pattern to safely close a channel that has multiple senders. `[Design]`

> **Hook:** Never close the channel from a sender. Use a `sync.WaitGroup` to track all senders. A coordinator goroutine calls `wg.Wait()` and then calls `close(ch)` once all senders have finished.
>
> **Internals:** This works because `close` is safe to call exactly once from exactly one goroutine. The WaitGroup provides the synchronisation: `wg.Add(1)` before each sender goroutine launches, `wg.Done()` deferred at the top of each sender, and `wg.Wait()` blocks the coordinator until all Done calls have been made. By the time `close(ch)` executes, all senders have already returned — so there can be no concurrent send-after-close.
>
> ```go
> func fanIn(sources []<-chan int) <-chan int {
>     out := make(chan int)
>     var wg sync.WaitGroup
>     for _, src := range sources {
>         wg.Add(1)
>         go func(s <-chan int) {
>             defer wg.Done()
>             for v := range s {
>                 out <- v
>             }
>         }(src)
>     }
>     go func() {
>         wg.Wait()
>         close(out) // safe: all senders have exited
>     }()
>     return out
> }
> ```
>
> **Real-world:** The alternative — using `recover()` to catch a send-on-closed panic — is an anti-pattern. It hides the bug, adds overhead on every send, and makes the code opaque. The WaitGroup approach makes the intent explicit and is race-detector clean.
>
> **Insight:** Channel ownership is a design decision: one goroutine owns the channel (creates and closes it); others only send or receive. Violating this invariant is the root cause of every double-close panic.

> ⚠️ **Weak answer sounds like:** "Use a mutex to guard the close call."
> A mutex guards concurrent close calls but doesn't prevent a send-after-close from a goroutine that holds the lock between checking-if-closed and sending.

> 💬 **Likely follow-up:** "What if a sender panics before calling wg.Done()?"

---

## Live Coding — Fix It

12. What does this code print? Is there a bug? `[Gotcha]`

```go
package main

import "fmt"

func main() {
    ch := make(chan int, 3)
    ch <- 1
    ch <- 2
    ch <- 3
    close(ch)
    ch <- 4 // line A
    for v := range ch {
        fmt.Println(v)
    }
}
```

> **Hook:** This program panics at line A with "send on closed channel." It never prints anything.
>
> **Internals:** `close(ch)` sets `hchan.closed=1`. The very next operation — `ch <- 4` — calls `chansend`, which checks `hchan.closed` at the top and panics immediately. The `for range` loop is never reached. The three values already in the buffer are also never printed.
>
> **Fix:** Remove line A. With line A gone, `range` drains the buffer (printing 1, 2, 3) and then returns `ok=false` on the fourth receive, exiting the loop.
>
> **Real-world:** This exact crash shows up when producers and the closer are not synchronised — a producer races to send one more value after another goroutine has already called close. The fix is always the same: ensure all sends complete before close is called (WaitGroup pattern from Q11).
>
> **Insight:** The close check in `chansend` runs before any other logic — it is the first thing the runtime verifies. There is no way to "safely" send to a channel you're not certain is open without external synchronisation.

> ⚠️ **Weak answer sounds like:** "It prints 1 2 3 4."
> Ignores the panic on the send-after-close entirely.

> 💬 **Likely follow-up:** "How would you use `recover` to handle a send-on-closed panic? Is that a good idea?"

---

13. What does this code print? `[Gotcha]`

```go
package main

import "fmt"

func main() {
    ch := make(chan int)
    close(ch)
    
    v1, ok1 := <-ch
    v2, ok2 := <-ch
    v3, ok3 := <-ch
    
    fmt.Println(v1, ok1)
    fmt.Println(v2, ok2)
    fmt.Println(v3, ok3)
}
```

> **Hook:** It prints:
> ```
> 0 false
> 0 false
> 0 false
> ```
> Every receive from a closed, empty channel returns the zero value of the element type (`0` for `int`) and `false` — immediately, without blocking, as many times as you call it.
>
> **Internals:** After close and with no buffer, `chanrecv` finds `hchan.closed=1` and `qcount=0`. It writes the zero value into the destination variable, sets `ok=false`, and returns. No sudog is created, no park happens. This is O(1) and essentially free per call.
>
> **Real-world:** This behaviour is what makes `select` timeouts work cleanly: after a `time.After` channel fires once, subsequent receives from it still return immediately (zero, false) rather than blocking, so a goroutine cleaning up doesn't get stuck.
>
> **Insight:** A closed channel is not "gone" — it exists, responds to receives, and can still be inspected. It's more like a drained source than a destroyed connection. The zero value signal (`ok=false`) is how consumers detect "this producer is done."

> ⚠️ **Weak answer sounds like:** "It blocks or panics."
> Receiving from a closed channel is well-defined and safe. Only *sending* to a closed channel panics.

> 💬 **Likely follow-up:** "How does this behaviour relate to the `for range ch` idiom?"
