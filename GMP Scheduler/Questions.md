# GMP Scheduler — Interview Questions & Answers

Difficulty tags: `[Conceptual]` · `[Internals]` · `[Applied]` · `[Gotcha]` · `[Design]`

---

## Core Concepts

1. ⭐ What are G, M, and P in Go's runtime scheduler? Explain each in one sentence. `[Conceptual]`

> **G (Goroutine):** Your concurrent task — a lightweight unit of execution with a ~2KB starting stack, managed entirely by the Go runtime.
> **M (Machine):** An OS thread — the actual executor that runs code on a CPU core; managed by the kernel.
> **P (Processor):** A logical processor — a scheduler context that holds a Local Run Queue of goroutines; fixed in count, equal to `GOMAXPROCS`.

2. Why does Go need a runtime scheduler at all? Why can't it just use OS threads directly? `[Conceptual]`

> OS threads are expensive — ~1MB stack allocated upfront, and creation requires a kernel call (~microseconds). If every goroutine was an OS thread, running 1 million goroutines would need ~1TB of RAM just for stacks. The runtime scheduler multiplexes millions of cheap goroutines (~2KB each) onto a small fixed number of OS threads — managing context switches in userspace at near function-call speed, with no kernel involvement.

3. ⭐ What is `GOMAXPROCS` and what does it actually control? What is its default value? `[Conceptual]`

> `GOMAXPROCS` controls the number of **P's (Logical Processors)** — which equals the maximum number of goroutines that can run truly in parallel at any moment. Default = number of logical CPU cores on the machine. You can change it with `runtime.GOMAXPROCS(n)`.

4. Why is the number of P's fixed, but the number of M's (OS threads) can fluctuate? `[Internals]`

> P is fixed because it's the **stable scheduler anchor** — work stealing only needs to scan a known, small set of P's. If the run queue lived inside M instead, work stealing would have to scan a potentially huge, ever-growing list of threads (including idle ones).
> M fluctuates because new threads are created when goroutines make blocking syscalls (P hands off to a new M), and old threads park as idle rather than being destroyed (thread creation is expensive).

5. ⭐ What is the difference between a goroutine and an OS thread in terms of memory cost and creation speed? `[Conceptual]`

> | | OS Thread | Goroutine |
> |---|---|---|
> | Stack size | ~1MB fixed, upfront | ~2KB, grows dynamically |
> | Creation cost | ~microseconds (kernel call) | ~nanoseconds (userspace only) |
> | Practical limit | Thousands | Millions |
>
> Goroutine stacks also grow and shrink automatically — an OS thread's stack is fixed at creation.

---

## Goroutine Lifecycle & States

6. What are the possible states of a goroutine? What causes each transition? `[Internals]`

> Four states:
> - **Runnable** — created, waiting in a run queue for an M to pick it up
> - **Running** — currently executing on an M
> - **Blocked** — waiting for something (channel, syscall, mutex, timer) — parked in a waiting room
> - **Dead** — finished executing, memory eligible for GC
>
> Transitions: `created → Runnable → (M picks up) → Running → (blocks) → Blocked → (condition met) → Runnable → (finishes) → Dead`

7. When you write `go doWork()`, walk through exactly what happens step by step until the goroutine starts executing. `[Internals]`

> 1. Runtime allocates a **G struct** with a ~2KB initial stack
> 2. G is marked **Runnable**
> 3. G is placed into the **current P's Local Run Queue** (or GRQ if LRQ is full)
> 4. At some point, M finishes its current goroutine and picks G from the front of the LRQ
> 5. M sets G to **Running** state
> 6. M begins executing G's function
>
> No kernel involvement. No OS scheduling. Entirely userspace until M itself needs to be scheduled by the OS.

8. What happens to a goroutine after it finishes executing? `[Conceptual]`

> G is marked **Dead**. Its stack memory is released back and eligible for GC. The G struct itself may be **pooled internally** by the runtime for reuse — to avoid the allocation cost of creating a new G struct next time. M loops back to pick the next goroutine from its LRQ.

---

## Run Queues & Scheduling

9. What is the Local Run Queue (LRQ)? What is its maximum capacity? `[Internals]`

> The LRQ is a **per-P queue** of runnable goroutines. Because only one M uses each P at a time, no lock is needed to read from it — making it very fast. Maximum capacity = **256 goroutines**. When a new goroutine is created and the LRQ is full, half the LRQ is moved to the Global Run Queue to make room.

10. What is the Global Run Queue (GRQ)? When does a goroutine end up there instead of an LRQ? `[Internals]`

> The GRQ is a **shared fallback queue** protected by a mutex. A goroutine ends up there when:
> 1. **LRQ overflow** — LRQ is full (256), so half the LRQ moves to GRQ
> 2. **After a blocking syscall** — the goroutine returns but its original P is already taken
> 3. **Created without a P context** — e.g., goroutines created during GC or init

11. In what order does a P look for its next goroutine to run? `[Internals]`

> 1. **Own Local Run Queue** — no lock, fastest path
> 2. **Global Run Queue** — locked, checked every 61 scheduler ticks
> 3. **Network Poller** — goroutines whose I/O is ready
> 4. **Work steal** from another P's Local Run Queue

