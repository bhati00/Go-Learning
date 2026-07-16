# Go Internals — Interview Prep Tracker
**Target level:** 3-4 YOE  ·  **Total topics:** 88

---

## Phase 1 — Language Data Structures · 9 topics
> Learn these first. Slices, maps, and strings appear in almost every other topic.

- Slice internals — 3-word header (`ptr`, `len`, `cap`)
- Slice — how `append` works, backing array sharing after append
- Slice — growth factor (smooth curve since Go 1.18, was 2x before)
- String internals — immutable `ptr + len` header, no null terminator
- String — `[]byte(s)` is a full copy, concatenation in loop is O(n²), use `strings.Builder`
- Map internals — array of buckets, 8 key-value pairs per bucket
- Map — incremental growth/evacuation (not all-at-once like a slice doubling)
- Map — non-deterministic iteration order (runtime randomizes start bucket on each range)
- Map — concurrent read+write causes runtime panic, not silent data corruption

---

## Phase 2 — Interface & Type System · 8 topics
> Must come before the nil interface bug, escape analysis, and GC topics.

- Interface two-word structure — type pointer + data pointer
- `iface` vs `eface` — typed interface vs `interface{}`/`any`
- nil interface vs nil pointer bug — typed nil stored inside interface is not nil
- Interface boxing — when does storing a value in an interface cause heap allocation
- Pointer vs value receiver rules — which receiver types satisfy which interface
- Type assertion cost — single pointer comparison, not expensive
- Dynamic dispatch cost — 2 pointer dereferences per method call through interface
- `make` vs `new` vs composite literal — when each is appropriate

---

## Phase 3 — Error Handling · 5 topics
> Completely distinct from exceptions. High frequency at 3-4 YOE — do not skip.

- `fmt.Errorf` with `%w` (wraps, preserves chain) vs `%v` (formats only, breaks chain)
- `errors.Is` vs `errors.As` — when to use each
- Error wrapping chain — how `errors.Is` traverses `Unwrap()` recursively
- Sentinel errors vs custom error types — trade-offs of each approach
- Handle an error only once — the Go idiom (log it or return it, not both)

---

## Phase 4 — defer / panic / recover · 8 topics
> Self-contained language mechanics. Appear in every interview round.

- When does `defer` execute relative to `return`
- Multiple defers — LIFO order
- `defer` with named return values — deferred func can read and modify them
- `defer` inside a loop — anti-pattern: defers stack until function returns, not per iteration
- `recover()` — must be called inside a deferred function to intercept a panic
- Panic scope — cannot `recover` a panic that originated in a different goroutine
- `runtime.Goexit()` vs `os.Exit()` — defers run vs do not run
- `defer` overhead — avoid on hot paths (open-coded since Go 1.14)

---

## Phase 5 — GMP Scheduler · 13 topics
> Highest overall frequency. Do not skip any sub-topic here.

- Why goroutines exist — OS thread cost vs goroutine cost
- G, M, P — what each one is (Goroutine · OS Thread · Logical Processor)
- GOMAXPROCS — what it controls, behaviour when set to 1
- Local Run Queue (LRQ) vs Global Run Queue (GRQ)
- Goroutine lifecycle — Runnable → Running → Blocked → Dead
- Goroutine initial stack size — starts at ~2KB, grows dynamically
- Stack growth — contiguous copy model (not segmented stacks)
- Work stealing — idle P steals from the back of another P's LRQ
- GRQ starvation prevention — every 61st scheduling tick, P checks GRQ instead of LRQ
- Blocking syscall — P detaches from M immediately and finds a new M to keep running
- Goroutine preemption — cooperative (pre-1.14) vs signal-based async (1.14+)
- `runtime.Gosched()` — manual cooperative yield to the scheduler
- Netpoller — network I/O is non-blocking at OS level; goroutine parked by Go runtime, not kernel

---

## Phase 6 — Escape Analysis · 5 topics
> Bridges the scheduler and GC. Explains why heap allocations happen.

- Stack vs heap — how the compiler decides where a variable lives
- How to verify — `go build -gcflags="-m"` shows escape decisions
- Returning a pointer to a local variable — always escapes to heap
- Closures and escape — variables captured by a closure escape to heap
- Interface and escape — storing a value in an interface often causes heap allocation

