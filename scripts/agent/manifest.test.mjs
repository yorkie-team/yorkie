// The core's manifest: that this tree matches it, that a copy still carries
// the one it adopted, and the three ways `manifest.mjs` compares.

import "./fixtures/use-fixture-config.mjs";

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { repoScopedEnv } from "./git-env.mjs";
import {
  MANIFEST_PATH,
  UPSTREAM_PATH,
  adoptionProblem,
  buildManifest,
  checkUpstream,
  compareTree,
  corePaths,
  defaultRoot,
  describeDifference,
  GITATTRIBUTES_LINES,
  isCorePath,
  lineEndingHint,
  manifestDigest,
  parseManifest,
  readManifest,
  readUpstream,
  validateUpstream,
} from "./manifest.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = defaultRoot(HERE);
const H = (c) => c.repeat(64);

// --- this tree -------------------------------------------------------------------------

test("the core matches its manifest", () => {
  // THE DRIFT CHECK. A core file edited, added or removed without the manifest
  // being regenerated fails here, with the path. In a repository that copied
  // the core, regenerating is not a way out: the next test compares the
  // manifest with the one that was adopted.
  const lines = describeDifference(compareTree(ROOT));
  assert.deepEqual(
    lines,
    [],
    `the shared harness files do not match ${MANIFEST_PATH}. A change to them belongs in the repository they are written in ` +
      "(there: `node scripts/agent/manifest.mjs build`); anywhere else, revert it or sync.",
  );
});

test("the manifest lists the core and nothing else", () => {
  const entries = parseManifest(readManifest(ROOT)); // throws on a path outside the core, a repeat, a bad line, bad order
  assert.ok(entries.size > 100, `expected the package and the workflows, found ${entries.size}`);
  for (const rel of entries.keys()) {
    assert.ok(rel.startsWith("scripts/agent/") || /^\.github\/workflows\/agent-[^/]+\.ya?ml$/.test(rel), rel);
  }
  // What it must hold, and what it must not.
  for (const rel of ["scripts/agent/manifest.mjs", "scripts/agent/README.md", "scripts/agent/package-lock.json", "scripts/agent/lenses/lenses.json", "scripts/agent/fixtures/harness-config/config.json", ".github/workflows/agent-review-panel.yml", ".github/workflows/agent-scripts.yml"]) {
    assert.ok(entries.has(rel), `${rel} is part of the core and is not in the manifest`);
  }
  for (const rel of entries.keys()) {
    assert.ok(!rel.includes("node_modules") && !rel.startsWith(".github/agent-harness") && !rel.startsWith(".github/actions"), rel);
    assert.notEqual(rel, MANIFEST_PATH);
  }
});

test("what git tracks under the package is exactly what the manifest rule calls the core", (t) => {
  // The rule decides without git (it has to work in a tree with no `.git`), so
  // it is checked against git here, where there is one: a tracked dotfile or a
  // tracked file under a `node_modules` would be in the package and outside
  // the manifest, where no check sees it.
  if (!existsSync(path.join(ROOT, ".git"))) return t.skip("not a git checkout");
  let tracked;
  try {
    tracked = execFileSync("git", ["ls-files", "--", "scripts/agent", ".github/workflows"], { cwd: ROOT, env: repoScopedEnv(ROOT), encoding: "utf8" })
      .split("\n")
      .filter((l) => l !== "");
  } catch {
    return t.skip("git is not available");
  }
  const core = tracked.filter((p) => p.startsWith("scripts/agent/") || /^\.github\/workflows\/agent-/.test(p)).filter((p) => p !== MANIFEST_PATH);
  for (const rel of core) assert.ok(isCorePath(rel), `${rel} is tracked but the manifest rule leaves it out`);
  assert.ok(tracked.includes(MANIFEST_PATH), "the manifest itself must be tracked");
});

