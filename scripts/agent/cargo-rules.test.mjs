import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  cargoManifestTally,
  cargoNeverRuns,
  cargoTally,
  patchLines,
  productTokens,
  rustTokens,
  testOnlyChange,
  testRegionLines,
  testRegions,
} from "./cargo-rules.mjs";

// The cargo rules are functions of text: no configuration, no repository. The
// Rust here is written for these tests — a product type with a `mod tests` at
// the bottom, the layout most Rust source files have.
const HERE = path.dirname(fileURLToPath(import.meta.url));
const COUNTER = readFileSync(path.join(HERE, "fixtures", "cargo", "counter.rs"), "utf8");
const TESTS_AT = COUNTER.indexOf("#[cfg(test)]");
const PRODUCT = COUNTER.slice(0, TESTS_AT);
const TESTS = COUNTER.slice(TESTS_AT);

/** `src` with the one occurrence of `from` replaced. A fixture that drifted fails here, not downstream. */
function edit(src, from, to) {
  assert.equal(src.split(from).length, 2, `expected exactly one ${JSON.stringify(from)} in the source`);
  return src.replace(from, to);
}
const lineOf = (src, needle) => src.slice(0, src.indexOf(needle)).split("\n").length;

// --- the lexer ---------------------------------------------------------------

test("rustTokens: a bracket in a literal or a comment is not a bracket", () => {
  const kinds = (src) => rustTokens(src).map((t) => `${t.k}:${t.t}`);
  assert.deepEqual(kinds("let c = '{';"), ["w:let", "w:c", "p:=", "s:'{'", "p:;"]);
  assert.deepEqual(kinds(`let s = "}\\"{";`), ["w:let", "w:s", "p:=", `s:"}\\"{"`, "p:;"]);
  assert.deepEqual(kinds(`r#"{"}"# r##"a"#b"## br"[" b"(" c"{" b'{' b'\\''`), [
    `s:r#"{"}"#`, `s:r##"a"#b"##`, `s:br"["`, `s:b"("`, `s:c"{"`, "s:b'{'", "s:b'\\''",
  ]);
  // A character literal against a lifetime: `'a'` is one token, `'a` another.
  assert.deepEqual(kinds("&'a str, 'x', '\\'', '\\\\', '\\u{7b}', 'outer: loop"), [
    "p:&", "l:'a", "w:str", "p:,", "s:'x'", "p:,", "s:'\\''", "p:,", "s:'\\\\'", "p:,", "s:'\\u{7b}'", "p:,", "l:'outer", "p::", "w:loop",
  ]);
  // A raw identifier is a word; `r` on its own is too.
  assert.deepEqual(kinds("r#type r + 1"), ["w:r#type", "w:r", "p:+", "w:1"]);
  // Comments nest; the three kinds are told apart because a doc comment is an
  // attribute on the item below and a plain comment is nothing.
  assert.deepEqual(rustTokens("/* { /* } */ { */ x").map((t) => t.k), ["c", "w"]);
  assert.deepEqual(rustTokens("/// outer\n//// plain\n//! inner\n// plain\n/** outer */ /*! inner */ /**/ /*** plain */").map((t) => t.k), ["d", "c", "D", "c", "d", "D", "c", "c"]);
  // Numbers do not swallow a range or a method call.
  assert.deepEqual(kinds("1..2 1.5 x.0 1.max(2)"), ["w:1", "p:.", "p:.", "w:2", "w:1.5", "w:x", "p:.", "w:0", "w:1", "p:.", "w:max", "p:(", "w:2", "p:)"]);
});

test("rustTokens: line numbers follow multi-line tokens", () => {
  const toks = rustTokens('a\n"two\nlines"\n/* three\n\nlines */\nb');
  assert.deepEqual(toks.map((t) => [t.t.slice(0, 4), t.line, t.endLine]), [["a", 1, 1], ['"two', 2, 3], ["/* t", 4, 6], ["b", 7, 7]]);
});

test("rustTokens: source that does not lex is null, never a guess", () => {
  for (const bad of ['let s = "never closed;', "/* never closed", "/* outer /* inner */", 'r#"never closed"', "b'ab'", 'b"open']) {
    assert.equal(rustTokens(bad), null, bad);
  }
  assert.equal(rustTokens(undefined), null);
  assert.equal(rustTokens(null), null);
  assert.deepEqual(rustTokens(""), []);
});

// --- test regions ------------------------------------------------------------

test("testRegions: the fixture has one region, the module at the bottom", () => {
  const parsed = testRegions(COUNTER);
  assert.equal(parsed.regions.length, 1);
  const [region] = parsed.regions;
  assert.equal(region.kind, "inline");
  assert.equal(region.name, "tests");
  assert.equal(region.startLine, lineOf(COUNTER, "#[cfg(test)]"));
  assert.equal(region.endLine, COUNTER.trimEnd().split("\n").length);
  const inside = testRegionLines(COUNTER);
  assert.equal(inside(lineOf(COUNTER, "fn increases_per_actor")), true);
  assert.equal(inside(lineOf(COUNTER, "pub fn increase")), false);
  assert.equal(inside(lineOf(COUNTER, "debug_assert!")), false, "an assertion in product code is not test code");
  assert.equal(inside(region.startLine - 1), false);
  // The product is everything else, and it is the same with the tests gone.
  assert.deepEqual(productTokens(COUNTER), productTokens(PRODUCT));
  assert.ok(productTokens(COUNTER).length > 100);
});

