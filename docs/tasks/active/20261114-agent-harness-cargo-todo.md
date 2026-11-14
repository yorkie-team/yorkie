# Let a Rust repository adopt the shared agent harness

**Created**: 2026-11-14

## Problem

The shared harness has two adopters, a Go repository and a JavaScript one,
and the next is a Cargo workspace. Its configuration could not say what it
is: `tests.rules` knew `go` and `vitest`, so a Rust repository had to name
`vitest`, and then no Rust test was a case, no `assert_eq!` an assertion, and
a deleted integration target recorded nothing. Under that sits a model
problem. The rules ask "is this FILE a test", and Rust keeps most unit tests
inside the source file they test.

Smaller gaps came with it: the design-spec and prose classes were fixed to
`docs/design/**` and `docs/**`; a replicated rule could name one counterpart
repository; and the issue-to-PR prompt names six files an adopter learns
about from a confused agent.

## Goal

A core a Cargo workspace can configure honestly, with nothing changed for the
Go and JavaScript rule sets. Core only: the Rust repository's own files are
its own pull request.

## Plan

- [x] Dump what the removal record and the fix guard say about yorkie (Go)
      and yorkie-js-sdk (vitest) before any edit, to diff afterwards
- [x] `fixGuard.counterpartRepo` becomes `counterpartRepos`, a list; the old
      key is refused, and the loader's "not a known key" line says what to
      write, so `sync.mjs` tells an adopter
- [x] `review.classRules.design-spec` and `.prose`: optional additions to the
      built-in globs; `lenses.json` names the class (`$class:design-spec`,
      `$class:prose`) instead of repeating its globs
- [x] `cargo-rules.mjs`: a Rust lexer as far as brackets need, top-level
      `#[cfg(test)]` module regions, `testOnlyChange`, and the patch tally
      for `.rs` files and `Cargo.toml`
- [x] `test-removals.mjs`: with `cargo` on, every `.rs` patch and every
      `Cargo.toml` is tallied; integration targets are "run by name";
      `tests.cargo` is required exactly when `cargo` is named
- [x] `fix-guard.mjs`: a Rust file outside the scope is allowed when each
      commit changed its test regions only, read from the file's contents on
      both sides (the hook with `git show`, the report job through the
      contents API); the assertion lint and the replicated-rule warning use
      the same reading
- [x] The empty-pattern trap: a known rule set with no assertion entry throws
      at load, and no enabled set compiles to a pattern that matches nothing
- [x] Adversarial tests for the scope rule: `cfg(any(test, …))`,
      `cfg_attr(test, …)`, a `mod tests` with no `cfg(test)`, a macro
      expanding outside the region, a region inside an existing macro
      invocation, an attribute macro before `cfg(test)`, the closing brace
      moved down and the header moved up, a region wedged between an
      attribute and its item; six mutations of the scanner each fail a test
- [x] `conventions.mjs`: the six paths the issue-to-PR prompt names, kept
      equal to the prompt by a test, and required of every `repo.test.mjs`
- [x] README: the key table, "The cargo rule set", the rename, the CI
      workflow's name, what `check-protection` needs
- [x] Equivalence: the same five dumps after the change are byte-identical
      (1,393 and 1,103 tracked files; 30 commits each, twice per repository;
      1,402 patches, 163,234 patch lines)
- [x] End to end against the Rust repository, not committed: its draft
      configuration loads; 1,340 cases and 10 switch-offs tallied over 136
      `.rs` files, the numbers `grep` gives; six commits' patches read; the
      three `#[cfg(test)] mod …;` commits to `crdt/mod.rs` are test-only and
      nothing else is; no wrong classification found in the sample
- [x] `npm test`, `npm run test:repo`, `manifest.mjs verify`, actionlint,
      `scripts/test`, `make verify`
- [x] Review round, scanner: `a && b` and `a & &b` were the same tokens, so
      an eager `bool & &bool` passed as test-only on a real file. A
      punctuation token now carries whether the next one touches it; every
      multi-character operator is tested split at each position, both ways;
      the snippet was compiled once (rustc 1.100.0) to confirm it is valid
- [x] Review round, commits: a merge that is not of the base branch, a
      commit with no parent, a round past 50 commits and a round that could
      not be listed are refused by the hook and paged by the report. A merge
      of the base branch is judged by the files it left different from both
      parents. Not cargo-specific: it applies to every adopter
- [x] Review round, paths: Rust source, manifests, lockfiles and `.cargo/`
      are judged by the cargo rules alone; nothing under `src/bin`,
      `examples` or `benches`, no manifest and no `build.rs` is in scope by
      its path; a `*_tests.rs` file is in scope only once a parent module is
      read declaring it under `#[cfg(test)]`
- [x] Review round, record: an inner `#![cfg(…)]` at the top of a test
      module, a `cfg` broken over lines, a repointed `#[path]`,
      `assert_eq!(f(), f())`, and nested test modules for the lints
- [x] The reviewer's probes re-run: the `&&` cases, H3, H5, H6, H11 and H15
      are refused; the five equivalence dumps are still byte-identical; the
      Rust repository's numbers are unchanged
- [ ] A fix round on a runner in a Cargo repository: the hook's `git show`
      reads and the report job's contents-API reads have run only in tests
      and by hand
- [ ] Review rounds on the pull request

## Not in this change

- The Rust repository's own files (configuration, coverage note, toolchain
  action, `repo.test.mjs`, the docs layout the prompt names).
- Making the issue-to-PR prompt's paths configurable. An adopter takes the
  layout.
- `#[test]` functions outside a `#[cfg(test)]` module, test regions in a
  nested module, and `#[cfg(test)]` on items other than modules: harmless to
  a release build, refused by the scope rule, and each pages. Delimiting an
  arbitrary item needs a parser.
- The other ways the review found to switch a test off without a line the
  record knows (a swallow-all macro, a renamed test, `|| true`, a flipped
  expectation, manifest features): listed in the README, not matched.
- A runtime self-skip (`return` before the assertions). No line pattern
  identifies it; the test-adequacy lens is its only reader.
