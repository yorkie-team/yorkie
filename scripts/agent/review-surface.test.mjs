import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { fixtureGitEnv } from "./git-env.mjs";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import path from "node:path";
import {
  SCOPES,
  DEMOTING_SCOPES,
  scopeFrom,
  frozenShaFrom,
  freezeResolves,
  surfaceOf,
  surfaceOfFinding,
  resolveFrozenSha,
} from "./review-surface.mjs";
import { serializeFixDispatch, FIX_DISPATCH_AUTHOR_LOGIN } from "./rounds.mjs";

// --- scopeFrom: the decision table -------------------------------------------

test("scopeFrom: no location, or plain blame could not answer → unknown", () => {
  assert.equal(scopeFrom({ hasLocation: false }), "unknown");
  assert.equal(scopeFrom({ hasLocation: true, addedAfterFreeze: null }), "unknown");
  assert.equal(scopeFrom({}), "unknown"); // defaults are the safe ones
  assert.equal(scopeFrom(), "unknown");
  assert.equal(scopeFrom(null), "unknown");
});

test("scopeFrom: a line that predates the freeze is in-scope whatever its content", () => {
  // The property that keeps the ORIGINAL implement diff fully on the gate.
  // Content evidence is irrelevant here and must not be consulted.
  for (const content of [true, false, null]) {
    assert.equal(
      scopeFrom({ hasLocation: true, addedAfterFreeze: false, contentAfterFreeze: content }),
      "in-scope",
      `contentAfterFreeze=${content}`,
    );
  }
});

test("scopeFrom: a fix round wrote the line AND the content is new → out-of-scope", () => {
  assert.equal(
    scopeFrom({ hasLocation: true, addedAfterFreeze: true, contentAfterFreeze: true }),
    "out-of-scope",
  );
});

test("scopeFrom: a fix round that MOVED pre-freeze content here stays in-scope", () => {
  // The inverted content test, and the mirror image of novelty.mjs's `relocated`.
  // A fix round that relocates original implement-diff code into a new file gets
  // plain-blamed to the fix commit, but the code is the PR's own and must keep
  // gating. Without this clause every refactoring fix round would demote itself
  // off the merge gate.
  assert.equal(
    scopeFrom({ hasLocation: true, addedAfterFreeze: true, contentAfterFreeze: false }),
    "in-scope",
  );
});

test("scopeFrom: content lookup broke → unknown, which keeps the finding blocking", () => {
  assert.equal(
    scopeFrom({ hasLocation: true, addedAfterFreeze: true, contentAfterFreeze: null }),
    "unknown",
  );
});

test("scopeFrom: only out-of-scope demotes, and every scope it returns is declared", () => {
  assert.deepEqual([...DEMOTING_SCOPES], ["out-of-scope"]);
  for (const hasLocation of [true, false]) {
    for (const added of [true, false, null]) {
      for (const content of [true, false, null]) {
        const s = scopeFrom({ hasLocation, addedAfterFreeze: added, contentAfterFreeze: content });
        assert.ok(SCOPES.includes(s), `undeclared scope ${s}`);
      }
    }
  }
});

// --- frozenShaFrom: the anchor ------------------------------------------------

const A_SHA = "a".repeat(40);
const B_SHA = "b".repeat(40);

/** A believable dispatch comment, as `github-actions[bot]` would write it. */
function dispatchComment(from, created_at, { login = FIX_DISPATCH_AUTHOR_LOGIN, type = "Bot" } = {}) {
  return {
    user: { login, type },
    created_at,
    body: `Dispatching the fix agent.\n\n${serializeFixDispatch({ from })}`,
  };
}

test("frozenShaFrom: the FIRST dispatch's head is the anchor, whatever comment order", () => {
  const later = dispatchComment(B_SHA, "2026-08-12T10:00:00Z");
  const first = dispatchComment(A_SHA, "2026-08-10T10:00:00Z");
  assert.equal(frozenShaFrom([later, first]), A_SHA);
  assert.equal(frozenShaFrom([first, later]), A_SHA);
});