test("testRegions: the forms that are a region", () => {
  const one = (src) => {
    const parsed = testRegions(src);
    assert.ok(parsed, src);
    assert.equal(parsed.regions.length, 1, src);
    return parsed.regions[0];
  };
  assert.equal(one("#[cfg(test)]\nmod tests {\n    fn a() {}\n}\n").kind, "inline");
  // The declaration that reaches a test file of its own.
  assert.deepEqual((({ kind, name }) => ({ kind, name }))(one("#[cfg(test)]\nmod counter_tests;\n")), { kind: "declaration", name: "counter_tests" });
  assert.equal(one("#[cfg(test)] mod t;").kind, "declaration");
  assert.equal(one("#[cfg(test)]\npub(crate) mod support {}\n").name, "support");
  assert.equal(one("#[cfg( test )]\npub mod t {}\n").name, "t");
  // More attributes AFTER `cfg(test)`: it is applied first, so they never run
  // in a release build.
  assert.equal(one('#[cfg(test)]\n#[allow(clippy::unwrap_used)]\n#[path = "elsewhere.rs"]\nmod t;\n').kind, "declaration");
  // The comments directly above go with it; the product line above does not.
  const src = "fn product() {} // trailing\n\n// Regression tests.\n/// For the counter.\n#[cfg(test)]\nmod tests {}\n";
  const region = one(src);
  assert.equal(region.startLine, 3);
  assert.deepEqual(productTokens(src), ["wfn", "wproduct", "j(", "p)", "j{", "p}", "c// trailing"]);
  // Two regions in one file.
  assert.equal(testRegions("#[cfg(test)]\nmod a_tests;\nfn p() {}\n#[cfg(test)]\nmod tests {}\n").regions.length, 2);
});

test("testRegions: what is NOT a region, and why each must not be", () => {
  const none = (src, why) => {
    const parsed = testRegions(src);
    assert.ok(parsed, `${why}: expected it to lex`);
    assert.deepEqual(parsed.regions, [], why);
  };
  // Compiled into a release build whenever the feature is on.
  none('#[cfg(any(test, feature = "x"))]\nmod tests { fn p() {} }\n', "cfg(any(test, …))");
  none('#[cfg(all(test, feature = "x"))]\nmod tests {}\n', "cfg(all(test, …)): narrower, but not the exact attribute");
  none("#[cfg(not(test))]\nmod tests {}\n", "cfg(not(test))");
  none("#[cfg_attr(test, allow(dead_code))]\nmod tests {}\n", "cfg_attr(test, …) gates an attribute, not the module");
  none("mod tests { fn p() {} }\n", "a mod tests with no cfg(test) is compiled always");
  none("#[test]\nfn lone() {}\n", "a bare #[test] fn: harmless, and not delimited here");
  // `cfg(test)` on something that is not a module.
  none("#[cfg(test)]\nfn helper() {}\n", "cfg(test) fn");
  none("#[cfg(test)]\nuse std::fmt;\n", "cfg(test) use");
  none("#[cfg(test)]\nimpl Counter { fn p(&self) {} }\n", "cfg(test) impl");
  none("#[cfg(test)]\nmacro_rules! m { () => {}; }\n", "cfg(test) macro_rules");
  // Not at the top level: whatever encloses it may be a macro, which is
  // handed the tokens and can emit them without the attribute.
  none("strip_cfg! {\n    #[cfg(test)]\n    mod tests { fn p() {} }\n}\n", "inside a macro invocation");
  none("strip_cfg!(\n    #[cfg(test)]\n    mod tests { fn p() {} }\n);\n", "inside a macro invocation (parentheses)");
  none("macro_rules! m {\n    () => {\n        #[cfg(test)]\n        mod tests {}\n    };\n}\n", "inside a macro_rules body");
  none("fn f() {\n    #[cfg(test)]\n    mod tests {}\n}\n", "inside a function");
  none("mod outer {\n    #[cfg(test)]\n    mod tests {}\n}\n", "inside another module");
  // An attribute BEFORE it runs first and receives the cfg as input.
  none("#[some_macro]\n#[cfg(test)]\nmod tests { fn p() {} }\n", "an attribute macro before cfg(test)");
  none("#[cfg(unix)]\n/// docs\n#[cfg(test)]\nmod tests {}\n", "…with a doc comment between");
  none("#[cfg(unix)] // why\n#[cfg(test)]\nmod tests {}\n", "…with a comment between");
  // An INNER attribute is the enclosing file's, and is not this form.
  none("#![cfg(test)]\nmod tests {}\n", "#![cfg(test)]");
  none("#[cfg(test)]\n#![allow(x)]\nmod tests {}\n", "an inner attribute after it");
  none("#[cfg(test)]\nmod tests\n", "a module with no body and no semicolon");
  // Brackets that do not balance: no regions at all, not "up to the end".
  assert.equal(testRegions("#[cfg(test)]\nmod tests {\n    fn a() {\n}\n"), null);
  assert.equal(testRegions("fn a() { ) }\n#[cfg(test)]\nmod tests {}\n"), null);
  assert.equal(testRegions("}\n#[cfg(test)]\nmod tests {}\n"), null);
});

// --- the scope question ------------------------------------------------------

