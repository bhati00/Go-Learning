# Phase 1 — Language Data Structures · Interview Questions

**Target level:** 3-4 YOE Go developer
**Total questions:** 10

> Research sources: Go official blog (slices, maps internals), Ardan Labs Go training,
> real interview reports from r/golang, Glassdoor, and Blind (2024–2025).
> Questions are synthesised from what senior engineers actually ask — not textbook definitions.

---

## Question Distribution

| Category | Questions |
|----------|-----------|
| Runtime & Internals | Q1, Q3, Q6, Q7 |
| Language Gotchas | Q2, Q4, Q8, Q9 |
| Applied / Performance | Q5 |
| Live Coding | Q10 |

---

## Q1 · What does a slice header look like in memory? `[Internals]`

**The question an interviewer actually asks:**
> "I pass a `[]int` to a function. The function modifies `s[0]`. Back in the caller, is `s[0]` changed? What if the function appends to `s`? Are those two cases different, and why?"

---

> **HOOK:** A slice is a 3-field struct — `ptr`, `len`, `cap`. Passing a slice copies
> the header (24 bytes on 64-bit), not the backing array. So element mutations through
> the copy are visible to the caller, but appends that exceed capacity are not.

> **INTERNALS:** The runtime represents a slice as:
> ```
> type SliceHeader struct {
>     Data uintptr // pointer to backing array element 0
>     Len  int     // number of accessible elements
>     Cap  int     // elements available from Data to end of backing array
> }
> ```
> When you call `f(s)`, Go copies the 24-byte header onto the callee's stack. Both
> caller and callee now have headers with the same `Data` pointer — they point at the
> same backing array. A write to `s[0]` goes through `Data` and is immediately visible
> to both. But if the callee calls `append` and it exceeds `cap`, the callee gets a
> brand-new backing array — its `Data` pointer changes. The caller's header still
> points at the old array. No mutation is visible.

> **REAL-WORLD:** This is a constant source of bugs in code that passes a slice to a
> helper function, appends inside, and then assumes the caller sees the result.
> I've seen production services where `processItems(items)` was supposed to accumulate
> results but the caller's `items` was always empty after the call. The fix is always
> one of two things: return the slice, or pass a pointer to the slice.

> **INSIGHT:** A slice is not a reference type — it is a *value type that contains a
> pointer*. These are not the same. The pointer part shares data; the header itself does not.

---

⚠️ **Weak answer sounds like:** "Slices are passed by reference in Go."
That answer shows a fundamental misunderstanding. Slices are passed by value — a copy
of the header. The confusion arises because the header contains a pointer, not because
the slice itself is a reference.

💬 **Likely follow-up:** "What is the 3-index slice expression `s[1:3:3]` and when would you use it?"

---

## Q2 · The Backing Array Sharing Gotcha `[Gotcha]`

**The question an interviewer actually asks:**
> "Given this code, what does it print? Explain what's happening."
> ```go
> s := make([]int, 3, 5)
> s[0], s[1], s[2] = 1, 2, 3
>
> t := append(s, 4)
> t[0] = 999
>
> fmt.Println(s[0]) // ?
> fmt.Println(t[0]) // ?
>
> u := append(t, 5, 6)
> u[0] = 111
>
> fmt.Println(t[0]) // ?
> fmt.Println(u[0]) // ?
> ```

---

> **HOOK:** `s[0]` is `999`. The first append stayed within the original capacity (5),
> so `s` and `t` share the same backing array. `t[0] = 999` mutated shared memory.
> The second block: `t[0]` is still `999`. `u[0]` is `111`. The second append exceeded
> capacity, so `u` got a new backing array — `t` is unaffected.

> **INTERNALS:** `make([]int, 3, 5)` creates a backing array of 5 ints. After
> `append(s, 4)`, `t.len = 4`, `t.cap = 5`, but `t.ptr == s.ptr` — both headers point
> at the same array. A write through either header writes to the same memory.
> Once `append(t, 5, 6)` needs 6 slots but only has 5, the runtime allocates a new
> array, copies `t`'s 4 elements, and returns a new header. Now `u.ptr ≠ t.ptr`.

> **REAL-WORLD:** This bit me hardest in a data pipeline where we pre-allocated a
> large shared buffer with `make([]Row, 0, 1000)` and then distributed sub-slices to
> workers to fill in. Workers appended within capacity and silently clobbered each
> other's data. The fix was the 3-index slice: `worker_slice := shared[lo:hi:hi]` —
> capping cap to `hi` forces the next append to allocate independently.

