// The `cargo` test rule set: what a Rust test, a switched-off Rust test and a
// test-only change look like.
//
// WHY RUST NEEDS MORE THAN A PATH. The Go and JavaScript rules ask one
// question of a path — "is this FILE a test?" — and everything else follows.
// Rust keeps most unit tests inside the file they test, in a
// `#[cfg(test)] mod tests { … }` at the bottom, so that question has no
// answer: the file is product code AND tests. Two models replace it, one per
// kind of consumer, and they are deliberately different:
//
//   THE REMOVAL RECORD (`test-removals.mjs`) reads PATCHES, and nothing else.
//   It needs no notion of where a test lives, because what it counts
//   identifies itself: `#[test]` and its family, `#[ignore]`,
//   `#[should_panic]` are attributes that occur only on tests. So every
//   `.rs` patch is tallied, whatever the file is. The few things that do not
//   identify themselves — a `#[cfg(…)]` gate, a `mod x;` line — are counted
//   only where the patch itself shows a test beside them (`cargoTally`).
//   It is evidence for an adjudicator, not a gate, and what a patch alone
//   cannot show is listed under "WHAT A PATCH ALONE CANNOT SHOW" below.
//
//   THE FIX GUARD (`fix-guard.mjs`) decides whether a fix round may touch a
//   file outside the pull request's diff, and that is a gate: a wrong "yes"
//   lets product code change unreviewed. A patch cannot carry that decision
//   — a hunk in the middle of a long `mod tests` shows three lines of
//   context and no `mod` header, and its `@@` heading is whatever line git's
//   function-name heuristic picked. So the guard reads the WHOLE FILE, before
//   and after the commit, and asks one question (`testOnlyChange`):
//
//       with every test region taken out, is the file the same tokens?
//
//   A TEST REGION is, at the top level of the file and nowhere else:
//
//       #[cfg(test)] <more outer attributes>* <visibility>? mod NAME { … }
//       #[cfg(test)] <more outer attributes>* <visibility>? mod NAME ;
//
//   together with the doc comments and own-line comments directly above it.
//   The second form is the declaration that reaches a `NAME.rs` /
//   `NAME_tests.rs` file, so a new test file's one line in its parent module
//   is a test region too. Everything outside the regions is the product, and
//   it must come out token for token identical. Whitespace is not a token;
//   comments and doc comments are. One thing whitespace decides is kept:
//   whether two punctuation characters touch. `a && b` and `a & &b` are the
//   same characters and different programs — the second evaluates both sides
//   — so a punctuation token carries whether the next one is right against
//   it, which is exactly what Rust's lexer tells its parser. The cost is a
//   refusal where the gap means nothing (`Vec<Vec<T>>` to `Vec<Vec<T> >`).
//
//   That one comparison is what makes these all out of scope, without a rule
//   for each:
//     - `#[cfg(any(test, feature = "x"))]`, `#[cfg(all(test, …))]`,
//       `#[cfg_attr(test, …)]`: not the exact attribute, so not a region —
//       the first is compiled into a release build with the feature on;
//     - a `mod tests { … }` with no `#[cfg(test)]`: compiled always;
//     - a region inside a macro invocation, a `macro_rules!` body, a function
//       or another module: only the TOP LEVEL counts, because a macro is
//       handed its tokens and may emit them without the attribute;
//     - an attribute BEFORE `#[cfg(test)]` (`#[some_macro] #[cfg(test)] mod`):
//       attributes apply in order, so that macro runs first and receives the
//       `cfg` as input. Not a region;
//     - a macro defined in the region and invoked outside it: the invocation
//       is a new token in the product;
//     - moving the region's closing brace down to swallow product code: the
//       swallowed tokens are gone from the product;
//     - inserting a region between an attribute and the item it was on
//       (`#[cfg(unix)]` / `fn product()`): the region then follows an
//       attribute, so it is not one;
//     - `#[cfg(test)]` on anything but a module (a `fn`, a `use`, an `impl`):
//       harmless to a release build, and still refused, because delimiting an
//       arbitrary item is a parser this file does not have.
//   A file that does not lex (an unterminated string, unbalanced brackets)
//   has no regions at all.
//
//   WHAT THIS STILL TRUSTS, stated because it is a gate:
//     - that the file is compiled as a module. A file pulled in with
//       `include!` inside a macro, or declared under a procedural-macro
//       attribute, could have its `#[cfg(test)]` stripped by that macro;
//     - that `test` means the test configuration: `--cfg test` passed to a
//       release build by a build script or `RUSTFLAGS` would compile the
//       regions in. Both live in CI-defining paths;
//     - the lexer below. It knows comments (nested), strings, raw strings,
//       byte and C strings, character literals and lifetimes, which is every
//       way Rust source can hold a bracket that is not a bracket.
//   And what it refuses although it is harmless (each pages a human): a
//   `#[test]` function outside any `#[cfg(test)]` module, test regions in a
//   nested module, and a new region placed right under a pre-existing
//   own-line comment (the comment moves into the region).
//
// WHAT A PATCH ALONE CANNOT SHOW (the removal record's blind spots):
//   - a test made by a macro or by a loop over a matrix: the `#[test]` line
//     is the unit, like Go's table rows;
//   - a test gutted by an early `return` — the idiom for "skip when the
//     server is not there" — or by deleting its assertions (the fix guard's
//     assertion lint sees the second);
//   - a `#[cfg(…)]` gate or a removed `mod x;` whose test is further away
//     than the lines the patch shows around it, and a test module whose name
//     does not say "test" and whose `#[cfg(test)]` line is not in the hunk;
//   - `#[test]` written in a string or a macro body counts as a case;
//   - doc-tests are not counted as cases (an opening fence cannot be told
//     from a closing one on one line); a fence SWITCHED to `ignore`,
//     `no_run`, `compile_fail`, `should_panic` or a non-Rust language is.
//
// This module reads no configuration and no repository: every function is a
// function of the text it is given, so it is tested directly.