test("testOnlyChange: what a fix round may do to a Rust file outside the PR's diff", () => {
  const ok = (after, what) => assert.equal(testOnlyChange(COUNTER, after), true, what);
  ok(COUNTER, "no change");
  ok(edit(COUNTER, "assert_eq!(c.value(), 5);", "assert_eq!(c.value(), 5);\n        assert_eq!(c.label(), \"counter\");"), "an assertion added to an existing test");
  ok(
    edit(COUNTER, "    #[test]\n    fn merge_keeps", "    #[test]\n    #[ignore = \"still reproduces: merge drops a count\"]\n    fn reproducer() {\n        assert!(false);\n    }\n\n    #[test]\n    fn merge_keeps"),
    "a reproducer added to the existing module",
  );
  ok(edit(COUNTER, "    #[test]\n    fn increases_per_actor() {\n        let mut c = Counter::default();\n        c.increase(\"a\", 2);\n        c.increase(\"b\", 3);\n        assert_eq!(c.value(), 5);\n    }\n\n", ""), "a test deleted (the removal record's business, not the scope's)");
  ok(`${PRODUCT}#[cfg(test)]\nmod counter_tests;\n\n${TESTS}`, "the declaration of a new test file");
  ok(`${COUNTER}\n// Regressions found by review.\n#[cfg(test)]\nmod regression_tests {\n    #[test]\n    fn merges() {}\n}\n`, "a second test module, with its comment, at the end");
  // A file with no tests yet gains its first module.
  assert.equal(testOnlyChange(PRODUCT, `${PRODUCT}\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn starts_empty() {\n        assert_eq!(Counter::default().value(), 0);\n    }\n}\n`), true);
  // Whitespace is not a token: rustfmt re-indenting the file changes nothing.
  ok(COUNTER.replace(/\n\n/g, "\n\n\n"), "blank lines");
});

test("testOnlyChange: product code smuggled through a test region is refused", () => {
  const refused = (after, what) => assert.equal(testOnlyChange(COUNTER, after), false, what);
  // The plain case first: one product line.
  refused(edit(COUNTER, "self.counts.values().sum()", "self.counts.values().sum::<u64>() + 1"), "a product change");
  refused(edit(COUNTER, "    /// The total over every actor.", "    /// The total over every actor, plus one."), "a doc comment on product code");
  refused(edit(COUNTER, "        // Brackets that are not brackets", "        // Reviewer: brackets that are not brackets"), "a comment in product code");
  // A region that is compiled into a release build.
  refused(`${COUNTER}\n#[cfg(any(test, feature = "x"))]\nmod extra {\n    pub fn product() {}\n}\n`, "cfg(any(test, feature))");
  refused(edit(COUNTER, "#[cfg(test)]\nmod tests {", '#[cfg(any(test, feature = "x"))]\nmod tests {'), "the existing region's gate widened");
  refused(`${COUNTER}\n#[cfg_attr(test, allow(dead_code))]\nmod extra {\n    pub fn product() {}\n}\n`, "cfg_attr(test, …)");
  refused(`${COUNTER}\nmod tests2 {\n    pub fn product() {}\n}\n`, "a mod tests without cfg(test)");
  refused(edit(COUNTER, "#[cfg(test)]\nmod tests {", "mod tests {"), "cfg(test) taken off the existing module");
  // A macro that expands outside the region.
  refused(
    edit(edit(COUNTER, "    use super::*;", "    use super::*;\n\n    macro_rules! double { ($e:expr) => { $e * 2 }; }\n    pub(crate) use double;"), "self.counts.values().sum()", "tests::double!(self.counts.values().sum())"),
    "a macro defined in the region and invoked in the product",
  );
  refused(`${PRODUCT}emit_unconditionally! {\n${TESTS}}\n`, "the region wrapped in a macro invocation");
  refused(`${PRODUCT}#[strip_cfg]\n${TESTS}`, "an attribute macro put before cfg(test)");
  // A macro invocation that was ALREADY there: a module slipped into its
  // input is handed to the macro, which may emit it with the cfg stripped.
  const wrapped = `${COUNTER}\nstrip_cfg! {\n    pub fn existing() {}\n}\n`;
  assert.equal(
    testOnlyChange(wrapped, edit(wrapped, "    pub fn existing() {}\n", "    pub fn existing() {}\n    #[cfg(test)]\n    mod tests {\n        pub fn reachable_in_release() {}\n    }\n")),
    false,
    "a cfg(test) module added inside an existing macro invocation",
  );
  const nested = `${COUNTER}\nmod inner {\n    pub fn existing() {}\n}\n`;
  assert.equal(
    testOnlyChange(nested, edit(nested, "    pub fn existing() {}\n", "    pub fn existing() {}\n    #[cfg(test)]\n    mod tests {}\n")),
    false,
    "a test module added inside a nested module: harmless, and refused (top level only)",
  );
  // The closing brace moved to swallow product code. Built from a file where
  // product code FOLLOWS the tests, so there is something to swallow.
  const tail = "\npub fn after_the_tests() -> u64 {\n    7\n}\n";
  const before = `${COUNTER}${tail}`;
  const swallowed = `${COUNTER.trimEnd().slice(0, -1)}${tail}}\n`;
  assert.equal(testRegions(swallowed).regions.length, 1, "the swallowing edit still lexes as one region");
  assert.equal(testOnlyChange(before, swallowed), false, "a closing brace moved down over product code");
  // …and the opening moved UP: the function above the module is now inside it.
  refused(
    edit(edit(COUNTER, "    fn label(&self)", "}\n\n#[cfg(test)]\nmod tests {\n    fn label(&self)"), "}\n\n#[cfg(test)]\nmod tests {\n    use super::*;", "\n    use super::*;"),
    "the module header moved up over product code",
  );
  // A region slid between an attribute and the item it gated: the item would
  // become unconditional.
  const gated = edit(COUNTER, "    fn label(&self)", "    #[cfg(unix)]\n    fn label(&self)");
  assert.equal(testOnlyChange(gated, gated), true);
  const topLevelGated = `#[cfg(unix)]\npub fn only_on_unix() {}\n${COUNTER}`;
  assert.equal(
    testOnlyChange(topLevelGated, `#[cfg(unix)]\n#[cfg(test)]\nmod wedge {}\npub fn only_on_unix() {}\n${COUNTER}`),
    false,
    "a region wedged between #[cfg(unix)] and its function",
  );
  // cfg(test) on a non-module item: harmless in a release build, refused
  // because it is not a region.
  refused(`${COUNTER}\n#[cfg(test)]\nfn helper() {}\n`, "cfg(test) fn");
  refused(`${COUNTER}\n#[test]\nfn lone() {}\n`, "a #[test] fn outside any test module");
  // Nothing provable: a side missing, or a side that does not lex.
  assert.equal(testOnlyChange(null, COUNTER), false, "a new file");
  assert.equal(testOnlyChange(COUNTER, null), false, "a deleted file");
  assert.equal(testOnlyChange(undefined, undefined), false);
  refused(`${COUNTER}\n#[cfg(test)]\nmod broken {\n`, "unbalanced after");
  assert.equal(testOnlyChange(`${COUNTER}"`, `${COUNTER}"`), false, "identical, and neither side lexes");
});