> **INSIGHT:** Two slices are independent only if they have different backing arrays.
> The only time that's guaranteed is when an append exceeded capacity, or when you
> used `copy` explicitly.

---

⚠️ **Weak answer sounds like:** "Append always creates a new slice, so `s` and `t` are independent."
This is wrong on two levels: append doesn't always create a new backing array, and
"new slice" doesn't mean "new backing array."

💬 **Likely follow-up:** "How does the 3-index slice expression prevent this sharing problem?"

---

## Q3 · What changed about slice growth in Go 1.18, and why? `[Internals]`

**The question an interviewer actually asks:**
> "You've read somewhere that Go slices always double in capacity. Is that accurate?
> What actually happens when `append` needs more space?"

---

> **HOOK:** The "always doubles" rule is wrong even for pre-1.18 Go, and was changed
> in 1.18 to a smooth curve. The runtime targets a growth rate that decreases as the
> slice gets larger — fast growth for small slices, slower growth for large ones.
> The exact new capacity is further rounded to the nearest memory class size.

> **INTERNALS:** Before 1.18: cap < 1024 → ~2×; cap ≥ 1024 → ~1.25×. The "1024" cliff
> was a hard threshold that produced a sudden step-change in growth behaviour.
> Since 1.18, the formula blends the two regimes continuously. There's no single
> cutoff. Additionally, the computed new capacity is rounded *up* to the nearest
> allocator size class (e.g., 48, 64, 80, 96... bytes) so the actual `cap` you observe
> is often larger than what any formula predicts. This is intentional — it avoids
> leaving tiny unusable gaps in the allocator's memory classes.

> **REAL-WORLD:** This matters when you're benchmarking code that depends on allocation
> counts. A benchmark written on Go 1.17 that expected exactly N capacity doublings
> could fail or misreport on 1.18 because the growth steps changed. It also means
> you should never hard-code growth assumptions in data structure implementations.

> **INSIGHT:** The growth factor is an implementation detail, not a language contract.
> If you need control over allocation behaviour, use `make([]T, 0, estimatedSize)` to
> pre-allocate the backing array. One allocation at the start is always better than N
> amortised ones.

---

⚠️ **Weak answer sounds like:** "Slices double in size when they run out of capacity."
A 3-4 YOE engineer should know this was approximately true for small slices before 1.18,
never a guarantee, and has changed.

💬 **Likely follow-up:** "How would you pre-size a slice to avoid reallocation when you know the approximate final size?"

---

## Q4 · Why does `len("héllo")` return 6, not 5? `[Gotcha]`

**The question an interviewer actually asks:**
> "A colleague reports a bug: their string truncation code cuts multibyte characters
> in half and produces garbage output. What's the likely root cause? How do you explain
> it and fix it?"

---

> **HOOK:** `len(s)` returns the number of *bytes*, not the number of *characters*.
> For ASCII strings they're the same. For UTF-8 strings with multibyte code points
> (like `é`, which is 2 bytes), `len(s)` > the visible character count. Slicing by
> byte index can land in the middle of a multibyte character.

> **INTERNALS:** A Go string is an immutable `(ptr, len)` header — no null terminator,
> no encoding metadata. The `len` field is the raw byte count. Go source files and
> string literals are UTF-8, but the string type itself has no awareness of Unicode —
> it's just bytes. When you do `s[i:j]`, you're slicing by byte offsets. If `i` or `j`
> fall inside a multibyte sequence, the resulting string is malformed UTF-8.
>
> For character-aware operations:
> - `utf8.RuneCountInString(s)` — O(n) scan, returns rune count
> - `for i, r := range s` — iterates by rune, `i` is the byte offset of each rune
> - `[]rune(s)` — converts to a rune slice (allocates, but allows index-safe character operations)

> **REAL-WORLD:** I've seen this in log truncation: `msg[:255]` to cap log line length,
> but if byte 255 is in the middle of a Japanese character, the log system rejects
> the line as invalid UTF-8. The fix is always to truncate by rune, not byte:
> ```go
> func truncateRunes(s string, n int) string {
>     runes := []rune(s)
>     if len(runes) > n {
>         return string(runes[:n])
>     }
>     return s
> }
> ```

> **INSIGHT:** In Go, "string" does not mean "text". It means "immutable byte sequence
> that is conventionally UTF-8 encoded". The `rune` type (alias for `int32`) is the
> unit of Unicode text.

