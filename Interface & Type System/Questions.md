# Phase 2 — Interface & Type System · Interview Questions

**Target level:** 3-4 YOE Go developer
**Total questions:** 10

> Research sources: Go FAQ (`nil_error`, interface/type sections), Go Wiki MethodSets, and Go Blog (Laws of Reflection).

---

## Question Distribution

| Category | Questions |
| -------- | --------- |
| Runtime & Internals | Q1, Q2, Q4, Q7 |
| Language Gotchas | Q3, Q5, Q8 |
| Applied / Design | Q6, Q9 |
| Live Coding | Q10 |

---

## Q1 · What is the runtime shape of an interface value? `[Internals]`

**The question an interviewer actually asks:**
> "When I assign a concrete value into an interface, what exactly is stored there? I want memory-level intuition, not just 'it stores a value'."

---

**HOOK:** An interface value stores two things: the concrete type metadata and the concrete value data (or pointer to it). In short, interface = `(type, data)`.

**INTERNALS:** Go runtime keeps interface values as a pair. The type part identifies the dynamic type currently inside the interface; the data part points to or carries the concrete payload. This is why a single interface variable can hold different concrete types over time but still dispatch methods correctly. Dynamic dispatch reads the type metadata, finds the method entry, then calls it with the data pointer.

**REAL-WORLD:** This model explains many Go surprises in production: nil-interface bugs, assertion behavior, and why interface calls have a tiny indirection cost. Teams that understand `(type, data)` debug these issues much faster.

**INSIGHT:** If you remember only one sentence: an interface is not the value itself; it is a typed box around that value.

---

⚠️ **Weak answer sounds like:** "Interface just stores the value and figures out methods magically."
That misses the type metadata half, which is the whole reason dynamic dispatch works.

💬 **Likely follow-up:** "How is this different for `any` versus a non-empty interface like `io.Reader`?"

---

## Q2 · What is the difference between `iface` and `eface`? `[Internals]`

**The question an interviewer actually asks:**
> "People say `interface{}` and typed interfaces are represented differently. What is the real difference and why does it matter?"

---

**HOOK:** `eface` is the runtime form of empty interface (`any`), while `iface` is the runtime form of a non-empty interface. `iface` needs method-related metadata; `eface` does not.

**INTERNALS:** `any` has no methods, so runtime only needs dynamic type + data. A non-empty interface like `io.Reader` must support method dispatch, so runtime includes metadata to map interface methods to concrete implementations. Both still conceptually carry `(type, data)`, but `iface` has extra method resolution context.

**REAL-WORLD:** This matters when profiling abstraction-heavy code. Repeated interface method calls in hot loops can add indirect call overhead, while plain `any` storage with type assertions shifts cost to assertion sites instead.

**INSIGHT:** Empty interface is "store anything"; non-empty interface is "store anything that can do these methods."

---

⚠️ **Weak answer sounds like:** "No difference, all interfaces are exactly the same."
At interview depth, that answer is too shallow.

💬 **Likely follow-up:** "If `any` is so flexible, why not use it everywhere?"

---

## Q3 · Why can `err != nil` be true when the underlying pointer is nil? `[Gotcha]`

**The question an interviewer actually asks:**
> "Show me the classic typed-nil error bug and explain why it happens under the hood."

---

**HOOK:** Because `error` is an interface. If it contains a typed nil pointer, its type field is non-nil, so the interface itself is non-nil.

**INTERNALS:** A nil interface is only nil when both type and data are unset. If you return `(*MyError)(nil)` as `error`, runtime stores `(type=*MyError, data=nil)`. Comparison with `nil` fails because type is set. Correct fix is to return literal `nil` when there is no error.

**REAL-WORLD:** This bug causes false failures in retries, metrics, and alerting pipelines because callers think an error occurred. It is one of the most common Go review comments in production code.

**INSIGHT:** Nil-ness of interface values is a two-field check, not a one-field check.

---

⚠️ **Weak answer sounds like:** "Pointer is nil, so `err` should be nil."
That ignores interface representation.

💬 **Likely follow-up:** "What is the safest function signature pattern to avoid this bug?"

---

## Q4 · What does dynamic dispatch through an interface do at call time? `[Internals]`

**The question an interviewer actually asks:**
> "When I call `s.Speak()` and `s` is an interface, what sequence happens at runtime?"

---

**HOOK:** Runtime uses interface type metadata to find the concrete method, then calls that method with the interface's data payload.