// THE CASE THAT GOT THROUGH. With whitespace dropped and punctuation read one
// character at a time, `a && b` and `a & &b` were the same tokens: a
// short-circuit AND turned into an eager `bool & &bool`, which compiles
// (rustc 1.100.0, warnings denied) and runs the right-hand side. Found by
// review on a real `!node.is_removed() && node.remove(..)`.
test("testOnlyChange: an operator split, or two operators joined, is a product change", () => {
  const lazy = edit(COUNTER, "if *count > *mine {", "if *count > *mine && self.counts.len() > 0 {");
  const eager = edit(lazy, "*mine && self", "*mine & &self");
  assert.equal(testOnlyChange(lazy, eager), false, "&& split into & &");
  assert.equal(testOnlyChange(eager, lazy), false, "& & joined into &&");
  assert.equal(testOnlyChange(lazy, edit(lazy, "*mine && self", "*mine &\n                &self")), false, "split across a line");
  assert.equal(testOnlyChange(lazy, edit(lazy, "*mine && self", "*mine & /* gap */ &self")), false, "split by a comment");
  assert.equal(testOnlyChange(lazy, edit(lazy, "*mine && self", "*mine   &&\n   self")), true, "space AROUND an operator is still nothing");
  // The same edit inside the test module is the test module's business.
  const inTests = edit(COUNTER, "assert!(a.label().starts_with(\"count\"));", "assert!(true && a.label().starts_with(\"count\"));");
  assert.equal(testOnlyChange(inTests, edit(inTests, "true && a", "true & &a")), true);

  // EVERY operator Rust spells with more than one punctuation character
  // (the reference's punctuation table). Split at each inner position, the
  // pieces are other valid tokens, so each split is a different program or a
  // different parse — and must be a different product, in both directions.
  const operators = [
    "&&", "||", "::", "->", "=>", "==", "!=", "<=", ">=", "<<", ">>", "..", "...", "..=",
    "+=", "-=", "*=", "/=", "%=", "^=", "&=", "|=", "<<=", ">>=", "<-",
  ];
  let checked = 0;
  for (const op of operators) {
    const glued = `const X: T = a ${op} b;\n`;
    assert.equal(testOnlyChange(glued, glued), true, op);
    assert.equal(testOnlyChange(glued, `const X: T = a${op}b;\n`), true, `${op}: space around it is nothing`);
    for (let at = 1; at < op.length; at++) {
      for (const gap of [" ", "\n", "\t", " /* c */ ", " // c\n"]) {
        const split = `const X: T = a ${op.slice(0, at)}${gap}${op.slice(at)} b;\n`;
        assert.equal(testOnlyChange(glued, split), false, `${op} split after ${at} by ${JSON.stringify(gap)}`);
        assert.equal(testOnlyChange(split, glued), false, `${op} joined at ${at} over ${JSON.stringify(gap)}`);
        checked++;
      }
    }
  }
  assert.equal(checked, 145);
  // Pairs that are two tokens either way, and still two ways to read: a
  // unary operator against a binary one, and the closing of nested generics.
  // Refusing these is the cost of the rule, and it is the safe side.
  for (const [a, b] of [["a - -b", "a --b"], ["a & !b", "a &!b"], ["Vec<Vec<T>>", "Vec<Vec<T> >"], ["x = &y", "x =&y"], ["a < -b", "a <-b"], ["a..=b", "a.. =b"]]) {
    assert.equal(testOnlyChange(`const X: T = ${a};\n`, `const X: T = ${b};\n`), false, `${a} / ${b}`);
  }
  // What a number or a word decides is in the token already.
  assert.equal(testOnlyChange("const X: f64 = 1.0;\n", "const X: f64 = 1 .0;\n"), false);
  assert.equal(testOnlyChange("const X: R = 1..2;\n", "const X: R = 1. .2;\n"), false);
  assert.equal(testOnlyChange("const X: &'a T = y;\n", "const X: &' a T = y;\n"), false);
  assert.equal(testOnlyChange('const X: &str = r#"a"#;\n', 'const X: &str = r #"a"#;\n'), false);
  // A region does not lend its first character to the product: the token
  // before it is joint with the region's `#` in one file and with nothing in
  // the other, and that is a difference (a refusal, never an acceptance).
  assert.equal(testOnlyChange("fn a() {}\n", "fn a() {}#[cfg(test)] mod t {}\n"), false);
  assert.equal(testOnlyChange("fn a() {}\n", "fn a() {} #[cfg(test)] mod t {}\n"), true);
});

test("rustTokens: a punctuation token knows whether the next one touches it", () => {
  const marks = (src) => rustTokens(src).filter((t) => t.k === "p").map((t) => `${t.t}${t.joint ? "+" : ""}`).join(" ");
  assert.equal(marks("a && b"), "&+ &");
  assert.equal(marks("a & &b"), "& &");
  assert.equal(marks("a &/**/& b"), "& &");
  assert.equal(marks("a >>= b"), ">+ >+ =");
  assert.equal(marks("x.y()"), ". (+ )");
  // A literal, a word or a lifetime after it is not punctuation.
  assert.equal(marks("&'a x, &\"s\", &1, -x"), "& , & , & , -");
});

