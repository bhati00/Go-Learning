# Phase 4 — defer / panic / recover · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 5
**Mode:** Work through one at a time. Write your answer, then read the walkthrough.

> Research sources: Blind Go interview reports, r/golang, Go FAQ, Go Spec — Defer statements, Go Blog — Defer, Panic, and Recover (Andrew Gerrand).

---

## Exercise Distribution

| # | Type | Topic |
|---|------|-------|
| 1 | `[Output]` | Argument evaluation timing vs closure capture |
| 2 | `[Output]` | defer in a loop — function scope, not block scope |
| 3 | `[Output]` | Named return value modified by a deferred function |
| 4 | `[FixIt]` | recover() called outside a deferred function |
| 5 | `[Output]` | panic + recover + named return — full interaction |

> Order: two output predictions first (builds the mental model) → one deeper output → one fix → one synthesis. Each exercise builds on the previous.

---

---

## Exercise 1 · `[Output]` — Argument evaluation timing vs closure capture

**Context:** This is the most-cited `defer` gotcha in real Go interviews on Blind and r/golang. It appears in real code review comments as "your defer is logging zero every time." The trap is subtle because both lines look identical at a glance.

```go
package main

import "fmt"

func main() {
	x := 1
	defer fmt.Println(x)
	defer func() { fmt.Println(x) }()
	x = 99
}
```

**What does this print?** The two `defer` statements look similar — both print `x`. Work through what each one captures and in what order they fire.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `x` is set to 1.
2. `defer fmt.Println(x)` is registered. The argument `x` is evaluated **immediately at this line** — Go evaluates arguments to a deferred call at the moment the `defer` statement executes, not when the deferred function runs. The integer `1` is baked in. No matter what happens to `x` later, this defer will print `1`.
3. `defer func() { fmt.Println(x) }()` is registered. This is a closure — the `func()` body is not executed now, only stored. The closure holds a reference to the variable `x`, not a snapshot of its value.
4. `x = 99` updates the variable.
5. `main()` returns. The defer stack unwinds **LIFO**: the last-registered defer fires first.
6. The closure (registered second) fires first: it reads `x` now — `x` is `99` → prints `99`.
7. `fmt.Println(x)` (registered first) fires second: argument was captured as `1` at registration → prints `1`.

**Output:**
```
99
1
```

**The Trap:** Both lines appear to "use `x`". The difference is **when the expression is evaluated**. For a regular deferred call, the arguments are computed at the `defer` line. For a deferred closure, the body runs later — reading the live value of any captured variable. This is why `defer log.Printf("elapsed: %v", time.Since(start))` logs a near-zero duration — `time.Since(start)` is computed immediately. The fix is always a closure: `defer func() { log.Printf("elapsed: %v", time.Since(start)) }()`.

**Interview Signal:** Tests whether you understand the difference between argument evaluation at registration time vs closure capture by reference. This is the most common defer bug in production Go code.

**Key Rule to Remember:** Rule: `defer f(x)` → `x` is evaluated NOW. `defer func() { f(x) }()` → `x` is read when the deferred closure runs. When in doubt, use a closure.

---

---

## Exercise 2 · `[Output]` — defer in a loop — function scope, not block scope

**Context:** This is the most common `defer` mistake in production Go code. Developers assume `defer` fires at the end of a loop iteration. It does not. This exercise makes the timing concrete and visible.

```go
package main

import "fmt"

func run() {
	for i := 0; i < 3; i++ {
		defer fmt.Println("deferred:", i)
		fmt.Println("running:", i)
	}
}

func main() {
	run()
}
```

**What does this print?** Focus on two things: the order of the `running` and `deferred` lines, and the values of `i` in each deferred call.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. **Iteration i=0:** `fmt.Println("running:", 0)` executes immediately → prints `running: 0`. Then `defer fmt.Println("deferred:", 0)` is registered — argument `i` is evaluated now and captured as `0`. No deferred function fires yet.
2. **Iteration i=1:** prints `running: 1`, registers `defer fmt.Println("deferred:", 1)`.
3. **Iteration i=2:** prints `running: 2`, registers `defer fmt.Println("deferred:", 2)`.
4. The loop ends. The function continues (there is nothing after the loop). `run()` returns.
5. The defer stack unwinds **LIFO**: last registered fires first.
6. `deferred: 2`, then `deferred: 1`, then `deferred: 0`.

**Output:**
```
running: 0
running: 1
running: 2
deferred: 2
deferred: 1
deferred: 0
```