const str = (v) => (typeof v === "string" ? v : "");

// --- a Rust lexer, as far as brackets need -----------------------------------

const IDENT = /[\p{L}\p{Nl}_][\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}]*/uy;
const NUMBER = /[0-9][0-9A-Za-z_]*(?:\.[0-9][0-9A-Za-z_]*)?/y;
const OPEN = { "{": "}", "(": ")", "[": "]" };
const CLOSE = new Set(["}", ")", "]"]);

/**
 * Rust source as tokens `{ k, t, line, endLine }`, or `null` when it does not
 * lex. Kinds: `w` a word (identifier, keyword, number), `p` one punctuation
 * character, `s` a string or character literal, `l` a lifetime, `c` a
 * comment, `d` an OUTER doc comment (`///`, `/** … *\/` — an attribute on the
 * item below), `D` an inner one (`//!`, `/*! … *\/`). Whitespace is dropped,
 * except for the one thing it decides: a punctuation token is `joint` when
 * the next character is punctuation too, with nothing between them. That is
 * how Rust's own lexer hands punctuation to its parser — one character at a
 * time, each marked joint or alone — so two sources with the same tokens and
 * the same marks are the same program, and `a && b` is not `a & &b`.
 *
 * It is exact about the one thing its callers need: which `{`, `(`, `[` are
 * brackets. It does not check that the program is valid Rust.
 */