test("testOnlyChange: a known false page — a region added right under an own-line comment that was there", () => {
  // The comment was the product's last token; the new module takes it as its
  // heading. Refused, and a human looks: the direction that is safe.
  const before = `${PRODUCT}// End of the product.\n`;
  assert.equal(testOnlyChange(before, `${before}#[cfg(test)]\nmod tests {}\n`), false);
  // A blank line between them does not help: the comment is still directly above.
  assert.equal(testOnlyChange(before, `${before}\n#[cfg(test)]\nmod tests {}\n`), false);
});

// --- patches -----------------------------------------------------------------

test("patchLines: numbers each line on the side it exists on, per hunk", () => {
  const patch = ["@@ -10,4 +10,5 @@ mod tests {", " ctx", "-old", "+new", "+more", " ctx2", "\\ No newline at end of file", "@@ -40,2 +41,2 @@", "-a", "+b"].join("\n");
  assert.deepEqual(patchLines(patch).map((l) => [l.sign, l.text, l.oldLine, l.newLine, l.hunk, l.heading]), [
    [" ", "ctx", 10, 10, 0, "mod tests {"],
    ["-", "old", 11, 0, 0, "mod tests {"],
    ["+", "new", 0, 11, 0, "mod tests {"],
    ["+", "more", 0, 12, 0, "mod tests {"],
    [" ", "ctx2", 12, 13, 0, "mod tests {"],
    ["-", "a", 40, 0, 1, ""],
    ["+", "b", 0, 41, 1, ""],
  ]);
  // No hunk header: the lines are still read, with no numbers to trust.
  assert.deepEqual(patchLines("-a\n+b").map((l) => [l.sign, l.oldLine, l.newLine]), [["-", 0, 0], ["+", 0, 0]]);
  assert.deepEqual(patchLines(undefined), []);
});

// --- the removal record's tally ----------------------------------------------

const counts = (t) => ({ removed: t.removed, added: t.added, offNet: t.offNet, gates: [...t.buildsAdded].filter((e) => !t.buildsRemoved.has(e)) });

test("cargoTally: a case is the test attribute, whatever the file", () => {
  const patch = [
    "@@ -40,12 +40,9 @@ mod tests {",
    "-    #[test]",
    "-    fn merge_keeps_the_larger_count() {",
    "-        assert_eq!(a.value(), 4);",
    "-    }",
    "+    #[tokio::test]",
    "+    async fn merges() {}",
    "+    #[tokio::test(flavor = \"multi_thread\")]",
    "+    async fn merges_in_parallel() {}",
    "+    #[rstest]",
    "+    #[case(1)]",
    "+    fn table(#[case] n: u64) {}",
    "+    #[wasm_bindgen_test]",
    "+    fn in_a_browser() {}",
    "+    #[test_log::test(tokio::test)]",
    "+    async fn logged() {}",
  ].join("\n");
  assert.deepEqual(counts(cargoTally(patch)), { removed: 1, added: 5, offNet: 0, gates: [] });
  // What is NOT a case: the function line, a commented-out attribute (so
  // commenting a test out counts as removing it), a table row, a bench.
  const not = ["+    fn test_something() {", "+    // #[test]", "+    #[test_case(1, 2)]", "+    #[bench]", "+    /// #[test]", "+    #[testing]"].join("\n");
  assert.deepEqual(counts(cargoTally(not)), { removed: 0, added: 0, offNet: 0, gates: [] });
  assert.deepEqual(counts(cargoTally("-    #[test]\n+    // #[test]")), { removed: 1, added: 0, offNet: 0, gates: [] });
  // A multi-line attribute: the opening line is the case.
  assert.deepEqual(counts(cargoTally("+    #[tokio::test(\n+        flavor = \"current_thread\",\n+    )]")), { removed: 0, added: 1, offNet: 0, gates: [] });
});

test("cargoTally: #[ignore], with or without a reason, on one line or several", () => {
  const added = (lines) => counts(cargoTally(["@@ -5,3 +5,4 @@", " #[tokio::test]", ...lines, " async fn handles_a_snapshot() {"].join("\n")));
  assert.deepEqual(added(["+#[ignore]"]), { removed: 0, added: 0, offNet: 1, gates: [] });
  assert.deepEqual(added(['+#[ignore = "still reproduces: the snapshot loses a member"]']), { removed: 0, added: 0, offNet: 1, gates: [] });
  // A reason that runs over several lines: the first line has no `]`.
  assert.deepEqual(
    added(['+#[ignore = "known limitation: after GC the text-node segmentation \\', "+            diverges when an element is deleted concurrently; a fix needs \\", '+            undo-aware GC"]']),
    { removed: 0, added: 0, offNet: 1, gates: [] },
  );
  assert.deepEqual(added(['+#[cfg_attr(not(feature = "server"), ignore)]']), { removed: 0, added: 0, offNet: 1, gates: [] });
  assert.deepEqual(added(['+#[cfg_attr(miri, ignore = "too slow")]']), { removed: 0, added: 0, offNet: 1, gates: [] });
  // Re-wording a reason is not a new switch-off; taking one out re-enables.
  assert.equal(cargoTally('-#[ignore = "old words"]\n+#[ignore = "new words"]').offNet, 0);
  assert.equal(cargoTally(" #[test]\n-#[ignore]\n fn back_on() {").offNet, -1);
  // A NEW test born ignored IS recorded: it is how a fixer keeps a reproducer
  // it could not fix, and the adjudicator is shown it beside the claim.
  assert.deepEqual(counts(cargoTally('+#[test]\n+#[ignore = "still reproduces: x"]\n+fn reproducer() {')), { removed: 0, added: 1, offNet: 1, gates: [] });
  // Deleted WITH its test: one case removed, and no `-1` to cancel a
  // switch-off made elsewhere in the same file.
  const both = ["@@ -1,4 +1,0 @@", "-#[test]", "-#[ignore]", "-fn gone() {", "-}", "@@ -20,2 +16,3 @@", " #[test]", "+#[ignore]", " fn newly_off() {"].join("\n");
  assert.deepEqual(counts(cargoTally(both)), { removed: 1, added: 0, offNet: 1, gates: [] });
  assert.equal(cargoTally("+// #[ignore]\n+/// #[ignore]").offNet, 0, "a comment is not an attribute");
  assert.equal(cargoTally("+#[ignored_by_us]").offNet, 0);
});

