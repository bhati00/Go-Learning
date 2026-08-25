# Phase 2 — Interface & Type System · Exercises

**Target level:** 3-4 YOE Go developer
**Total exercises:** 5
**Mode:** Work through one at a time. Write your answer, then read the walkthrough.

> Research sources: Blind Go interview reports, r/golang, Go FAQ (nil_error), Go Spec (Method Sets), Go Blog.

---

## Exercise Distribution

| # | Type | Topic |
|---|------|-------|
| 1 | `[Output]` | Typed nil stored in error interface |
| 2 | `[Output]` | Pointer vs value receiver — method set and interface satisfaction |
| 3 | `[BugHunt]` | Typed nil always returned from conditional path |
| 4 | `[Output]` | Value receiver mutation through interface — lost state |
| 5 | `[FixIt]` | Fix the typed-nil returning function |

> Order: two output predictions first (builds the mental model) → one bug hunt → one deeper output → one fix. Each exercise builds on the previous.

---

---

## Exercise 1 · `[Output]` — Typed nil in the error interface

**Context:** The typed-nil error bug is the #1 interface gotcha reported in Go interview debriefs on Blind. You will see this in real code reviews.

```go
package main

import "fmt"

type AppError struct{ code int }

func (e *AppError) Error() string {
    return fmt.Sprintf("error code %d", e.code)
}

func process(input int) error {
    var err *AppError
    if input < 0 {
        err = &AppError{code: 400}
    }
    return err
}

func main() {
    e := process(42)
    fmt.Println(e == nil)
    fmt.Println(e)
}
```

**What does this print?**
`process(42)` is called with a non-negative input, so the `if` branch is never taken.

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `var err *AppError` declares a nil pointer of type `*AppError`. The variable itself holds `nil`, but its static type is `*AppError`.
2. `input` is 42, so the `if` block is skipped. `err` stays as `(*AppError)(nil)`.
3. `return err` returns this typed nil as an `error` interface.
4. The interface value now has two fields set: `type = *AppError` and `data = nil`.
5. `e == nil` compares the interface against nil. An interface is only nil when **both** type and data are unset. Here, type is `*AppError`, so the check returns **false**.
6. `fmt.Println(e)` — fmt detects the interface is non-nil, calls `e.Error()`. The receiver `e` is a nil pointer; `e.code` would panic… but `fmt` internally checks for nil pointer values before calling methods and safely prints `<nil>`.

**Output:**
```
false
<nil>
```

**The Trap:** People expect `process(42)` to return a nil error because no error was created. The assumption is "nil pointer → nil error". But an interface is not just a pointer — it is a `(type, data)` pair. As soon as a typed pointer is assigned and returned, the type field is set. That is enough to make the interface non-nil.

**Interview Signal:** Tests whether you understand the two-field internal structure of interface values, not just their surface behavior.

**Key Rule to Remember:** Rule: typed nil → non-nil interface. A nil `*T` returned as `error` makes `err != nil` **true**. Always return literal `nil` to produce a truly nil interface.

---

---

## Exercise 2 · `[Output]` — Method set and interface satisfaction

**Context:** Method set rules are one of the most common sources of compile errors for developers coming from other OO languages. This is exactly the kind of subtle code an interviewer shows you and says "does this compile?"

```go
package main

import "fmt"

type Speaker interface {
    Speak() string
}

type Cat struct{ name string }

func (c *Cat) Speak() string { return c.name + " says meow" }

func announce(s Speaker) {
    fmt.Println(s.Speak())
}

func main() {
    kitty := Cat{name: "Kitty"}
    announce(kitty)
}
```

**Does this compile? If yes, what does it print? If no, what is the error?**

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `Speak()` is declared with a pointer receiver `*Cat`.
2. Go's method set rules: the method set of `Cat` (value type) contains only value-receiver methods. The method set of `*Cat` contains both value-receiver and pointer-receiver methods.
3. `Speaker` requires a `Speak()` method. Only `*Cat`'s method set includes `Speak()`. `Cat`'s method set does not.
4. `announce(kitty)` tries to assign a `Cat` value to a `Speaker` interface. Compiler checks `Cat`'s method set — `Speak()` is not there.
5. **Compile error.**

**Error message:**
```
cannot use kitty (variable of type Cat) as type Speaker in argument to announce:
    Cat does not implement Speaker (Speak method has pointer receiver)
```

