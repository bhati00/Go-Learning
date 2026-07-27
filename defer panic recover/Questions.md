# Phase 4 — defer / panic / recover · Interview Questions

**Target level:** 3-4 YOE Go developer
**Total questions:** 10

> Research sources consulted: Go official blog — [Defer, Panic, and Recover](https://go.dev/blog/defer-panic-and-recover)
> (Andrew Gerrand); open-coded defer proposal — [Design 34481](https://go.googlesource.com/proposal/+/refs/heads/master/design/34481-opencoded-defers.md)
> (Dan Scales, Keith Randall, Austin Clements); Go spec — [Defer statements](https://go.dev/ref/spec#Defer_statements)
> and [Handling panics](https://go.dev/ref/spec#Handling_panics); Ardan Labs Go training; Go FAQ.

---

## Question Distribution

| Category | Questions |
|---|---|
| Runtime & Internals | Q1, Q2, Q3 |
| Language Gotchas | Q4, Q5, Q6 |
| Applied / Design | Q7, Q8 |
| Live Coding — Fix It | Q9 |
| Live Coding — Implement | Q10 |

---

## Q1 · Walk me through exactly what happens when a function hits `return x`. Where does `defer` fit in that sequence? `[Internals]`

**The question an interviewer actually asks:**
> "`return` feels like one thing. But someone told me deferred functions run 'during' the return. What does that actually mean at the machine level?"

---

**HOOK:** `return x` in Go compiles into **three separate steps**, not one. First, the return value is written into its slot. Second, all deferred functions run. Third, control jumps back to the caller. `defer` occupies the gap between steps one and two.

**INTERNALS:** The Go compiler does not treat `return x` as a single instruction. It emits:
1. An assignment to the return slot — either the named return variable directly, or an anonymous temporary.
2. A call to `runtime.deferreturn`, which pops and executes each deferred function in LIFO order.
3. A `RET` instruction to hand control back to the caller.

This sequence is why named return values (Topic 3) are writable by deferred functions — the value has already been committed to its variable before any deferred function runs. The deferred function sees the *already-set* value and can change it before the caller ever sees it.

One subtle consequence: the arguments to a deferred call are evaluated at **step zero** — at the moment the `defer` statement is executed, not when the deferred function fires. Only values captured inside a closure are evaluated at call time.

```go
x := 1
defer fmt.Println(x) // argument captured NOW → prints 1
x = 99
// function returns → deferred Println fires → output: 1, not 99

// Closure captures by reference — sees live value at call time:
defer func() { fmt.Println(x) }() // → prints 99
```

**REAL-WORLD:** The argument-evaluation timing causes real bugs. Teams instrument deferred log calls: `defer log.Printf("elapsed: %v", time.Since(start))` — they think it will print the elapsed time at exit, but `time.Since(start)` is evaluated immediately. The logged duration is always near-zero. The fix is a closure: `defer func() { log.Printf("elapsed: %v", time.Since(start)) }()`.

**INSIGHT:** Think of `return` as a three-act play: commit the value, run cleanup, exit the stage. `defer` owns act two — after the value is set but before the curtain falls.

---

⚠️ **Weak answer sounds like:** "Defer runs when the function returns."
That is true but non-specific. A strong answer explains the three-step compilation model and the argument-evaluation timing separately.

💬 **Likely follow-up:** "Can a deferred function change what the caller receives? When and when not?"

---

## Q2 · Explain exactly how `recover()` stops a panic. Why must it be inside a deferred function — and why can't you just call it anywhere in the function body? `[Internals]`

**The question an interviewer actually asks:**
> "I've seen people call `recover()` at the top of a function thinking it'll catch panics below. Why doesn't that work?"

---

**HOOK:** `recover()` only has power during a narrow window: when the runtime is unwinding the goroutine's call stack and is actively executing a deferred function. Outside that window, it returns `nil` and does nothing. The deferred function IS the catch site — there is no other catch site.

**INTERNALS:** When a goroutine panics, the runtime sets a panic flag and starts walking the defer chain for the current goroutine — not the full call stack, the defer chain. For each entry, it executes the deferred function. If, during the execution of one of those deferred functions, code calls `recover()`, the runtime:
1. Detects that an active panic exists for this goroutine.
2. Clears the panic flag.
3. Returns the panic value to the caller of `recover()`.
4. Resumes execution after the deferred call site — not after the line that panicked.

If `recover()` is called at any other time (normal execution, non-deferred function, any function not actively running during a panic unwind), the runtime sees no active panic and returns `nil`.

A critical subtlety: you cannot *delegate* recovery to a helper. This does not work:

```go
// BROKEN — recovery does not propagate up the call chain
func safeRecover() {
    if r := recover(); r != nil { // called from a helper, not from a deferred func
        fmt.Println("caught", r)
    }
}

func doSomething() {
    defer safeRecover() // THIS WORKS — safeRecover is directly deferred
    panic("oops")
}

// But this does NOT work:
func doSomethingBad() {
    defer func() {
        safeRecover() // safeRecover is called FROM the deferred func — recover() inside it CAN see the panic
    }()
    panic("oops")
}
```

Wait — actually `safeRecover()` called *from* a deferred closure works, because the recover is still executing within the panic unwind context. What doesn't work is calling `recover()` in a completely non-deferred path, or deferring a function that calls `recover()` in a goroutine that is not panicking.

**REAL-WORLD:** The most common mistake is writing a middleware function that calls `recover()` inside a helper that is not itself deferred: `handlePanic()` called from request processing, not from a `defer`. The unit tests pass because the middleware is wired up correctly in tests, but in production a different code path skips the defer and the panic leaks. Review your HTTP middleware and gRPC interceptors — every one of them that claims to catch panics should have the `defer func() { recover() }` as the very first statement.

**INSIGHT:** `recover()` is a signal interceptor that is only active during the unwind. The deferred function is the only code running during the unwind. Therefore the deferred function is the only place where that signal can be intercepted.

---

⚠️ **Weak answer sounds like:** "You have to put `recover()` in a defer because that's the rule."
That is the observation, not the explanation. The strong answer describes the runtime unwind mechanism and the active-panic window.

💬 **Likely follow-up:** "Can you recover from a panic that happens in a goroutine you spawned? Why or why not?"

---

## Q3 · What is open-coded defer? What changed in Go 1.14 and what makes a defer ineligible for this optimisation? `[Internals]`

**The question an interviewer actually asks:**
> "I've heard defer used to be expensive. Is that still true? Should I remove defers from performance-critical code?"

---

**HOOK:** Before Go 1.14, every `defer` allocated a struct on the heap and linked it into the goroutine's defer chain — about 35–50ns overhead per defer. Since Go 1.14, the compiler open-codes eligible defers as **direct inline calls at every return site** — no heap allocation, no chain, cost comparable to a plain function call.

**INTERNALS:** The proposal is documented in [Go issue 34481](https://go.googlesource.com/proposal/+/refs/heads/master/design/34481-opencoded-defers.md). The compiler assigns a **`deferBits` bitmask** — one bit per defer site. When execution reaches a `defer` statement, the compiler sets the corresponding bit and stores the function pointer and arguments in stack slots. At every function exit point, the compiler emits inline code: for each bit set in `deferBits`, call the corresponding stored function.

This approach has zero heap allocation: the bitmask and arguments live on the stack. In the panic case, the runtime reads the `deferBits` and stack slots from the FUNCDATA section to know which defers are still pending — slightly more complex, but correct.

**What makes a defer ineligible for open-coding:**
1. `defer` inside a `for` loop — the number of defers is dynamic, the bitmask cannot represent it statically.
2. Functions that call `recover()` — they require the full runtime defer chain machinery for panic interception.
3. More than 8 `defer` statements in one function — the `deferBits` bitmask is 8 bits by default; beyond that, fallback to the heap chain.

```go
// Eligible — compiled as inline calls at return sites. Zero allocation.
func get(mu *sync.Mutex, m map[string]int, key string) int {
    mu.Lock()
    defer mu.Unlock()
    return m[key]
}

// Ineligible — loop makes defer count dynamic → heap-allocated chain
func drainAll(items []item, mu *sync.Mutex) {
    for _, it := range items {
        mu.Lock()
        defer mu.Unlock() // falls back to runtime defer chain per iteration
        process(it)
    }
}
```

**REAL-WORLD:** Teams auditing Go 1.12 era codebases sometimes removed `defer` from tight loops for performance — a manual optimisation that introduced bugs when early returns were added later. After upgrading to Go 1.14+, those manual optimisations became unnecessary overhead in the codebase: worse readability, same performance, additional risk. The lesson: always benchmark before removing `defer`. The compiler likely already handled it.

**INSIGHT:** The performance concern about `defer` is largely historical for Go 1.14+. Profile before you optimise. If `defer` shows up in a `pprof` flame graph, you probably have it inside a loop — fix the loop, not the `defer`.

---

⚠️ **Weak answer sounds like:** "Defer is slow, so don't use it in hot paths."
That was the advice for Go 1.12 and earlier. Repeating it in 2025 without qualification reveals unfamiliarity with compiler improvements that have been shipping for six years.

💬 **Likely follow-up:** "How would you verify at build time whether a specific defer is being open-coded or heap-allocated?"

---

## Q4 · What does this function return? Explain why. `[Gotcha]`

**The question an interviewer actually asks:**
> "What does `c()` return — 1 or 2? Walk me through it."

```go
func c() (i int) {
    defer func() { i++ }()
    return 1
}
```

---

**HOOK:** `c()` returns **2**, not 1. The deferred function increments `i` after `return 1` sets it but before the caller receives it.

**INTERNALS:** `i` is a named return variable — it lives in the function's scope and is shared with any deferred function that references it. The sequence is:
1. `return 1` is reached → the compiler assigns `i = 1` into the named return slot.
2. The deferred function runs → it executes `i++` → `i` is now 2.
3. The caller receives `i`, which is 2.

If the return value were anonymous — `func c() int` — the deferred function would have no named variable to reach, and incrementing `i` inside the defer would have no effect on what the caller sees. The behaviour is entirely dependent on whether the return is named.

```go
func anonymous() int {   // returns 1 — defer cannot reach the return slot
    i := 0
    defer func() { i++ }()
    return 1
}

func named() (i int) {   // returns 2 — defer CAN reach and modify the return slot
    defer func() { i++ }()
    return 1
}
```

**REAL-WORLD:** This pattern is used deliberately in production code — most commonly in the recover-into-error pattern, where a deferred function sets the named `err` return value if a panic occurred. It is also a source of hard-to-spot bugs when a developer modifies a named return variable inside a defer thinking it is a local variable with no side effects on the return.

```go
// Bug: developer thinks they're logging the error, not changing the return
func riskyOp() (err error) {
    defer func() {
        if err != nil {
            log.Printf("op failed: %v", err)
            err = nil // unintentionally suppresses the error to the caller
        }
    }()
    return doWork()
}
```

**INSIGHT:** Named return variables are not cosmetic. They are real variables shared between the function body and all its deferred functions. Modifying them in a defer is a feature, not a side effect — but it must be intentional.

---

⚠️ **Weak answer sounds like:** "It returns 1 because you returned 1."
That answer ignores the defer entirely. A worse answer says "it depends" without being able to explain what it depends on.

💬 **Likely follow-up:** "What if the return value were anonymous — `func c() int`? Would the defer change anything?"

---

## Q5 · What is wrong with this code? What will happen at runtime and how do you fix it? `[Gotcha]`

**The question an interviewer actually asks:**
> "This looks like clean, defensive code. Walk me through what actually happens when we process 50,000 files."

```go
func processLogs(paths []string) error {
    for _, p := range paths {
        f, err := os.Open(p)
        if err != nil {
            return err
        }
        defer f.Close()
        if err := parse(f); err != nil {
            return fmt.Errorf("parse %s: %w", p, err)
        }
    }
    return nil
}
```

---

**HOOK:** `defer f.Close()` is inside a loop. All 50,000 files are opened and held simultaneously. None closes until `processLogs` returns. You will hit the OS file descriptor limit (typically 1024 on Linux by default, up to ~1 million with `ulimit -n`) long before finishing the list.

**INTERNALS:** `defer` defers to the **surrounding function's exit**, not the enclosing block. The `for` loop boundary is not a function boundary. Each iteration registers another deferred call — all 50,000 stack up until `processLogs` itself exits. At that point, 50,000 `f.Close()` calls fire in LIFO order. The OS sees 50,000 simultaneously open file descriptors for the entire duration of the function.

The error is subtle because the code looks correct for single-file usage, and unit tests typically run with small fixtures. It only manifests in production with large datasets or long-running processes.

**The fix — extract to a helper function:**

```go
func processLogs(paths []string) error {
    for _, p := range paths {
        if err := processOneLog(p); err != nil {
            return err
        }
    }
    return nil
}

func processOneLog(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer f.Close() // fires when processOneLog returns — per-file, correct
    return parse(f)
}
```

**REAL-WORLD:** This exact pattern has caused production outages. A background job process large data exports — works fine in staging with 100 files, crashes in production with 50,000 with `too many open files`. The fix is non-obvious to developers who have not internalised that `defer` is function-scoped. This is one of the most common `defer` review findings in Go codebases.

**INSIGHT:** `defer` is a function-scope construct. When you reach for `defer` inside a loop, your first question should be: "Can I extract this loop body into its own function?" If yes — do it. The defer is then correctly bounded.

---

⚠️ **Weak answer sounds like:** "You should close the file explicitly instead of using defer."
That misses the root cause and the idiomatic fix. The root cause is scope confusion, not misuse of defer. The fix is adding a function boundary, not abandoning defer.

💬 **Likely follow-up:** "Is there a way to use an immediately-invoked closure to fix this without extracting a named function?"

---

## Q6 · Can you recover from a panic that happens in a goroutine you spawned with `go`? Walk through exactly what happens if you try. `[Gotcha]`

**The question an interviewer actually asks:**
> "My service has a top-level `recover()` in main. I thought that would protect me from any panic. A goroutine panicked and took down the whole server. Why didn't my recover catch it?"

---

**HOOK:** No. Each goroutine has its own independent call stack. A panic unwinds **only the stack of the goroutine in which it occurred**. A `recover()` in a different goroutine's deferred function has no connection to that unwind and cannot intercept it.

**INTERNALS:** When a goroutine panics, the runtime calls `runtime.gopanic`, which walks the current goroutine's defer chain and executes each deferred function looking for a `recover()` call. The search is bounded entirely to this goroutine's defer chain. If no `recover()` is found anywhere in the chain, `runtime.gopanic` calls `runtime.fatalpanic`, which prints the stack trace and terminates the entire program — not just the goroutine.

No amount of `recover()` in other goroutines matters. The panic is goroutine-local. The crash is program-global.

```go
func main() {
    defer func() {
        if r := recover(); r != nil {
            fmt.Println("caught:", r) // NEVER FIRES for the goroutine below
        }
    }()

    go func() {
        panic("goroutine panic") // unwinds only THIS goroutine's stack
                                 // finds no recover() → program crashes
    }()

    time.Sleep(time.Second)
}
```

Output:
```
goroutine 6 [running]:
main.main.func2(...)
...
panic: goroutine panic
exit status 2
```

**The correct pattern — every goroutine owns its own recovery:**

```go
func safeGo(fn func()) {
    go func() {
        defer func() {
            if r := recover(); r != nil {
                log.Printf("goroutine panic: %v\n%s", r, debug.Stack())
            }
        }()
        fn()
    }()
}
```

**REAL-WORLD:** gRPC server frameworks, HTTP servers, and worker pools that spawn goroutines per request or connection must put recovery at the goroutine boundary, not at the top of `main`. The standard library's `net/http` server wraps each request handler goroutine with a recover — which is why a single panicking HTTP handler doesn't crash the server. Any goroutine you spawn manually does not have this protection unless you add it yourself.

**INSIGHT:** Each goroutine is an autonomous island. Its panic is its own business. Your job as the goroutine author is to put a recovery on every goroutine you create — the same way you own that goroutine's cleanup, cancellation, and leak prevention.

---

⚠️ **Weak answer sounds like:** "You need to use a recover somewhere higher up."
That answer fails to specify the goroutine boundary constraint and implies a global catch mechanism exists. It doesn't.

💬 **Likely follow-up:** "What happens to the deferred functions in the panicking goroutine if no `recover()` is found? Do they run?"

---

## Q7 · What is the difference between `runtime.Goexit()` and `os.Exit()` with respect to deferred functions? When does this distinction matter in practice? `[Applied]`

**The question an interviewer actually asks:**
> "My test cleanup function isn't running. The test calls `t.FailNow()`. I expected my defer to run. Did I miss something?"

---

**HOOK:** `runtime.Goexit()` terminates the current goroutine **and runs all its deferred functions** before doing so. `os.Exit()` terminates the entire program process **without running any deferred functions**. `t.FailNow()` calls `runtime.Goexit()` internally — so your defer will run.

**INTERNALS:** `runtime.Goexit()` works by entering the same defer-unwinding path that a panic uses, but without setting a panic flag. The runtime walks the goroutine's defer chain, executing every entry in LIFO order. Nothing can stop it — not even a `recover()` (which returns `nil` and resumes the Goexit processing unchanged). When all defers finish and the goroutine returns, it is removed from the scheduler.

`os.Exit()` bypasses all of this. It calls the OS `exit()` syscall directly. The OS reclaims the process's resources immediately. No deferred functions run. No buffers flush. No cleanup happens. It is a hard kill.

| | `runtime.Goexit()` | `os.Exit(code)` |
|---|---|---|
| Scope | Current goroutine | Entire process |
| Deferred functions | **Run** | **Do not run** |
| Stoppable by `recover()` | No | N/A |
| Other goroutines continue | Yes | No |
| Primary use | `t.FailNow()`, graceful goroutine exit | Fatal startup errors |

**REAL-WORLD:** The `log.Fatal` trap: `log.Fatal(err)` is `log.Println(err)` followed by `os.Exit(1)`. Many teams put `defer flushMetrics()`, `defer closeTracer()`, or `defer saveState()` in `main()` and call `log.Fatal` on startup failures — confident that cleanup will run. It won't. The deferred functions are silently skipped. This is a frequent source of missing shutdown metrics, unclosed database connections, and incomplete trace exports in production services.

```go
func main() {
    defer sendShutdownEvent() // WILL NOT run if os.Exit is called
    defer db.Close()          // WILL NOT run

    cfg, err := loadConfig()
    if err != nil {
        log.Fatal(err) // os.Exit(1) → sendShutdownEvent and db.Close are lost
    }
    // ...
}

// Fix: explicit cleanup before exit
func main() {
    cfg, err := loadConfig()
    if err != nil {
        log.Println(err)
        sendShutdownEvent() // call explicitly
        os.Exit(1)
    }
    defer sendShutdownEvent()
    defer db.Close()
    // ...
}
```

**INSIGHT:** Treat `os.Exit` as a hard kill: everything deferred is gone. Use it only when you explicitly do not want cleanup. For test helpers, rely on `t.FailNow()` — it uses `Goexit`, your defers run. Avoid `log.Fatal` anywhere except the very start of `main` before any defers are registered.

---

⚠️ **Weak answer sounds like:** "They both stop the program, the difference is the exit code."
That misses the essential distinction entirely and would be a red flag in a code review discussion.

💬 **Likely follow-up:** "Can `recover()` stop a `runtime.Goexit()`? What does recover return in that case?"

---

## Q8 · How would you design a goroutine wrapper that converts any panic into a returned error and guarantees the goroutine always exits cleanly? `[Design]`

**The question an interviewer actually asks:**
> "We spawn goroutines in a worker pool. Sometimes they panic with unhandled conditions. We want the pool to keep running and propagate the error back to the caller instead of crashing. How do you build this?"

---

**HOOK:** Wrap the goroutine body in a function that uses `defer + recover()` and writes the result — either the normal output or the recovered panic as an error — to a channel or error group. The key constraint is: recovery must be inside the *spawned* goroutine's deferred function, not in the pool itself.

**Design — channels:**

```go
// Run executes fn in a new goroutine and returns either the result or
// any panic converted to an error. The caller blocks until completion.
func safeRun(fn func() (any, error)) <-chan result {
    ch := make(chan result, 1)
    go func() {
        defer func() {
            if r := recover(); r != nil {
                // Panic converted to error with a stack trace for debugging
                ch <- result{
                    err: fmt.Errorf("panic: %v\n%s", r, debug.Stack()),
                }
            }
        }()

        val, err := fn()
        ch <- result{val: val, err: err}
    }()
    return ch
}

type result struct {
    val any
    err error
}
```

**Design — errgroup with recover:**

```go
// safeDo wraps a function to be used with errgroup.Group, converting
// any panic into a returned error so the group can continue.
func safeDo(fn func() error) func() error {
    return func() (err error) {
        defer func() {
            if r := recover(); r != nil {
                err = fmt.Errorf("panic recovered: %v\n%s", r, debug.Stack())
            }
        }()
        return fn()
    }
}

// Usage:
var g errgroup.Group
g.Go(safeDo(func() error { return doRiskyWork() }))
if err := g.Wait(); err != nil {
    log.Printf("worker failed: %v", err)
}
```

**Key design decisions to call out in an interview:**
1. **Buffered channel (size 1)** — prevents the goroutine from blocking if the caller times out and abandons the channel.
2. **Named return `(err error)` in the closure** — enables the `defer` to assign to `err` via the named return pattern.
3. **`debug.Stack()` in the error** — preserves the original panic location for debugging. Without it, the stack trace is lost.
4. **Only one result sent** — either the defer sends (on panic) or the normal path sends. Never both. The deferred function only fires on panic because the normal `ch <- result{...}` runs before the function returns.

**REAL-WORLD:** This pattern is the standard way gRPC interceptors and HTTP middleware implement panic recovery — they wrap the handler call in a deferred recover. The `net/http` package does this internally for each request handler goroutine.

**INSIGHT:** Recovery is always local to the goroutine. The wrapper pattern exposes the goroutine's outcome as a channel or error — letting the pool continue operating while making the failure visible to the orchestrator.

---

⚠️ **Weak answer sounds like:** "Use a defer with recover and log the panic."
Logging is a band-aid. The design question asks for the failure to propagate back to the caller. A strong answer addresses the channel/error-group plumbing and the named-return detail.

💬 **Likely follow-up:** "How do you handle the case where the goroutine panics after already writing partial results to a shared data structure?"

---

## Q9 · What does this program print? Explain each line of output. `[Gotcha]`

**The question an interviewer actually asks:**
> "Step me through the output. No running it."

```go
package main

import "fmt"

func main() {
    for i := 0; i < 3; i++ {
        defer fmt.Println(i) // Line A
    }
}
```

---

**HOOK:** The output is:
```
2
1
0
```

**INTERNALS:** Two mechanics interact here:

**1 — LIFO order.** Each `defer` statement pushes a call onto the goroutine's defer stack. When `main` exits, they fire in Last In, First Out order. The last registered call (`i=2`) fires first; the first registered call (`i=0`) fires last.

**2 — Argument evaluation at defer time.** `fmt.Println(i)` is the *argument* to defer — `i` is evaluated immediately when the `defer` statement executes, not when it fires. So:
- First iteration (`i=0`): defer registered with argument `0`
- Second iteration (`i=1`): defer registered with argument `1`
- Third iteration (`i=2`): defer registered with argument `2`

At exit, LIFO order fires: `2`, then `1`, then `0`.

**The common wrong answer — "it prints 2 2 2":** Candidates who have learned about the closure-in-loop gotcha (where all goroutines capture the same loop variable) incorrectly apply that reasoning here. But this is **not** a closure — the argument `i` is passed by value directly to `fmt.Println`. There is no shared reference. Each deferred call has its own copy of `i` at the time the defer was registered.

```go
// This WOULD print "2 2 2" — closure captures i by reference:
for i := 0; i < 3; i++ {
    defer func() { fmt.Println(i) }() // i is captured, not copied
}
// Output: 2\n2\n2

// The original code copies i immediately:
for i := 0; i < 3; i++ {
    defer fmt.Println(i) // i evaluated now → each defer has its own copy
}
// Output: 2\n1\n0
```

**REAL-WORLD:** This exact snippet appears in the official Go blog post on defer. It is asked specifically to probe whether the candidate understands the difference between argument evaluation at defer-time versus inside a closure, and LIFO order. Getting the wrong answer on this — especially "2 2 2" — reveals a specific and common misunderstanding.

**INSIGHT:** Direct arguments to defer are snapshots, taken at registration. Closures are live references. The difference between `defer f(x)` and `defer func() { f(x) }()` is the difference between "capture now" and "capture later."

---

⚠️ **Weak answer sounds like:** "It prints 2 2 2 because of the loop variable capture."
That answer conflates argument evaluation with closure capture. Correct on the closure observation in other contexts, but wrong here because there is no closure.

💬 **Likely follow-up:** "If I wrapped the `fmt.Println(i)` in an anonymous closure, what would the output be and why?"

---

## Q10 · Implement `safeCall`: a function that calls any `func() error` and returns its result, but if the call panics, converts the panic into an error and returns that instead. `[Coding]`

**The question an interviewer actually asks:**
> "Write a production-quality wrapper. I want the panic value in the returned error, and I want the original stack trace preserved."

---

**Requirements (evaluator checklist):**
1. Calls the provided function
2. Returns the function's error if it errors normally
3. If the function panics, returns the panic value as an error — does not re-panic
4. Preserves the original stack trace in the error message
5. Goroutine-safe (no shared state)
6. `recover()` is called correctly — inside a deferred function

---

**Reference implementation:**

```go
package main

import (
    "fmt"
    "runtime/debug"
)

// safeCall invokes fn and returns its error.
// If fn panics, the panic is recovered and returned as an error
// with the original stack trace embedded for debugging.
func safeCall(fn func() error) (err error) {
    defer func() {
        if r := recover(); r != nil {
            // r can be any type — string, error, int, or an arbitrary value.
            // Format it generically and include the original stack trace
            // so the caller knows where the panic occurred.
            err = fmt.Errorf("panic: %v\n\nstack trace:\n%s", r, debug.Stack())
        }
    }()

    // Normal execution path.
    // If fn returns without panicking, 'err' receives the return value.
    // If fn panics, the defer above fires and overwrites 'err'.
    return fn()
}
```

**Usage:**

```go
func riskyOp() error {
    panic("something went very wrong")
}

func safeDivide(a, b int) error {
    if b == 0 {
        panic(fmt.Sprintf("division by zero: %d / %d", a, b))
    }
    fmt.Printf("%d / %d = %d\n", a, b, a/b)
    return nil
}

func main() {
    err := safeCall(riskyOp)
    fmt.Println("riskyOp error:", err)

    err = safeCall(func() error { return safeDivide(10, 0) })
    fmt.Println("divide error:", err)

    err = safeCall(func() error { return safeDivide(10, 2) })
    fmt.Println("divide error:", err) // nil — no panic
}
```

**Evaluator checklist applied:**

| Criterion | Pass? | Notes |
|---|---|---|
| Calls `fn` | ✅ | `return fn()` |
| Returns fn's normal error | ✅ | Named return `err` receives `fn()`'s return |
| Converts panic to error | ✅ | `defer + recover()` → assigns to named `err` |
| Preserves stack trace | ✅ | `debug.Stack()` embedded in error message |
| No re-panic | ✅ | `recover()` clears the panic; does not call `panic(r)` |
| recover() correctly placed | ✅ | Inside a deferred function |
| Goroutine-safe | ✅ | No shared mutable state |

**Key design notes for the interview:**

- **Named return `(err error)`** is essential. Without it, the deferred function cannot assign to the return value. `defer func() { err = ... }()` only works when `err` is a named return variable.
- **`debug.Stack()` captures the panic site**, not the recovery site. That is the right stack trace — it shows where things went wrong, not where they were caught.
- **`r` can be any type.** Never type-assert `r` directly to `error` without checking. A `panic(42)` would cause a nil-dereference in the recovery code if you wrote `err = r.(error)`.
- **One send, not two.** Either `fn()` returns normally and `err` gets that value, or the panic fires and the defer overwrites `err`. These are mutually exclusive paths.

---

⚠️ **Weak implementation (common mistakes):**

```go
// BROKEN — anonymous return, defer cannot reach it
func safeCall(fn func() error) error {
    defer func() {
        if r := recover(); r != nil {
            // 'r' is recovered but we have no way to return it!
            // this just swallows the panic silently
            log.Printf("panic: %v", r)
        }
    }()
    return fn()
}
```

This version recovers the panic but cannot return it — the anonymous return value is unreachable from the deferred closure. The caller sees `nil` (zero value for `error`) and has no idea a panic occurred.

💬 **Likely follow-up:** "How would you modify `safeCall` to also accept a context for timeout-based cancellation?"