**The Trap:** Two things surprise people here. First, all three `running` prints complete before any `deferred` print — `defer` waits for the **function** to return, not the loop iteration to end. Second, the `i` values are correct (0, 1, 2) and not all the same final value. That is because `defer fmt.Println("deferred:", i)` evaluates the argument `i` at each iteration's `defer` statement — it captures the current integer value, not the variable. This distinguishes it from the goroutine-closure-in-loop bug where `go func() { fmt.Println(i) }()` would capture the variable and print the final value three times.

**Interview Signal:** Tests whether you understand that `defer` is scoped to the function, not the enclosing block or loop. In production, this manifests as file descriptor exhaustion: 10,000 iterations of `defer f.Close()` hold all 10,000 files open until the outer function returns.

**Key Rule to Remember:** Rule: `defer` fires when the enclosing **function** exits, not when the enclosing block or loop iteration ends. For per-iteration cleanup, extract the loop body into a helper function.

---

---

## Exercise 3 · `[Output]` — Named return value modified by a deferred function

**Context:** Named return values are one of the few Go features that feel genuinely magical until the mental model clicks. This exercise is exactly the kind of code an interviewer writes on a whiteboard and asks "what does this return?" It separates people who memorised the rule from people who actually understand the three-step return sequence.

```go
package main

import "fmt"

func triple(n int) (result int) {
	defer func() {
		result *= 2
	}()
	return n * 3
}

func main() {
	fmt.Println(triple(5))
}
```

**What does this print?**

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `triple(5)` is called. `result` is the named return variable — it is an ordinary integer variable in the function's scope, initialised to zero.
2. The deferred closure is registered. It holds a reference to `result` (shared scope).
3. `return n * 3` executes. In Go, `return x` is not a single instruction — it compiles into three steps: (a) assign `n * 3` (which is `15`) into the return slot, i.e., `result = 15`; (b) run all deferred functions; (c) hand control back to the caller.
4. Step (b): the deferred closure fires. It reads `result` (which is now `15`) and executes `result *= 2` → `result = 30`.
5. Step (c): the caller receives whatever is currently in `result` — which is `30`.

**Output:**
```
30
```

**The Trap:** Almost everyone says `15`. The intuition is "I returned `n * 3`, which is `15`, so the answer is `15`". That is correct if there are no deferred functions that modify `result`. But the deferred closure runs **after** the return value is committed to `result` and **before** the caller sees it. `result` is shared between the function body and its deferred functions — the deferred function is a participant, not an observer. It gets to change the outcome.

**Interview Signal:** Tests whether you understand the three-step return sequence and why named return values specifically (not anonymous returns) enable this pattern. The real-world use is panic-to-error conversion: `func safeDiv(a, b int) (result int, err error)` with a `recover()` in a deferred closure that sets `err` from the panic value.

**Key Rule to Remember:** Rule: `return x` with a named return → step order is: set named var = x → run defers (defers see and can modify the named var) → caller receives the final value. Defers can change what the caller receives.

---

---

## Exercise 4 · `[FixIt]` — Fix the broken panic recovery

**Context:** This exact pattern appears in real code reviews. The developer knows `recover()` exists and wants to prevent a crash — but placing it in the wrong location makes it completely inert. The program still crashes.

The function below is supposed to catch any panic that occurs inside it and print a message instead of crashing. It does not work. Fix it.

```go
package main

import "fmt"

func safeOperation() {
	r := recover()
	if r != nil {
		fmt.Println("recovered:", r)
	}
	panic("something went wrong")
}

func main() {
	safeOperation()
	fmt.Println("program continues")
}
```

**What is wrong, and what does the fixed version look like?**

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening (the broken version):**
1. `safeOperation()` is called during normal execution.
2. `r := recover()` executes. At this moment, there is no active panic unwind — execution is perfectly normal. `recover()` only intercepts a panic during the narrow window when the runtime is actively unwinding the goroutine's stack and executing a deferred function. Outside that window, it always returns `nil` and does nothing.
3. `r` is `nil`. The `if` block is skipped.
4. `panic("something went wrong")` executes. The runtime begins unwinding the stack of this goroutine. The defer chain is walked — but there are no deferred functions registered. The unwind reaches `main()`, finds no `recover()`, and the program crashes.