test("frozenShaFrom: a stranger cannot move the freeze point", () => {
  // The whole trust posture. A human (or any non-`github-actions[bot]` account)
  // writing the marker by hand could otherwise re-freeze the surface around code
  // the fixer just wrote, demoting it off the gate.
  const forged = dispatchComment(B_SHA, "2026-08-09T10:00:00Z", { login: "attacker", type: "User" });
  const real = dispatchComment(A_SHA, "2026-08-10T10:00:00Z");
  assert.equal(frozenShaFrom([forged, real]), A_SHA);
  assert.equal(frozenShaFrom([forged]), null);
  // Right login, wrong account type — an App impersonating the Actions bot.
  const wrongType = dispatchComment(B_SHA, "2026-08-09T10:00:00Z", { type: "User" });
  assert.equal(frozenShaFrom([wrongType]), null);
});

test("frozenShaFrom: no records, or an unusable sha → null (gate off)", () => {
  assert.equal(frozenShaFrom([]), null);
  assert.equal(frozenShaFrom(null), null);
  assert.equal(frozenShaFrom(undefined), null);
  assert.equal(frozenShaFrom([{ user: { login: FIX_DISPATCH_AUTHOR_LOGIN, type: "Bot" }, body: "no marker" }]), null);
  // An abbreviated sha is not usable for `merge-base --is-ancestor`.
  assert.equal(frozenShaFrom([dispatchComment("abc1234", "2026-08-10T10:00:00Z")]), null);
  assert.equal(frozenShaFrom([dispatchComment("", "2026-08-10T10:00:00Z")]), null);
});

test("frozenShaFrom: a @claude rerun does NOT re-freeze the surface", () => {
  // `fixRoundsUsed` applies the rerun floor because a hand-back grants a fresh
  // BUDGET. The surface is not a budget: re-freezing on every rerun would let the
  // treadmill back in one rerun at a time, which is what this module exists to
  // stop. So the anchor ignores the floor and stays at the first dispatch even
  // when later dispatches postdate a rerun.
  const comments = [
    dispatchComment(A_SHA, "2026-08-10T10:00:00Z"),
    { user: { login: "harrykim8672", type: "User" }, created_at: "2026-08-11T10:00:00Z", body: "@claude rerun" },
    dispatchComment(B_SHA, "2026-08-12T10:00:00Z"),
  ];
  assert.equal(frozenShaFrom(comments), A_SHA);
});

// --- resolveFrozenSha: the reader ---------------------------------------------

test("resolveFrozenSha: reads the ledger off the PR", () => {
  const api = () => [dispatchComment(A_SHA, "2026-08-10T10:00:00Z"), dispatchComment(B_SHA, "2026-08-12T10:00:00Z")];
  assert.equal(resolveFrozenSha(742, { api, log: () => {} }), A_SHA);
});

test("resolveFrozenSha: an unreadable side-channel turns the gate OFF, never fails", () => {
  // The same contract `readRebuttals` keeps: the panel must not go down because an
  // optional side-channel could not be read. Off = every finding routes as today.
  const logged = [];
  const boom = () => { throw new Error("gh exploded"); };
  assert.equal(resolveFrozenSha(742, { api: boom, log: (m) => logged.push(m) }), null);
  assert.equal(logged.length, 1);
  assert.match(logged[0], /surface gate stays off/);
});

test("resolveFrozenSha: junk from the API is not a freeze point", () => {
  for (const payload of [null, undefined, {}, "nope", [], [{}], [{ user: null, body: "x" }]]) {
    assert.equal(resolveFrozenSha(742, { api: () => payload, log: () => {} }), null, JSON.stringify(payload));
  }
});

// --- git fixtures -------------------------------------------------------------

/**
 * git with a fixed identity, in an environment that cannot reach any repository
 * but `dir`.
 *
 * `fixtureGitEnv` removes every git-steering variable AND pins
 * `GIT_DIR`/`GIT_WORK_TREE`. Pinning `GIT_DIR` alone is NOT enough: an inherited
 * `GIT_INDEX_FILE` still makes `git add` write another repository's index, and
 * because the objects it records live in this fixture's store, that index is left
 * CORRUPT rather than merely modified. The escape test below pins that.
 */
function git(dir, ...args) {
  try {
    return execFileSync("git", ["-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", ...args], {
      cwd: dir,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      env: fixtureGitEnv(dir),
    });
  } catch (e) {
    const why = String(e?.stderr || e?.message || e).trim();
    throw new Error(`fixture git failed: git ${args.join(" ")} (in ${dir}): ${why}`);
  }
}