**The Fix:**
```go
announce(&kitty)   // pass pointer — *Cat satisfies Speaker
```

**The Trap:** Go lets you call pointer-receiver methods on addressable values as a convenience: `kitty.Speak()` compiles fine as a direct call (compiler automatically takes address). But that syntactic convenience does **not** extend to interface assignment. Interface satisfaction is checked strictly against method sets, not call-site rewrites.

**Interview Signal:** Tests whether you know the difference between "can call a method" and "satisfies an interface" — they are related but not the same rule.

**Key Rule to Remember:** Rule: method set of `T` ≠ method set of `*T`. Only `*T` includes pointer-receiver methods. Interface satisfaction uses method sets — no automatic address-taking.

---

---

## Exercise 3 · `[BugHunt]` — Typed nil always returned

**Context:** This is real code review feedback that appears constantly in Go teams. The function looks correct — it only sets an error under a specific condition — but there is a subtle bug in every return path.

```go
package main

import "fmt"

type ValidationError struct {
    field string
}

func (e *ValidationError) Error() string {
    return "validation failed for: " + e.field
}

func validate(username string) error {
    var err *ValidationError
    if len(username) < 3 {
        err = &ValidationError{field: "username"}
    }
    return err
}

func main() {
    err := validate("Gopher123")
    if err != nil {
        fmt.Println("validation failed:", err)
        return
    }
    fmt.Println("validation passed")
}
```

**Find the bug.** `validate("Gopher123")` passes a valid username (9 chars, above the 3-char minimum). The program should print `validation passed`. It does not.

What is wrong, and where exactly?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `var err *ValidationError` declares a typed nil: type is `*ValidationError`, data is nil.
2. `"Gopher123"` is 9 characters, so `len(username) < 3` is false. The `if` block is skipped.
3. `return err` returns the typed nil `(*ValidationError)(nil)` wrapped inside an `error` interface.
4. The interface value: `type = *ValidationError`, `data = nil`. Both fields are stored.
5. In `main`, `err != nil` evaluates the interface. The type field is set (`*ValidationError`) — interface is **non-nil**. Condition is true.
6. Program prints `validation failed: <nil>` and returns — wrong behavior.

**The Bug:** Line `return err` — always returns a typed nil via a `*ValidationError` variable, which becomes a non-nil `error` interface regardless of whether an actual error occurred.

**The Fix:**
```go
func validate(username string) error {
    if len(username) < 3 {
        return &ValidationError{field: "username"}
    }
    return nil  // explicit nil interface — both type and data unset
}
```

**Why the fix works:** `return nil` when the return type is `error` sets both interface fields to nil simultaneously. The interface is now truly nil. `err != nil` is false.

**Interview Signal:** Tests whether you understand that `var err *T; return err` and `return nil` are not equivalent when the return type is an interface. This is a design discipline point, not just a gotcha.

**Key Rule to Remember:** Rule: `return (typed nil pointer)` ≠ `return nil` for interface return types. Always return literal `nil` on the non-error path.

---

---

## Exercise 4 · `[Output]` — Value receiver mutation through interface

**Context:** This is a subtler trap that combines interface boxing with value receiver semantics. It appears in code reviews when developers try to make a concrete type implement a stateful interface using value receivers.

```go
package main

import "fmt"

type Mover interface {
    Move()
    Position() int
}

type Robot struct{ pos int }

func (r Robot) Move()         { r.pos++ }
func (r Robot) Position() int { return r.pos }

func main() {
    var m Mover = Robot{pos: 0}
    m.Move()
    m.Move()
    m.Move()
    fmt.Println(m.Position())
}
```

**What does this print?**

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**What's Happening:**
1. `Robot{pos: 0}` is stored in the `Mover` interface. The interface holds a **copy** of the Robot value in its data field.
2. `m.Move()` is called. Since `Move()` has a value receiver, Go passes a **copy** of the Robot stored in the interface to the method. `r.pos++` increments the copy's pos field from 0 to 1.
3. The copy is discarded when `Move()` returns. The Robot stored inside the interface is unchanged.
4. This happens identically for the second and third `m.Move()` calls.
5. `m.Position()` is called. It receives another copy of the interface's Robot (still `pos = 0`). Returns 0.

**Output:**
```
0
```