**INTERNALS:** A direct call on a concrete type is resolved statically by the compiler. An interface call is resolved dynamically: read interface metadata, locate method function pointer for the dynamic type, perform indirect call. That extra indirection is the core dispatch cost.

**REAL-WORLD:** In most business code this overhead is tiny and worth the abstraction. In tight loops or serialization hot paths, replacing interface dispatch with concrete/generic code can show measurable gains.

**INSIGHT:** Interface dispatch trades a small constant-time runtime cost for large design flexibility.

---

⚠️ **Weak answer sounds like:** "It just calls the method directly like normal."
No, interface calls are indirect by design.

💬 **Likely follow-up:** "How would you benchmark whether this overhead matters in your code?"

---

## Q5 · Why does `T` sometimes fail to satisfy an interface while `*T` succeeds? `[Gotcha]`

**The question an interviewer actually asks:**
> "Why does this compile with `&v` but fail with `v` for the same interface?"

---

**HOOK:** Method sets differ. `T` has only value-receiver methods; `*T` has both pointer and value-receiver methods.

**INTERNALS:** Interface satisfaction is checked against method sets, not call-site convenience rewrites. You may call a pointer-receiver method on an addressable value in some contexts, but that does not mean `T`'s method set includes pointer methods. For interface assignment, compiler enforces exact method-set rules.

**REAL-WORLD:** This shows up in constructors, mocks, and dependency injection code. Teams often expose interfaces expecting `T`, but only `*T` satisfies due to one pointer receiver, causing surprising compile failures.

**INSIGHT:** "Can call" and "implements interface" are related but not identical concepts in Go.

---

⚠️ **Weak answer sounds like:** "Go randomly wants pointers sometimes."
The rule is deterministic and method-set based.

💬 **Likely follow-up:** "What happens for map elements and interface-held values, which are not addressable?"

---

## Q6 · When would you choose interface-based polymorphism vs generics in Go 1.18+? `[Design]`

**The question an interviewer actually asks:**
> "Post-generics, when should I still use interfaces, and when should I switch to type parameters?"

---

**HOOK:** Use interfaces for behavior polymorphism; use generics for type-safe algorithms/data structures over multiple concrete types.

**INTERNALS:** Interfaces model "what operations can this value do" and resolve behavior at runtime. Generics model "same algorithm for many types" and resolve type substitution at compile time (with compiler strategy choices per instantiation shape). They solve different design problems.

**REAL-WORLD:** Example split that works well in large services: use generics for reusable containers/helpers (`Min`, sets, typed utilities), and interfaces at boundaries (`io.Reader`, repositories, plugin behavior). Mixing both is common and healthy.

**INSIGHT:** Generics reduce `any` + assertion noise; interfaces preserve clean boundaries and decoupling.

---

⚠️ **Weak answer sounds like:** "Generics replaced interfaces."
They did not; they complement interfaces.

💬 **Likely follow-up:** "Give one API you would redesign from `any` to generics and why."

---

## Q7 · What is the cost model of type assertion `v, ok := x.(T)`? `[Internals]`

**The question an interviewer actually asks:**
> "Are type assertions expensive? Should I avoid them in normal code?"

---

**HOOK:** A normal type assertion is usually a quick runtime type check, not heavy reflection.

**INTERNALS:** Runtime compares the dynamic type metadata inside the interface against target type `T`. On match, it unwraps value and sets `ok=true`; otherwise `ok=false` (or panic in single-result form). Cost is typically small and constant-time for direct assertions.

**REAL-WORLD:** Assertions are fine in boundary code (decode paths, plugin adapters, generic containers using `any`). The bigger risk is readability and panic behavior, not raw CPU cost.

**INSIGHT:** Prefer two-result assertions in production paths unless panic is truly intended.

---

⚠️ **Weak answer sounds like:** "Assertions are always slow and should be avoided."
That is usually incorrect.

💬 **Likely follow-up:** "When should you use a type switch instead of repeated assertions?"

---

## Q8 · Why can't `[]T` be directly converted to `[]any`? `[Gotcha]`

**The question an interviewer actually asks:**
> "`T` can go into `any`, so why can't `[]T` go into `[]any` in one cast?"

---

**HOOK:** Because their memory layouts differ. A slice of concrete values is not represented like a slice of interface values.

**INTERNALS:** `[]T` stores contiguous elements of `T`. `[]any` stores contiguous interface values, and each interface element has its own `(type, data)` representation. So element representations are incompatible. Go forbids zero-copy conversion to avoid broken memory semantics.