/**
 * Prove the fixture's git resolves to `dir`'s own repository. `--absolute-git-dir`
 * is the load-bearing assertion: `--show-toplevel` merely echoes whatever
 * `GIT_WORK_TREE` was pinned to, so on its own it cannot detect a `GIT_DIR`
 * escape — the exact incident shape — and asserting it alone would be tautological.
 */
function assertIsolated(dir) {
  const gitDir = git(dir, "rev-parse", "--absolute-git-dir").trim();
  assert.equal(
    realpathSync(gitDir),
    realpathSync(path.join(dir, ".git")),
    `fixture git dir escaped to ${gitDir} — commits would land in another repository`,
  );
}

const rev = (dir, ref) => git(dir, "rev-parse", ref).trim();
const sha256 = (file) => createHash("sha256").update(readFileSync(file)).digest("hex");

const IMPL_FN = [
  "export function resolveColorAtPosition(doc, pos) {",
  "  const run = doc.runAt(pos);",
  "  return run.color || doc.defaultColor;",
  "}",
].join("\n");

/**
 * A repo shaped like a real agent PR mid-loop:
 *
 *   commit BASE   — main
 *   commit IMPL   — the implement job's work. THIS IS THE FREEZE POINT.
 *   commit FIX    — one fix round, doing all three things a fix round does:
 *                     * writes a brand-new file (`added-by-fixer.mjs`)
 *                     * appends a brand-new line to an original file
 *                     * MOVES `resolveColorAtPosition` verbatim into a new file
 */
function makeRepo({ tag = "primary" } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), "surface-test-"));
  git(dir, "init", "-q", "-b", "main");
  assertIsolated(dir);

  // `tag` varies the FIRST commit's content, which cascades a different sha into
  // every commit below it. This is load-bearing for the isolation test, not
  // cosmetic: the fixture pins user.name/user.email and git derives a commit sha
  // from tree + parents + identity + TIMESTAMP, so two byte-identical repos built
  // inside the same one-second tick produce IDENTICAL shas. The isolation test then
  // compares a sha read from the decoy against the same sha from the real repo and
  // passes while blame is in fact reading the wrong repository — it survived exactly
  // that mutation. Distinct content makes the two repos incomparable by sha, so the
  // assertion means what it says regardless of timing.
  writeFileSync(path.join(dir, "base.mjs"), `export const BASE = 1; // ${tag}\n`);
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "base");
  const base = rev(dir, "HEAD");

  // The implement commit: the original review surface.
  writeFileSync(path.join(dir, "impl.mjs"), `${IMPL_FN}\n\nexport const IMPL_ONLY = "original";\n`);
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "implement the issue");
  const impl = rev(dir, "HEAD");

  // One fix round.
  writeFileSync(
    path.join(dir, "added-by-fixer.mjs"),
    [
      "export function validateIncomingPayloadShape(payload) {",
      "  if (!payload || typeof payload !== 'object') throw new TypeError('bad payload');",
      "  return payload;",
      "}",
      "",
    ].join("\n"),
  );
  // The move: same content, new home. `impl.mjs` keeps only its non-moved line.
  writeFileSync(path.join(dir, "moved-by-fixer.mjs"), `${IMPL_FN}\n`);
  writeFileSync(
    path.join(dir, "impl.mjs"),
    `export const IMPL_ONLY = "original";\nexport const APPENDED_BY_FIXER = "brand new content here";\n`,
  );
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "address panel findings");
  const fix = rev(dir, "HEAD");

  return { dir, base, impl, fix };
}

/** 1-indexed line number of the first line containing `needle`. */
function lineOf(dir, file, needle) {
  const lines = readFileSync(path.join(dir, file), "utf8").split("\n");
  const i = lines.findIndex((l) => l.includes(needle));
  assert.ok(i >= 0, `fixture drift: no line containing ${JSON.stringify(needle)} in ${file}`);
  return i + 1;
}

// --- freezeResolves -----------------------------------------------------------

test("freezeResolves: a real ancestor of HEAD is usable", async (t) => {
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  assert.equal(await freezeResolves(dir, impl), true);
});