test("a copy still carries the manifest it adopted", () => {
  // In the repository the core is written in, `upstream.json` says
  // `repository: null` and this asserts nothing more. In a copy, the manifest's
  // digest must be the one recorded when it was synced — so the copy cannot
  // drift without a visible edit to `upstream.json`, and the network check in
  // `agent-scripts.yml` compares that record with upstream itself.
  const upstream = readUpstream(ROOT); // throws if missing or malformed
  assert.equal(adoptionProblem(ROOT), null);
  if (upstream.repository !== null) assert.equal(manifestDigest(readManifest(ROOT)), upstream.manifestDigest);
});

// --- the rule ----------------------------------------------------------------------------

test("isCorePath: the package and the agent workflows, nothing dot-named, nothing installed", () => {
  for (const rel of ["scripts/agent/config.mjs", "scripts/agent/lenses/docs.md", "scripts/agent/fixtures/harness-config/config.json", ".github/workflows/agent-fix.yml", ".github/workflows/agent-x.yaml"]) {
    assert.equal(isCorePath(rel), true, rel);
  }
  for (const rel of [
    MANIFEST_PATH,
    "scripts/agent/node_modules/zod/index.js",
    "scripts/agent/.cache/x",
    "scripts/agent/.DS_Store",
    "scripts/agent/sub/.hidden",
    "scripts/agent",
    "scripts/other/x.mjs",
    "scripts/agent/../ci/x.mjs",
    ".github/workflows/ci.yml",
    ".github/workflows/agent-fix.yml.bak",
    ".github/workflows/sub/agent-fix.yml",
    ".github/agent-harness/config.json",
    ".github/actions/agent-toolchain/action.yml",
    "/scripts/agent/x.mjs",
    "scripts\\agent\\x.mjs",
    "",
    null,
  ]) {
    assert.equal(isCorePath(rel), false, String(rel));
  }
});