test("cargoTally: #[should_panic] counts when it is added to a test that was there", () => {
  assert.equal(cargoTally(" #[test]\n+#[should_panic]\n fn reproducer() {").offNet, 1, "an existing test inverted");
  assert.equal(cargoTally(' #[tokio::test]\n+#[should_panic(expected = "overflow")]\n async fn reproducer() {').offNet, 1);
  // The test attribute is out of the hunk's context: counted, not assumed new.
  assert.equal(cargoTally("+#[should_panic]\n fn reproducer() {").offNet, 1);
  // Written with a new test, it is that test's design.
  assert.equal(cargoTally('+#[test]\n+#[should_panic(expected = "overflow")]\n+fn rejects_overflow() {').offNet, 0);
  assert.equal(cargoTally("+#[should_panic]\n+#[test]\n+fn rejects_overflow() {").offNet, 0, "in either order");
  assert.equal(cargoTally(" #[test]\n-#[should_panic]\n fn upright_again() {").offNet, -1);
  assert.equal(cargoTally("-#[test]\n-#[should_panic]\n-fn gone() {").offNet, 0, "removed with its test");
});

test("cargoTally: a cfg gate counts on a test, and anywhere in a test file", () => {
  const gate = '#[cfg(feature = "slow-tests")]';
  // In a source file: only where the patch shows a test beside it.
  assert.deepEqual(counts(cargoTally(`+${gate}\n #[test]\n fn slow() {`)), { removed: 0, added: 0, offNet: 0, gates: ['feature = "slow-tests"'] });
  assert.deepEqual(counts(cargoTally(` #[test]\n+${gate}\n fn slow() {`)).gates, ['feature = "slow-tests"']);
  assert.deepEqual(counts(cargoTally(`+${gate}\n mod tests {`)).gates, ['feature = "slow-tests"'], "on a module named as tests");
  assert.deepEqual(counts(cargoTally(` #[cfg(test)]\n+${gate}\n mod scenarios {`)).gates, ['feature = "slow-tests"'], "on a module under cfg(test)");
  assert.deepEqual(counts(cargoTally(`+${gate}\n pub fn product() {`)).gates, [], "a gate on product code is not a test switched off");
  assert.deepEqual(counts(cargoTally(`+${gate}\n mod network {`)).gates, []);
  assert.deepEqual(counts(cargoTally("+#![cfg(unix)]")).gates, [], "a file-level gate on a source file gates the product too");
  // In a test file: every gate, the file-level one included.
  assert.deepEqual(counts(cargoTally(`+${gate}\n pub fn helper() {`, { testFile: true })).gates, ['feature = "slow-tests"']);
  assert.deepEqual(counts(cargoTally('+#![cfg(feature = "server")]', { testFile: true })).gates, ['feature = "server"']);
  // `cfg(test)` itself is not a gate, and a gate changed and changed back nets out.
  assert.deepEqual(counts(cargoTally("+#[cfg(test)]\n+mod tests {")).gates, []);
  assert.deepEqual(counts(cargoTally(`-${gate}\n+${gate}\n #[test]\n fn slow() {`)).gates, []);
  assert.deepEqual(counts(cargoTally('-#[cfg(unix)]\n+#[cfg(any())]\n #[test]\n fn x() {')).gates, ["any()"]);
  // What counts in a NEW file: only a predicate nothing satisfies.
  for (const never of ["any()", "false", "not(all())", "not(true)", " any( ) "]) assert.equal(cargoNeverRuns(never), true, never);
  for (const maybe of ["unix", 'feature = "x"', "any(unix)", "not(any())", "all()", "true", "falsey"]) assert.equal(cargoNeverRuns(maybe), false, maybe);
  assert.equal(cargoTally("+#[cfg(any())]\n+#[test]\n+fn x() {", { status: "added" }).newFile, true);
});