test("freezeResolves: a freeze point that is NOT an ancestor of HEAD turns the gate OFF", async (t) => {
  // The one catastrophic failure available to this module. After a rebase,
  // force-push or amend the frozen sha still RESOLVES but no longer lies on this
  // branch's history, so `merge-base --is-ancestor <blamed> <frozen>` answers "no"
  // for every line and the gate would demote the entire PR off the merge gate at
  // once. Checked explicitly, not inferred per finding.
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  git(dir, "checkout", "-q", "-b", "divergent", impl);
  writeFileSync(path.join(dir, "divergent.mjs"), "export const D = 1;\n");
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "divergent work");
  const orphan = rev(dir, "HEAD");
  git(dir, "checkout", "-q", "main");

  assert.equal(await freezeResolves(dir, orphan), false, "a sha off this branch must not freeze anything");
});

test("freezeResolves: unknown, malformed and missing shas are all unusable", async (t) => {
  const { dir } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  assert.equal(await freezeResolves(dir, "c".repeat(40)), false, "resolves to nothing");
  assert.equal(await freezeResolves(dir, "abc1234"), false, "abbreviated");
  assert.equal(await freezeResolves(dir, ""), false);
  assert.equal(await freezeResolves(dir, null), false);
  assert.equal(await freezeResolves(null, "a".repeat(40)), false);
  // A tag-like ref is not a sha and must not be accepted as one.
  assert.equal(await freezeResolves(dir, "HEAD"), false);
});

// --- surfaceOf: the real thing ------------------------------------------------

test("surfaceOf: a line the fixer wrote, with new content → out-of-scope", async (t) => {
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const r = await surfaceOf({
    repo: dir,
    file: "added-by-fixer.mjs",
    line: lineOf(dir, "added-by-fixer.mjs", "validateIncomingPayloadShape"),
    frozenSha: impl,
  });
  assert.equal(r.scope, "out-of-scope");
  assert.ok(DEMOTING_SCOPES.has(r.scope), "this is the finding class that must stop gating");
});

test("surfaceOf: a line appended to an ORIGINAL file by a fix round is still out-of-scope", async (t) => {
  // File identity is not the test — line provenance is. An original file the fixer
  // grew is exactly where the treadmill lives.
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const r = await surfaceOf({
    repo: dir,
    file: "impl.mjs",
    line: lineOf(dir, "impl.mjs", "APPENDED_BY_FIXER"),
    frozenSha: impl,
  });
  assert.equal(r.scope, "out-of-scope");
});

test("surfaceOf: a line from the freeze commit itself is in-scope", async (t) => {
  // `--is-ancestor` treats a commit as an ancestor of itself. If it did not, the
  // ENTIRE original implement diff would demote and the PR would gate on nothing.
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const r = await surfaceOf({
    repo: dir,
    file: "impl.mjs",
    line: lineOf(dir, "impl.mjs", "IMPL_ONLY"),
    frozenSha: impl,
  });
  assert.equal(r.scope, "in-scope");
  assert.equal(r.addedBy, impl);
  // `contentSha` stays null because the move-aware blame is never consulted for a
  // line that predates the freeze. That early return is what keeps the reported
  // provenance honest: content-origin blame can only ever walk BACKWARDS, so for a
  // pre-freeze line it cannot change the scope, and running it anyway would report
  // a content sha for a question that was already settled — and turn a broken
  // second lookup into `unknown` where the right answer is `in-scope`.
  assert.equal(r.contentSha, null);
});

test("surfaceOf: a line predating the branch entirely is in-scope", async (t) => {
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const r = await surfaceOf({ repo: dir, file: "base.mjs", line: 1, frozenSha: impl });
  assert.equal(r.scope, "in-scope");
});

test("surfaceOf: content the fix round MOVED from the original diff stays in-scope", async (t) => {
  // The inverted content test, against real git. Plain blame credits the fix
  // commit (it created the file); move-aware blame finds the content at the
  // implement commit. That is the PR's own code and must keep gating.
  const { dir, impl, fix } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const line = lineOf(dir, "moved-by-fixer.mjs", "resolveColorAtPosition");
  const r = await surfaceOf({ repo: dir, file: "moved-by-fixer.mjs", line, frozenSha: impl });
  assert.equal(r.addedBy, fix, "plain blame must credit the fix commit — else the test proves nothing");
  assert.equal(r.scope, "in-scope", "move-aware blame must rescue relocated original code");
});