**The Trap:** `recover()` looks like a regular function call. It is tempting to call it at the top of a function to "arm" it as a catch handler for everything below. But `recover()` is not a handler — it is a signal interceptor that only activates when it is called *from within a deferred function during an active panic unwind*. Calling it anywhere else is inert.

**The Fix:**
```go
package main

import "fmt"

func safeOperation() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
		}
	}()
	panic("something went wrong")
}

func main() {
	safeOperation()
	fmt.Println("program continues")
}
```

**Output of the fixed version:**
```
recovered: something went wrong
program continues
```

**Why the fix works:** The deferred closure registers a catch site before the panic happens. When `panic("something went wrong")` fires, the runtime starts unwinding `safeOperation()`'s defer chain. It calls the deferred closure. Inside the closure, `recover()` is called — and at this moment an active panic exists for this goroutine. The runtime clears the panic flag, returns the panic value `"something went wrong"` to `recover()`, and resumes execution after the deferred call. The panic is stopped. `main()` continues normally.

**Interview Signal:** Tests whether you understand why `recover()` must live inside a deferred function — and what specifically makes a deferred function different from any other function in this context. The deferred function IS the catch block; there is no other catch block.

**Key Rule to Remember:** Rule: `recover()` outside a deferred function always returns `nil` and does nothing. The deferred function is the only legal catch site. No deferred function → no recovery.

---

---

## Exercise 5 · `[Output]` — panic + recover + named return — full interaction

**Context:** This is a synthesis exercise combining three topics: named return values, recover(), and the three-step return sequence. It is a realistic pattern used in production Go (safe wrappers, plugin sandboxes, test harnesses). Interviewers use this exact style of code to find the boundary between developers who have used `recover()` and those who understand it deeply.

```go
package main

import "fmt"

func divide(a, b int) (result int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("caught: %v", r)
		}
	}()
	result = a / b
	return
}

func main() {
	r, e := divide(10, 2)
	fmt.Println(r, e)

	r, e = divide(10, 0)
	fmt.Println(r, e)
}
```

**What does this print?** Trace both calls separately. For `divide(10, 0)`, trace what happens to `result` and `err` step by step.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening — `divide(10, 2)`:**
1. Deferred closure is registered. `result` and `err` are named return variables initialised to `0` and `nil`.
2. `result = a / b` → `result = 5`. No panic.
3. `return` executes → sets the named return variables (already set: `result=5`, `err=nil`) → runs deferred closure → `recover()` returns `nil` (no active panic) → `if` block skipped → function returns normally.
4. Caller receives `(5, nil)`.

**What's Happening — `divide(10, 0)`:**
1. Deferred closure is registered.
2. `result = a / b` → division by zero → runtime panics with `runtime error: integer divide by zero`. Normal execution stops immediately. `result` was never assigned (the assignment itself is what panicked) — it stays at its zero value `0`. `err` is still `nil`.
3. The runtime begins unwinding `divide()`'s stack. It runs the deferred closure.
4. Inside the closure, `recover()` is called during an active panic → runtime stops the unwind, returns the panic value (`runtime.Error` with message `"integer divide by zero"`).
5. `r` is non-nil → `err = fmt.Errorf("caught: %v", r)` → `err` is now set to the error string.
6. The deferred closure returns. Since `recover()` stopped the unwind, execution resumes after the deferred call. The function returns with the current state of the named variables: `result=0`, `err="caught: runtime error: integer divide by zero"`.
7. Caller receives `(0, "caught: runtime error: integer divide by zero")`.

**Output:**
```
5 <nil>
0 caught: runtime error: integer divide by zero
```

**The Trap:** Several things surprise people here. First, they expect the program to crash on `divide(10, 0)` — the `recover()` pattern is less intuitive than try/catch. Second, they expect `result` to have some partial value after the panic — it doesn't, because the panic fired before the assignment completed; `result` is still `0`. Third, they may not realise the deferred closure can set `err` because `err` is a named return variable (shared scope between function body and deferred functions).

**Interview Signal:** Tests three things simultaneously: the three-step return sequence, the named-return-as-shared-variable mechanic, and the exact moment `recover()` stops the unwind. This is the canonical Go "safe wrapper" pattern used in HTTP middleware, plugin hosts, and test frameworks. Understanding all three components together is a clear 3-4 YOE signal.

**Key Rule to Remember:** Rule: panic fires → named return vars hold whatever value they had at the moment of panic (not the intended return value) → deferred `recover()` catches the panic → deferred func can set named return vars before returning. The caller sees whatever the named vars contain after the deferred func completes.

---