---

## Phase 7 — Go Memory Model · 5 topics
> The theoretical foundation for everything in Phase 9. Learn before channels.

- Happens-before — definition and why it matters for correctness
- No guarantee without synchronisation — data race exists even with "safe" timing
- Channel as a happens-before guarantee — send completes before receive sees value
- `sync.Mutex` memory ordering guarantee — stronger than just mutual exclusion
- What the race detector checks — happens-before violations, not hardware races

---

## Phase 8 — Garbage Collector · 8 topics
> Learn after escape analysis and memory model.

- GC algorithm — concurrent tri-color mark-and-sweep
- STW pauses — what they are and how Go has reduced them over versions
- `GOGC=100` — what it means and how to tune it for your workload
- Write barrier — what it is and why the concurrent GC needs it
- GC trigger — next cycle starts when live heap reaches ~2x the size after last GC
- Reducing GC pressure — `sync.Pool`, avoid allocations on hot paths
- Finalizers — what they are and why to avoid them in production
- `GODEBUG=gctrace=1` — lightweight way to observe GC activity in production

---

## Phase 9 — Concurrency Depth · 20 topics
> Most interview time at 3-4 YOE is spent here. Learn after the memory model.

### Channels
- Channel internals — goroutine parked into send/receive wait queue (`sendq` / `recvq`)
- Buffered vs unbuffered — scheduler-level difference
- Send on closed channel (panic) vs receive from closed channel (zero value, `ok=false`)
- `select` pseudo-random — when multiple cases are ready, choice is random not FIFO
- `select` when all cases block — goroutine parked on all channels simultaneously
- Direct send optimisation — sender copies value directly to receiver's stack, skipping buffer

### Sync Primitives & Goroutine Safety
- `sync.Mutex` vs channel — when to use which
- `sync.RWMutex` — when does it hurt performance (write lock causes reader starvation)
- `sync.WaitGroup` — waiting for a group of goroutines to complete (`Add` / `Done` / `Wait`)
- `sync.Pool` — objects cleared every GC cycle (the main gotcha)
- `sync.Map` — when to prefer over `map + RWMutex` (read-heavy, mostly-stable key sets)
- `sync.Once` — safe lazy initialisation, cannot be reset
- `sync.Mutex` starvation mode — after 1ms wait, mutex switches to FIFO handoff (Go 1.9+)
- Goroutine leak — two common causes and how to detect them in production

### Context & Patterns
- `context.WithCancel` — cancellation propagates down the context tree
- Context leak — not calling the cancel function keeps child resources alive
- `WithTimeout` vs `WithDeadline` — difference and when to use each
- `context.Value` lookup cost — O(depth) linear walk up the tree
- Worker pool — N workers draining a channel, clean shutdown on context cancel
- Fan-out / fan-in pipeline — when useful and when it creates back-pressure problems

---

## Phase 10 — Profiling & Tooling · 7 topics
> Learn last. Makes full sense only after understanding the runtime.

- CPU profile — `pprof`, finding which function consumes most CPU
- Memory profile — finding which code causes the most heap allocations
- Goroutine dump — detecting goroutine leaks in a running service
- Race detector — `go test -race`, runtime overhead (~5–10x), what it catches
- How to read a flame graph — wide boxes = time, call stacks read bottom-up
- `go tool trace` vs `pprof` — trace answers timing questions; pprof answers cost questions
- Collecting a CPU profile from a live service without restarting it (`net/http/pprof`)

---

## Progress Summary

| Phase | Area | Topics |
|---|---|---|
| 1 | Language Data Structures | 9 |
| 2 | Interface & Type System | 8 |
| 3 | Error Handling | 5 |
| 4 | defer / panic / recover | 8 |
| 5 | GMP Scheduler | 13 |
| 6 | Escape Analysis | 5 |
| 7 | Go Memory Model | 5 |
| 8 | Garbage Collector | 8 |
| 9 | Concurrency Depth | 20 |
| 10 | Profiling & Tooling | 7 |
| | **Total** | **88** |
