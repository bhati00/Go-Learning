# Phase 9c — Context & Patterns · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 6
**Mode:** Work through one at a time. Write your answer, then read the walkthrough.

> Research sources: Go blog "Go Concurrency Patterns: Context," `context` package godoc,
> r/golang threads on "context leak" and "context.Value misuse," Blind Go interview reports
> on graceful shutdown / rate limiting design questions, `golang.org/x/sync/errgroup` docs
> (the de-facto standard pattern interviewers expect you to know even if you implement it
> by hand), common onsite prompt "design a rate limiter" / "implement graceful shutdown."

---

## Exercise Distribution

| # | Type | Topic |
|---|------|-------|
| 1 | `[Trace]` | Context cancellation propagation through a tree of derived contexts |
| 2 | `[BugHunt]` | Context leak — missing `cancel()` call |
| 3 | `[Output]` | `WithTimeout` vs `WithDeadline` racing with manual cancel |
| 4 | `[BugHunt]` | Misusing `context.Value` for request-scoped business data |
| 5 | `[Implement]` | Graceful HTTP server shutdown with in-flight request draining |
| 6 | `[Design]` | Fan-out with context cancellation — first error wins (errgroup pattern) |

> Order: one trace to build the mental model of the context tree → two bug hunts on the
> two most common context misuses in real code review → one output-prediction gotcha on
> timeout/deadline/cancel interaction → two applied design problems that are extremely
> common in Go system-design interviews (graceful shutdown, and the errgroup fan-out pattern).

---

---

## Exercise 1 · `[Trace]` — Context cancellation propagation through a tree

**Context:** Understanding that contexts form a **tree**, and that cancellation flows strictly downward (parent → children, never sideways or upward), is the foundation for every other context question. Interviewers use this scenario to check whether you actually understand the mechanism or just memorized "pass ctx as the first argument."

**Scenario (no code — trace it in words):**

```
root := context.Background()
ctxA, cancelA := context.WithCancel(root)
ctxB, cancelB := context.WithCancel(ctxA)
ctxC, cancelC := context.WithTimeout(ctxA, 5*time.Second)

// A goroutine is blocked on <-ctxB.Done()
// Another goroutine is blocked on <-ctxC.Done()

cancelA() // called after 1 second
```