---

⚠️ **Weak answer sounds like:** "`len` counts characters."
This is the single most common string misconception in Go. A 3-4 YOE engineer must know this cold.

💬 **Likely follow-up:** "What's the difference between iterating `for i := 0; i < len(s); i++` vs `for i, r := range s`?"

---

## Q5 · Why is `result += word` in a loop O(n²)? What's the fix? `[Applied]`

**The question an interviewer actually asks:**
> "We have a service that builds a large JSON string by concatenating ~5000 small
> fragments in a loop. It's slow and allocating a lot. What's wrong and how do you fix it?"

---

> **HOOK:** String concatenation with `+` in a loop is O(n²) because strings are
> immutable — every `+=` allocates a new string, copies everything accumulated so far,
> then appends the new piece. After n iterations, total bytes copied = 1+2+3+...+n = O(n²).
> The fix is `strings.Builder`, which writes into an internal `[]byte` buffer and
> allocates once at the end.

> **INTERNALS:** Each `result += word` desugars to:
> 1. Allocate a new backing array of len(result) + len(word) bytes
> 2. Copy `result`'s bytes into the new array
> 3. Copy `word`'s bytes after them
> 4. Build a new string header pointing at the new array
> 5. Let the old `result` backing array become unreachable (GC pressure)
>
> `strings.Builder` sidesteps this by using a `[]byte` internally. Appends use Go's
> slice growth strategy — amortised O(1) per append. The `String()` method performs
> exactly one allocation at the end. If you know the approximate total size, call
> `b.Grow(n)` beforehand to reduce the number of internal re-growths to zero.

> **REAL-WORLD:** I've seen a templating function that built HTML by doing
> `output += "<tag>" + content + "</tag>"` in a loop over 300 items. It was 80ms
> per request. Replacing it with a `strings.Builder` dropped it to under 2ms. Same
> for `fmt.Sprintf` in a hot path — it uses reflection and allocates a new string every call.
> In tight loops, `b.WriteString()` is always the right choice.

> **INSIGHT:** The rule is: if you're building a string *incrementally* (loop, recursion,
> unknown number of pieces), use `strings.Builder`. If you have a *fixed set* of pieces
> you know at compile time, `+` is fine — the compiler can collapse them.

---

⚠️ **Weak answer sounds like:** "Use `fmt.Sprintf` to build the string."
`fmt.Sprintf` is slower than `+` for simple cases and far slower than `strings.Builder`
for loops. It invokes the reflection machinery on every call.

💬 **Likely follow-up:** "Is `[]byte(s)` a free conversion because strings are just byte slices internally?"

---

## Q6 · What is the internal structure of a Go map? What is `tophash`? `[Internals]`

**The question an interviewer actually asks:**
> "Walk me through what happens at the memory level when I do `m["foo"]`. Don't just
> say 'O(1) lookup' — walk through the actual steps."

---

> **HOOK:** A Go map is an array of *buckets*, each holding up to 8 key-value pairs.
> The key is hashed — the low bits select the bucket, the top 8 bits are stored as a
> `tophash` entry for a fast pre-check before doing a full key comparison.

> **INTERNALS:** For `m["foo"]`:
> 1. `hash("foo")` → say `0xAB...34`
> 2. Low bits `& (numBuckets - 1)` → select bucket index (e.g., bucket 4)
> 3. Top 8 bits `0xAB` → scan bucket 4's 8 `tophash` bytes for `0xAB`
> 4. On each `tophash` match → full key comparison (`key[i] == "foo"`)
> 5. On key match → return `value[i]`
> 6. If no match in 8 slots → follow the overflow bucket pointer
>
> Go stores all 8 keys together and all 8 values together in each bucket (not
> interleaved). This is cache-friendly: scanning 8 `tophash` bytes is a hot-cache
> operation. Full key comparisons — which may involve string comparisons — only
> happen after a `tophash` hit. In the common case ("key not in map"), this means
> zero full key comparisons.

> **REAL-WORLD:** Understanding the bucket structure explains several observable
> behaviours: why maps consume noticeably more memory per entry than a slice (bucket
> overhead + overflow buckets), why map iteration order is non-deterministic (random
> start bucket), and why concurrent access causes a panic rather than silent
> corruption (the `hashWriting` flag is set at the bucket level before any writes).

> **INSIGHT:** The `tophash` fast-scan is the key optimisation. Without it, every
> lookup would require a full key comparison for every key in the bucket. With it,
> most "not present" checks complete after scanning 8 bytes.

