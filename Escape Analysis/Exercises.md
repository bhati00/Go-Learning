# Phase 6 — Escape Analysis · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 5
**Mode:** Work through one at a time. Predict first, then use the compiler or benchmark to verify.

> Research sources: Go FAQ — `stack_or_heap`, Go diagnostics documentation, Go compiler escape diagnostics (`-gcflags=-m`), and Go interview reports referenced in the phase questions.

---

## Exercise Distribution

| # | Type | Topic |
|---|------|-------|
| 1 | `[Trace]` | Predict stack vs heap from pointer lifetime |
| 2 | `[Output]` | Read escape-analysis diagnostics |
| 3 | `[BugHunt]` | Closure captures shared mutable state |
| 4 | `[FixIt]` | Remove an avoidable hot-path allocation |
| 5 | `[Implement]` | Prove an optimization with a benchmark |

> Order: establish the lifetime model → read the compiler's evidence → connect closures to a real concurrency bug → optimize a narrow hot path → measure rather than guess.

---

---

## Exercise 1 · `[Trace]` — Predict the lifetime boundary

**Context:** A pointer is not automatically a heap allocation in Go. The deciding question is whether the pointed-to value must survive its function call. Predict first; the compiler gets the final vote.

```go
package main

type Point struct {
	X, Y int
}

func add(a, b int) int {
	total := a + b
	return total
}

func localPointer() int {
	value := 10
	pointer := &value
	return *pointer
}

func newPoint() *Point {
	point := Point{X: 3, Y: 4}
	return &point
}

func main() {
	_, _, _ = add(1, 2), localPointer(), newPoint()
}
```

**Before running anything, classify `total`, `value`, and `point`: which must escape to the heap, and why?**

Then verify:

```bash
go build -gcflags="-m -l" main.go
```

`-l` disables inlining so the diagnostic is easier to connect to each function. It does not change the lifetime reasoning.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `total` is returned by value. The caller only receives the integer result, not an address that refers to `total`. It can stay on the stack, or the compiler can keep it in a register.
2. `value` has its address taken, but that address is used only within `localPointer`. `pointer` never leaves the function, so the compiler can prove that `value` dies before the stack frame disappears. Taking an address makes a variable a **candidate** for heap allocation; it does not force one.
3. `point` is different. `newPoint` returns `&point`; the caller needs that pointer after `newPoint` returns. The stack frame cannot provide that lifetime, so `point` moves to the heap.
4. With a current Go compiler, the useful diagnostic is typically `moved to heap: point`. The exact wording and whether stack-local variables are mentioned can vary by Go version.

**The Trap:** `&value` looks like it should force a heap allocation. The compiler only needs heap memory when the address can outlive the current function. A local pointer dereference does not cross that boundary.

**Answer:**

| Variable | Must escape? | Reason |
|---|---|---|
| `total` | No | Its value is copied to the result. |
| `value` | No | Its address is used only before `localPointer` returns. |
| `point` | Yes | A pointer to it is returned to the caller. |

**Interview Signal:** Tests whether you understand lifetime flow rather than applying the false rule "address taken means heap".

**Key Rule to Remember:** Rule: taking `&x` is not enough. `x` escapes only when its address can outlive the function or the compiler cannot prove otherwise.

---

---

## Exercise 2 · `[Output]` — Read the compiler's evidence

**Context:** In a performance review, intuition is only a hypothesis. `-gcflags=-m` explains the compiler's allocation decision at the source line that caused it.

Suppose this command is run against the code below:

```bash
go build -gcflags="-m -l" main.go
```

```go
package main

var latest *int

func remember() {
	requestID := 42
	latest = &requestID
}

func sum() int {
	left, right := 20, 22
	return left + right
}

func main() {
	remember()
	_ = sum()
}
```

And the relevant compiler output is:

```text
./main.go:8:2: moved to heap: requestID
```