**Trace what happens, step by step, to `ctxB` and `ctxC` the instant `cancelA()` is called.** Does `ctxC`'s own 5-second timeout still matter? What does `ctxB.Err()` and `ctxC.Err()` return afterward?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `context.WithCancel` and `context.WithTimeout` both create a **child** context that registers itself with its parent specifically so the parent can propagate cancellation downward. `ctxB` is a child of `ctxA`; `ctxC` is also a child of `ctxA` (siblings, not related to each other).
2. When `cancelA()` runs, the runtime walks `ctxA`'s registered children and cancels **every one of them recursively** — `ctxB` is cancelled, and `ctxC` is cancelled too, even though `ctxC`'s own 5-second timer hasn't fired yet. Cancellation always flows from parent down through the entire subtree, regardless of each child's own cancellation reason.
3. Both `ctxB.Done()` and `ctxC.Done()` channels are closed at that instant. Both goroutines blocked on `<-ctx.Done()` unblock immediately (at ~1 second, not waiting for `ctxC`'s 5-second deadline).
4. `ctxB.Err()` returns `context.Canceled` (it was explicitly canceled). `ctxC.Err()` **also** returns `context.Canceled`, not `context.DeadlineExceeded` — because the actual reason it stopped was its parent's cancellation, which arrived before its own deadline.
5. `ctxC`'s internal timer is also stopped/cleaned up as part of this — it does not still fire uselessly at the 5-second mark.

**The Trap:** People assume a child's own `WithTimeout` "protects" it from being affected by anything else — it does not. A context's own deadline is only one of *two* ways it can end; a parent cancellation always wins if it happens first, and the `Err()` value correctly reflects whichever cause actually applied.

**Interview Signal:** Tests whether you understand contexts form a tree with cancellation propagating strictly downward from any ancestor, and that a child's own timeout doesn't shield it from an ancestor's earlier cancellation.

**Key Rule to Remember:** Rule: canceling a parent context cancels its entire subtree immediately, regardless of any child's own timeout/deadline. `ctx.Err()` reflects whichever cause (own deadline vs. inherited cancellation) actually occurred first.

---

---

## Exercise 2 · `[BugHunt]` — Context leak: missing `cancel()` call

**Context:** `go vet` actually has a specific lint rule for this (`lostcancel`) because it is such a common real bug. Every `context.WithCancel` / `WithTimeout` / `WithDeadline` allocates resources (a timer, in the timeout/deadline case) that must be released.

```go
func fetchWithBudget(url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	// BUG: cancel is never called

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	return http.DefaultClient.Do(req)
}
```

**Find the bug.** If this function is called 10,000 times over the life of a service, what actually leaks, and why does it matter even though the request itself completes fine?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `context.WithTimeout` internally starts a `time.Timer` (via `time.AfterFunc`) that will fire at the 3-second mark to cancel the context if nothing else does first. That timer, and the small bookkeeping structure tracking the context's child list and done channel, are only fully released when `cancel()` is called **or** the timeout actually fires.
2. If the request completes in, say, 200ms (well before the 3-second timeout), the context and its timer remain alive in memory for the *entire remaining 2.8 seconds* until the timeout eventually fires on its own and cleans things up. Calling `cancel()` immediately after use releases these resources the instant they're no longer needed, instead of leaving them alive doing nothing.
3. At low call volume this is invisible. At 10,000 calls/sec sustained (a real production API gateway calling downstream services), you're holding up to `10,000 * 3 seconds` = 30,000 concurrent live timers and context structures at any given moment even though each individual request finished almost instantly — a genuine, measurable memory and scheduler overhead that `go vet`'s `lostcancel` check exists specifically to catch at compile time.

**The Fix:** Always `defer cancel()` immediately after creating a cancellable context:

```go
func fetchWithBudget(url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	return http.DefaultClient.Do(req)
}
```

**The Trap:** The function "works" — the HTTP call succeeds or fails normally, so this bug produces no visible symptom in a code review or a quick test run. It's purely a resource-lifetime bug that only shows up as elevated memory/goroutine-adjacent overhead under sustained load, which is exactly why it's caught by static analysis (`go vet`) rather than by testing.

**Interview Signal:** Tests whether you know every `context.With{Cancel,Timeout,Deadline}` call returns a `cancel` function that must always be called — even when the context "naturally" expires — to release internal timer/bookkeeping resources immediately rather than leaving them until expiry.

**Key Rule to Remember:** Rule: every `cancel` returned by `context.With*` must be called via `defer cancel()` right after creation — even if you expect the context to time out naturally, calling `cancel()` early releases resources immediately instead of waiting for the deadline.

---

---

## Exercise 3 · `[Output]` — `WithTimeout` vs manual cancel racing

**Context:** This tests precise understanding of what `ctx.Err()` reports when multiple cancellation causes are "in flight" — a common follow-up question after the basic timeout/cancel explanation.

```go
package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel() // called well AFTER the timeout should have fired
	}()

	<-ctx.Done()
	fmt.Println("done:", ctx.Err())

	time.Sleep(300 * time.Millisecond) // let the late cancel() call happen too
	fmt.Println("after late cancel:", ctx.Err())
}
```

**What does this print for both lines?** Does calling `cancel()` late, after the context already timed out, change anything?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `ctx`'s internal timer fires at 50ms because nothing canceled it first — the context transitions to "done" with cause `context.DeadlineExceeded`. This is a **one-way, terminal transition**: once a context is done, its `Err()` value is fixed forever.
2. `<-ctx.Done()` unblocks at ~50ms. `ctx.Err()` returns `context.DeadlineExceeded`. Prints `done: context deadline exceeded`.
3. At 200ms, the goroutine calls `cancel()` — but the context is already done. Calling `cancel()` on an already-done context is a **safe no-op**: it does not overwrite the existing `Err()` value, and it does not re-close the already-closed `Done()` channel (which would panic if it were a plain channel close — the context package's internal implementation specifically guards against double-close/overwrite).
4. The second print, after waiting past the late cancel, still shows `context deadline exceeded` — unchanged.

**Output:**
```
done: context deadline exceeded
after late cancel: context deadline exceeded
```

**The Trap:** People sometimes assume that any call to `cancel()` immediately sets `Err()` to `context.Canceled`, overwriting whatever was there. It does not — `Err()` reflects whichever cause won the race **first**, and is permanently locked in after that. This is also why calling `cancel()` "just in case" (as you always should, per Exercise 2) is completely safe even after a context has already ended for another reason — it's explicitly designed to be a safe, idempotent no-op in that case.

**Interview Signal:** Tests whether you understand that a context's terminal cause is set exactly once, by whichever event (timeout or explicit cancel) happens first — and that calling `cancel()` afterward is always safe, never double-fires, and never overwrites the recorded reason.

**Key Rule to Remember:** Rule: a context's `Err()` is set once, by whichever cause (deadline or explicit cancel) occurs first, and is permanent. Calling `cancel()` after that point is always a safe no-op — which is exactly why `defer cancel()` is always safe to write unconditionally.

---

---

## Exercise 4 · `[BugHunt]` — Misusing `context.Value` for business data

**Context:** The Go docs explicitly say `context.Value` should only carry **request-scoped data that transits process/API boundaries** (request IDs, auth tokens, tracing spans) — not general application parameters. This is one of the most-cited Go anti-patterns in real code review and a common interview "spot the smell" question.

```go
func ProcessOrder(ctx context.Context) error {
	userID := ctx.Value("userID").(string)          // BUG smell #1: string key
	discount := ctx.Value("discountPercent").(float64) // BUG smell #2: business logic in ctx
	items := ctx.Value("items").([]Item)             // BUG smell #3: core input via ctx

	total := calculateTotal(items, discount)
	return saveOrder(userID, total)
}

func main() {
	ctx := context.Background()
	ctx = context.WithValue(ctx, "userID", "u123")
	ctx = context.WithValue(ctx, "discountPercent", 0.1)
	ctx = context.WithValue(ctx, "items", []Item{{Name: "Widget", Price: 10}})
	ProcessOrder(ctx)
}
```

**Identify every problem with this code**, beyond "it happens to compile and run."

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening / The Problems:**
1. **String keys collide.** `context.WithValue(ctx, "userID", ...)` uses a plain string as the key. Any other package that also happens to use the string `"userID"` as a context key (very plausible in a large codebase with multiple teams) will silently overwrite or shadow this value — there's no namespacing. The Go-idiomatic fix is an unexported custom type (`type ctxKey int`) so keys can never collide across packages.
2. **`items` and `discountPercent` are core function inputs, not request-scoped metadata.** They should simply be regular parameters to `ProcessOrder(ctx, userID, items, discount)`. Passing genuine business logic inputs through `context.Value` makes the function signature lie about its real dependencies — you can no longer tell what `ProcessOrder` needs just by reading its signature, and the compiler can't help you catch a missing or wrong-typed value; every one of those three lines is an unchecked type assertion that **panics** at runtime if the value is absent or the wrong type, instead of a compile error.
3. **No compile-time safety.** If `main()` forgets to set `"items"`, `ProcessOrder` panics on `ctx.Value("items").(string)` — a runtime crash for what should have been a compile error (a missing function argument).
4. **Testability is worse.** Testing `ProcessOrder` now requires constructing a fake `context.Context` with the exact right keys and values instead of just passing plain arguments — more ceremony for zero benefit.

**The Fix:**
```go
type ctxKey int
const userIDKey ctxKey = iota // unexported, collision-proof key type

func ProcessOrder(ctx context.Context, userID string, items []Item, discount float64) error {
	total := calculateTotal(items, discount)
	return saveOrder(ctx, userID, total) // ctx still passed for cancellation/tracing downstream
}
```
`context.Value` is reserved for things like a request ID or trace span that need to flow transparently through layers that don't know or care about them — not for `userID`, `items`, or `discount`, which are `ProcessOrder`'s actual, explicit inputs.

**Interview Signal:** Tests whether you understand `context.Value` is for cross-cutting, request-scoped metadata only — not a shortcut around passing explicit function parameters — and whether you know the string-key collision risk and the loss of compile-time type safety that comes with overusing it.

**Key Rule to Remember:** Rule: `context.Value` is for request-scoped metadata (IDs, auth, tracing) that transits API boundaries — never for a function's actual business-logic inputs. Use unexported custom key types, never raw strings, to avoid collisions.

---

---

## Exercise 5 · `[Implement]` — Graceful HTTP server shutdown

**Context:** "Implement graceful shutdown for a Go HTTP server" is one of the most commonly asked applied-context questions in mid-to-senior interviews — it directly combines `context.WithTimeout`, OS signal handling, and `http.Server.Shutdown`.

**Specification:**
- Start an `http.Server` listening on `:8080`.
- On receiving `SIGINT` or `SIGTERM`, stop accepting new connections immediately, but allow in-flight requests up to 10 seconds to finish before forcibly closing them.
- Log when shutdown begins and when it completes (or if the 10-second budget is exceeded).
- Do not care about the actual route handlers — a placeholder `http.HandlerFunc` is fine.

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. `http.Server.Shutdown(ctx)` is purpose-built for exactly this: it stops the listener immediately (no new connections accepted) and waits for active handlers to finish, up to the deadline of the `ctx` you pass in — this is the standard library's own context-based graceful-shutdown API, not something you build from scratch.
2. OS signal delivery is bridged into a Go channel via `signal.Notify`, so the main goroutine can `select`/block on it just like any other channel event.
3. The server must run in its own goroutine (`ListenAndServe` blocks), while the main goroutine waits for a shutdown signal, then triggers `Shutdown` with a bounded context.

```go
func main() {
	srv := &http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second) // simulate slow in-flight work
			w.Write([]byte("ok"))
		}),
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop // block until a shutdown signal arrives
	log.Println("shutdown signal received, draining in-flight requests...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown exceeded deadline: %v", err) // ctx.Err() surfaces here
	} else {
		log.Println("shutdown completed cleanly")
	}
}
```

**The Trap:** Calling `srv.Close()` instead of `srv.Shutdown(ctx)` immediately terminates all connections, dropping in-flight requests — it does not drain anything. Another common mistake is forgetting `signal.Notify`'s buffered channel (`make(chan os.Signal, 1)`) — an unbuffered channel can miss a signal if the runtime delivers it before the receiver is ready to read. A third mistake: not distinguishing `http.ErrServerClosed` (the expected error `ListenAndServe` returns after a clean `Shutdown` call) from a genuine startup failure — treating it as fatal would incorrectly crash-log every graceful shutdown.

**Interview Signal:** Tests whether you know the standard library already solves this (`Shutdown(ctx)`), whether you understand the context passed to it acts as the drain budget, and whether you correctly wire OS signals into Go's cooperative shutdown model.

**Key Rule to Remember:** Rule: use `srv.Shutdown(ctx)`, never `srv.Close()`, for graceful shutdown — the context you pass sets the drain deadline; `err == http.ErrServerClosed` after a triggered shutdown is expected, not an error to log as fatal.

---

---

## Exercise 6 · `[Design]` — Fan-out with context cancellation (errgroup pattern)

**Context:** This is the applied, production-grade version of Exercise 4 from Sync Primitives — it's effectively reimplementing `golang.org/x/sync/errgroup`, which interviewers frequently ask candidates to build "from scratch" to check they understand what the library abstracts away.

**Specification:**
- Write `FetchAllOrFail(ctx context.Context, urls []string) ([]string, error)`.
- Fetch all URLs concurrently (assume a `fetchURL(ctx, url) (string, error)` helper exists).
- If any fetch fails, **cancel all other in-flight fetches immediately** (they should receive a cancelled context and can abort early) and return that first error — don't wait for slow, now-pointless fetches to finish.
- If the caller's own `ctx` is cancelled externally (e.g., an upstream request timeout), all fetches should also stop promptly.

Take your time. Write your answer, then read the walkthrough below. Or type **skip** to see the solution directly.

---

### Solution Walkthrough

**What's Happening (design):**
1. A single derived `context.WithCancel(ctx)` is shared by every fetch goroutine — this gives two independent ways to stop everything: the caller's own `ctx` being cancelled propagates down automatically (Exercise 1's tree behavior), and any goroutine can call the local `cancel()` the moment it hits an error, which immediately cancels every sibling fetch too (since they're all children of the same derived context).
2. Ordering matters here just like the worker-pool exercise: results must land at their original index, guarded by a mutex (or, since we bail on first error anyway, a `sync.Once`-guarded error variable is enough — we don't need full result ordering if we're discarding results on any error).
3. `sync.Once` again ensures only the *first* error is recorded even if multiple fetches fail around the same time — a direct callback to the Sync Primitives exercises, showing these patterns compose across topics.

```go
func FetchAllOrFail(ctx context.Context, urls []string) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // ensures cleanup on every return path, success or failure

	results := make([]string, len(urls))
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error

	for i, url := range urls {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			val, err := fetchURL(ctx, url) // fetchURL must itself respect ctx cancellation
			if err != nil {
				once.Do(func() {
					firstErr = fmt.Errorf("fetching %s: %w", url, err)
					cancel() // stop every other in-flight fetch immediately
				})
				return
			}
			results[i] = val
		}(i, url)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
```

**The Trap:** The most common mistake is creating a **separate** `context.WithCancel` per goroutine instead of one shared derived context for the whole group — that makes it impossible for one failing fetch to cancel its siblings, defeating the entire point of "fail fast." The second common mistake: forgetting that `fetchURL` itself must check `ctx.Done()` internally (e.g., pass `ctx` into the underlying `http.NewRequestWithContext`) — cancelling a context does nothing on its own unless the code actually doing the work polls or responds to it.

**Interview Signal:** Tests whether you can build the "cancel siblings on first failure" pattern that `errgroup.WithContext` provides out of the box — proving you understand what the library does rather than just knowing its API. Interviewers who ask this are checking whether you'd reach for `errgroup` correctly in real code, having understood what it saves you from writing.

**Key Rule to Remember:** Rule: to make one failing goroutine cancel its siblings, derive **one shared** cancellable context for the whole fan-out group — not one per goroutine — and call its `cancel()` the moment any goroutine fails. This is exactly what `errgroup.WithContext` does for you.

---