---

⚠️ **Weak answer sounds like:** "Maps are O(1) because they hash the key to find the value."
That's not wrong, but it shows no understanding of the implementation. At 3-4 YOE,
you're expected to know what's inside the hash table, not just that it exists.

💬 **Likely follow-up:** "What happens when a bucket fills up all 8 slots? How does that affect performance?"

---

## Q7 · How does Go map growth work? How is it different from slice growth? `[Internals]`

**The question an interviewer actually asks:**
> "I have a map that starts small and grows to a million entries during a request.
> Does Go ever block the entire operation to do a full rehash? How does map growth
> differ from what happens when a slice's capacity is exceeded?"

---

> **HOOK:** Go map growth is **incremental** — only 2 old buckets are evacuated per
> write operation. The old bucket array stays alive in parallel until all buckets are
> migrated. This is the opposite of slice growth, which does a full O(n) copy
> immediately when capacity is exceeded.

> **INTERNALS:** When the load factor exceeds ~6.5 items per bucket (or when too many
> overflow buckets accumulate):
> 1. A new bucket array is allocated at 2× the size. This is immediate.
> 2. Each subsequent map **write** evacuates exactly 2 buckets from old → new.
> 3. Each map **read** checks both arrays: if the target bucket hasn't been evacuated
>    yet, it reads from the old array transparently.
> 4. After all buckets are evacuated, the old array is released to the GC.
>
> This means during growth, the map uses roughly 3× its normal memory (old array +
> new array + the data). Growth is never "stop the world" — each write pays a small
> O(1) migration cost.
>
> Contrast with slices: when `append` exceeds capacity, Go allocates the new backing
> array and copies *all* elements immediately. That's one O(n) pause per capacity event.
> For a slice of a million elements, that's one expensive copy. Slices trade latency
> for simplicity; maps trade memory (3× temporary) for latency smoothness.

> **REAL-WORLD:** The 3× memory usage during growth can matter for memory-constrained
> services. I've seen a service that loaded a large lookup map at startup hit its
> container memory limit during the growth phase — not during steady state. The fix
> is to pre-size the map: `make(map[K]V, expectedSize)`. This sets the initial bucket
> count to avoid growth during load entirely.

> **INSIGHT:** Pre-sizing both slices and maps is the single highest-leverage
> performance optimisation in Go data structure code. `make([]T, 0, n)` and
> `make(map[K]V, n)` both eliminate all growth allocations when `n` is a good estimate.

---

⚠️ **Weak answer sounds like:** "Map growth works the same as slice growth — it copies everything."
This is wrong. Knowing the difference is what separates someone who reads code from
someone who understands the runtime.

💬 **Likely follow-up:** "If you know a map will hold about 10,000 entries, how do you initialise it to avoid growth overhead?"

---

## Q8 · Why is map iteration order non-deterministic, even within a single run? `[Gotcha]`

**The question an interviewer actually asks:**
> "A colleague wrote a test that passes locally but is flaky in CI. The test iterates a
> map and compares the output to a hardcoded expected string. Why is this flaky?
> Is map iteration order just 'undefined' or is it actively randomised?"

---

> **HOOK:** Map iteration is **actively randomised** — not just undefined. On every
> `range` loop, the runtime picks a random starting bucket and a random offset within
> that bucket. Two consecutive `range` loops over the same unchanged map in the same
> program run can produce different orderings.

> **INTERNALS:** The deliberate randomisation was introduced in Go 1 as a breaking
> change. Before Go 1, the iteration order happened to be stable on a given binary
> because it followed the bucket array layout. Developers relied on it. When the
> map implementation changed, programs broke silently in production.
>
> The Go team's response was to randomise the starting point explicitly using the
> runtime's fast random number generator — ensuring no program can depend on order
> even accidentally. The start bucket index and the start slot within that bucket are
> both randomised.
>
> If ordered iteration is required, the canonical pattern is:
> ```go
> keys := make([]string, 0, len(m))
> for k := range m {
>     keys = append(keys, k)
> }
> sort.Strings(keys)
> for _, k := range keys {
>     fmt.Println(k, m[k])
> }
> ```

> **REAL-WORLD:** The flaky test pattern is extremely common. Beyond tests, I've seen
> this cause subtle non-determinism in configuration serialisation (the marshalled JSON
> has fields in random order), making config diffs unreadable and git history noisy.
> The fix for JSON is `encoding/json` which already sorts map keys by default when
> marshalling — but in custom serialisation code, you have to sort explicitly.