export function rustTokens(src) {
  if (typeof src !== "string") return null;
  const s = src;
  const n = s.length;
  const out = [];
  let i = 0;
  let line = 1;
  const emit = (k, from, to) => {
    const startLine = line;
    for (let p = from; p < to; p++) if (s.charCodeAt(p) === 10) line++;
    // JOINT: a punctuation character with another one right against it.
    // Rust reads `&&` as one operator and `& &` as two, and which it is
    // depends on nothing but this, so it is part of what the token IS.
    const prev = out[out.length - 1];
    if (k === "p" && prev !== undefined && prev.k === "p" && prev.end === from) prev.joint = true;
    out.push({ k, t: s.slice(from, to), line: startLine, endLine: line, end: to, joint: false });
    i = to;
  };
  /** The index after the `"` that closes a string opened at `at`, or -1. */
  const stringEnd = (at) => {
    for (let p = at + 1; p < n; p++) {
      if (s[p] === "\\") p++;
      else if (s[p] === '"') return p + 1;
    }
    return -1;
  };
  /** The index after a character literal opened at `at`, or -1 when it is not one. */
  const charEnd = (at) => {
    if (s[at + 1] === "\\") {
      let p = at + 3;
      while (p < n && s[p] !== "'" && s[p] !== "\n") p++;
      return s[p] === "'" ? p + 1 : -1;
    }
    const cp = s.codePointAt(at + 1);
    if (cp === undefined || cp === 10 || cp === 39) return -1;
    const after = at + 1 + (cp > 0xffff ? 2 : 1);
    return s[after] === "'" ? after + 1 : -1;
  };
  while (i < n) {
    const ch = s[i];
    if (ch === "\n") {
      line++;
      i++;
      continue;
    }
    if (/\s/.test(ch)) {
      i++;
      continue;
    }
    if (ch === "/" && s[i + 1] === "/") {
      let j = s.indexOf("\n", i);
      if (j < 0) j = n;
      const body = s.slice(i, j);
      const kind = body.startsWith("///") && !body.startsWith("////") ? "d" : body.startsWith("//!") ? "D" : "c";
      emit(kind, i, j);
      continue;
    }
    if (ch === "/" && s[i + 1] === "*") {
      let depth = 1;
      let j = i + 2;
      while (j < n && depth > 0) {
        if (s[j] === "/" && s[j + 1] === "*") {
          depth++;
          j += 2;
        } else if (s[j] === "*" && s[j + 1] === "/") {
          depth--;
          j += 2;
        } else j++;
      }
      if (depth > 0) return null;
      const body = s.slice(i, j);
      const kind = body.startsWith("/**") && !body.startsWith("/***") && body !== "/**/" ? "d" : body.startsWith("/*!") ? "D" : "c";
      emit(kind, i, j);
      continue;
    }
    if (ch === '"') {
      const j = stringEnd(i);
      if (j < 0) return null;
      emit("s", i, j);
      continue;
    }
    if (ch === "'") {
      const j = charEnd(i);
      if (j > 0) {
        emit("s", i, j);
        continue;
      }
      IDENT.lastIndex = i + 1;
      const m = IDENT.exec(s);
      // A lifetime or a loop label; a quote with neither is left as punctuation.
      emit(m ? "l" : "p", i, m ? IDENT.lastIndex : i + 1);
      continue;
    }
    IDENT.lastIndex = i;
    const word = IDENT.exec(s);
    if (word) {
      const j = IDENT.lastIndex;
      const w = word[0];
      if ((w === "r" || w === "br" || w === "cr") && (s[j] === '"' || s[j] === "#")) {
        let h = j;
        while (s[h] === "#") h++;
        if (s[h] === '"') {
          const close = `"${"#".repeat(h - j)}`;
          const end = s.indexOf(close, h + 1);
          if (end < 0) return null;
          emit("s", i, end + close.length);
          continue;
        }
        if (w === "r" && h === j + 1) {
          // A raw identifier, `r#type`.
          IDENT.lastIndex = h;
          if (IDENT.exec(s)) {
            emit("w", i, IDENT.lastIndex);
            continue;
          }
        }
      } else if ((w === "b" || w === "c") && s[j] === '"') {
        const end = stringEnd(j);
        if (end < 0) return null;
        emit("s", i, end);
        continue;
      } else if (w === "b" && s[j] === "'") {
        const end = charEnd(j);
        if (end < 0) return null;
        emit("s", i, end);
        continue;
      }
      emit("w", i, j);
      continue;
    }
    NUMBER.lastIndex = i;
    if (NUMBER.exec(s)) {
      emit("w", i, NUMBER.lastIndex);
      continue;
    }
    const cp = s.codePointAt(i);
    emit("p", i, i + (cp > 0xffff ? 2 : 1));
  }
  return out;
}

const isComment = (tok) => tok.k === "c";
const is = (tok, k, t) => tok !== undefined && tok.k === k && tok.t === t;

/** The index of the bracket that closes the one at `at`, or -1. */
function closeOf(toks, at) {
  const stack = [];
  for (let i = at; i < toks.length; i++) {
    const tok = toks[i];
    if (tok.k !== "p") continue;
    if (Object.hasOwn(OPEN, tok.t)) stack.push(OPEN[tok.t]);
    else if (CLOSE.has(tok.t)) {
      if (stack.pop() !== tok.t) return -1;
      if (stack.length === 0) return i;
    }
  }
  return -1;
}