test("cargoTally: an inner cfg at the top of a test module, a cfg over several lines, a repointed path", () => {
  // ONE added line, and every test in the module is gone from every lane.
  assert.deepEqual(counts(cargoTally(" #[cfg(test)]\n mod tests {\n+    #![cfg(any())]\n     use super::*;")).gates, ["any()"]);
  assert.deepEqual(counts(cargoTally(" mod merge_tests {\n \n+    #![cfg(feature = \"slow\")]")).gates, ['feature = "slow"'], "by the module's name, over a blank line");
  assert.deepEqual(counts(cargoTally(" #[cfg(test)]\n mod scenarios {\n+    #![cfg(unix)]")).gates, ["unix"], "by the cfg(test) above");
  assert.deepEqual(counts(cargoTally(" mod network {\n+    #![cfg(unix)]")).gates, [], "a product module's inner gate");
  assert.deepEqual(counts(cargoTally("+#![cfg(unix)]\n mod tests {")).gates, [], "above the module it is the FILE's gate");
  assert.deepEqual(counts(cargoTally(" mod tests {\n+    #![cfg(test)]")).gates, []);
  // The attribute broken over lines, as rustfmt leaves a long one.
  const broken = ["-#[cfg(test)]", "+#[cfg(all(", "+    test,", "+    not(test)", "+))]", " mod tests {"].join("\n");
  assert.deepEqual(counts(cargoTally(broken)).gates, ["all(test, not(test))"]);
  assert.deepEqual(counts(cargoTally(["+#[cfg(any(", '+    feature = "a",', '+    feature = "b",', "+))]", " #[test]", " fn x() {"].join("\n"))).gates, ['any(feature = "a", feature = "b",)']);
  assert.deepEqual(counts(cargoTally(["+#[cfg(any(", "+    unix,", "+))]", " pub fn product() {"].join("\n"))).gates, [], "on product code");
  assert.deepEqual(counts(cargoTally(["+#[cfg(all(", "+    test,", "+))]", " mod tests {"].join("\n"))).gates, ["all(test,)"]);
  // Broken the same way on both sides nets out, as one line does.
  assert.deepEqual(counts(cargoTally(["-#[cfg(all(", "-    unix,", "-))]", "+#[cfg(all(", "+    unix,", "+))]", " #[test]", " fn x() {"].join("\n"))).gates, []);
  // An attribute that never closes in the hunk is not guessed at.
  assert.deepEqual(counts(cargoTally("+#[cfg(all(\n+    test,\n mod tests {")).gates, []);
  // `#[path]` put on a test module that was already declared.
  assert.deepEqual(counts(cargoTally(' #[cfg(test)]\n+#[path = "history/empty_tests.rs"]\n mod depth_tests;')).gates, ["path history/empty_tests.rs for mod depth_tests"]);
  assert.deepEqual(counts(cargoTally('-#[path = "a_tests.rs"]\n+#[path = "b_tests.rs"]\n mod tests;')).gates, ["path b_tests.rs for mod tests"]);
  assert.deepEqual(counts(cargoTally('-#[cfg(test)]\n-mod depth_tests;\n+#[cfg(test)]\n+#[path = "empty.rs"]\n+mod depth_tests;')).gates, ["path empty.rs for mod depth_tests"], "the declaration rewritten whole");
  // Written with a NEW declaration, it is where the new module lives.
  assert.deepEqual(counts(cargoTally('+#[cfg(test)]\n+#[path = "reissue_tests.rs"]\n+mod tests;')).gates, []);
  assert.deepEqual(counts(cargoTally(' #[cfg(test)]\n-#[path = "a_tests.rs"]\n mod tests;')).gates, [], "taken off");
  assert.deepEqual(counts(cargoTally('+#[path = "platform/unix.rs"]\n mod platform;')).gates, [], "a product module");
  assert.equal(cargoNeverRuns("path x.rs for mod tests"), false, "not counted in a file that is new");
});

test("testRegions with anyDepth: nested test modules, for the lints only", () => {
  const src = "mod outer {\n    pub fn p() {}\n    #[cfg(test)]\n    mod tests {\n        fn t() {}\n    }\n}\n#[cfg(test)]\nmod tests {}\n";
  assert.equal(testRegions(src).regions.length, 1, "the scope question sees the top level only");
  assert.deepEqual(testRegions(src, { anyDepth: true }).regions.map((r) => [r.startLine, r.endLine]), [[3, 6], [8, 9]]);
  const inside = testRegionLines(src, { anyDepth: true });
  assert.deepEqual([2, 3, 5, 6, 7, 8].map(inside), [false, true, true, true, false, true]);
  assert.equal(testRegionLines(src)(5), false);
  // The product is still judged at the top level: the nested module is product to the scope rule.
  assert.equal(testOnlyChange(src, src.replace("fn t() {}", "fn t() { let _ = 1; }")), false);
});

test("cargoTally: a removed test-module declaration is a suite out of reach", () => {
  // The file `counter_tests.rs` still exists and still looks tested.
  assert.deepEqual(counts(cargoTally(" mod counter;\n-#[cfg(test)]\n-mod counter_tests;\n mod merge;")).gates, ["mod counter_tests"]);
  assert.deepEqual(counts(cargoTally("-mod tests;")).gates, ["mod tests"], "by name alone");
  assert.deepEqual(counts(cargoTally("-pub(crate) mod merge_test;")).gates, ["mod merge_test"]);
  // A name that does not say "test": the cfg(test) line above is what says it.
  assert.deepEqual(counts(cargoTally(" #[cfg(test)]\n-mod sync_scenarios;")).gates, ["mod sync_scenarios"]);
  assert.deepEqual(counts(cargoTally("-#[cfg(test)] mod sync_scenarios;")).gates, ["mod sync_scenarios"]);
  assert.deepEqual(counts(cargoTally("-mod sync_scenarios;")).gates, [], "nothing in the patch says it is a test module");
  assert.deepEqual(counts(cargoTally("-mod network;\n-pub mod testing;")).gates, []);
  // Moved within the file, or removed and put back: nets out.
  assert.deepEqual(counts(cargoTally("-#[cfg(test)]\n-mod counter_tests;\n mod merge;\n+#[cfg(test)]\n+mod counter_tests;")).gates, []);
  // An ADDED declaration is not a switch-off, and does not cancel another's removal.
  assert.deepEqual(counts(cargoTally("+#[cfg(test)]\n+mod new_tests;\n-#[cfg(test)]\n-mod old_tests;")).gates, ["mod old_tests"]);
  // An inline module is not a declaration.
  assert.deepEqual(counts(cargoTally("-mod tests {")).gates, []);
  assert.equal(cargoNeverRuns("mod counter_tests"), true, "the marker counts in a new file too");
});