/** A small tree with a core in it, and its manifest written. */
function tree(t) {
  const root = mkdtempSync(path.join(os.tmpdir(), "manifest-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const write = (rel, text) => {
    mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    writeFileSync(path.join(root, rel), text);
  };
  write("scripts/agent/a.mjs", "export const a = 1;\n");
  write("scripts/agent/lenses/x.md", "# x\n");
  write(".github/workflows/agent-one.yml", "name: one\n");
  write(".github/workflows/ci.yml", "name: CI\n");
  write(".github/agent-harness/config.json", "{}\n");
  write(MANIFEST_PATH, buildManifest(root));
  return { root, write };
}

test("buildManifest: sorted `<sha256>  <path>` lines over the core only", (t) => {
  const { root, write } = tree(t);
  // None of these is the core, and none may appear or disturb it.
  write("scripts/agent/node_modules/dep/index.js", "x");
  write("scripts/agent/.scratch/file", "x");
  write("scripts/agent/.DS_Store", "x");
  write("scripts/other.mjs", "x");
  const text = buildManifest(root);
  assert.deepEqual(
    text.trimEnd().split("\n").map((l) => l.slice(66)),
    [".github/workflows/agent-one.yml", "scripts/agent/a.mjs", "scripts/agent/lenses/x.md"],
  );
  assert.match(text, /^[0-9a-f]{64} {2}\.github\/workflows\/agent-one\.yml\n/);
  assert.deepEqual(corePaths(root), [...parseManifest(text).keys()]);
  assert.deepEqual(describeDifference(compareTree(root)), []);
});

test("compareTree: names what changed, what was added and what was removed", (t) => {
  const { root, write } = tree(t);
  write("scripts/agent/a.mjs", "export const a = 2;\n");
  write("scripts/agent/new.mjs", "x\n");
  write(".github/workflows/agent-two.yml", "name: two\n");
  rmSync(path.join(root, "scripts/agent/lenses/x.md"));
  assert.deepEqual(compareTree(root), {
    added: [".github/workflows/agent-two.yml", "scripts/agent/new.mjs"],
    removed: ["scripts/agent/lenses/x.md"],
    changed: ["scripts/agent/a.mjs"],
  });
  assert.deepEqual(describeDifference(compareTree(root)), [
    "changed: scripts/agent/a.mjs",
    "added (not in the manifest): .github/workflows/agent-two.yml",
    "added (not in the manifest): scripts/agent/new.mjs",
    "removed (in the manifest, not in the tree): scripts/agent/lenses/x.md",
  ]);
  // One byte is a change.
  const { root: other, write: w } = tree(t);
  w("scripts/agent/lenses/x.md", "# x \n");
  assert.deepEqual(compareTree(other).changed, ["scripts/agent/lenses/x.md"]);
});

test("a symbolic link in the core is refused, not hashed as its target", (t) => {
  const { root } = tree(t);
  symlinkSync(path.join(root, "scripts/agent/a.mjs"), path.join(root, "scripts/agent/link.mjs"));
  assert.throws(() => buildManifest(root), /link\.mjs is a symbolic link/);
  const { root: other } = tree(t);
  symlinkSync(path.join(other, ".github/workflows/ci.yml"), path.join(other, ".github/workflows/agent-linked.yml"));
  assert.throws(() => buildManifest(other), /agent-linked\.yml is not a regular file/);
});

test("a checkout that converted line endings is told so", (t) => {
  // `core.autocrlf` with no `-text` attribute rewrites every file on checkout.
  // Each then hashes differently, and "126 files changed" says nothing useful.
  const { root, write } = tree(t);
  write("scripts/agent/a.mjs", "export const a = 1;\r\n");
  write("scripts/agent/lenses/x.md", "# x\r\n");
  const { changed } = compareTree(root);
  assert.deepEqual(changed, ["scripts/agent/a.mjs", "scripts/agent/lenses/x.md"]);
  assert.match(lineEndingHint(root, changed), /^2 of the changed files have CRLF line endings[\s\S]*\.gitattributes/);
  // An ordinary edit gets no such hint.
  write("scripts/agent/a.mjs", "export const a = 2;\n");
  assert.equal(lineEndingHint(root, ["scripts/agent/a.mjs"]), "");
  assert.deepEqual([...GITATTRIBUTES_LINES], ["scripts/agent/** -text", ".github/workflows/agent-*.yml -text"]);
});

test("parseManifest: refuses anything that is not a sorted list of core files", () => {
  const line = (rel, c = "a") => `${H(c)}  ${rel}\n`;
  assert.equal(parseManifest(line("scripts/agent/a.mjs") + line("scripts/agent/b.mjs")).size, 2);
  for (const [text, why] of [
    ["", /empty/],
    [line("scripts/agent/a.mjs").trimEnd(), /newline/],
    [`${H("a")} scripts/agent/a.mjs\n`, /not a `<sha256> {2}<path>` line/],
    [`${H("A")}  scripts/agent/a.mjs\n`, /not a `<sha256> {2}<path>` line/],
    [line("scripts/agent/b.mjs") + line("scripts/agent/a.mjs"), /not sorted/],
    [line("scripts/agent/a.mjs") + line("scripts/agent/a.mjs"), /twice|not sorted/],
    [line(".github/agent-harness/config.json"), /not a file of the core/],
    [line("scripts/agent/../../etc/passwd"), /not a file of the core/],
    [line(MANIFEST_PATH), /not a file of the core/],
    // Converted line endings are named as such, not reported as a malformed line.
    [line("scripts/agent/a.mjs").replace("\n", "\r\n"), /CR line endings[\s\S]*scripts\/agent\/\*\* -text[\s\S]*\.github\/workflows\/agent-\*\.yml -text/],
  ]) {
    assert.throws(() => parseManifest(text), why, JSON.stringify(text.slice(0, 80)));
  }
});

test("manifestDigest: one digest per manifest, and a different one for any other", () => {
  const a = `${H("a")}  scripts/agent/a.mjs\n`;
  assert.match(manifestDigest(a), /^[0-9a-f]{64}$/);
  assert.equal(manifestDigest(a), manifestDigest(a));
  assert.notEqual(manifestDigest(a), manifestDigest(`${H("b")}  scripts/agent/a.mjs\n`));
});

// --- upstream.json -----------------------------------------------------------------------

test("validateUpstream: null for the origin, three exact fields for a copy", () => {
  const copy = { repository: "org/repo", commit: "0123456789abcdef0123456789abcdef01234567", manifestDigest: H("c") };
  assert.deepEqual(validateUpstream({ repository: null }), { repository: null });
  assert.deepEqual(validateUpstream(copy), copy);
  for (const [value, why] of [
    [null, /must be an object/],
    [[], /must be an object/],
    [{}, /repository is missing/],
    [{ repository: null, commit: copy.commit }, /commit is not a known key when repository is null/],
    [{ ...copy, note: "x" }, /note is not a known key/],
    [{ ...copy, repository: "repo" }, /`owner\/name`/],
    [{ ...copy, repository: "https://github.com/org/repo" }, /`owner\/name`/],
    [{ ...copy, repository: "org/.." }, /`owner\/name`/],
    [{ ...copy, repository: "org/." }, /`owner\/name`/],
    [{ ...copy, repository: "../repo" }, /`owner\/name`/],
    [{ ...copy, repository: "org/repo/extra" }, /`owner\/name`/],
    [{ ...copy, repository: "-org/repo" }, /`owner\/name`/],
    [{ ...copy, repository: "org/re po" }, /`owner\/name`/],
    [{ ...copy, commit: "main" }, /40-character/],
    [{ ...copy, commit: copy.commit.slice(0, 7) }, /40-character/],
    [{ ...copy, commit: copy.commit.toUpperCase() }, /40-character/],
    [{ ...copy, manifestDigest: "abc" }, /sha256/],
    [{ repository: "org/repo" }, /commit must be[\s\S]*manifestDigest must be/],
  ]) {
    assert.throws(() => validateUpstream(value), why, JSON.stringify(value));
  }
});

test("adoptionProblem: a copy whose manifest is not the recorded one says so", (t) => {
  const { root, write } = tree(t);
  // Missing altogether is an error, not "this must be the origin".
  assert.throws(() => adoptionProblem(root), /cannot read .*upstream\.json/);
  write(UPSTREAM_PATH, JSON.stringify({ repository: null }));
  assert.equal(adoptionProblem(root), null);
  const digest = manifestDigest(readManifest(root));
  const record = { repository: "org/repo", commit: "0123456789abcdef0123456789abcdef01234567", manifestDigest: digest };
  write(UPSTREAM_PATH, JSON.stringify(record));
  assert.equal(adoptionProblem(root), null);
  // The copy is edited and its manifest regenerated to match: check 1 passes, this one does not.
  write("scripts/agent/a.mjs", "export const a = 99;\n");
  write(MANIFEST_PATH, buildManifest(root));
  assert.deepEqual(describeDifference(compareTree(root)), []);
  assert.match(adoptionProblem(root), new RegExp(`records ${digest} \\(org/repo at 0123456789abcdef0123456789abcdef01234567\\)`));
  write(UPSTREAM_PATH, "{ not json");
  assert.throws(() => adoptionProblem(root), /not valid JSON/);
});

// --- the network check, with the network handed in ----------------------------------------

const MANIFEST = `${H("a")}  scripts/agent/a.mjs\n`;
const DIGEST = manifestDigest(MANIFEST);
const COPY = { repository: "org/repo", commit: "0123456789abcdef0123456789abcdef01234567", manifestDigest: DIGEST };
const API = "https://api.github.com/repos/org/repo";
const COMPARE = `${API}/compare/trunk...${COPY.commit}`;
const PINNED = `${API}/contents/${MANIFEST_PATH}?ref=${COPY.commit}`;
const HEAD = `${API}/contents/${MANIFEST_PATH}`;
const ok = (text) => ({ status: 200, text });
/** Upstream as it should be: default branch `trunk`, the commit on it, the manifest there and at the tip. */
const GOOD = {
  [API]: ok(JSON.stringify({ default_branch: "trunk" })),
  [COMPARE]: ok(JSON.stringify({ status: "behind" })),
  [PINNED]: ok(MANIFEST),
  [HEAD]: ok(MANIFEST),
};
/** A fetch that answers from `answers`; an array is one answer per call. */
const fetcher = (answers, seen = []) => async (url) => {
  seen.push(url);
  let answer = answers[url];
  if (Array.isArray(answer)) answer = answer.length > 1 ? answer.shift() : answer[0];
  if (answer instanceof Error) throw answer;
  return answer ?? { status: 404, text: "" };
};
const slept = [];
const check = (answers, { upstream = COPY, localDigest = DIGEST, seen = [], self } = {}) =>
  checkUpstream({ upstream, localDigest, self, fetchText: fetcher(answers, seen), sleep: async (ms) => void slept.push(ms) });

test("checkUpstream: the origin has nothing to compare, asks nothing, and says who claims to be it", async () => {
  const seen = [];
  const r = await check({}, { upstream: { repository: null }, seen, self: "some-org/some-copy" });
  assert.equal(r.ok, true);
  assert.deepEqual(seen, []);
  assert.deepEqual(r.warnings, []);
  // A copy that sets `repository: null` passes here; the log at least names it.
  assert.match(r.lines.join("\n"), /^some-org\/some-copy is recorded as the repository the core is written in/);
  assert.match((await check({}, { upstream: { repository: null } })).lines.join("\n"), /^This is recorded as the repository/);
});

test("checkUpstream: a copy of upstream's default branch at the recorded commit passes", async () => {
  for (const status of ["behind", "identical"]) {
    const seen = [];
    const r = await check({ ...GOOD, [COMPARE]: ok(JSON.stringify({ status })) }, { seen });
    assert.equal(r.ok, true, status);
    assert.deepEqual(r.warnings, []);
    // The default branch is asked for, never assumed.
    assert.deepEqual(seen, [API, COMPARE, PINNED, HEAD]);
    assert.match(r.lines.join("\n"), /This copy is org\/repo at 0123456789abcdef0123456789abcdef01234567, on trunk/);
    // …and handed on, for the notifier to ask where it is now.
    assert.equal(r.branch, "trunk");
  }
});

test("checkUpstream: a commit that is not on upstream's default branch fails, whatever its manifest says", async () => {
  // Every head a pull request ever had stays fetchable upstream. Its manifest
  // can match the record exactly and it is still code upstream never took.
  for (const status of ["ahead", "diverged", undefined, "something-new"]) {
    const seen = [];
    const r = await check({ ...GOOD, [COMPARE]: ok(JSON.stringify({ status })) }, { seen });
    assert.equal(r.ok, false, String(status));
    assert.match(r.lines.join("\n"), /is not on org\/repo's default branch \(trunk\)[\s\S]*Sync again from trunk/);
    assert.ok(!seen.includes(PINNED), "the manifest is not consulted for a commit that is not on the branch: there is no fallback");
  }
  const gone = await check({ ...GOOD, [COMPARE]: { status: 404, text: "" } });
  assert.equal(gone.ok, false);
  assert.match(gone.lines.join("\n"), /has no commit 0123456789abcdef0123456789abcdef01234567 that can be compared[\s\S]*must record a commit on trunk/);
});

test("checkUpstream: fails closed when upstream cannot be read, at each of the three questions", async () => {
  for (const url of [API, COMPARE, PINNED]) {
    for (const [answer, why] of [
      [{ status: 403, text: "" }, /returned HTTP 403\. The copy cannot be compared, so this check fails rather than passes/],
      [new Error("getaddrinfo ENOTFOUND api.github.com"), /Could not fetch .*ENOTFOUND api\.github\.com \(after 4 attempts\)[\s\S]*fails rather than passes/],
      [{ status: 503, text: "" }, /returned HTTP 503/],
    ]) {
      const r = await check({ ...GOOD, [url]: answer });
      assert.equal(r.ok, false, `${url} ${answer.status ?? answer.message}`);
      assert.match(r.lines.join("\n"), why);
      assert.ok(r.lines.join("\n").includes(url), "the message names what it tried to fetch");
    }
  }
  // No manifest at the recorded commit is its own sentence: the record predates it, or is wrong.
  const none = await check({ ...GOOD, [PINNED]: { status: 404, text: "" } });
  assert.equal(none.ok, false);
  assert.match(none.lines.join("\n"), /has no scripts\/agent\/MANIFEST\.sha256 at 0123456789abcdef0123456789abcdef01234567/);
  // A repository answer with no default branch in it is not a pass either.
  for (const text of ["{}", "not json", JSON.stringify({ default_branch: "a b" })]) {
    const r = await check({ ...GOOD, [API]: ok(text) });
    assert.equal(r.ok, false, text);
    assert.match(r.lines.join("\n"), /did not say what org\/repo's default branch is/);
  }
});

test("checkUpstream: a transient refusal is retried, a lasting one still fails", async () => {
  slept.length = 0;
  const seen = [];
  const r = await check(
    { ...GOOD, [API]: [{ status: 503, text: "" }, GOOD[API]], [COMPARE]: [new Error("socket hang up"), { status: 429, text: "" }, GOOD[COMPARE]], [PINNED]: [{ status: 502, text: "" }, GOOD[PINNED]] },
    { seen },
  );
  assert.equal(r.ok, true, r.lines.join("\n"));
  assert.deepEqual(seen, [API, API, COMPARE, COMPARE, COMPARE, PINNED, PINNED, HEAD]);
  assert.deepEqual(slept, [1000, 1000, 3000, 1000]);
  // 404 and 403 are answers, not weather: asked once.
  const once = [];
  await check({ ...GOOD, [PINNED]: { status: 404, text: "" } }, { seen: once });
  assert.equal(once.filter((u) => u === PINNED).length, 1);
  // …and four refusals in a row are a failure, after three pauses.
  slept.length = 0;
  const down = [];
  const failed = await check({ ...GOOD, [PINNED]: { status: 500, text: "" } }, { seen: down });
  assert.equal(failed.ok, false);
  assert.equal(down.filter((u) => u === PINNED).length, 4);
  assert.deepEqual(slept, [1000, 3000, 9000]);
});

test("checkUpstream: fails when the record, the copy and upstream do not agree", async () => {
  const other = `${H("b")}  scripts/agent/a.mjs\n`;
  // upstream.json edited to match a drifted copy: upstream at that commit says otherwise.
  const drifted = await check(GOOD, { upstream: { ...COPY, manifestDigest: manifestDigest(other) }, localDigest: manifestDigest(other) });
  assert.equal(drifted.ok, false);
  assert.match(drifted.lines.join("\n"), /The record does not describe that commit/);
  // The copy no longer carries the recorded manifest: nothing is fetched to say so.
  const seen = [];
  const local = await check(GOOD, { localDigest: manifestDigest(other), seen });
  assert.equal(local.ok, false);
  assert.deepEqual(seen, []);
});

test("checkUpstream: being behind is a warning, and so is not knowing", async () => {
  const newer = `${H("b")}  scripts/agent/a.mjs\n`;
  const behind = await check({ ...GOOD, [HEAD]: ok(newer) });
  assert.equal(behind.ok, true);
  assert.equal(behind.warnings.length, 1);
  assert.match(behind.warnings[0], /default branch has a different manifest[\s\S]*this copy is behind/);
  for (const head of [{ status: 500, text: "" }, new Error("timeout")]) {
    const unknown = await check({ ...GOOD, [HEAD]: head });
    assert.equal(unknown.ok, true, "not knowing whether upstream moved must not fail the check");
    assert.match(unknown.warnings[0], /Could not tell whether org\/repo has moved on/);
  }
});

// --- where the network check runs ----------------------------------------------------------

test("agent-scripts.yml asks upstream on every run, weekly, and cannot pass by failing to ask", () => {
  const wf = readFileSync(path.join(ROOT, ".github", "workflows", "agent-scripts.yml"), "utf8");
  const code = wf.split("\n").filter((l) => !/^\s*#/.test(l)).join("\n");
  const at = code.indexOf("      - name: Compare the shared files with the repository they were copied from\n");
  assert.ok(at > 0, "agent-scripts.yml no longer compares the core with upstream");
  const next = code.indexOf("\n      - ", at + 1);
  const nextJob = code.slice(at).search(/\n {2}[A-Za-z0-9_-]+:\n/);
  const step = code.slice(at, Math.min(...[next, at + nextJob].filter((n) => n > at)));
  assert.match(step, /\n {8}run: npm run check:upstream\n/);
  assert.match(step, /\n {8}working-directory: scripts\/agent\n/);
  // The job's own read token and nothing wider; it is for the rate limit.
  assert.match(step, /\n {10}GH_TOKEN: \$\{\{ github\.token \}\}\n/);
  assert.ok(!/secrets\./.test(step), "the upstream check must not be handed a secret");
  // Failing to fetch is a failure. `continue-on-error` or an `|| true` would make it a pass.
  assert.ok(!/continue-on-error|\|\| true/.test(step), "the upstream check must fail the job when it fails");
  // After the suite, in the same job: the package script exists only once `npm ci` ran.
  assert.ok(code.indexOf("run: npm test\n") < at && code.indexOf("run: npm ci ") < at);
  assert.ok(code.indexOf("\n  lint-workflows:\n") > at, "the step belongs to the test job");
  // Weekly, so that being behind upstream is noticed without a change here.
  assert.match(code, /\non:\n {2}schedule:\n {4}- cron: "/);
  // The token is read-only for the whole workflow.
  assert.match(code, /\npermissions:\n {2}contents: read\n/);
  const pkg = JSON.parse(readFileSync(path.join(HERE, "package.json"), "utf8"));
  assert.equal(pkg.scripts["check:upstream"], "node manifest.mjs upstream");
});

// --- the CLI -------------------------------------------------------------------------------

test("CLI: verify and digest answer about the tree the script sits in", () => {
  const cli = path.join(HERE, "manifest.mjs");
  const run = (...args) => spawnSync(process.execPath, [cli, ...args], { encoding: "utf8" });
  const verify = run("verify");
  assert.equal(verify.status, 0, verify.stderr);
  assert.equal(run("digest").stdout.trim(), manifestDigest(readFileSync(path.join(ROOT, MANIFEST_PATH), "utf8")));
  for (const args of [[], ["nope"], ["verify", "extra"]]) assert.equal(run(...args).status, 1, JSON.stringify(args));
});

test("CLI: verify fails and names the path when a core file is changed", (t) => {
  // A copy of the modules the CLI needs, in a tree of its own.
  const { root, write } = tree(t);
  for (const f of ["manifest.mjs", "config.mjs"]) write(`scripts/agent/${f}`, readFileSync(path.join(HERE, f), "utf8"));
  write(MANIFEST_PATH, buildManifest(root));
  const run = (...args) => spawnSync(process.execPath, [path.join(root, "scripts/agent/manifest.mjs"), ...args], { encoding: "utf8" });
  assert.equal(run("verify").status, 0);
  write("scripts/agent/a.mjs", "export const a = 3;\n");
  const bad = run("verify");
  assert.equal(bad.status, 1);
  assert.match(bad.stderr, /changed: scripts\/agent\/a\.mjs/);
  // `build` makes it match again, and says what it wrote.
  const built = run("build");
  assert.equal(built.status, 0, built.stderr);
  assert.match(built.stdout, /MANIFEST\.sha256: 5 files, digest [0-9a-f]{64}/);
  assert.equal(run("verify").status, 0);
});