/**
 * The test region that starts with the `#` at `at`, or `null`. See the top of
 * the file for the two forms and for why each refusal is one.
 */
function regionAt(toks, at) {
  /** The next token that is not a comment or an outer doc comment. */
  const next = (j) => {
    let k = j + 1;
    while (k < toks.length && (isComment(toks[k]) || toks[k].k === "d")) k++;
    return k;
  };
  let j = at;
  for (const [k, t] of [["p", "#"], ["p", "["], ["w", "cfg"], ["p", "("], ["w", "test"], ["p", ")"], ["p", "]"]]) {
    if (!is(toks[j], k, t)) return null;
    j = next(j);
  }
  // More OUTER attributes may follow: `cfg` is applied first, so whatever
  // they are, the item is gone from a release build before they run.
  while (is(toks[j], "p", "#")) {
    const open = next(j);
    if (!is(toks[open], "p", "[")) return null;
    const close = closeOf(toks, open);
    if (close < 0) return null;
    j = next(close);
  }
  if (is(toks[j], "w", "pub")) {
    j = next(j);
    if (is(toks[j], "p", "(")) {
      const close = closeOf(toks, j);
      if (close < 0) return null;
      j = next(close);
    }
  }
  if (!is(toks[j], "w", "mod")) return null;
  j = next(j);
  if (toks[j]?.k !== "w") return null;
  const name = toks[j].t;
  j = next(j);
  let to;
  let kind;
  if (is(toks[j], "p", ";")) {
    to = j;
    kind = "declaration";
  } else if (is(toks[j], "p", "{")) {
    to = closeOf(toks, j);
    if (to < 0) return null;
    kind = "inline";
  } else {
    return null;
  }
  // The doc comments and own-line comments directly above belong to the
  // region: "// Regression tests for …" over a new module is not a change to
  // the product. A comment trailing the line above is that line's.
  let from = at;
  while (from > 0) {
    const prev = toks[from - 1];
    const ownLine = from - 1 === 0 || prev.line > toks[from - 2].endLine;
    if (prev.k === "d" || (isComment(prev) && ownLine)) from--;
    else break;
  }
  // An attribute before `#[cfg(test)]` runs before it. The token before the
  // region must not close one: at the top level a `]` closes nothing else.
  let before = from - 1;
  while (before >= 0 && isComment(toks[before])) before--;
  if (before >= 0 && is(toks[before], "p", "]")) return null;
  return { from, to, kind, name, startLine: toks[from].line, endLine: toks[to].endLine };
}

/**
 * `{ tokens, regions }` for a Rust source file, or `null` when it does not lex
 * or its brackets do not balance. `regions` are the top-level test regions, in
 * order, as token index ranges and 1-based line ranges, both inclusive.
 *
 * `anyDepth` also takes a `#[cfg(test)]` module nested in another item. That
 * is for the LINTS, which ask "is this line test code" and warn: a nested
 * test module is test code to them. It is never for the scope question,
 * where whatever encloses the module may be a macro.
 */
export function testRegions(src, { anyDepth = false } = {}) {
  const tokens = rustTokens(src);
  if (!tokens) return null;
  const regions = [];
  const stack = [];
  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    if (tok.k !== "p") continue;
    if (Object.hasOwn(OPEN, tok.t)) stack.push(OPEN[tok.t]);
    else if (CLOSE.has(tok.t)) {
      if (stack.pop() !== tok.t) return null;
    } else if (tok.t === "#" && (anyDepth || stack.length === 0)) {
      const region = regionAt(tokens, i);
      if (region) {
        regions.push(region);
        i = region.to;
      }
    }
  }
  if (stack.length > 0) return null;
  return { tokens, regions };
}

/** The file's tokens outside every test region — the product — or `null`. */
export function productTokens(src) {
  const parsed = testRegions(src);
  if (!parsed) return null;
  const out = [];
  let r = 0;
  for (let i = 0; i < parsed.tokens.length; i++) {
    while (r < parsed.regions.length && parsed.regions[r].to < i) r++;
    if (r < parsed.regions.length && parsed.regions[r].from <= i) continue;
    const tok = parsed.tokens[i];
    out.push(`${tok.joint ? "j" : tok.k}${tok.t}`);
  }
  return out;
}

