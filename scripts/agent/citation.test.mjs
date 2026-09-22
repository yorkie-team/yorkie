import { test } from "node:test";
import assert from "node:assert/strict";
import { CITATION, parseCitation, parseCitations } from "./citation.mjs";

test("CITATION: matches a file:line anywhere, rejects anything that locates nothing", () => {
  assert.ok(CITATION.test("scripts/agent/ask.mjs:105"));
  assert.ok(CITATION.test("see review-panel.mjs:436 for the guard")); // embedded in prose
  assert.ok(CITATION.test("a.ts:1"));
  // A bare filename locates nothing — rejected deliberately (the prompt asks for
  // file:line, and rejecting merely keeps the finding).
  assert.equal(CITATION.test("scripts/agent/ask.mjs"), false);
  assert.equal(CITATION.test("looks fine"), false); // the costume-of-evidence case
  assert.equal(CITATION.test(""), false);
  assert.equal(CITATION.test("no-extension:105"), false); // needs a .ext
});

test("CITATION: has no `g` flag, so three importers cannot poison each other", () => {
  // With /g, `.test` advances lastIndex and the SAME string would alternate
  // true/false between callers. Pin it: repeated calls must agree.
  assert.equal(CITATION.flags.includes("g"), false);
  const s = "ask.mjs:105";
  assert.ok(CITATION.test(s));
  assert.ok(CITATION.test(s));
  assert.ok(CITATION.test(s));
});

test("parseCitation: extracts file and line from the first citation", () => {
  assert.deepEqual(parseCitation("scripts/agent/ask.mjs:105"), {
    file: "scripts/agent/ask.mjs",
    line: 105,
  });
  assert.deepEqual(parseCitation("as seen in a/b/c.test.mjs:42 today"), {
    file: "a/b/c.test.mjs",
    line: 42,
  });
  // First citation wins when several are present.
  assert.deepEqual(parseCitation("x.mjs:1 and y.mjs:2"), { file: "x.mjs", line: 1 });
});

test("parseCitation: returns null — never throws, never guesses — on anything unlocatable", () => {
  for (const bad of [null, undefined, 42, {}, [], "", "looks fine", "ask.mjs", "ask.mjs:0"]) {
    assert.equal(parseCitation(bad), null, `expected null for ${JSON.stringify(bad)}`);
  }
});

test("parseCitation: a dotted path keeps the last colon as the separator", () => {
  // `a.b.c.mjs:7` must not split on an earlier dot-ish boundary.
  assert.deepEqual(parseCitation("a.b.c.mjs:7"), { file: "a.b.c.mjs", line: 7 });
});

// --- parseCitations: every citation, in order ---------------------------------

test("parseCitations: returns every citation in source order", () => {
  assert.deepEqual(parseCitations("x.mjs:1 and y.mjs:2, then z.ts:30"), [
    { file: "x.mjs", line: 1 },
    { file: "y.mjs", line: 2 },
    { file: "z.ts", line: 30 },
  ]);
});

test("parseCitations: agrees with parseCitation on the first element", () => {
  // The two must never disagree about what the FIRST citation is — the grounding
  // checks read the singular and the locators read the plural, and a drift between
  // them would mean "what counts as evidence" had two answers.
  for (const text of [
    "scripts/agent/ask.mjs:105",
    "as seen in a/b/c.test.mjs:42 today",
    "x.mjs:1 and y.mjs:2",
    "a.b.c.mjs:7",
    "no citation here",
    "bare/path.mjs with no line",
    "",
  ]) {
    assert.deepEqual(parseCitations(text)[0] ?? null, parseCitation(text), JSON.stringify(text));
  }
});

test("parseCitations: unlocatable input is an empty array, never a throw", () => {
  for (const bad of [null, undefined, 42, {}, [], "", "looks fine", "no-extension:105", "a.mjs"]) {
    assert.deepEqual(parseCitations(bad), [], `expected [] for ${JSON.stringify(bad)}`);
  }
});

test("parseCitations: a `:0` citation locates nothing and is dropped, not returned as 0", () => {
  // `pieces` rejects it, and dropping rather than returning it keeps the array's
  // contract ("every element locates a real line") true for every consumer.
  assert.deepEqual(parseCitations("a.mjs:0"), []);
  assert.deepEqual(parseCitations("a.mjs:0 but b.mjs:5"), [{ file: "b.mjs", line: 5 }]);
});

test("parseCitations: repeated calls are independent — no shared lastIndex", () => {
  // CITATION carries no `g` flag precisely so its three importers cannot poison
  // each other. This builds its own `g` copy, so the same guarantee has to be
  // re-established here: a module-level `g` regex would make call N+1 resume from
  // where call N stopped and silently return fewer citations.
  const s = "a.mjs:1 b.mjs:2 c.mjs:3";
  const first = parseCitations(s);
  assert.equal(first.length, 3);
  for (let i = 0; i < 4; i++) assert.deepEqual(parseCitations(s), first, `call ${i} drifted`);
  // And CITATION itself is still un-flagged after all of that.
  assert.equal(CITATION.flags.includes("g"), false);
});

