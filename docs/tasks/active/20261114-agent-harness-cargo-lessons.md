# Let a Rust repository adopt the shared agent harness — Lessons

**Created**: 2026-11-14

## Notes

- "Is this file a test" is two questions that happened to share an answer in
  Go and JavaScript. The removal record asks "what did this patch take away",
  and Rust's markers answer it by themselves: `#[test]` occurs only on a
  test. The fix guard asks "may this file change", and that is a gate. They
  need different evidence, and giving both the same model would have made
  one of them guess.
- A patch cannot carry a gate's decision about a region. Three lines of
  context do not show a `mod` header, and GitHub's hunk heading is git's
  function-name heuristic: on the real repository it reads `impl TreeNode {`.
  Where the answer has to be sound, read the file.
- Define the allowed change by what must NOT differ. "The product's tokens
  are unchanged" closed the moved brace, the macro invoked outside the
  region and the widened gate without a rule for any of them. A list of
  allowed edits would have needed a rule for each, and the next one.
- An attribute before `#[cfg(test)]` runs before it, and receives the `cfg`
  as input. Attribute order is evaluation order, so "more attributes" is
  safe on one side of the gate and not on the other.
- Only the top level of a file is safe to read without a parser. Anything
  inside a bracket may be a macro's input, and a macro decides what its
  input means.
- A regular expression joined from several rule sets is one namespace.
  A numbered backreference in one set's pattern points into another's; a
  named group does not. And joining no sets gives `new RegExp("")`, which
  matches every line — the opposite of "nothing applies".
- Rule sets that share a repository must not share idioms. JavaScript's
  `expect(` is Rust's `.expect("…")`, which is in every line of set-up.
  Dispatching by file kept the old pattern byte-identical for the old sets,
  which is also what made "nothing changed" provable.
- Net a marker only against the same thing. A removed `mod x;` and an added
  `mod y;` are not a wash, and an `#[ignore]` deleted along with its test
  must not cancel one added elsewhere in the file.
- A comment that names a glob can end the comment: `crates/*/tests` inside a
  `/** … */` block closed it, and the module still parsed as far as the
  first test that did not import it.
- The pure rule tests passed while `config.mjs` did not parse, because they
  do not import it. A module split so that it can be tested alone needs one
  test that loads the whole.
- Prove "no change" on a corpus fixed before the first edit, pinned to a
  commit, not to `HEAD`: the branch under test moves the corpus otherwise.
- "Whitespace is not a token" is false for one thing: whether two
  punctuation characters touch. `&&` and `& &` are the same characters and
  different programs. Rust's own lexer keeps that bit for its parser; a
  scanner that claims token equality has to keep it too.
- A name is a convention until something is read. `x_tests.rs` is test code
  because a parent says `#[cfg(test)] mod x_tests;`, and a binary under
  `src/bin/` whatever it is called. A gate that trusts the name trusts
  whoever chose it.
- A rule written for one language's layout is a hole in another's. The
  generic `tests/` and `docs/` clauses were correct for Go and JavaScript
  and opened `src/bin/tests/main.rs` and `docs/Cargo.toml` under cargo.
- A check that reads "the round's commits" must say what it does with the
  ones it skips. Both layers skipped merges and parentless commits for a
  good reason, and neither treated "skipped" as "not judged".
- "Could not read" has to pick a side, and for a gate the side is closed.
  `blocking=false` on a failed listing made failing the read a way through.
- Splitting two intents that share files: build the earlier tree by exact
  inverse edits and test it, rather than reverse-applying zero-context
  hunks, which landed on the wrong lines without an error.
