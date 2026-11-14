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