**The Trap:** People expect `pos` to be 3 after three `Move()` calls. The confusion comes from conflating "calling a method changes the receiver" with "value receivers can mutate stored state". Value receivers always operate on a copy — mutation to that copy does not propagate back to the original value held in the interface.

**The Fix:** Use pointer receivers for methods that mutate state, and store `*Robot`:
```go
func (r *Robot) Move()         { r.pos++ }   // pointer receiver — mutates original
func (r *Robot) Position() int { return r.pos }

func main() {
    var m Mover = &Robot{pos: 0}  // store pointer, not value
    m.Move()
    m.Move()
    m.Move()
    fmt.Println(m.Position())     // prints 3
}
```

**Interview Signal:** Tests whether you understand that interface boxing copies the value, and that value receivers work on copies of that copy — making them unsuitable for stateful interfaces.

**Key Rule to Remember:** Rule: value receiver on interface-boxed value → method operates on a copy of a copy. Mutations are silently lost. Stateful interfaces need pointer receivers and pointer storage.

---

---

## Exercise 5 · `[FixIt]` — Fix the typed-nil returning function

**Context:** A production query helper was written quickly. Code review flagged it. Fix it so that `RunQuery` returns a non-nil error only when `code != 0`, and callers can rely on the standard `err != nil` check.

```go
package main

import "fmt"

type DBError struct {
    query string
    code  int
}

func (e *DBError) Error() string {
    return fmt.Sprintf("query %q failed with code %d", e.query, e.code)
}

func RunQuery(q string, code int) error {
    var err *DBError
    if code != 0 {
        err = &DBError{query: q, code: code}
    }
    return err
}

func main() {
    err := RunQuery("SELECT 1", 0)
    if err != nil {
        fmt.Println("BUG — unexpected error:", err)
        return
    }
    fmt.Println("query succeeded")
}
```

**Fix `RunQuery` so the program prints `query succeeded`.**

Also answer: what would happen if you only changed `var err *DBError` to `var err error` — would that fix it?

Take your time. Write your answer, then read the walkthrough below.

---

### Solution Walkthrough

**The Fix:**
```go
func RunQuery(q string, code int) error {
    if code != 0 {
        return &DBError{query: q, code: code}
    }
    return nil
}
```

**What's Happening (original bug):**
1. `var err *DBError` declares a typed nil.
2. `code` is 0, so `if` is skipped.
3. `return err` returns `(*DBError)(nil)` as an `error` interface. Type field is `*DBError` — non-nil interface.
4. Caller's `err != nil` is true. Bug fires.

**Answer to the `var err error` question:** Yes — changing the declaration to `var err error` would fix it. `error` is an interface type. `var err error` initializes both type and data fields to nil. `return err` at the end returns a truly nil interface. However, this is a fragile fix — it relies on the variable type suppressing boxing. The cleaner and more explicit fix is `return nil`.

```go
// This also works, but the explicit return nil version is more readable:
func RunQuery(q string, code int) error {
    var err error              // interface nil — both fields unset
    if code != 0 {
        err = &DBError{query: q, code: code}
    }
    return err
}
```

**The Trap:** Developers learn "nil means no error" and assume any nil-like variable satisfies that. The distinction is: nil interface vs nil pointer stored inside a non-nil interface. These look the same when you write `return err`, but are fundamentally different at the interface level.

**Interview Signal:** Tests whether you can not only spot the bug but also reason about multiple fix strategies and their trade-offs — a strong signal of genuine understanding vs surface familiarity.

**Key Rule to Remember:** Rule: the safest fix for typed-nil returns is always `return nil` on the non-error path. Do not rely on variable type tricks. Explicit `return nil` is zero-ambiguity.

---

---

## Quick Reference — Rules from This Exercise Set

| Rule | Source Exercise |
|------|----------------|
| Typed nil `*T` returned as `error` → `err != nil` is **true** | Ex 1, 3, 5 |
| `return nil` (literal) is the only safe way to return a nil error interface | Ex 3, 5 |
| Method set of `T` does not include pointer-receiver methods | Ex 2 |
| Interface satisfaction uses method sets — compiler does NOT auto-take address | Ex 2 |
| Value receiver on interface-boxed value → operates on a copy — mutations lost | Ex 4 |
| Stateful interface implementations require pointer receivers + pointer storage | Ex 4 |