**REAL-WORLD:** You must allocate `dst := make([]any, len(src))` and copy element-by-element. This cost is easy to miss in logging, formatting, and adapter layers.

**INSIGHT:** "Each element is assignable" does not imply "the slice representations are assignable."

---

⚠️ **Weak answer sounds like:** "Compiler limitation; maybe fixed later."
This is a design-level representation rule, not a temporary limitation.

💬 **Likely follow-up:** "How would you write a helper to convert `[]T` to `[]any` safely?"

---

## Q9 · `make` vs `new` vs composite literals: when is each idiomatic? `[Applied]`

**The question an interviewer actually asks:**
> "Give practical rules for when to use each construction style in real code."

---

**HOOK:** `new` allocates zeroed `*T`; `make` initializes slices/maps/channels; composite literals directly construct values.

**INTERNALS:** `make` is special because slices, maps, and channels need runtime headers/internal state to become usable. `new(T)` only gives pointer to zero value, with no map/channel initialization semantics. Literals are most expressive when you already know field/element values.

**REAL-WORLD:** Idiomatic Go often favors literals for structs and `make` with capacity hints for performance-sensitive collections. Overusing `new` for structs can reduce readability when plain `T{}` is sufficient.

**INSIGHT:** Choose by semantic intent, not habit: initialize runtime-backed types with `make`, data values with literals, pointer-to-zero with `new`.

---

⚠️ **Weak answer sounds like:** "`new` and `make` are basically the same."
They are not interchangeable.

💬 **Likely follow-up:** "When does `make(map[K]V, n)` actually help performance?"

---

## Q10 · Live Coding — typed-nil error classification helper `[Coding]`

**The question an interviewer actually asks:**
> "Write a helper that takes `error` and reports: (1) truly nil interface, (2) typed nil inside interface, (3) real non-nil error value."

---

**Reference solution:**

```go
package main

import (
    "fmt"
    "reflect"
)

func classifyError(err error) string {
    if err == nil {
        return "nil interface error"
    }

    v := reflect.ValueOf(err)
    switch v.Kind() {
    case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
        if v.IsNil() {
            return "typed nil inside non-nil interface"
        }
    }

    return "real non-nil error"
}

type MyErr struct{}

func (e *MyErr) Error() string { return "boom" }

func bad() error {
    var e *MyErr = nil
    return e // typed nil in error interface
}

func main() {
    fmt.Println(classifyError(nil))
    fmt.Println(classifyError(bad()))
}
```

---

**HOOK:** First check `err == nil`; then inspect whether dynamic value is nil-capable and nil via reflection.

**INTERNALS:** `err == nil` only checks whether interface has both fields unset. For typed-nil payloads, interface is non-nil, so second-stage inspection is required if you need explicit classification.

**REAL-WORLD:** This pattern is useful in debugging tooling and logs, but in normal app logic the best practice is preventing typed-nil returns at the source.

**INSIGHT:** Fix producer-side error construction; consumer-side reflection should be exceptional.

---

⚠️ **Weak answer sounds like:** "If `err != nil`, it is always a real error object."
Not always true with typed nil values.

💬 **Likely follow-up:** "How would you redesign `bad()` so callers never see this ambiguity?"

---

## Quick Summary Card

| Q | Topic | Tag | Core takeaway |
| -- | ----- | --- | ------------- |
| 1 | Interface runtime shape | `[Internals]` | Interface value is `(type, data)` pair. |
| 2 | `iface` vs `eface` | `[Internals]` | Non-empty interfaces carry method-resolution metadata. |
| 3 | Nil interface bug | `[Gotcha]` | `(type=*T, data=nil)` is non-nil interface. |
| 4 | Dynamic dispatch | `[Internals]` | Interface calls are indirect runtime dispatch. |
| 5 | Method set mismatch | `[Gotcha]` | `T` and `*T` implement different interface sets. |
| 6 | Interfaces vs generics | `[Design]` | Behavior abstraction vs algorithm reuse. |
| 7 | Assertion cost | `[Internals]` | Usually a small runtime type check. |
| 8 | `[]T` to `[]any` | `[Gotcha]` | Slice element representations differ, so no direct cast. |
| 9 | `make` / `new` / literal | `[Applied]` | Use each based on semantic intent and type category. |
| 10 | Typed-nil detection | `[Coding]` | Distinguish nil interface from typed nil payload safely. |