**Answer all three:**
1. Why does `requestID` move to the heap?
2. Why are `left` and `right` absent from the escape diagnostic?
3. Does `moved to heap` mean the garbage collector immediately frees `requestID` when `remember` returns?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `requestID` begins as a local variable in `remember`.
2. `latest = &requestID` stores its address in a package-level variable.
3. Package-level state can be read after `remember` returns. A stack frame cannot supply that lifetime.
4. The compiler therefore allocates `requestID` on the heap and assigns its address to `latest`.
5. `left` and `right` are only used to calculate a return value. Neither variable nor its address escapes `sum`, so they need no heap allocation and often need no materialized stack slot at all.
6. The GC does not free the value simply because `remember` returned. It stays live as long as `latest` remains reachable and points to it. If `latest` is later replaced or cleared and no other reference remains, a future GC cycle may reclaim it.

**The Trap:** `moved to heap` describes the variable's **storage location**, not its immediate lifetime or an error. Heap allocation is often necessary and correct; the question is whether it matters in a measured hot path.

**Answers:**
1. It escapes because its address is stored in `latest`, which outlives `remember`.
2. They do not escape. The compiler may keep them on the stack or in registers, so there is no heap decision to report.
3. No. Reachability controls GC lifetime, not function return.

**Interview Signal:** Tests whether you can turn an escape diagnostic into a concrete data-flow explanation.

**Key Rule to Remember:** Rule: `moved to heap` means "needs storage beyond this stack frame," not "this is a leak" or "GC frees it on return."

---

---

## Exercise 3 · `[BugHunt]` — Captured state outlives the factory

**Context:** Closures are a common and legitimate reason for heap allocation. They are also a common place to accidentally share state between goroutines.

```go
package main

import (
	"fmt"
	"sync"
)

func makeIDGenerator() func() int {
	next := 0
	return func() int {
		next++
		return next
	}
}

func main() {
	id := makeIDGenerator()

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println(id())
		}()
	}
	wg.Wait()
}
```

**Find the primary bug.** Also explain why `next` cannot remain in `makeIDGenerator`'s stack frame after that function returns.

Verify the bug with:

```bash
go run -race main.go
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `makeIDGenerator` creates `next` and returns a closure that references it.
2. The returned closure may be called long after `makeIDGenerator` returns. Therefore `next` must outlive that function's stack frame; the compiler arranges heap-backed closure state.
3. All three goroutines call the **same** closure and therefore read and write the same `next` variable.
4. `next++` is a read-modify-write operation, not an atomic operation. The goroutines can overlap, creating a data race and possibly duplicate or missing IDs.
5. `go run -race main.go` should report a race involving the closure's captured state.

**The Trap:** The escape is not the bug. Moving `next` to the heap makes the closure safe to use after the factory returns. The bug is unsynchronized concurrent access to that shared state.

**The Fix:** Protect the closure state with a mutex.

```go
func makeIDGenerator() func() int {
	var mu sync.Mutex
	next := 0

	return func() int {
		mu.Lock()
		defer mu.Unlock()
		next++
		return next
	}
}
```

Now each call gets a unique ID, though the print order remains nondeterministic.

**Interview Signal:** Tests whether you can distinguish memory lifetime (escape analysis) from concurrency safety (the race detector).

**Key Rule to Remember:** Rule: a returned closure keeps captured variables alive, but it does not make concurrent access to them safe.

---

---

## Exercise 4 · `[FixIt]` — Remove the avoidable allocation

**Context:** A service serializes millions of small coordinates per minute. A benchmark points to this helper as allocating once per call. The returned pointer is unnecessary because the caller only needs the value.

```go
package location

type Coordinate struct {
	Latitude  int
	Longitude int
}

func Origin() *Coordinate {
	coordinate := Coordinate{
		Latitude:  0,
		Longitude: 0,
	}
	return &coordinate
}