test("surfaceOf: every unusable input resolves to unknown, i.e. still blocking", async (t) => {
  const { dir, impl } = microRepoGuard(t);
  const bad = [
    { label: "no repo", args: { repo: "", file: "impl.mjs", line: 1, frozenSha: impl } },
    { label: "no file", args: { repo: dir, file: "", line: 1, frozenSha: impl } },
    { label: "no frozen sha", args: { repo: dir, file: "impl.mjs", line: 1, frozenSha: null } },
    { label: "abbreviated sha", args: { repo: dir, file: "impl.mjs", line: 1, frozenSha: "abc1234" } },
    { label: "line 0", args: { repo: dir, file: "impl.mjs", line: 0, frozenSha: impl } },
    { label: "line null", args: { repo: dir, file: "impl.mjs", line: null, frozenSha: impl } },
    { label: "line non-integer", args: { repo: dir, file: "impl.mjs", line: 1.5, frozenSha: impl } },
    // A NUMERIC STRING is the case only this module's guard catches: git accepts
    // `-L 1,1` built from "1" quite happily, so without the `Number.isInteger`
    // test a caller passing a stringly-typed line gets a confident answer from a
    // code path that never validated its input. git rejects 0 and 1.5 on its own.
    { label: "line as a string", args: { repo: dir, file: "impl.mjs", line: "1", frozenSha: impl } },
    // Likewise `merge-base --is-ancestor <sha> HEAD` SUCCEEDS, so the sha regex is
    // the only thing standing between a ref name and a silently different freeze
    // point. Passing a branch name here would scope every finding against whatever
    // that ref happens to point at.
    { label: "frozen sha is a ref name", args: { repo: dir, file: "impl.mjs", line: 1, frozenSha: "HEAD" } },
    { label: "frozen sha is a branch name", args: { repo: dir, file: "impl.mjs", line: 1, frozenSha: "main" } },
    { label: "file not in repo", args: { repo: dir, file: "nope.mjs", line: 1, frozenSha: impl } },
    { label: "line past EOF", args: { repo: dir, file: "impl.mjs", line: 9999, frozenSha: impl } },
    { label: "unresolvable sha", args: { repo: dir, file: "impl.mjs", line: 1, frozenSha: "c".repeat(40) } },
  ];
  for (const { label, args } of bad) {
    const r = await surfaceOf(args);
    assert.equal(r.scope, "unknown", label);
    assert.equal(DEMOTING_SCOPES.has(r.scope), false, `${label} must not demote`);
  }
});

/** Shared fixture for the table-driven test above, with cleanup registered. */
function microRepoGuard(t) {
  const made = makeRepo();
  t.after(() => rmSync(made.dir, { recursive: true, force: true }));
  return made;
}

test("surfaceOf: the cache is keyed on the freeze point, not just the location", async (t) => {
  // A stale entry from a different freeze point would silently mis-scope every
  // finding at that location.
  const { dir, impl, base } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const cache = new Map();
  const line = lineOf(dir, "impl.mjs", "IMPL_ONLY");
  const atImpl = await surfaceOf({ repo: dir, file: "impl.mjs", line, frozenSha: impl, cache });
  const atBase = await surfaceOf({ repo: dir, file: "impl.mjs", line, frozenSha: base, cache });
  assert.equal(atImpl.scope, "in-scope", "written by the freeze commit");
  assert.equal(atBase.scope, "out-of-scope", "written after the base commit");
  assert.equal(cache.size, 2, "two freeze points must not share one entry");

  // And a repeat hit returns the cached object rather than re-probing.
  const again = await surfaceOf({ repo: dir, file: "impl.mjs", line, frozenSha: impl, cache });
  assert.equal(again, atImpl);
});

// --- surfaceOfFinding ---------------------------------------------------------

test("surfaceOfFinding: routes a located finding and refuses an unlocated one", async (t) => {
  const { dir, impl } = makeRepo();
  t.after(() => rmSync(dir, { recursive: true, force: true }));

  const line = lineOf(dir, "added-by-fixer.mjs", "validateIncomingPayloadShape");
  const located = await surfaceOfFinding(
    { severity: "major", file: "added-by-fixer.mjs", line, summary: "unvalidated input" },
    { repo: dir, frozenSha: impl },
  );
  assert.equal(located.scope, "out-of-scope");

  // A finding with a file but no line cannot be placed, and a file-level guess is
  // deliberately not offered — see the module header.
  for (const f of [
    { severity: "major", file: "added-by-fixer.mjs", summary: "no line" },
    { severity: "major", summary: "no location at all" },
    null,
  ]) {
    const r = await surfaceOfFinding(f, { repo: dir, frozenSha: impl });
    assert.equal(r.scope, "unknown", JSON.stringify(f));
  }
});