12. ⭐ What is work stealing? Which queue does a thief steal from, and how much does it steal? `[Internals]`

> When a P's LRQ is empty, it **steals half the goroutines** from another P's LRQ. It steals from the **back** of the victim's queue (victim uses the front — this minimises cache interference). Only P's are scanned (fixed count) — not all threads. This ensures no CPU sits idle while other P's have a backlog of work.

13. Why is the global run queue checked every 61st tick specifically? Why not every 50th or every 100th? `[Internals]`

> **61 is prime.** Periodic events in systems tend to synchronise on round numbers (50, 100, 1000). If the GRQ check happened every 50 ticks and another event happened every 100 ticks, they'd collide regularly — causing periodic bursts of work. Using a prime number distributes the check evenly and avoids accidental synchronisation with other background tasks.

---

## Blocking & Syscalls

14. What is a syscall? Give three examples of Go code that triggers a syscall underneath. `[Conceptual]`

> A syscall is a **request from your program to the OS kernel** to perform a privileged operation — touching hardware, managing files, network, processes etc. Your code runs in userspace and cannot touch hardware directly; it must ask the kernel.
>
> Examples:
> - `os.ReadFile("data.txt")` → `read()` syscall
> - `http.Get("https://...")` → `connect()`, `send()`, `recv()` syscalls
> - `fmt.Println("hello")` → `write()` syscall

15. ⭐ What happens to G, M, and P when a goroutine makes a **blocking syscall**? Walk through each one. `[Internals]`