func IsOrigin(c Coordinate) bool {
	return c == *Origin()
}
```

**Rewrite this code so `IsOrigin` does not need a heap allocation from `Origin`. Preserve the package API only where it makes sense.** Then verify your reasoning with:

```bash
go test -gcflags="-m -l" ./...
```

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening (original version):**
1. `Origin` returns `&coordinate`, so the pointer must remain valid after `Origin` returns.
2. `coordinate` therefore escapes to the heap.
3. `IsOrigin` immediately dereferences the pointer and only compares the value. It never needs pointer identity or mutation.
4. Returning a pointer here adds lifetime work without representing a real API need.

**The Fix:** Return the small immutable value directly.

```go
package location

type Coordinate struct {
	Latitude  int
	Longitude int
}

func Origin() Coordinate {
	return Coordinate{
		Latitude:  0,
		Longitude: 0,
	}
}

func IsOrigin(c Coordinate) bool {
	return c == Origin()
}
```

**The Trap:** "Return pointers to avoid copies" is incomplete advice. A pointer can add a heap allocation and GC work. For a two-`int` value such as `Coordinate`, copying is cheap. The right choice depends on the type size, mutability, API semantics, and measurements.

**Interview Signal:** Tests whether you can remove the cause of an allocation instead of masking it with pooling.

**Key Rule to Remember:** Rule: return a value when callers need a value. A pointer is for identity, mutation, optionality, or avoiding a measured expensive copy — not a default performance choice.

---

---

## Exercise 5 · `[Implement]` — Prove the hot-path claim

**Context:** A teammate says this small formatting helper is "allocation-free." Do not accept or reject the claim by inspection: write a benchmark that makes the answer observable.

```go
package label

import "strconv"

func Build(prefix string, id int) string {
	return prefix + ":" + strconv.Itoa(id)
}
```

**Write `label_test.go` with a benchmark for `Build`.** Requirements:

- Call `b.ReportAllocs()`.
- Keep work inside the timed loop.
- Store the result in a package-level sink so the compiler cannot discard the call.
- Run it with `go test -bench=Build -benchmem`.
- State what the `allocs/op` result proves, and what it does **not** prove.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**The Answer:**

```go
package label

import "testing"

var result string

func BenchmarkBuild(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		result = Build("order", i)
	}
}
```

Run:

```bash
go test -bench=Build -benchmem
```

**What's Happening:**
1. `b.ReportAllocs()` asks the benchmark framework to report allocations per operation.
2. The loop repeats the actual operation enough times for a stable measurement.
3. Assigning to package-level `result` makes the result observable outside the benchmark. This prevents the compiler from proving that `Build` is unused and removing the work.
4. `-benchmem` reports `B/op` and `allocs/op`. A non-zero count proves that this benchmarked call path allocates under the current Go version, architecture, and build settings.
5. If it allocates, use `go test -gcflags="-m -m"` to investigate why. If it does not, you have a useful result, not a permanent language guarantee.

**The Trap:** An allocation report is not an abstract property of a line of Go source. Inlining, compiler version, call context, inputs, and architecture can affect it. The benchmark is the runtime measurement; escape diagnostics explain the compiler's current reasoning.

**Interview Signal:** Tests whether you use the right evidence chain: benchmark to measure, compiler diagnostics to explain, then benchmark again to verify an optimization.

**Key Rule to Remember:** Rule: performance claim → benchmark with `-benchmem` → explain with `-gcflags=-m` → re-benchmark. Never optimize allocation behavior by folklore.

---

---

## Quick Reference — Rules from This Exercise Set

| Rule | Source Exercise |
|------|----------------|
| Taking an address alone does not force heap allocation | Ex 1 |
| A returned or globally stored pointer needs heap-backed lifetime | Ex 1, 2 |
| Heap allocation is not the same as a memory leak | Ex 2 |
| Returned closures keep captured state alive; concurrent access still needs synchronization | Ex 3 |
| Prefer values when pointer semantics are unnecessary and measurements support it | Ex 4 |
| Use `-benchmem` to measure and `-gcflags=-m` to explain | Ex 5 |