/**
 * Did a change to a Rust file touch test regions only? `before` and `after`
 * are the whole file on each side. False whenever that cannot be SHOWN: a
 * side that is missing (the file was added, deleted, or could not be read), a
 * side that does not lex, or any difference in the product's tokens.
 */
export function testOnlyChange(before, after) {
  if (typeof before !== "string" || typeof after !== "string") return false;
  const a = productTokens(before);
  const b = productTokens(after);
  if (!a || !b || a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/**
 * Does this parent module declare `name` as a test module, and as nothing
 * else? True when the source has a top-level `#[cfg(test)] mod name;` with no
 * `#[path]` on it, and no `mod name` anywhere in its product. It is what
 * makes a file called `name.rs` test code: a file is compiled because a `mod`
 * line reaches it, and compiled into the product when that line has no
 * `cfg(test)`. False for a source that is missing or does not lex.
 */
export function declaresTestModule(src, name) {
  const parsed = testRegions(src);
  if (!parsed) return false;
  const declared = parsed.regions.some((r) =>
    r.kind === "declaration" && r.name === name && !parsed.tokens.slice(r.from, r.to + 1).some((t) => t.k === "w" && t.t === "path"));
  if (!declared) return false;
  let r = 0;
  for (let i = 0; i + 1 < parsed.tokens.length; i++) {
    while (r < parsed.regions.length && parsed.regions[r].to < i) r++;
    if (r < parsed.regions.length && parsed.regions[r].from <= i) continue;
    if (is(parsed.tokens[i], "w", "mod") && is(parsed.tokens[i + 1], "w", name)) return false;
  }
  return true;
}

/** A predicate over 1-based line numbers: is this line inside a test region? `null` when the file does not lex. `{ anyDepth: true }` counts nested test modules, for the lints. */
export function testRegionLines(src, options = {}) {
  const parsed = testRegions(src, options);
  if (!parsed) return null;
  return (line) => parsed.regions.some((r) => r.startLine <= line && line <= r.endLine);
}

// --- patches -----------------------------------------------------------------

const HUNK = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@ ?(.*)$/;

/**
 * A unified diff's lines, hunk by hunk: `{ sign, text, oldLine, newLine, hunk,
 * heading }`. `sign` is `+`, `-` or a space; `oldLine`/`newLine` are the
 * line's 1-based number on the side(s) it exists on, or 0 when the patch has
 * no hunk header to count from; `heading` is what git printed after the
 * second `@@`.
 */
export function patchLines(patch) {
  const out = [];
  let oldLine = 0, newLine = 0, hunk = -1, heading = "", numbered = false;
  const raws = str(patch).split("\n");
  // The text after the last newline is not a line (and an empty patch has none).
  if (raws.at(-1) === "") raws.pop();
  for (const raw of raws) {
    const h = HUNK.exec(raw);
    if (h) {
      oldLine = Number(h[1]);
      newLine = Number(h[2]);
      heading = h[3];
      numbered = true;
      hunk++;
      continue;
    }
    if (raw.startsWith("\\")) continue;
    if (hunk < 0) {
      if (raw.startsWith("---") || raw.startsWith("+++")) continue;
      hunk = 0;
    }
    const sign = raw[0] === "+" || raw[0] === "-" ? raw[0] : " ";
    const line = { sign, text: raw.slice(1), oldLine: 0, newLine: 0, hunk, heading };
    if (numbered) {
      if (sign !== "+") line.oldLine = oldLine++;
      if (sign !== "-") line.newLine = newLine++;
    }
    out.push(line);
  }
  return out;
}

// --- the removal record's tally ----------------------------------------------

// A CASE is the test attribute, not the `fn` under it: `#[test]`,
// `#[tokio::test]`, `#[tokio::test(flavor = "multi_thread")]` and any
// `path::test`; `#[rstest]`; and the `*_test` attribute macros
// (`#[wasm_bindgen_test]`). The name must be followed by `]` or `(`, so
// `#[test_case(…)]` rows are not cases. A commented-out `// #[test]` stops
// matching, so commenting a test out counts as removing it.
const CASE = /^\s*#\[(?:\w+::)*(?:test|rstest|\w+_test)\s*[\](]/;
// Switched off in place. The closing bracket is not asked for: a reason may
// run over several lines (`#[ignore = "known limitation … \`).
const IGNORE = /^\s*#\[(?:ignore\b|cfg_attr\(.*\bignore\b)/;
// Inverted rather than disabled: on a test that fails, it is how the test
// goes green without the code changing.
const SHOULD_PANIC = /^\s*#\[should_panic\b/;
const CFG = /^\s*#(!?)\[cfg\((.*)\)\]\s*$/;
// The same attribute broken over lines: `#[cfg(all(` … `))]`.
const CFG_OPEN = /^\s*#(!?)\[cfg\((.*)$/;
const CFG_CLOSE = /^(.*)\)\]\s*$/;
const PATH_ATTR = /^\s*#\[path\s*=\s*"([^"]*)"\s*\]/;
const MOD_OPEN = /^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+(\w+)\s*\{\s*$/;
const CFG_TEST = /^\s*#\[cfg\(\s*test\s*\)\]/;
const MOD_DECL = /^\s*(#\[cfg\(\s*test\s*\)\]\s*)?(?:pub(?:\([^)]*\))?\s+)?mod\s+(\w+)\s*;/;
const ITEM = /^\s*(?:pub(?:\([^)]*\))?\s+)?(?:(?:async|const|unsafe|default|extern(?:\s+"[^"]*")?)\s+)*(fn|mod|struct|enum|union|trait|impl|type|static|use|macro_rules!)\b\s*(\w+)?/;
const TEST_MODULE_NAME = /^(?:tests?|\w+_tests?)$/;
const DOC_FENCE = /^\s*\/\/[/!]\s*(?:```+|~~~+)\s*(.*?)\s*$/;
// What rustdoc reads as "this fence is Rust"; anything else makes it text.
const RUST_FENCE_TAG = /^(?:rust|should_panic|no_run|ignore(?:-.+)?|compile_fail|edition\d{4}|standalone_crate|test_harness|E\d{4})$/;
const FENCE_OFF_TAG = /^(?:should_panic|no_run|ignore(?:-.+)?|compile_fail)$/;

/** A `cfg` predicate that is false in every build: a test born under it never ran. */
const NEVER_TRUE = /^\s*(?:any\(\s*\)|false|not\(\s*(?:all\(\s*\)|true)\s*\))\s*$/;
/**
 * In a NEW file, which gates count as a switch-off: a predicate nothing can
 * satisfy, and the two markers below, which are not predicates at all.
 */
export const cargoNeverRuns = (expr) => /^(?:mod|member) /.test(expr) || NEVER_TRUE.test(expr);

/**
 * The attribute lines a patch shows around line `k` of `lines`, and the item
 * they are on: `{ cases: Set<sign>, cfgTest, item, name }`. Read from the
 * hunk only — up to the blank line or the end of the item above, down to the
 * `fn`/`mod` line below — so it is as good as the patch's context is.
 */
function attributeRun(lines, k) {
  const seen = { cases: new Set(), cfgTest: false, item: "", name: "", itemSign: "" };
  const note = (line) => {
    if (CASE.test(line.text)) seen.cases.add(line.sign);
    if (CFG_TEST.test(line.text)) seen.cfgTest = true;
  };
  note(lines[k]);
  for (let i = k - 1, steps = 0; i >= 0 && steps < 12 && lines[i].hunk === lines[k].hunk; i--, steps++) {
    const text = lines[i].text.trim();
    if (text === "" || /[;{}]$/.test(text) || ITEM.test(lines[i].text)) break;
    note(lines[i]);
  }
  for (let i = k + 1, steps = 0; i < lines.length && steps < 12 && lines[i].hunk === lines[k].hunk; i++, steps++) {
    const item = ITEM.exec(lines[i].text);
    if (item) {
      seen.item = item[1];
      seen.name = item[2] ?? "";
      seen.itemSign = lines[i].sign;
      break;
    }
    if (lines[i].text.trim() === "}") break;
    note(lines[i]);
  }
  return seen;
}

/** A doc fence's info string → `"off"` (rustdoc will not run it as written), `"runs"`, or `""` for no fence. */
function fenceKind(text) {
  const m = DOC_FENCE.exec(text);
  if (!m) return "";
  const tags = m[1].split(/[\s,]+/).filter(Boolean);
  return tags.some((t) => FENCE_OFF_TAG.test(t) || !RUST_FENCE_TAG.test(t)) ? "off" : "runs";
}

/**
 * One Rust file's patch, tallied for the removal record. The shape is
 * `test-removals.mjs`'s own: cases `removed` and `added`, `offNet` (tests
 * switched off minus switched back on, signed), and the two gate sets, where
 * an entry in `buildsAdded` that is not in `buildsRemoved` is one suite off.
 *
 * `testFile`: the path says the whole file is test code, so every `cfg` gate
 * in it is a gate on tests. In any other file a gate counts only when the
 * patch shows a test attribute or a test module directly beside it.
 */
export function cargoTally(patch, { status = "", testFile = false } = {}) {
  const lines = patchLines(patch);
  let removed = 0, added = 0, offNet = 0;
  const builds = { "+": new Set(), "-": new Set() };
  const fences = { "+": { off: 0, runs: 0 }, "-": { off: 0, runs: 0 } };
  const consumed = new Set();
  lines.forEach((line, k) => {
    if (line.sign === " ") return;
    const sign = line.sign === "+" ? 1 : -1;
    const other = line.sign === "+" ? "-" : "+";
    if (CASE.test(line.text)) {
      if (sign < 0) removed++;
      else added++;
      return;
    }
    if (IGNORE.test(line.text) || SHOULD_PANIC.test(line.text)) {
      const run = attributeRun(lines, k);
      // Taken out WITH its test: the case is already counted as removed, and
      // an `-1` here would cancel a switch-off made elsewhere in the file.
      if (sign < 0 && run.cases.has("-")) return;
      // `#[should_panic]` written with a NEW test is that test's design, not
      // a way to turn a failing one green. `#[ignore]` on a new test IS
      // recorded: it is how a fixer keeps a reproducer it could not fix.
      if (sign > 0 && SHOULD_PANIC.test(line.text) && run.cases.has("+")) return;
      offNet += sign;
      return;
    }
    if (consumed.has(k)) return;
    let cfg = CFG.exec(line.text);
    let last = k;
    if (!cfg && CFG_OPEN.test(line.text) && !line.text.includes(")]")) {
      // An attribute over several lines of the same sign, up to its `)]`.
      const parts = [CFG_OPEN.exec(line.text)[2]];
      for (let i = k + 1; i < lines.length && i <= k + 12 && lines[i].sign === line.sign && lines[i].hunk === line.hunk; i++) {
        const close = CFG_CLOSE.exec(lines[i].text);
        parts.push(close ? close[1] : lines[i].text);
        if (close) {
          cfg = [null, CFG_OPEN.exec(line.text)[1], parts.join(" ")];
          last = i;
          break;
        }
      }
      if (cfg) for (let i = k + 1; i <= last; i++) consumed.add(i);
    }
    if (cfg) {
      const expr = cfg[2].replace(/\s+/g, " ").replace(/\( /g, "(").replace(/ \)/g, ")").trim();
      if (expr === "test") return;
      if (!testFile) {
        let onTest;
        if (cfg[1] === "!") {
          // An inner attribute gates what ENCLOSES it. As the first line of
          // a test module, one added line switches the whole module off.
          let up = k - 1;
          while (up >= 0 && lines[up].hunk === line.hunk && lines[up].text.trim() === "") up--;
          const open = up >= 0 && lines[up].hunk === line.hunk ? MOD_OPEN.exec(lines[up].text) : null;
          onTest = !!open && (TEST_MODULE_NAME.test(open[1]) || (up > 0 && lines[up - 1].hunk === line.hunk && CFG_TEST.test(lines[up - 1].text)));
        } else {
          const run = attributeRun(lines, last);
          onTest = run.cases.size > 0 || run.cfgTest || (run.item === "mod" && TEST_MODULE_NAME.test(run.name));
        }
        if (!onTest) return;
      }
      builds[line.sign].add(expr);
      return;
    }
    // `#[path = "…"]` on a test module that was already declared: the
    // module now means another file, and the old one is no longer compiled.
    // Written with a NEW declaration it is just where that module lives.
    const repoint = PATH_ATTR.exec(line.text);
    if (repoint) {
      const run = attributeRun(lines, k);
      if (run.item !== "mod" || !(run.cfgTest || TEST_MODULE_NAME.test(run.name))) return;
      const existing = run.itemSign !== "+" || lines.some((l) => l.hunk === line.hunk && l.sign === "-" && MOD_DECL.exec(l.text)?.[2] === run.name);
      if (!existing) return;
      builds[line.sign].add(`path ${repoint[1]} for mod ${run.name}`);
      return;
    }
    const mod = MOD_DECL.exec(line.text);
    if (mod) {
      const underCfgTest = !!mod[1] || (k > 0 && lines[k - 1].hunk === line.hunk && CFG_TEST.test(lines[k - 1].text));
      if (!underCfgTest && !TEST_MODULE_NAME.test(mod[2])) return;
      // "This module is out of reach" is the gate: a declaration REMOVED adds
      // it, one ADDED takes it away, so a line that only moved nets out.
      builds[other].add(`mod ${mod[2]}`);
      return;
    }
    const fence = fenceKind(line.text);
    if (fence) fences[line.sign][fence]++;
  });
  // A doc-test SWITCHED off is an off-fence gained where a running one was
  // lost. A new example that was never meant to run gains one and loses none.
  const gained = fences["+"].off - fences["-"].off;
  if (gained > 0) offNet += Math.min(gained, fences["-"].runs);
  else if (gained < 0) offNet -= Math.min(-gained, fences["+"].runs);
  return { removed, added, offNet, buildsAdded: builds["+"], buildsRemoved: builds["-"], newFile: status === "added", never: cargoNeverRuns };
}

