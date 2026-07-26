# Phase 3 — Error Handling · Interview Questions

**Target level:** 3-4 YOE Go developer
**Total questions:** 10

> Research sources consulted: Go official blog — [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors)
> (Damien Neil & Jonathan Amsterdam); Dave Cheney — [Don't just check errors, handle them gracefully](https://dave.cheney.net/2016/04/27/dont-just-check-errors-handle-them-gracefully);
> Ardan Labs Go training materials (error handling design); official `errors` and `fmt` package docs.

---

## Question Distribution

| Category | Questions |
|----------|-----------|
| Runtime & Internals | Q1, Q2, Q5 |
| Language Gotchas | Q3, Q4, Q8 |
| Applied / Design | Q6, Q7, Q9 |
| Live Coding | Q10 |

---

## Q1 · What is the difference between `%w` and `%v` in `fmt.Errorf`? `[Internals]`

**The question an interviewer actually asks:**
> "Both `%w` and `%v` produce the same string output. So why does it matter which one I use?"

---

**HOOK:** `%v` formats the error as a flat string and discards it. `%w` formats it **and** stores the original error inside a wrapper struct, keeping it accessible via `Unwrap()`. Same message; completely different programmability.

**INTERNALS:** When `fmt.Errorf` sees `%w`, it returns a private `wrapError{msg: "...", err: original}` struct instead of a plain string-backed error. This struct implements `Unwrap() error`, which returns the original. That `Unwrap()` method is exactly what `errors.Is` and `errors.As` call when walking the chain. With `%v`, no `Unwrap()` is attached — the chain is severed permanently at that layer.

**REAL-WORLD:** Teams that use `%v` throughout their codebase end up with string-only errors at every API boundary. `errors.Is(err, sql.ErrNoRows)` returns false, retries fire when they shouldn't, and HTTP status code selection based on error type breaks silently. Switching to `%w` after the fact requires changing every logging and error-check call site.

**INSIGHT:** `%w` is a wrapping operation; `%v` is a formatting operation. The string you see is irrelevant — the question is whether the original error survives the layer crossing.

---

⚠️ **Weak answer sounds like:** "They both print the error, so it's just style."
That misses the entire programmability difference and reveals unfamiliarity with `errors.Is`.

💬 **Likely follow-up:** "When would you deliberately use `%v` instead of `%w`?"

---

## Q2 · How does `errors.Is` walk an error chain? What does it check at each step? `[Internals]`

**The question an interviewer actually asks:**
> "Walk me through what `errors.Is(err, target)` actually does internally. Don't just say 'it checks the chain.'"

---

**HOOK:** `errors.Is` is a loop, not a single check. It compares the current error to `target` by value/pointer equality, then calls `Unwrap()` to move one level deeper, and repeats until it finds a match or runs out of chain.

**INTERNALS:** Three things happen at each link of the chain:
1. **Equality check** — `err == target` (pointer comparison for most error types).
2. **Custom `Is()` method check** — if `err` implements `Is(error) bool`, that method is called. This lets you define custom equality semantics (e.g., "two errors are equal if their HTTP status codes match, regardless of instance").
3. **Unwrap** — call `Unwrap()` on `err` to get the next link. If `Unwrap()` returns nil, the chain is exhausted and `errors.Is` returns false.

The key implication: if any error type in the chain doesn't implement `Unwrap()`, traversal stops there — everything below is invisible.

**REAL-WORLD:** A custom error struct without `Unwrap()` is a dead end. It looks correct in unit tests where the error is returned directly, but breaks in integration tests where the error has been wrapped by middleware or logging layers. This is one of the most common review findings when auditing error handling in mid-size Go codebases.

**INSIGHT:** `Unwrap()` is the door between chain links. No `Unwrap()` = no transparency.

---

⚠️ **Weak answer sounds like:** "It searches through wrapped errors."
That describes the outcome, not the mechanism. A strong answer names the loop, the three checks, and the Unwrap() contract.

💬 **Likely follow-up:** "What is the custom `Is()` method for and when would you implement one?"

---

## Q3 · What is the difference between `errors.Is` and `errors.As`? When do you use each? `[Applied]`

**The question an interviewer actually asks:**
> "Give me a concrete rule for choosing between `errors.Is` and `errors.As` in production code."

---

**HOOK:** `errors.Is` is an identity check — "is this exact error value in the chain?" `errors.As` is a type check with extraction — "is there a value of this type in the chain, and give it to me so I can read its fields?"

**INTERNALS:** Both walk the chain the same way via `Unwrap()`. The difference is what they look for: `errors.Is` compares each node to a target value by equality. `errors.As` checks if each node is assignable to the target type, and if so, sets the target pointer and returns. The key is that `errors.As` gives you back the actual error value — not just a boolean.

```go
// errors.Is — sentinel match (you don't need the error value, just the fact)
if errors.Is(err, sql.ErrNoRows) {
    return nil, ErrNotFound // translate to your domain error
}

// errors.As — type match with data extraction (you need the fields)
var syntaxErr *json.SyntaxError
if errors.As(err, &syntaxErr) {
    return fmt.Errorf("invalid JSON at offset %d", syntaxErr.Offset)
}
```

**REAL-WORLD:** The decision is: "does the caller need the error's data, or just its identity?" If you need to log which file path failed (`*os.PathError.Path`), which SQL query was running, or which validation field rejected input — use `errors.As`. If you only need to branch on the type of failure (not found, permission denied, timeout) — use `errors.Is` against a sentinel.

**INSIGHT:** `errors.Is` answers "did X go wrong?"; `errors.As` answers "X went wrong — now tell me the details."

---

⚠️ **Weak answer sounds like:** "Use `errors.Is` for errors and `errors.As` for error types."
Technically true but too vague to demonstrate production experience. The strong signal is explaining *why* you need the value in one case and not the other.

💬 **Likely follow-up:** "Why can't you use a direct type assertion `err.(*os.PathError)` instead of `errors.As`?"

---

## Q4 · You use `err == ErrNotFound` to check errors. What is wrong with this? `[Gotcha]`

**The question an interviewer actually asks:**
> "This pattern worked fine until we started wrapping errors. Explain exactly why it breaks and how to fix it."

---

**HOOK:** Direct `==` comparison only checks the outermost error. The moment that error has been wrapped — even once with `fmt.Errorf("%w", err)` — the comparison returns false, silently misclassifying the error.

**INTERNALS:** `err == ErrNotFound` compares two `error` interface values. An interface comparison checks that both the dynamic type and the dynamic value are identical. A wrapped error is a `*wrapError` struct — its type is not the same as `ErrNotFound`'s type. The comparison fails at the first layer, with no chain traversal.

```go
var ErrNotFound = errors.New("not found")

raw := ErrNotFound
wrapped := fmt.Errorf("loadUser: %w", raw)

fmt.Println(raw == ErrNotFound)              // true  — unwrapped, direct match
fmt.Println(wrapped == ErrNotFound)          // false — wrong: wrapped is *wrapError
fmt.Println(errors.Is(wrapped, ErrNotFound)) // true  — correct: walks the chain
```

**REAL-WORLD:** This bug is introduced silently when someone adds context to an error return. An existing `==` check in the caller continues to compile and run without error, it just starts returning false. The result is misrouted requests, spurious retries, or incorrect HTTP status codes — all without any compile-time warning.

**INSIGHT:** `==` on errors is only safe for the exact value returned directly from a function, with no wrapping anywhere in the call stack. In any layered system, use `errors.Is`.

**The string comparison variant is equally wrong:**
```go
// Also broken — never inspect err.Error() for program logic
if strings.Contains(err.Error(), "not found") {
    // fragile: depends on a human-readable message never changing
}
```
Dave Cheney's explicit rule: *the `Error()` method exists for humans, not code.* Its output belongs in a log file or on screen. Changing an error message string — a cosmetic change — should never break program behaviour. Any code that branches on `err.Error()` output violates this.

---

⚠️ **Weak answer sounds like:** "You should always use `errors.Is`."
True, but an interviewer wants to hear *why* `==` breaks — interface comparison mechanics, not just the rule.

💬 **Likely follow-up:** "Are there any cases where `==` on errors is acceptable?"

---

## Q5 · Your custom error type wraps another error. What must you implement and why? `[Internals]`

**The question an interviewer actually asks:**
> "I have a struct with an `error` field inside it. I return it as the `error` type. `errors.Is` can't find the wrapped error. Why not, and what's the fix?"

---

**HOOK:** Your struct must implement `Unwrap() error`. Without it, the error chain is severed at your type — `errors.Is` and `errors.As` cannot see anything you're wrapping.

**INTERNALS:** `errors.Is` and `errors.As` advance the chain by calling `errors.Unwrap(err)`, which in turn checks if the current error implements `interface{ Unwrap() error }`. If it doesn't, traversal stops. Your struct's `error` field is private storage — no external code knows about it unless you expose it through `Unwrap()`.

```go
type AppError struct {
    Code    int
    Message string
    cause   error // private — invisible without Unwrap()
}

func (e *AppError) Error() string { return e.Message }

// Without this, errors.Is(err, someSentinel) stops at AppError
func (e *AppError) Unwrap() error { return e.cause }
```

The Go 1.20 multi-error variant uses `Unwrap() []error` (returns a slice) — used by `errors.Join`. Both forms are supported by `errors.Is` and `errors.As`.

**REAL-WORLD:** This mistake is extremely common in codebases that introduce custom error types. Developers write `AppError{cause: err}` and assume wrapping "just works." Tests that check `errors.Is(err, ErrSpecific)` pass when the error is returned unwrapped but fail when routed through middleware that wraps it again — because the custom type silently broke the chain.

**INSIGHT:** Wrapping an error in a struct without `Unwrap()` is like sealing something in a box with no label and no way to open it.

---

⚠️ **Weak answer sounds like:** "Just implement the `error` interface with `Error() string`."
That only makes the type a valid error. It says nothing about chain participation, which is the actual question.

💬 **Likely follow-up:** "What does `errors.Join` do and how is its `Unwrap()` different from the single-error version?"

---

## Q6 · Sentinel errors vs custom error types: what are the trade-offs? `[Design]`

**The question an interviewer actually asks:**
> "Your team is designing a new package. When do you define `var ErrXxx = errors.New(...)` and when do you define a struct type for your errors?"

---

**HOOK:** Sentinels signal conditions; custom types carry data. Use sentinels when the caller only needs to branch; use custom types when the caller needs to read fields.

**INTERNALS:** A sentinel is a package-level `var` — its value is a fixed pointer created once. Every caller that uses `errors.Is(err, ErrXxx)` is depending on that pointer's identity, making it a stable public API contract. A custom error type is a struct — every returned instance is different, but `errors.As` can find any instance of that type in the chain and give the caller the full value with all fields.

The critical design question is coupling: exporting either a sentinel or a custom error type creates a dependency from callers back to your package. A `var ErrNotFound` in your public API can never be removed without a breaking change. An unexported type should be used when callers don't need to inspect the error — keeping your internal error space private.

**REAL-WORLD:** `io.EOF` is the canonical sentinel — callers only need to know "stream ended," they don't need a struct. `*os.PathError` is the canonical custom type — callers need the path and the OS error. The real design question to ask: "What is the minimum information my caller actually needs to handle this error correctly?"

```go
// Sentinel: the caller just needs to know the outcome
var ErrNotFound = errors.New("not found")

// Custom type: the caller needs to know WHAT wasn't found
type NotFoundError struct {
    Resource string
    ID       int
}
func (e *NotFoundError) Error() string {
    return fmt.Sprintf("%s with id %d not found", e.Resource, e.ID)
}
```

**INSIGHT:** Over-reaching for custom types creates brittle caller code that depends on your internal struct layout. Over-using sentinels produces meaningless "not found" or "invalid" errors with no actionable context. Match the type to what the caller actually needs.

---

⚠️ **Weak answer sounds like:** "Use sentinels for simple errors and custom types for complex ones."
"Simple" and "complex" are vague. The right frame is: "does the caller need to read fields or just branch?"

💬 **Likely follow-up:** "If both are exported, what breaking-change rules apply to each?"

---

## Q7 · What does "handle an error only once" mean, and what is the consequence of violating it? `[Applied]`

**The question an interviewer actually asks:**
> "I see this a lot in Go codebases: every function both logs the error and returns it. Why is this considered bad practice?"

---

**HOOK:** Each error event should produce exactly one log entry, from the layer with the most context. Logging and returning at every layer creates duplicate, contradictory entries for a single failure — it's noise that hides real signal.

**INTERNALS:** In a three-layer call stack, if each layer logs before returning:
- `queryDB` logs: `"db: connection refused"`
- `loadUser` logs: `"service: failed to load user: connection refused"`
- `handleHTTP` logs: `"handler: failed: service: failed to load user: connection refused"`

Three log entries. Same event. Each partially redundant. Now multiply by your request rate and log aggregation becomes meaningless.

The correct pattern: every intermediate layer adds context with `%w` and returns without logging. The top-level handler — the one with full request context (user ID, request ID, operation name) — logs exactly once.

```go
// Wrong: logs AND returns
func loadUser(id int) error {
    if err := queryDB(id); err != nil {
        log.Printf("loadUser failed: %v", err) // ← logs
        return fmt.Errorf("loadUser: %w", err) // ← also returns
    }
    return nil
}

// Correct: adds context and returns, no logging
func loadUser(id int) error {
    if err := queryDB(id); err != nil {
        return fmt.Errorf("loadUser(id=%d): %w", id, err) // context added, returned
    }
    return nil
}
// Logging happens once, at the top-level handler
```

**REAL-WORLD:** In distributed systems with log aggregation (Datadog, Splunk, ELK), duplicate log entries from the same error inflate error rates, trigger false alerts, and make tracing through microservices almost impossible. This is a leading cause of alert fatigue in teams that have adopted Go without Go-specific error handling patterns.

**INSIGHT:** The "handle once" rule is not about politeness — it's about signal clarity. One error, one log line, all the context.

---

⚠️ **Weak answer sounds like:** "You shouldn't log too much."
That's generic advice. The sharp answer names the specific anti-pattern (log + return), explains the duplicate-entry consequence, and articulates which layer should log.

💬 **Likely follow-up:** "What does 'deliberately discard an error' look like in Go, and when is it acceptable?"

---

## Q8 · Why is `errors.New("not found")` inside a function body not a sentinel error? `[Gotcha]`

**The question an interviewer actually asks:**
> "I wrote this function that returns `errors.New("not found")`. My caller's `errors.Is` check always returns false. Why?"

---

**HOOK:** `errors.New` creates a new, unique error value every time it's called. Two calls to `errors.New("not found")` produce two different pointers. `errors.Is` compares by identity — so it never matches.

**INTERNALS:** `errors.New` returns a `*errorString` struct. Each call allocates a new struct at a new address. The string content is irrelevant to comparison — `errors.Is` compares the pointer, not the message. A sentinel error is only useful because it is created **once** at package initialisation, giving callers a stable pointer to compare against.

```go
// BROKEN — new pointer every call; errors.Is always returns false
func getUser(id int) error {
    return errors.New("not found") // different *errorString each time
}

// CORRECT — same pointer every call; errors.Is works
var ErrNotFound = errors.New("not found") // created once
func getUser(id int) error {
    return ErrNotFound
}
```

**REAL-WORLD:** This mistake is extremely easy to introduce during refactoring — someone extracts error creation inline "for readability" and silently breaks every `errors.Is` check in the codebase. Tests that checked the message string continue to pass; tests that used `errors.Is` all start failing. The fix is trivial once you understand it, but the root cause is non-obvious.

**INSIGHT:** Sentinel errors are singletons by design. The moment error creation moves inside a function, the singleton property is destroyed.

---

⚠️ **Weak answer sounds like:** "Maybe you need to unwrap the error first."
The bug isn't about unwrapping — it's about identity. Two different allocations with the same message are not equal.

💬 **Likely follow-up:** "Is there a case where you would intentionally NOT use a package-level var for an error?"

---

## Q9 · When would you deliberately break the error chain with `%v` instead of `%w`? `[Design]`

**The question an interviewer actually asks:**
> "You said `%w` is the default. Give me a concrete situation where `%v` is the right choice."

---

**HOOK:** Use `%v` when crossing a public API boundary where you do not want internal error types to leak to external callers — either for security, for versioning stability, or because the internal error has no meaning outside your package.

**INTERNALS:** Every error you wrap with `%w` becomes reachable to your caller via `errors.Is` and `errors.As`. If the wrapped error is an internal type (a database driver error, a private struct from a vendor package), exposing it to callers via `%w` creates a hidden dependency. Callers can start writing `errors.As(err, &pgErr)` for your internal Postgres driver error, and now you can never swap drivers without breaking them.

```go
// Breaking the chain intentionally at the service boundary
func (s *UserService) GetUser(id int) (*User, error) {
    u, err := s.db.QueryUser(id)
    if err != nil {
        if isNotFound(err) {
            return nil, ErrNotFound // controlled translation; no internal type leaked
        }
        // %v deliberately severs the chain — callers cannot inspect db internals
        return nil, fmt.Errorf("GetUser: failed to query: %v", err)
    }
    return u, nil
}
```

**REAL-WORLD:** This is a deliberate technique in library and microservice design. Your service's public error contract is `ErrNotFound`, `ErrUnauthorised`, etc. — not `*pgconn.PgError` or `*grpc.Status`. Using `%v` at the boundary enforces this contract and prevents accidental coupling through the error chain.

The Go 1.13 blog states this precisely: *"wrapping an error makes that error part of your API. If you don't want to commit to supporting that error as part of your API in the future, you shouldn't wrap the error."* This is the exact mental model an interviewer is looking for.

**INSIGHT:** `%w` is transparent; `%v` is opaque. Choose opaque when transparency would leak implementation details across a public contract.

---

⚠️ **Weak answer sounds like:** "Use `%v` when you don't need to check the error later."
That's vague. The precise answer is about API contracts, error type leakage, and intentional boundary enforcement.

💬 **Likely follow-up:** "How do you translate internal database errors to domain errors safely without leaking implementation details?"

---

## Q10 · Live Coding — Fix the error chain `[Gotcha]` + `[Coding]`

**The question an interviewer actually asks:**
> "This code has two problems. Find them and fix both."

---

**Broken code:**

```go
package main

import (
    "errors"
    "fmt"
)

type DBError struct {
    Code    int
    Message string
    cause   error
}

func (e *DBError) Error() string {
    return fmt.Sprintf("db error %d: %s", e.Code, e.Message)
}

var ErrTimeout = errors.New("timeout")

func queryDB() error {
    return &DBError{Code: 503, Message: "upstream timeout", cause: ErrTimeout}
}

func loadData() error {
    err := queryDB()
    return fmt.Errorf("loadData: %v", err) // ← wrapping with %v
}

func main() {
    err := loadData()

    // Bug 1: should print true, prints false
    fmt.Println(errors.Is(err, ErrTimeout))

    // Bug 2: should print true, prints false
    var dbErr *DBError
    fmt.Println(errors.As(err, &dbErr))
}
```

---

**Where to look:**

Two independent bugs compound each other. Find both before looking at the solution.

---

**Fixed code:**

```go
package main

import (
    "errors"
    "fmt"
)

type DBError struct {
    Code    int
    Message string
    cause   error
}

func (e *DBError) Error() string {
    return fmt.Sprintf("db error %d: %s", e.Code, e.Message)
}

// FIX 1: DBError must implement Unwrap() so errors.Is can reach e.cause
func (e *DBError) Unwrap() error {
    return e.cause
}

var ErrTimeout = errors.New("timeout")

func queryDB() error {
    return &DBError{Code: 503, Message: "upstream timeout", cause: ErrTimeout}
}

func loadData() error {
    err := queryDB()
    // FIX 2: use %w, not %v, so the chain survives this layer
    return fmt.Errorf("loadData: %w", err)
}

func main() {
    err := loadData()

    // Chain: wrapError("loadData: ...") → *DBError → ErrTimeout
    fmt.Println(errors.Is(err, ErrTimeout)) // now: true
    var dbErr *DBError
    fmt.Println(errors.As(err, &dbErr)) // now: true — dbErr.Code == 503
}
```

---

**Why each bug mattered:**

| Bug | Root cause | Symptom |
|-----|-----------|---------|
| Missing `Unwrap()` on `DBError` | Chain severed at `*DBError` — nothing below visible | `errors.Is(err, ErrTimeout)` → false |
| `%v` in `loadData` | Chain severed at `loadData` layer | Both `errors.Is` and `errors.As` → false |

**Evaluation checklist:**
1. Did the candidate identify **both** bugs without being told there were two?
2. Did they explain *why* each breaks the chain (not just fix it mechanically)?
3. After the fix, can they trace the full chain: `wrapError → *DBError → ErrTimeout`?

---

⚠️ **Weak answer sounds like:** "Just change `%v` to `%w`."
That fixes one bug. A candidate who misses `Unwrap()` is not thinking about the full chain — they're pattern-matching on the `%w` rule without understanding what drives it.

💬 **Likely follow-up:** "If you could only fix one bug, which would you fix first to regain the most functionality?"