// --- punctuation abutting a citation ------------------------------------------

test("parseCitation: strips punctuation the pattern swallowed off the front", () => {
  // `CITATION`'s leading `[^\s:]+` is permissive about path shape, so it also eats
  // whatever abuts the citation — and lenses cite in prose, so that is usually a
  // paren or a backtick. `(auth.controller.ts:130` parsed as the file
  // `(auth.controller.ts`, which no path comparison can match, so the finding
  // silently lost its location and both provenance gates answered `unknown`.
  // Each case pins the WHOLE result. The first version of this test asserted
  // `{ file, line: parseCitation(text).line }` — comparing `line` against itself,
  // which is vacuous: any regression returning a wrong-but-positive line passed.
  for (const [text, expected] of [
    ["(auth.controller.ts:130-135)", { file: "auth.controller.ts", line: 130 }],
    ["`a/b.mjs:44`", { file: "a/b.mjs", line: 44 }],
    ["[a/b.mjs:44]", { file: "a/b.mjs", line: 44 }],
    ['"x.ts:7"', { file: "x.ts", line: 7 }],
    ["{y.tsx:2}", { file: "y.tsx", line: 2 }],
    ["<z.ts:3>", { file: "z.ts", line: 3 }],
    ["*emphasised.ts:8*", { file: "emphasised.ts", line: 8 }],
    ["((double.ts:2", { file: "double.ts", line: 2 }],
    ["`(mixed.ts:11", { file: "mixed.ts", line: 11 }],
  ]) {
    assert.deepEqual(parseCitation(text), expected, text);
  }
});

test("parseCitation: an unfamiliar leading character is NOT stripped from a filename", () => {
  // The trim is a DENYLIST of prose wrappers, not an allowlist of legal path starts.
  // An allowlist strips whatever it was not told about, which silently corrupts real
  // filenames — `+page.svelte` is SvelteKit's routing convention, and turning it into
  // `page.svelte` reproduces the exact matching failure this trim exists to fix.
  for (const [text, expected] of [
    ["+page.svelte:12", { file: "+page.svelte", line: 12 }],
    ["+layout.ts:3", { file: "+layout.ts", line: 3 }],
    ["+generated.ts:9", { file: "+generated.ts", line: 9 }],
    ["$special.ts:4", { file: "$special.ts", line: 4 }],
    ["_private.ts:3", { file: "_private.ts", line: 3 }],
    ["-weird.ts:3", { file: "-weird.ts", line: 3 }],
    ["~home.ts:2", { file: "~home.ts", line: 2 }],
    ["@scope/pkg.ts:9", { file: "@scope/pkg.ts", line: 9 }],
    ["./a/b.mjs:44", { file: "./a/b.mjs", line: 44 }],
  ]) {
    assert.deepEqual(parseCitation(text), expected, text);
  }
  // ...and a wrapper around one of those is still removed, without eating the name.
  assert.deepEqual(parseCitation("(+page.svelte:12)"), { file: "+page.svelte", line: 12 });
  assert.deepEqual(parseCitation("`$special.ts:4`"), { file: "$special.ts", line: 4 });
});

test("parseCitation: a legitimate leading `./` or `-` or `_` is NOT stripped", () => {
  // `samePath` relies on `./` being preserved so it can normalise it itself.
  assert.deepEqual(parseCitation("./a/b.mjs:44"), { file: "./a/b.mjs", line: 44 });
  assert.deepEqual(parseCitation("_private.ts:3"), { file: "_private.ts", line: 3 });
  assert.deepEqual(parseCitation("-weird.ts:3"), { file: "-weird.ts", line: 3 });
  assert.deepEqual(parseCitation("@scope/pkg.ts:9"), { file: "@scope/pkg.ts", line: 9 });
  assert.deepEqual(parseCitation("a.b.c.mjs:7"), { file: "a.b.c.mjs", line: 7 });
});

test("parseCitation: the trim can never strip a path down to nothing", () => {
  // `CITATION` only matches a token carrying `.` + an extension before the colon,
  // and `.` is in PATH_START's allowlist, so the trim always stops at or before it.
  // There is consequently no emptiness guard in `pieces` — one would be unreachable.
  // These pin that property rather than a guard.
  assert.equal(parseCitation("(:5"), null); // CITATION never matches: no `.ext`
  assert.equal(parseCitation("((.ts:5")?.file, ".ts"); // trim stopped at the dot
  assert.ok(parseCitations("(((a.ts:5 [[[b/c.mjs:9").every((c) => c.file.includes(".")));
  assert.equal(parseCitation("a.mjs:0"), null); // line 0 locates nothing (unchanged)
});

test("parseCitations: the punctuation trim applies to every element", () => {
  assert.deepEqual(
    parseCitations("first (cli-auth.store.ts:39) then `auth.controller.ts:130`"),
    [
      { file: "cli-auth.store.ts", line: 39 },
      { file: "auth.controller.ts", line: 130 },
    ],
  );
});