> **INSIGHT:** Go's randomisation is a correctness guardrail. It's forcing you to
> write code that's correct *regardless of order*, which is the only correct way to
> use a hash map. If your code breaks with random ordering, it had a latent bug even
> before Go randomised it.

---

⚠️ **Weak answer sounds like:** "Map iteration order is undefined, so you shouldn't rely on it."
The word "undefined" sounds like an oversight. The correct framing is "deliberately
and actively randomised on every range loop." That signals you know *why*, not just *what*.

💬 **Likely follow-up:** "Does `encoding/json` preserve insertion order for maps? What does it do instead?"

---

## Q9 · Concurrent map read+write: panic or silent corruption? Can `recover()` save you? `[Gotcha]`

**The question an interviewer actually asks:**
> "Two goroutines access a map simultaneously — one reads, one writes different keys.
> Is this safe? What happens? Can you use `recover()` to handle it gracefully?"

---

> **HOOK:** Concurrent map access with any write results in a `runtime.throw` — not a
> regular panic. The program terminates immediately. `recover()` cannot intercept
> `runtime.throw`. There is no graceful handling: the answer is to protect access
> with `sync.RWMutex` or use `sync.Map`.

> **INTERNALS:** Go's map implementation sets a `hashWriting` flag in the `hmap`
> struct at the start of every write. Every read operation checks this flag. If it's
> set, `runtime.throw("concurrent map read and map write")` is called. `throw` is not
> a regular `panic` — it bypasses the defer/recover mechanism entirely and calls
> `exit(2)`. This is by design: a map mid-write has undefined internal state.
> Continuing execution would be unsound; crashing immediately gives you a stack trace
> pointing directly at the problem.
>
> Importantly: **concurrent reads** are safe as long as there is no concurrent writer.
> It's the presence of any writer that makes concurrent access unsafe — even if the
> read and write target completely different keys.

> **REAL-WORLD:** This is one of the most common sources of production crashes in Go
> services. The pattern that causes it: a globally shared map that is populated at
> startup (writes) and then read by request handlers (reads) — but "startup" and
> "first request" can overlap. The fix:
> ```go
> // Option 1: RWMutex for fine-grained control
> var mu sync.RWMutex
> var m = map[string]int{}
>
> // Option 2: sync.Map for read-heavy, mostly-stable key sets
> var m sync.Map
> ```
> `sync.Map` is NOT always better. It's optimised for high read-to-write ratios with
> mostly stable key sets. For write-heavy workloads, `map + sync.Mutex` outperforms it.

> **INSIGHT:** Go chose a hard crash over silent corruption deliberately. A crash
> surfaces the bug at the exact moment it happens with a full stack trace. Silent
> memory corruption would let the corrupted state propagate for minutes or hours
> before any symptom appeared.

---

⚠️ **Weak answer sounds like:** "You'd get data races but the program might still work."
Wrong on both counts. It's not a data race (the race detector catches those separately).
It's a specific runtime check that hard-crashes the program with `runtime.throw`.
And `recover()` will not save you.

💬 **Likely follow-up:** "When would you choose `sync.Map` over a regular `map + sync.RWMutex`?"

---

## Q10 · Live Coding — Fix It `[Gotcha]`

**The question an interviewer actually asks:**
> "Here is a real function from a production codebase. It reads a large file, finds a
> pattern in it, and returns the matching bytes. There's a subtle memory bug.
> Can you find it and fix it?"

```go
func findPattern(filename string, pattern []byte) ([]byte, error) {
    data, err := os.ReadFile(filename)
    if err != nil {
        return nil, err
    }

    idx := bytes.Index(data, pattern)
    if idx == -1 {
        return nil, nil
    }

    // Return the matched portion
    return data[idx : idx+len(pattern)], nil
}
```

---

**What the bug is:**

The returned slice `data[idx : idx+len(pattern)]` is a sub-slice of `data`.
`data` holds the entire file content in memory. As long as the caller holds the
returned slice, **the entire file backing array cannot be garbage collected**,
even though the caller only needs a few bytes.

If this function is called often (e.g., per request in a server), you slowly
accumulate large backing arrays in memory — a classic GC leak via slice retention.

---

**The fix:**