> - **G** — stays attached to M (the kernel is running on behalf of G, runtime can't move it)
> - **M** — freezes in the kernel, blocked until the syscall returns
> - **P** — immediately **detaches** from M, gets picked up by an idle or newly created M2
> - **M2** — takes P and continues running other goroutines from LRQ normally
>
> When syscall returns:
> - G tries to reclaim its original P → if P is busy, G goes to GRQ
> - M1 becomes idle/spinning — not destroyed

16. Why doesn't Go just destroy the idle thread M after a blocking syscall returns, instead of spinning it? `[Internals]`

> Thread creation requires a **kernel call** — expensive (~microseconds). Services with frequent short blocking syscalls (file I/O, DB queries) would constantly destroy and recreate threads, causing constant kernel overhead. A spinning idle thread costs almost nothing — it stays in userspace, uses negligible CPU, and is ready to pick up a P **instantly** when one becomes available.

17. What is the difference between a **blocking syscall** and a **non-blocking syscall** from the scheduler's perspective? `[Internals]`

> - **Blocking syscall** (file read, process wait): OS **freezes the thread** until done. P must immediately hand off to a new M. Thread is genuinely stuck in kernel.
> - **Non-blocking syscall** (network read): OS returns `EAGAIN` immediately ("not ready yet"). Goroutine **registers with Network Poller** and parks. Thread M is freed right away — no P handoff needed, no new thread created.
>
> Network I/O never blocks an OS thread. File/disk I/O always does. This is why Go can handle millions of network connections cheaply but CGo file operations are more expensive.

---

## Network Poller

18. What is the Network Poller? Why does it exist? `[Conceptual]`

> The Network Poller is a Go runtime component that **wraps the OS's I/O readiness notification system**. It exists because network data arrival is unpredictable — instead of blocking a thread waiting for data, a goroutine registers with the poller ("wake me when this socket has data") and parks. The thread is freed immediately to run other goroutines. When data arrives, the OS notifies the poller, which puts the goroutine back into the run queue.

19. ⭐ What happens to a goroutine that is waiting for network data (e.g., an HTTP response)? Does it block an OS thread? `[Internals]`

> No — it does **not** block an OS thread. The goroutine parks in the Network Poller's wait list. Thread M is freed immediately to run other goroutines. When the OS signals the data is ready (via epoll/kqueue), the Network Poller puts G back into the Global Run Queue, where it gets picked up by the next available M.

20. What OS mechanisms does the Network Poller use? (Name at least two across different platforms.) `[Internals]`

> - Linux → **epoll**
> - macOS / BSD → **kqueue**
> - Windows → **IOCP** (I/O Completion Ports)
> - Solaris → **event ports**
>
> Go abstracts all of these behind a single internal poller interface — your code never sees which one is used.

21. What is `sysmon`? What is its role in the scheduler? `[Internals]`

> `sysmon` is a special **background OS thread** that runs outside the GMP model — it has no P, never gets scheduled by Go's scheduler. It runs in a tight loop (sleeps max 10ms between runs). Its roles:
> - **Polls network** if no worker thread has polled in >10ms (prevents network poller starvation)
> - **Preempts goroutines** running >10ms (marks them as preemptible)
> - **Retakes P** from goroutines that have been blocked too long
> - Handles some GC background tasks

---

## Fairness & Preemption

22. What prevents one goroutine from running forever and starving others? `[Conceptual]`

> Four fairness mechanisms:
> 1. **Time slice (~10ms):** `sysmon` marks any goroutine running >10ms as preemptible
> 2. **Global queue check:** every **61st scheduler tick**, P checks GRQ — prevents goroutines there from starving forever
> 3. **Network poller polling:** `sysmon` polls network every 10ms if no worker has — prevents network goroutines from starving
> 4. **Async preemption (Go 1.14+):** goroutines can be forcibly preempted anywhere via OS signals

23. What changed in Go 1.14 regarding goroutine preemption? Why was it needed? `[Internals]`

> Before 1.14: preemption only happened at **function call boundaries**. A tight loop with no function calls could never be preempted — it would hold the P indefinitely and starve other goroutines.
>
> Go 1.14 introduced **asynchronous preemption** via OS signals (SIGURG on Unix). The runtime sends a signal to the thread running the tight-loop goroutine, which interrupts it at any instruction — not just at function calls. This made the Go scheduler truly preemptive.

24. Write a code snippet that would starve other goroutines in Go versions before 1.14. `[Gotcha]`

> ```go
> package main
>
> import "fmt"
>
> func main() {
>     go fmt.Println("I may never run")
>
>     for {
>         // No function calls, no memory allocation
>         // No preemption point — this goroutine owns the P forever
>         // On GOMAXPROCS=1 with Go ≤ 1.13, the println above never executes
>     }
> }
> ```
> Run with `GOMAXPROCS=1` on Go ≤1.13 — the goroutine above may never print.
> On Go 1.14+, `sysmon` sends SIGURG after ~10ms and forces preemption.

---

## Practical & Design

25. ⭐ You deploy a Go service inside a Docker container limited to 2 CPU cores. The host machine has 64 cores. What problem might occur and how do you fix it? `[Applied]`

> `GOMAXPROCS` reads the **host machine's** CPU count (64), not the container's limit (2). Go creates 64 P's fighting over 2 real cores — constant context switching with no gain. Result: ~30-40% performance degradation with zero code changes.
>
> Fix:
> ```go
> import _ "go.uber.org/automaxprocs" // reads cgroup CPU limits, sets GOMAXPROCS correctly
> ```
> This is a real production issue that hit Uber and is why `automaxprocs` exists.

26. ⭐ Your service spawns a goroutine per incoming HTTP request with no limit. What can go wrong under a traffic spike? `[Applied]`

> Under a spike: 100,000 requests → 100,000 goroutines created instantly. Each holds stack memory. The GC must scan **all goroutine stacks** — pause times grow. Memory bloat. Scheduler overhead increases. Eventually OOM crash.
>
> Fix — bounded worker pool:
> ```go
> pool := make(chan struct{}, 100) // max 100 concurrent goroutines
> http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
>     pool <- struct{}{}
>     go func() {
>         defer func() { <-pool }()
>         handleRequest(w, r)
>     }()
> })
> ```

27. ⭐ What is a goroutine leak? Name two common causes and how you would detect one in production. `[Applied]`

> A **goroutine leak** is a goroutine that never exits — permanently consuming memory and a slot in the scheduler.
>
> Common causes:
> 1. **Channel send with no receiver** — goroutine parks in channel's `sendq` forever when caller abandons the channel
> 2. **Context never cancelled** — goroutine blocked on `ctx.Done()` but context is never cancelled
>
> Detection:
> - **In tests:** `github.com/uber-go/goleak` — asserts no unexpected goroutines are alive after test
> - **In production:** `pprof` goroutine profile at `/debug/pprof/goroutine` — shows all live goroutines with stack traces

28. When would you use a worker pool instead of spawning goroutines freely? `[Design]`

> Use a worker pool when:
> - **Downstream resource has limits** — DB has 100 max connections; 10,000 goroutines hitting it thrash the pool
> - **CPU-bound work** — limit to `GOMAXPROCS` workers so goroutines don't thrash each other
> - **Memory pressure** — unbounded creation during traffic spikes can OOM the service
> - **Backpressure needed** — a full pool naturally signals callers to slow down (instead of accepting unbounded load)

29. What happens if you use CGo heavily in a Go service that handles thousands of concurrent requests? `[Applied]`

> Each CGo call is treated as a **blocking syscall** — it pins a real OS thread (M). 1,000 concurrent goroutines making CGo calls → 1,000 OS threads created. The default thread limit is **10,000** — hit it and the program panics with thread exhaustion.
>
> Fix: use a semaphore to limit concurrent CGo calls, or switch to pure-Go alternatives (e.g., `modernc.org/sqlite` instead of the CGo SQLite driver).

30. ⭐ You set `GOMAXPROCS(1)`. Can your program still have multiple OS threads? Explain. `[Gotcha]`

> **Yes.** `GOMAXPROCS(1)` means only 1 P — so only 1 goroutine executes at a time. But if that goroutine makes a **blocking syscall**, Go spins up a new M to keep P running. So even with `GOMAXPROCS=1`, you can have 2+ OS threads — one stuck in a syscall, one actively scheduling.
>
> `GOMAXPROCS` controls **active scheduling parallelism**, not total thread count. Total threads can always exceed `GOMAXPROCS` due to syscalls.
