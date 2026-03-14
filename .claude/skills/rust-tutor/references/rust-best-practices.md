# Rust Best Practices Reference

## Table of Contents
1. [Ownership & Borrowing](#ownership--borrowing)
2. [Error Handling](#error-handling)
3. [Lifetimes](#lifetimes)
4. [Trait Design](#trait-design)
5. [Iterators & Functional Style](#iterators--functional-style)
6. [Pattern Matching](#pattern-matching)
7. [Type System Idioms](#type-system-idioms)
8. [Clippy Lints](#clippy-lints)
9. [Common Anti-Patterns](#common-anti-patterns)
10. [Module & Crate Organization](#module--crate-organization)
11. [Unsafe Code](#unsafe-code)
12. [Performance](#performance)
13. [Concurrency](#concurrency)
14. [Testing](#testing)
15. [Documentation](#documentation)

---

## Ownership & Borrowing

- Default to borrowing (`&T`) in parameters. Take ownership only when storing/consuming.
- Use `&mut T` only when mutation is needed.
- Accept `&str` not `&String`, `&[T]` not `&Vec<T>`, `&Path` not `&PathBuf`.
- Use `Cow<'_, str>` when a function sometimes allocates, sometimes borrows.
- Use `impl Into<String>` at API boundaries for flexible callers.
- Moves are cheap (memcpy). Don't fear them.
- Cloning to satisfy the borrow checker is a code smell — restructure borrow scopes instead.

## Error Handling

- `Result<T, E>` for recoverable errors; `panic!` only for invariant violations.
- Propagate with `?` — the core of Rust error handling.
- **Libraries**: custom error enum with `thiserror`. Each variant = distinct failure mode.
- **Applications**: `anyhow::Result` with `.context("msg")` for human-readable propagation.
- `Option` when absence is normal, not an error.
- Convert: `.ok_or()` / `.ok_or_else()` (Option->Result), `.ok()` (Result->Option).
- **Never**: `.unwrap()` in production (use `.expect("reason")` for true invariants), `Box<dyn Error>` from libraries, `String` as error type.

## Lifetimes

- Let elision work. Only annotate when the compiler requires it.
- Descriptive names for complex cases: `'input`, `'conn` not `'a`, `'b`.
- Struct lifetime parameters infect the entire API — think hard before adding `'a` to a struct.
- `'static` means "can live as long as needed" — owned data satisfies `'static`.
- Self-referential structs aren't directly expressible. Restructure, or use `Pin`/`ouroboros`.
- Multiple lifetime params on a function = code smell in application code.

## Trait Design

- **Static dispatch** (`impl Trait` / generics): default choice, zero-cost, monomorphized.
- **Dynamic dispatch** (`dyn Trait`): for heterogeneous collections or reducing binary size.
- Associated types when one impl per type (`Iterator::Item`). Generic params when multiple impls (`From<T>`).
- Keep traits small and focused (Interface Segregation).
- Extension traits + blanket impls to add methods to foreign types.
- Newtype pattern to bypass orphan rule.
- Seal traits with private supertraits when downstream impls are unwanted.
- Do NOT abuse `Deref`/`DerefMut` for inheritance-like behavior.

## Iterators & Functional Style

- Prefer iterator chains over manual loops with `push`. Compiler optimizes aggressively.
- `for x in &collection` over `for x in collection.iter()`.
- `.collect::<Result<Vec<_>, _>>()` to short-circuit on first error.
- `.filter_map()` for filter+transform. `.flat_map()` for one-to-many.
- Don't `.collect()` just to `.iter()` again — chain lazily.
- Indexing in loops (`v[i]`) incurs bounds checks; iterators often don't.
- `itertools` crate for `.chunks()`, `.tuple_windows()`, `.sorted()`, etc.

## Pattern Matching

- Match exhaustively. Avoid wildcard `_` when you can name all variants.
- `if let` for single-variant, `while let` for loop extraction.
- `matches!(value, Pattern)` for boolean checks.
- Destructure deeply: `if let Some(Ok(inner)) = x { ... }`.
- `@` bindings: `n @ 1..=100 => ...`.
- Tuple matching for multiple conditions: `match (a, b) { ... }`.
- Prefer `match` over `if/else if` chains for enum variants.

## Type System Idioms

**Newtype**: `struct UserId(u64)` — semantic meaning, prevents parameter mixups. Derive `Debug`, `Clone`, `PartialEq`, `Eq`, `Hash`.

**Typestate**: encode state machines in types. `Connection<Connected>` has `.send()`; `Connection<Disconnected>` doesn't. Zero-sized marker types, zero runtime cost.

**Builder**: for many optional params. Chain `.field(value)`, finalize with `.build() -> Result<T, E>`. `bon` or `derive_builder` crates.

**Other**: `PhantomData<T>` for unused type params. `NonZeroU32` for niche optimization. Enums over boolean flags: `enum Ordering { Asc, Desc }` not `ascending: bool`.

**Core principle**: "Make illegal states unrepresentable." "Parse, don't validate."

## Clippy Lints

- `clippy::pedantic` — enable for library code, allow `module_name_repetitions`.
- `clippy::unwrap_used` / `clippy::expect_used` — catch unwraps in production.
- `clippy::large_enum_variant` — box oversized variants.
- `clippy::needless_pass_by_value` — should borrow, not own.
- `clippy::clone_on_ref_ptr` — `Arc::clone(&x)` over `x.clone()` for clarity.
- `clippy::wildcard_enum_match_arm` — warns on `_` catch-all.
- `clippy::perf` — unnecessary allocations and inefficient patterns.
- `clippy::correctness` — likely bugs (deny by default).

## Common Anti-Patterns

- **Unwrap abuse**: Use `?`, `.unwrap_or()`, `.unwrap_or_default()`, `if let`.
- **Clone abuse**: Restructure scopes, split borrows, use indices. `Arc::clone(&x)` is idiomatic.
- **Stringly-typed code**: Parse into structured types at boundaries. Enums/newtypes over raw strings.
- **Over-boxing**: Don't `Box` everything. Use `Box<dyn Trait>` only for genuine dynamic dispatch needs.
- **Fighting the borrow checker**: If reaching for `Rc<RefCell<T>>` or `unsafe`, rethink the data structure.
- **`&String`/`&Vec<T>`/`&Box<T>`** as params — use `&str`/`&[T]`/`&T`.
- **Boolean blindness**: `fn process(x: Data, flag: bool)` — use an enum.
- **`println!` for logging**: Use `tracing` or `log` crate.
- **`collect()` then `iter()`**: Just chain iterators.

## Module & Crate Organization

- `lib.rs`/`main.rs` stays lean — re-exports and module declarations.
- `foo.rs` + `foo/` directory (modern style, not `mod.rs`).
- `pub use` re-exports for clean public API. `pub(crate)` for internal visibility.
- Group by domain concept, not by type-of-thing.
- Workspace for multi-crate projects. Separate library from binary crate.
- Feature flags for optional functionality; keep defaults minimal.

## Unsafe Code

- Acceptable for: FFI, measured performance-critical paths, low-level abstractions.
- Minimize scope. Document with `// SAFETY:` comments.
- Encapsulate behind safe APIs. Never expose raw pointers publicly.
- Use `#[deny(unsafe_op_in_unsafe_fn)]`. Audit with `cargo-miri`.
- Never alias `&mut T`. Uphold `Send`/`Sync` invariants. Don't assume layout without `#[repr(C)]`.

## Performance

- Iterators, closures, generics = zero-cost. Use freely.
- Preallocate: `Vec::with_capacity(n)`. Reuse: `.clear()` + refill.
- `Cow`, `&str`, `&[T]` to avoid cloning. `SmallVec`/`ArrayVec` for small stack collections.
- Avoid `format!()` in hot paths — `write!` to a reusable buffer.
- `Vec` with linear scan beats `HashMap` for <20 elements.
- `FxHashMap` (rustc-hash) when DoS resistance isn't needed.
- Profile first: `cargo flamegraph`, `perf`, `dhat`.

## Concurrency

- `Arc<Mutex<T>>` for shared mutable state. Keep lock scope minimal.
- `parking_lot::Mutex` often faster, no poisoning.
- Don't hold locks across `.await` — use `tokio::sync::Mutex` or restructure.
- Prefer message passing (channels) over shared state when possible.
- `tokio::task::spawn_blocking` for CPU-heavy work in async contexts.
- `rayon` for data parallelism (`.par_iter()`).
- `std::thread::scope` for scoped threads borrowing from stack.
- Don't use `async` if the function has no `.await` inside.

## Testing

- Unit tests in `#[cfg(test)] mod tests` at bottom of file.
- Return `Result<(), E>` from tests to use `?`.
- Integration tests in `tests/`. Shared utilities in `tests/common/mod.rs`.
- `proptest`/`quickcheck` for property-based testing. `insta` for snapshots.
- `rstest` for parameterized tests. `cargo-nextest` for faster execution.
- Doc test examples in `///` comments are compiled and run — keeps examples correct.

## Documentation

- `///` for items, `//!` for modules/crates. First line: imperative summary.
- `# Examples`, `# Errors`, `# Panics`, `# Safety` sections as applicable.
- Intra-doc links: `[`OtherType`]`.
- `#[must_use]` on functions whose return values shouldn't be ignored.
- `#![warn(missing_docs)]` on library crates.