```go
func findPattern(filename string, pattern []byte) ([]byte, error) {
    data, err := os.ReadFile(filename)
    if err != nil {
        return nil, err
    }

    idx := bytes.Index(data, pattern)
    if idx == -1 {
        return nil, nil
    }

    // Copy the match into a new, small allocation.
    // This releases the reference to the large backing array (data).
    result := make([]byte, len(pattern))
    copy(result, data[idx:idx+len(pattern)])
    return result, nil

    // Or more concisely:
    // return bytes.Clone(data[idx : idx+len(pattern)]), nil
}
```

---

**Evaluation checklist for this fix:**

| Check | What to look for |
|-------|-----------------|
| Correctness | Does it return the right bytes? Yes — `copy` copies the exact matched bytes. |
| Memory safety | Does the caller hold a reference to the large file buffer? No — after `copy`, `data` is unreachable. |
| Idiomatic? | `bytes.Clone` (Go 1.20+) is the idiomatic one-liner for this pattern. |
| Edge case | What if `len(pattern) == 0`? `make([]byte, 0)` is valid; `copy` is a no-op; returns empty slice, not nil. |

---

> **HOOK:** Returning a sub-slice of a large buffer keeps the entire buffer alive
> as long as any reference to the sub-slice exists. The GC cannot collect partial
> backing arrays — it's all or nothing. The fix is always: `copy` the interesting
> bytes into a freshly allocated, right-sized slice before returning.

> **INTERNALS:** A `[]byte` header contains `(ptr, len, cap)`. When the GC scans for
> reachable objects, it follows `ptr`. Even though `len` covers only 10 bytes of a
> 10MB file buffer, `ptr` points into that 10MB allocation. The allocator tracks the
> entire backing array as one object. It cannot free the 10MB − 10 bytes that are
> outside `len`. The entire allocation stays live until `ptr` becomes unreachable.

> **REAL-WORLD:** This pattern is extremely common in parsing code: you `ReadFile`,
> extract a field with a sub-slice, return it, and accumulate file-sized allocations
> for the lifetime of the returned sub-slices. `go tool pprof` memory profiles will
> show this as a large heap allocation attributed to `os.ReadFile` — which is correct
> but misleading. The fix is `bytes.Clone` or `append([]byte{}, sub...)`.

> **INSIGHT:** The rule: **if a small slice is extracted from a large buffer and the
> large buffer can be discarded after extraction, always copy before returning.**

---

⚠️ **Weak answer sounds like:** "This looks fine to me — it returns the matching bytes."
Missing the GC retention issue is what separates a 1-2 YOE engineer from a 3-4 YOE one.
Memory leaks through slice retention are a Go-specific footgun.

💬 **Likely follow-up:** "Can `bytes.Clone` (Go 1.20+) replace the `make + copy` pattern? What does it do internally?"

---

## Quick Summary Card

| Q | Topic | Tag | The core gotcha |
|---|-------|-----|----------------|
| 1 | Slice header `ptr+len+cap` | `[Internals]` | Passing a slice copies the header, not the data. Element writes are shared; appends beyond cap are not. |
| 2 | Append + backing array sharing | `[Gotcha]` | Within-capacity appends share the backing array. Use 3-index slice `[lo:hi:hi]` to force independence. |
| 3 | Slice growth factor | `[Internals]` | Changed to a smooth curve in Go 1.18. Never a hard 2×. Pre-size with `make([]T, 0, n)`. |
| 4 | `len(s)` returns bytes, not runes | `[Gotcha]` | Slicing by byte index can split a multibyte character. Use `range s` or `utf8.RuneCountInString`. |
| 5 | O(n²) string concatenation | `[Applied]` | `+=` in a loop allocates and copies every iteration. Use `strings.Builder` + optional `Grow`. |
| 6 | Map bucket internals + `tophash` | `[Internals]` | 8 key-value pairs per bucket. `tophash` enables fast key-not-present check without full key comparison. |
| 7 | Map incremental evacuation | `[Internals]` | 2 buckets evacuated per write. Old array stays alive. 3× memory during growth. Pre-size with `make(map[K]V, n)`. |
| 8 | Map iteration randomised by design | `[Gotcha]` | Actively randomised on every `range`, not just "undefined". Sort keys explicitly when order matters. |
| 9 | Concurrent map = `runtime.throw` | `[Gotcha]` | Not a data race. Not catchable with `recover()`. Hard crash. Use `sync.RWMutex` or `sync.Map`. |
| 10 | Sub-slice retains large backing array | `[Gotcha]` | GC cannot free partial arrays. Copy before returning a small slice extracted from a large buffer. |