test("cargoTally: a doc-test fence switched off, not one that never ran", () => {
  const off = (from, to) => cargoTally(`-    /// ${from}\n+    /// ${to}`).offNet;
  assert.equal(off("```", "```ignore"), 1);
  assert.equal(off("```rust", "```no_run"), 1);
  assert.equal(off("```", "```compile_fail"), 1);
  assert.equal(off("```", "```should_panic"), 1);
  assert.equal(off("```", "```text"), 1, "a non-Rust fence is not compiled at all");
  assert.equal(off("```rust", "```rust,ignore"), 1);
  assert.equal(off("```", "```ignore-wasm32"), 1);
  assert.equal(off("```ignore", "```"), -1, "switched back on");
  assert.equal(off("```", "```rust"), 0);
  assert.equal(off("```", "```edition2021"), 0);
  assert.equal(off("```ignore", "```no_run"), 0, "off before, off after");
  assert.equal(cargoTally("-//! ```\n+//! ```ignore").offNet, 1, "inner doc comments too");
  // A NEW example that was never meant to run: an off-fence gained, no
  // running one lost.
  assert.equal(cargoTally("+/// ```text\n+/// a diagram\n+/// ```").offNet, 0);
  assert.equal(cargoTally("+/// ```ignore\n+/// let x = pseudo();\n+/// ```").offNet, 0);
  // An ignored example deleted whole is not "switched back on".
  assert.equal(cargoTally("-/// ```ignore\n-/// let x = pseudo();\n-/// ```").offNet, 0);
  // A fence in ordinary code or an ordinary comment is not a doc-test.
  assert.equal(cargoTally("-// ```\n+// ```ignore\n-let s = \"```\";\n+let s = \"```ignore\";").offNet, 0);
});

// --- Cargo.toml --------------------------------------------------------------

test("cargoManifestTally: the switches that stop tests from being built or run", () => {
  const net = (patch) => cargoManifestTally(patch).offNet;
  assert.equal(net(" [lib]\n+test = false"), 1);
  assert.equal(net(" [lib]\n+doctest = false"), 1);
  assert.equal(net(" [package]\n name = \"x\"\n+autotests = false"), 1);
  assert.equal(net(" [[test]]\n name = \"integration\"\n+harness = false"), 1);
  assert.equal(net("-test = true\n+test = false"), 1);
  assert.equal(net("-doctest = false"), -1, "switched back on");
  assert.equal(net("-doctest = false\n+doctest = true"), -1);
  assert.equal(net("-test = false   # slow\n+test = false"), 0, "re-worded");
  // Where `false` is the default or the norm, when the patch shows the table.
  assert.equal(net(" [[bench]]\n name = \"merge\"\n+harness = false"), 0);
  assert.equal(net("+[[bench]]\n+name = \"merge\"\n+harness = false\n+test = false"), 0);
  assert.equal(net(" [[example]]\n name = \"quickstart\"\n+test = false"), 0);
  assert.equal(net(" [[bench]]\n name = \"b\"\n harness = false\n \n [lib]\n+doctest = false"), 1, "the nearest table is what counts");
  // Not a switch.
  assert.equal(net("+test = true\n+autobenches = false\n+# test = false\n+testing = false\n+default-features = false"), 0);
  const t = cargoManifestTally("+test = false", { status: "added" });
  assert.deepEqual([t.removed, t.added, t.newFile], [0, 0, true]);
});

test("cargoManifestTally: a workspace member taken off the list", () => {
  const gone = (patch) => {
    const t = cargoManifestTally(patch);
    return [...t.buildsAdded].filter((e) => !t.buildsRemoved.has(e));
  };
  const list = [" [workspace]", " members = [", '     "crates/core",', '-    "crates/client",', '     "tools/gen",', " ]"].join("\n");
  assert.deepEqual(gone(list), ["member crates/client"]);
  // The opening line is not in the hunk; git's heading names it.
  assert.deepEqual(gone('@@ -8,5 +8,4 @@ members = [\n     "crates/e",\n-    "crates/f",\n     "crates/g",'), ["member crates/f"]);
  // One line.
  assert.deepEqual(gone('-members = ["a", "b", "c"]\n+members = ["a", "c"]'), ["member b"]);
  assert.deepEqual(gone("-members = ['a', 'b']\n+members = ['a']"), ["member b"]);
  // Renamed or reordered: what was removed and added back nets out, and what
  // was only added is not a switch-off.
  assert.deepEqual(gone(' members = [\n-    "crates/a",\n     "crates/b",\n+    "crates/a",\n+    "crates/new",\n ]'), []);
  // Other lists are not the member list.
  assert.deepEqual(gone(' default-members = [\n-    "crates/client",\n ]'), []);
  assert.deepEqual(gone(' exclude = [\n-    "crates/client",\n ]'), []);
  assert.deepEqual(gone(' features = [\n-    "macros",\n ]'), []);
  assert.deepEqual(gone('@@ -30,3 +30,2 @@ tokio = { version = "1", features = [\n-    "macros",'), []);
  // The heading says where the hunk starts, and the list may have ended just
  // above it: a line that is not an entry ends the list.
  assert.deepEqual(gone('@@ -9,4 +9,3 @@ members = [\n resolver = "2"\n-edition = "2021"\n license = "MIT"'), []);
  assert.deepEqual(gone(' members = [\n     "a",\n ]\n-resolver = "2"'), []);
  // A new table ends it too.
  assert.deepEqual(gone(' members = [\n     "a",\n [workspace.package]\n-version = "0.1.0"'), []);
  assert.equal(cargoNeverRuns("member crates/client"), true);
});