// --- isolation ----------------------------------------------------------------

test("git location vars in the environment cannot redirect this module", async (t) => {
  // The pre-push hook exports GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE, and those
  // OVERRIDE `cwd`. A module that spawns git with an inherited environment reads
  // (and can damage) whatever repository the hook was pushing, not the one it was
  // handed. `repoScopedEnv` is what prevents it; this asserts it is actually used.
  const { dir, impl, fix } = makeRepo();
  const decoy = makeRepo({ tag: "decoy" });
  t.after(() => {
    rmSync(dir, { recursive: true, force: true });
    rmSync(decoy.dir, { recursive: true, force: true });
  });

  const decoyHeadBefore = rev(decoy.dir, "HEAD");
  const decoyIndexBefore = sha256(path.join(decoy.dir, ".git", "index"));

  const saved = {};
  for (const v of ["GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"]) saved[v] = process.env[v];
  process.env.GIT_DIR = path.join(decoy.dir, ".git");
  process.env.GIT_WORK_TREE = decoy.dir;
  process.env.GIT_INDEX_FILE = path.join(decoy.dir, ".git", "index");
  t.after(() => {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  });

  // Still reads the repo it was HANDED.
  const line = lineOf(dir, "moved-by-fixer.mjs", "resolveColorAtPosition");
  const r = await surfaceOf({ repo: dir, file: "moved-by-fixer.mjs", line, frozenSha: impl });
  assert.equal(r.addedBy, fix, "blame escaped to the decoy repository");
  assert.equal(await freezeResolves(dir, impl), true, "freezeResolves escaped to the decoy repository");

  // And left the decoy untouched.
  assert.equal(rev(decoy.dir, "HEAD"), decoyHeadBefore, "decoy HEAD moved");
  assert.equal(
    sha256(path.join(decoy.dir, ".git", "index")),
    decoyIndexBefore,
    "decoy index was rewritten — GIT_INDEX_FILE leaked through",
  );
});


// --- workflow wiring ----------------------------------------------------------

test("the panel job resolves the freeze point and hands it to review-panel.mjs", () => {
  // A gate nothing invokes is inert. Each assertion below matches the ACTUAL
  // invocation or binding rather than the `[ -f ]` existence guard beside it —
  // matching the guard passes even when the `node` line is gone. Verified by
  // mutation: deleting any one of these lines from the workflow reds this test.
  const wf = readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), "../../.github/workflows/agent-review-panel.yml"),
    "utf8",
  );
  // The resolver runs, from the TRUSTED copy — the branch must not get to decide
  // where its own review surface froze.
  assert.match(wf, /node \.trusted\/scripts\/agent\/review-surface\.mjs resolve "\$PR"/);
  // ...and still guarded, so a branch older than the script skips instead of redding.
  assert.match(wf, /\[ -f \.trusted\/scripts\/agent\/review-surface\.mjs \]/);
  // The step's output is bound to an env var...
  assert.match(wf, /FROZEN_SHA: \$\{\{ steps\.surface\.outputs\.frozen \}\}/);
  // ...and that env var reaches the panel. Both halves are required: an unbound
  // FROZEN_SHA would pass `--frozen-sha ""` forever and the gate would never run.
  assert.match(wf, /--frozen-sha "\$FROZEN_SHA"/);
  // The step id the binding reads must actually exist. Anchored to end-of-line:
  // a bare /id: surface/ also matches `id: surface2`, so a renamed step would
  // leave `steps.surface.outputs.frozen` reading nothing and pass anyway.
  assert.match(wf, /^\s*id: surface$/m);
  // GH_REPO, because the panel job has no git remote to infer {owner}/{repo} from
  // (#786's failure mode after the pipeline was extracted).
  const step = wf.slice(wf.indexOf("Resolve the frozen review surface"), wf.indexOf("review-surface.mjs resolve"));
  assert.match(step, /GH_REPO: \$\{\{ github\.repository \}\}/);
  // It must not be able to fail the panel.
  assert.match(step, /continue-on-error: true/);
});