const MANIFEST_SWITCH = /^\s*(test|doctest|harness|autotests)\s*=\s*false\b/;
const TABLE = /^\s*\[\[?\s*([\w-]+)[^\]]*\]\]?\s*(?:#.*)?$/;
const LIST_ENTRY = /^\s*(?:(?:"[^"]*"|'[^']*')\s*,?\s*)*\]?\s*,?\s*(?:#.*)?$/;
const MEMBERS = /^\s*members\s*=\s*\[/;
// Where `test = false` and `harness = false` are the default or the norm.
const NOT_A_TEST_TARGET = /^(?:bench|example)$/;

/**
 * A `Cargo.toml` patch, tallied the same way. A switch is `test`, `doctest`,
 * `harness` or `autotests` set to `false` (not under `[[bench]]` or
 * `[[example]]`, where the patch shows the table), and a workspace member
 * taken off the `members` list — `cargo test --workspace` stops building it.
 * The list is recognised by its `members = [` line, in the hunk or as the
 * hunk's heading; an entry removed from the middle of a list long enough to
 * show neither is not seen.
 */
export function cargoManifestTally(patch, { status = "" } = {}) {
  const lines = patchLines(patch);
  let offNet = 0;
  const builds = { "+": new Set(), "-": new Set() };
  let hunk = -1, table = "", inMembers = false;
  for (const line of lines) {
    if (line.hunk !== hunk) {
      hunk = line.hunk;
      table = "";
      inMembers = MEMBERS.test(line.heading);
    }
    const header = TABLE.exec(line.text);
    if (header) {
      table = header[1];
      inMembers = false;
      continue;
    }
    const opens = MEMBERS.test(line.text);
    if (opens) inMembers = true;
    // A heading says where the hunk STARTS, and the list may have ended just
    // above it: a line that is not a list entry ends the list.
    else if (inMembers && !LIST_ENTRY.test(line.text)) inMembers = false;
    if (inMembers) {
      const list = opens ? line.text.slice(line.text.indexOf("[") + 1) : line.text;
      if (line.sign !== " ") {
        const other = line.sign === "+" ? "-" : "+";
        for (const m of list.split("]")[0].matchAll(/"([^"]*)"|'([^']*)'/g)) builds[other].add(`member ${m[1] ?? m[2]}`);
      }
      if (list.includes("]")) inMembers = false;
      continue;
    }
    if (line.sign === " ") continue;
    if (MANIFEST_SWITCH.test(line.text) && !NOT_A_TEST_TARGET.test(table)) offNet += line.sign === "+" ? 1 : -1;
  }
  return { removed: 0, added: 0, offNet, buildsAdded: builds["+"], buildsRemoved: builds["-"], newFile: status === "added", never: cargoNeverRuns };
}
