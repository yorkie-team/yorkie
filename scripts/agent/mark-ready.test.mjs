// Pins mark-ready.mjs's EXIT-CODE CONTRACT (issue #852).
//
// mark-ready reports its whole outcome through its exit status, and the
// `promote` job in agent-review-panel.yml branches on every value:
//
//   0 → promoted, or deliberately left alone   (`ready=true` output)
//   1 → a gate said no; job SUCCEEDS           ("leaving as draft")
//   2 → tooling error                          (job exits 2)
//   3 → gates passed, flip-to-ready FAILED     (job exits 3, pages a human)
//
// 0 and 1 both let that job succeed, so collapsing 1 into 0 — or renumbering
// 2/3 — would keep every other test green while a PR that should have merged
// sat as a draft forever. That is what these tests exist to catch.
//
// mark-ready runs its CLI at import time (top-level `process.exit`), so it
// cannot be imported — the same constraint that put DEFAULT_REVIEW_CHECKS in
// checks.mjs and HANDOFF_MARKER in disclosure.mjs. It is therefore tested as a
// PROCESS, with a stub `gh` first on PATH. The stub logs every invocation, so a
// test can assert the mutation as well as the code: "0 means promoted" is false
// if nothing called `gh pr ready`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { CI_WORKFLOW_PATH, DEFAULT_REVIEW_CHECKS } from "./checks.mjs";
import { HANDOFF_MARKER } from "./disclosure.mjs";

const CI_PATH = CI_WORKFLOW_PATH;

const CLI = fileURLToPath(new URL("./mark-ready.mjs", import.meta.url));
const SOURCE = readFileSync(CLI, "utf8");
const PANEL_YML = fileURLToPath(
  new URL("../../.github/workflows/agent-review-panel.yml", import.meta.url),
);

// A `gh` that answers from a JSON config and records what it was asked. CommonJS
// on purpose: the file has no extension (it must be named exactly `gh` to be
// found on PATH), and node loads an extensionless file as CJS.
const STUB_GH = `#!/usr/bin/env node
const { appendFileSync, readFileSync } = require("node:fs");
const argv = process.argv.slice(2);
const joined = argv.join(" ");
const cfg = JSON.parse(readFileSync(process.env.GH_STUB_CONFIG, "utf8"));
appendFileSync(process.env.GH_STUB_LOG, joined + "\\n");
// Which credential each call went out with. A SEPARATE log so the call
// assertions above stay string-clean, and one line per call — the hand-off
// comment's body is multi-line, which the log above can afford to be and a
// token-per-call log cannot.
appendFileSync(
  process.env.GH_STUB_TOKEN_LOG,
  (process.env.GH_TOKEN || "<unset>") + "\\t" + joined.split("\\n").join(" ") + "\\n",
);

if ((cfg.fail || []).some((p) => joined.startsWith(p))) {
  process.stderr.write("stub gh: forced failure for '" + joined + "'\\n");
  process.exit(1);
}

if (argv[0] === "pr" && argv[1] === "view") {
  // The label re-read just before the PUT asks for labels alone.
  if (joined.endsWith("--json labels")) {
    process.stdout.write(JSON.stringify({ labels: cfg.pr.labels || [] }));
  } else if (joined.endsWith("--json headRefOid")) {
    // Gate 1b's SECOND read of the head. \`headAfter\` is how a test expresses a
    // push that landed while the gate was reading; unset means the head held
    // still, which is the ordinary case.
    process.stdout.write(JSON.stringify({ headRefOid: cfg.headAfter || cfg.pr.headRefOid }));
  } else {
    process.stdout.write(JSON.stringify(cfg.pr));
  }
} else if (argv[0] === "api" && /^repos\\/\\{owner\\}\\/\\{repo\\}$/.test(argv[1] || "")) {
  // Repository metadata — gate 1b reads \`default_branch\` from it to check the
  // PR is based on the branch whose CI definition it claims to have run.
  // \`repo\` overrides the whole body, which is how a test expresses a response
  // that carries no \`default_branch\` at all.
  process.stdout.write(JSON.stringify(cfg.repo || { default_branch: cfg.defaultBranch || "main" }));
} else if (argv[0] === "api" && joined.includes("actions/workflows/ci.yml/runs?head_sha=")) {
  // The SCOPED listing: GitHub resolved \`ci.yml\` to one workflow file, so only
  // that file's runs come back.
  process.stdout.write(JSON.stringify({ workflow_runs: cfg.workflowRuns || [] }));
} else if (argv[0] === "api" && joined.includes("actions/runs?head_sha=")) {
  // The UNSCOPED listing, modelled as the real API serves it: EVERY workflow's
  // runs for the SHA, forged and foreign ones included. This branch is what
  // makes gate 1's identity property testable as behavior — a reader that
  // regresses to this endpoint is handed the forgeries and promotes on them.
  process.stdout.write(
    JSON.stringify({ workflow_runs: [...(cfg.otherRuns || []), ...(cfg.workflowRuns || [])] }),
  );
} else if (argv[0] === "api" && /pulls\\/\\d+\\/files/.test(joined)) {
  // PER-PAGE, as the real endpoint is. \`filePages\` is an array of pages, so a
  // test can put a workflow edit on page 2 — the case a reader that only ever
  // looks at page 1 passes. \`files\` is the single-page shorthand. Any page past
  // the supplied ones is EMPTY, which is how the CLI's loop terminates. A page
  // of the literal string "junk" serves a malformed (non-array) response.
  const page = Number((joined.match(/[?&]page=(\\d+)/) || [])[1] || 1);
  const pages = cfg.filePages || [cfg.files || []];
  const body = page <= pages.length ? pages[page - 1] : [];
  process.stdout.write(JSON.stringify(body === "junk" ? { message: "not an array" } : body));
} else if (argv[0] === "api" && joined.includes("check-runs")) {
  process.stdout.write(JSON.stringify({ check_runs: cfg.checkRuns || [] }));
} else if (argv[0] === "api" && /issues\\/\\d+\\/comments/.test(joined)) {
  // \`--paginate --slurp\` wraps the pages in an array, so the shape is
  // [[...page1], [...page2]] and the caller flattens it. \`comments\` is the
  // single-page shorthand a test uses to say "the hand-off is already there".
  process.stdout.write(JSON.stringify([cfg.comments || []]));
} else {
  process.stdout.write("{}");
}
`;

const DISCLOSED = "Authored autonomously by Claude Code; no human wrote a line.";
const AT = "2026-08-22T00:00:00Z";

/** A world where all three gates pass and the PR is still a draft. */
function okConfig(over = {}) {
  return {
    pr: {
      number: 7,
      body: DISCLOSED,
      isDraft: true,
      labels: [{ name: "agent:reviewing" }, { name: "enhancement" }],
      baseRefName: "main",
      headRefName: "agent/852-x",
      headRefOid: "cafe7",
      url: "https://github.com/o/r/pull/7",
    },
    // Gate 1b measures the diff against the DEFAULT BRANCH, so which branch that
    // is has to be part of the world the stub describes.
    defaultBranch: "main",
    // `workflowRuns` are the runs of the CI WORKFLOW FILE; `otherRuns` are the
    // rest of the repo's runs for the same SHA, which only the unscoped
    // endpoint returns. A test that puts a green run in `otherRuns` is asking
    // "would this promote if the reader stopped scoping its query?".
    workflowRuns: [{ name: "CI", path: CI_PATH, conclusion: "success", created_at: AT }],
    otherRuns: [],
    // `app.slug` is part of the fixture because `checkPassed` requires it: a
    // check-run name is not reserved, so gate 2 only believes runs GitHub
    // Actions produced. A fixture without it would make every gate-2 test read
    // as "no review evidence" rather than as the case it names.
    checkRuns: DEFAULT_REVIEW_CHECKS.map((name) => ({
      name,
      conclusion: "success",
      started_at: AT,
      app: { slug: "github-actions" },
    })),
    files: [{ filename: "pkg/document/crdt/tree.go" }],
    fail: [],
    ...over,
  };
}

// Two DIFFERENT credentials, so "which token was this call made with" is an
// observable rather than a guess: the App token may only appear on the three
// promotion mutations, the read token only on the reads.
const APP_TOKEN = "stub-app-token";
const READ_TOKEN = "stub-default-token";

function run(argv, cfg = okConfig()) {
  const dir = mkdtempSync(path.join(tmpdir(), "mark-ready-"));
  const cfgPath = path.join(dir, "config.json");
  const logPath = path.join(dir, "calls.log");
  const tokenLogPath = path.join(dir, "tokens.log");
  writeFileSync(path.join(dir, "gh"), STUB_GH, { mode: 0o755 });
  writeFileSync(cfgPath, JSON.stringify(cfg));
  writeFileSync(logPath, "");
  writeFileSync(tokenLogPath, "");
  const res = spawnSync(process.execPath, [CLI, ...argv], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${dir}${path.delimiter}${process.env.PATH}`,
      GH_STUB_CONFIG: cfgPath,
      GH_STUB_LOG: logPath,
      GH_STUB_TOKEN_LOG: tokenLogPath,
      GH_TOKEN: READ_TOKEN,
      GH_MUTATION_TOKEN: APP_TOKEN,
    },
  });
  return {
    code: res.status,
    stdout: res.stdout ?? "",
    stderr: res.stderr ?? "",
    calls: readFileSync(logPath, "utf8").split("\n").filter(Boolean),
    tokenCalls: readFileSync(tokenLogPath, "utf8")
      .split("\n")
      .filter(Boolean)
      .map((line) => {
        const tab = line.indexOf("\t");
        return { token: line.slice(0, tab), call: line.slice(tab + 1) };
      }),
  };
}

const promoted = (calls) => calls.some((c) => c.startsWith("pr ready"));

// ---- exit 2: tooling error (three sites) ----------------------------------

test("exit 2: a missing or non-numeric PR argument is a tooling error", () => {
  for (const argv of [[], ["--promote"], ["abc"], ["7.5"]]) {
    const { code, stderr, calls } = run(argv);
    assert.equal(code, 2, `argv ${JSON.stringify(argv)} must exit 2`);
    assert.match(stderr, /Usage: node \.\/scripts\/agent\/mark-ready\.mjs/);
    assert.deepEqual(calls, [], "must not touch the PR at all");
  }
});

test("exit 2: an empty required-check set fails closed unless opted into", () => {
  const { code, stderr, calls } = run(["7", "--promote", "--require-checks", ""]);
  assert.equal(code, 2);
  assert.match(stderr, /Refusing to promote with an empty required-check set/);
  assert.deepEqual(calls, []);

  // ...and --allow-no-checks is the opt-in, so it is NOT a tooling error.
  const opted = run(["7", "--promote", "--require-checks", "", "--allow-no-checks"]);
  assert.equal(opted.code, 0);
  assert.ok(promoted(opted.calls));
});

test("exit 2: failing to read the PR is a tooling error, not a gate failure", () => {
  const { code, stderr } = run(["7", "--promote"], okConfig({ fail: ["pr view"] }));
  assert.equal(code, 2, "an unreadable PR must not be reported as 'gates not satisfied'");
  assert.match(stderr, /Failed to read PR #7/);
});

test("exit 2: failing to read the CI workflow's runs is a tooling error, not a red gate", () => {
  // Scoping gate 1's query to a workflow FILE added a 404 class the unscoped
  // endpoint did not have — rename or delete ci.yml and the read throws. Read
  // as "CI is not green" that would leave every PR a silent draft forever while
  // the promote job SUCCEEDED: exactly the crash-reported-as-a-gate-refusal
  // this whole change exists to stop, one level down from where it started.
  const { code, stderr, calls } = run(
    ["7", "--promote"],
    okConfig({ fail: ["api repos/{owner}/{repo}/actions/workflows"] }),
  );
  assert.equal(code, 2, "an unreadable CI workflow must not be reported as 'gates not satisfied'");
  assert.match(stderr, /tooling error, not a gate refusal/);
  assert.ok(!promoted(calls), "and nothing may be promoted on evidence that could not be read");
});

test("exit 2: failing to read the CHECK RUNS is a tooling error, not an unapproved review", () => {
  // The same distinction, in the adjacent gate. Reporting an unreadable API as
  // "the reviewer did not approve" leaves a PR the reviewer HAD approved sitting
  // as a draft while the promote job succeeds and nothing re-runs the panel.
  const { code, stderr, calls } = run(
    ["7", "--promote"],
    okConfig({ fail: ["api repos/{owner}/{repo}/commits"] }),
  );
  assert.equal(code, 2, "an unreadable check-runs API must not be reported as 'not approved'");
  assert.match(stderr, /no review verdict was read/);
  assert.ok(!promoted(calls));
});

// ---- exit 1: a gate said no (job still SUCCEEDS) --------------------------

test("exit 1, not 0: CI not green leaves the PR a draft without promoting", () => {
  for (const workflowRuns of [
    [], // no CI run for this SHA yet
    [{ name: "CI", path: CI_PATH, conclusion: "failure", created_at: AT }],
    [{ name: "CI", path: CI_PATH, conclusion: null, created_at: AT }], // still running
  ]) {
    const { code, stdout, calls } = run(["7", "--promote"], okConfig({ workflowRuns }));
    assert.equal(code, 1, `CI runs ${JSON.stringify(workflowRuns)} must exit 1, never 0`);
    assert.match(stdout, /Not promoting: one or more gates are not satisfied/);
    assert.ok(!promoted(calls), "a failed gate must never flip the PR to ready");
  }
});

test("gate 1 reads the CI WORKFLOW FILE's runs — another green workflow is not evidence", () => {
  // THE IDENTITY PROPERTY, ASSERTED AS BEHAVIOR. Every workflow on the repo
  // reports a run for the same head SHA, and the panel's own runs go green
  // routinely; a run's `name` is only a `name:` key, so anyone able to add
  // `.github/workflows/pwn.yml` with `name: CI` gets a green run of their own.
  // Gate 1 is safe from both only because it asks the API for the runs OF ONE
  // WORKFLOW FILE (`/actions/workflows/ci.yml/runs`) instead of for every run on
  // the SHA.
  //
  // The stub serves both endpoints the way GitHub does, so this is not a check
  // on a URL string: regress the reader to the unscoped endpoint and it is
  // handed `forged` — newest, green — and promotes. The URL is asserted too,
  // below, but the exit code is what fails first.
  const forged = { name: "CI", path: ".github/workflows/pwn.yml", conclusion: "success", created_at: AT, id: 99 };
  const foreign = {
    name: "agent-review-panel",
    path: ".github/workflows/agent-review-panel.yml",
    conclusion: "success",
    created_at: AT,
    id: 98,
  };

  const ciRed = run(
    ["7", "--promote"],
    okConfig({
      workflowRuns: [{ name: "CI", path: CI_PATH, conclusion: "failure", created_at: AT, id: 1 }],
      otherRuns: [forged, foreign],
    }),
  );
  assert.equal(ciRed.code, 1, "a green run from another FILE must not stand in for a red CI run");
  assert.ok(!promoted(ciRed.calls), "the PR must stay a draft");

  const ciAbsent = run(["7", "--promote"], okConfig({ workflowRuns: [], otherRuns: [forged, foreign] }));
  assert.equal(ciAbsent.code, 1, "a green run from another file is not evidence that CI ran at all");
  assert.ok(!promoted(ciAbsent.calls));

  // The URL is the mechanism, so pin it: the scoped endpoint must be the one
  // asked, and the unscoped one must not be asked at all.
  const runQueries = ciRed.calls.filter((c) => c.startsWith("api ") && c.includes("head_sha="));
  assert.ok(runQueries.length > 0, "gate 1 must still read workflow runs for the head SHA");
  for (const q of runQueries) {
    assert.match(
      q,
      /actions\/workflows\/ci\.yml\/runs\?head_sha=cafe7/,
      "gate 1 must ask GitHub for the CI workflow's runs, not filter in JS",
    );
    assert.doesNotMatch(q, /actions\/runs\?/, "the unscoped listing returns forged runs and must not be used");
  }

  // ...and the scoping must not go the other way: CI green among the noise promotes.
  const ciGreen = run(["7", "--promote"], okConfig({ otherRuns: [forged, foreign] }));
  assert.equal(ciGreen.code, 0);
  assert.ok(promoted(ciGreen.calls));
});

test("gate 1b: a branch that edits the CI definition is not promoted by its own CI run", () => {
  // Scoping by workflow file proves WHICH file ran, never WHAT was in it: a
  // `pull_request` run executes the merge ref's copy of `ci.yml`, so a branch
  // able to edit that file gets a genuinely green run at the genuine path with
  // no tests in it. An `agent:managed` human PR is on this promote path and is
  // not restricted from pushing workflows, so the gate has to refuse it — every
  // other gate here would be satisfied by a PR that deleted CI's jobs.
  for (const filename of [
    ".github/workflows/ci.yml",
    ".github/workflows/some-other.yml",
    ".github/actions/setup/action.yml", // composite action = workflow content elsewhere
  ]) {
    const { code, stdout, calls } = run(["7", "--promote"], okConfig({ files: [{ filename }] }));
    assert.equal(code, 1, `${filename} changes what CI runs, so its green run is not evidence`);
    assert.match(stdout, /Not promoting: one or more gates are not satisfied/);
    assert.ok(!promoted(calls), "the PR must stay a draft for a human to review the workflow change");
  }

  // A RENAME out of the workflow directory only names the new path in
  // `filename`; the workflow it removed is in `previous_filename`.
  const renamed = run(
    ["7", "--promote"],
    okConfig({ files: [{ filename: "docs/old-ci.yml", previous_filename: ".github/workflows/ci.yml" }] }),
  );
  assert.equal(renamed.code, 1, "moving ci.yml away is an edit to what CI runs");
  assert.ok(!promoted(renamed.calls));

  // An unreadable file list cannot rule a workflow edit out, so it fails CLOSED.
  const unreadable = run(["7", "--promote"], okConfig({ fail: ["api repos/{owner}/{repo}/pulls/7/files"] }));
  assert.equal(unreadable.code, 1, "not knowing what the branch changed is not a pass");
  assert.match(unreadable.stderr, /Could not establish PR #7's CI definition/);
  assert.ok(!promoted(unreadable.calls));

  // ...and an ordinary code-only PR is unaffected.
  const ordinary = run(["7", "--promote"], okConfig({ files: [{ filename: "server/rpc/yorkie_server.go" }] }));
  assert.equal(ordinary.code, 0);
  assert.ok(promoted(ordinary.calls));
});

test("gate 1b covers the WHOLE CI-defining surface, not just .github/**", () => {
  // `ci.yml` contains almost no test logic: it calls `make lint` and `make build`,
  // which resolve through the MERGE REF's `Makefile` into whatever golangci-lint
  // config the branch ships, and it grades the integration suite against a
  // compose stack the branch also owns. A gate that refused only
  // `.github/workflows|actions/**` let a branch gut CI through any of those and
  // still auto-promote — while the hand-off comment told the human reviewer the
  // run had executed main's CI definition. Every path here is one the agent App
  // CAN push.
  for (const filename of [
    "Makefile",
    ".golangci.yml",
    "codecov.yml",
    "go.mod",
    "go.sum",
    "buf.gen.yaml",
    "api/buf.gen.yaml",
    "build/docker/docker-compose.yml",
    "scripts/ci/parse-bench.js",
    "scripts/verify-doc-links.mjs",
    ".github/CODEOWNERS",
  ]) {
    const { code, stdout, stderr, calls } = run(["7", "--promote"], okConfig({ files: [{ filename }] }));
    assert.equal(code, 1, `${filename} decides what CI does, so its green run is not evidence about main's CI`);
    assert.match(stdout, /Not promoting: one or more gates are not satisfied/);
    assert.match(stderr, new RegExp(filename.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), "the refusal must name the path");
    assert.ok(!promoted(calls));
  }
});

test("gate 1b enumerates EVERY page of the file list, and refuses an unreadable one", () => {
  // The loop pages up to 10 × 100. A reader that stopped at page 1 — which the
  // previous stub could not even express, since it served one page — passes every
  // other test in this file while a workflow edit sits on page 2 of a big PR.
  const filler = (n, prefix) => Array.from({ length: n }, (_, i) => ({ filename: `${prefix}/f${i}.ts` }));

  const hiddenOnPage2 = run(
    ["7", "--promote"],
    okConfig({ filePages: [filler(100, "packages/sheets/src"), [{ filename: ".github/workflows/ci.yml" }]] }),
  );
  assert.equal(hiddenOnPage2.code, 1, "a CI-definition edit on page 2 must be found");
  assert.match(hiddenOnPage2.stderr, /\.github\/workflows\/ci\.yml/);
  assert.ok(!promoted(hiddenOnPage2.calls));

  // ...and a genuinely large, genuinely innocuous PR still promotes — so the
  // assertion above is about finding the edit, not about refusing big PRs.
  const bigButClean = run(
    ["7", "--promote"],
    okConfig({ filePages: [filler(100, "packages/sheets/src"), filler(3, "packages/docs/src")] }),
  );
  assert.equal(bigButClean.code, 0, "paging must continue, not refuse");
  assert.ok(promoted(bigButClean.calls));
  // Page 3 must actually have been asked for (the loop stops on a short page).
  const pagesAsked = bigButClean.calls.filter((c) => c.includes("pulls/7/files")).length;
  assert.equal(pagesAsked, 2, "one request per page until a short page ends the walk");

  // Past the bound, the list is not enumerable and the gate cannot rule an edit
  // out — a REFUSAL, not a pass. Exactly 1000 files is fine; 10 full pages is
  // what the bound allows, and an off-by-one here would refuse a legal PR.
  const tenFullPages = Array.from({ length: 10 }, () => filler(100, "packages/sheets/src"));
  const atTheBound = run(["7", "--promote"], okConfig({ filePages: tenFullPages }));
  assert.equal(atTheBound.code, 1, "a PR whose 10th page is still full may hide an 11th");
  assert.match(atTheBound.stderr, /more than 1000 changed files/);
  assert.ok(!promoted(atTheBound.calls));

  // A malformed page is not an empty page. `[]` would terminate the walk and
  // read as "nothing else changed"; anything that is not an array is a refusal.
  const malformed = run(["7", "--promote"], okConfig({ filePages: ["junk"] }));
  assert.equal(malformed.code, 1, "a non-array response must not read as an empty file list");
  assert.match(malformed.stderr, /unexpected response from the PR files endpoint/);
  assert.ok(!promoted(malformed.calls));
});

test("gate 1b's evidence is pinned to the default branch and to gate 1's SHA", () => {
  // `pulls/N/files` answers for the PR's CURRENT base — a mutable,
  // author-controlled field — and for whatever the head is NOW, while gate 1's
  // evidence is a CI run pinned to the SHA read at the top of the run. Both ends
  // have to be pinned or the file list describes a different comparison than the
  // one the gate reports on.

  // BASE RETARGETING: branch `x` carries a gutted `ci.yml` and a green CI run;
  // open the PR against a branch that already has that edit and the diff no
  // longer shows it. Refuse rather than measure.
  const stacked = run(
    ["7", "--promote"],
    okConfig({ pr: { ...okConfig().pr, baseRefName: "agent/stacked-base" } }),
  );
  assert.equal(stacked.code, 1, "a PR based anywhere but the default branch has no comparison to make");
  assert.match(stacked.stderr, /not the default branch 'main'/);
  assert.ok(!promoted(stacked.calls));

  // ...including when the repo's default branch is not called `main`, so the
  // check is against the repository's answer rather than a hard-coded name.
  const renamedDefault = run(["7", "--promote"], okConfig({ defaultBranch: "trunk" }));
  assert.equal(renamedDefault.code, 1);
  assert.match(renamedDefault.stderr, /not the default branch 'trunk'/);
  const onTrunk = run(
    ["7", "--promote"],
    okConfig({ defaultBranch: "trunk", pr: { ...okConfig().pr, baseRefName: "trunk" } }),
  );
  assert.equal(onTrunk.code, 0, "the gate must follow the repository's default branch, not the string 'main'");
  assert.ok(promoted(onTrunk.calls));

  // A repository response with no `default_branch` leaves the base unknown, so
  // there is nothing to compare the file list against — fail closed.
  const noRepo = run(["7", "--promote"], okConfig({ repo: {} }));
  assert.equal(noRepo.code, 1);
  assert.match(noRepo.stderr, /could not read the repository's default branch/);
  assert.ok(!promoted(noRepo.calls));

  // HEAD MOVING MID-GATE: gate 1 read a green CI run for `cafe7` (which gutted
  // CI); a push then lands `beef8`, which restores `ci.yml`, so the live file
  // list is clean. Pairing the two would promote on evidence from neither commit.
  const moved = run(["7", "--promote"], okConfig({ headAfter: "beef8" }));
  assert.equal(moved.code, 1, "a head that moved mid-gate makes the two reads incomparable");
  assert.match(moved.stderr, /the head moved from cafe7 to beef8/);
  assert.ok(!promoted(moved.calls));
});

test("gate 2 requires the check run's PRODUCER, not just its name", () => {
  // THE FORGERY. A check-run name is not reserved: any App on the installation
  // may create `agent-review-correctness` and conclude it `success`. Gate 2 is
  // the gate that means "an independent reviewer approved this", so identifying
  // it by name alone is the same class of hole gate 1 closes by scoping its
  // query to a workflow FILE. `app.slug` is set by GitHub from the installation.
  const forged = DEFAULT_REVIEW_CHECKS.map((name) => ({
    name,
    conclusion: "success",
    started_at: AT,
    app: { slug: "some-other-app" },
  }));
  const { code, stdout, calls } = run(["7", "--promote"], okConfig({ checkRuns: forged }));
  assert.equal(code, 1, "a green lens check from another App must not satisfy the review gate");
  assert.match(stdout, /Not promoting: one or more gates are not satisfied/);
  for (const name of DEFAULT_REVIEW_CHECKS) {
    assert.match(stdout, new RegExp(`❌ ${name}`), `${name} must be reported as not passed`);
  }
  assert.ok(!promoted(calls), "the PR must stay a draft");

  // ...and a forged run must not shadow a real one either, in either direction.
  const real = (conclusion, t) => DEFAULT_REVIEW_CHECKS.map((name) => ({
    name,
    conclusion,
    started_at: t,
    app: { slug: "github-actions" },
  }));
  const shadowed = run(
    ["7", "--promote"],
    okConfig({ checkRuns: [...real("failure", AT), ...forged.map((c) => ({ ...c, started_at: "2026-08-23T00:00:00Z" }))] }),
  );
  assert.equal(shadowed.code, 1, "a newer forged success must not overturn the real failure");
  assert.ok(!promoted(shadowed.calls));
});

test("gate 1 reads the NEWEST CI run for the SHA", () => {
  // `ciConclusion` reads the newest run by id, and a SHA can legitimately carry
  // more than one CI run — closing and reopening a PR files a second
  // `pull_request` run for the same commit, and newest-wins is right there
  // because the later run is a fresh execution of the same file at the same
  // tree. What checks.test.mjs pins is the narrower invariant this rests on: no
  // two DIFFERENT ci.yml triggers can fire for one PR head, so "newest"
  // supersedes rather than picking arbitrarily among unrelated runs.
  const older = { name: "CI", path: CI_PATH, conclusion: "success", created_at: AT, id: 1 };
  const newerRed = { name: "CI", path: CI_PATH, conclusion: "failure", created_at: AT, id: 9 };

  const red = run(["7", "--promote"], okConfig({ workflowRuns: [older, newerRed] }));
  assert.equal(red.code, 1, "the newest CI run is red, so an older green one must not promote");
  assert.ok(!promoted(red.calls), "the PR must stay a draft");

  const green = run(
    ["7", "--promote"],
    okConfig({
      workflowRuns: [
        { name: "CI", path: CI_PATH, conclusion: "failure", created_at: AT, id: 1 },
        { name: "CI", path: CI_PATH, conclusion: "success", created_at: AT, id: 9 },
      ],
    }),
  );
  assert.equal(green.code, 0, "a newer green run supersedes the red one it replaced");
  assert.ok(promoted(green.calls));

  // The newest still running is "not known yet", not a pass.
  const inFlight = run(
    ["7", "--promote"],
    okConfig({ workflowRuns: [older, { name: "CI", path: CI_PATH, conclusion: null, created_at: AT, id: 9 }] }),
  );
  assert.equal(inFlight.code, 1, "an unfinished CI run must not be read as a pass");
  assert.ok(!promoted(inFlight.calls));
});

test("exit 1: a missing review check or a missing disclosure is also code 1", () => {
  const noReview = run(["7", "--promote"], okConfig({ checkRuns: [] }));
  assert.equal(noReview.code, 1);
  assert.ok(!promoted(noReview.calls));

  const noDisclosure = run(
    ["7", "--promote"],
    okConfig({ pr: { ...okConfig().pr, body: "A perfectly ordinary human PR body." } }),
  );
  assert.equal(noDisclosure.code, 1);
  assert.ok(!promoted(noDisclosure.calls));
});

// ---- exit 0: promoted, or deliberately left alone -------------------------

test("exit 0: a dry run reports the gates and mutates nothing", () => {
  const { code, stdout, calls } = run(["7"]);
  assert.equal(code, 0);
  assert.match(stdout, /All gates satisfied\. Re-run with --promote/);
  assert.ok(!promoted(calls), "without --promote nothing may be flipped");
  assert.ok(!calls.some((c) => c.startsWith("pr comment")));
});

test("exit 0: --promote flips the PR, sets one lifecycle label, posts the hand-off", () => {
  const { code, stdout, calls } = run(["7", "--promote"]);
  assert.equal(code, 0);
  assert.match(stdout, /Promoted PR #7 to ready/);
  assert.ok(promoted(calls), "code 0 under --promote must mean the PR was flipped");

  const put = calls.find((c) => c.startsWith("api -X PUT"));
  assert.ok(put, "the label set must be REPLACED, not appended to");
  assert.match(put, /labels\[\]=agent:ready/);
  assert.match(put, /labels\[\]=enhancement/, "non-agent labels survive");
  assert.doesNotMatch(put, /labels\[\]=agent:reviewing/, "the old lifecycle label is dropped");

  const comment = calls.find((c) => c.startsWith("pr comment"));
  assert.ok(comment?.includes(HANDOFF_MARKER), "harvest.mjs finds the hand-off by this marker");
});

test("exit 0: a NON-DRAFT PR still gets the label and the hand-off (#2033)", () => {
  // THE BUG THIS PINS. `agent:ready` and the hand-off comment used to sit BELOW
  // an `if (!pr.isDraft) process.exit(0)` early return, so a PR opened
  // non-draft — every `@claude loop` opt-in, and every human PR carrying
  // `agent:managed` — cleared all four gates and was then left on
  // `agent:reviewing` forever with no hand-off posted. The promote job exited 0
  // and reported `promoted`, so nothing anywhere said the hand-off had not
  // happened. Being out of draft is the state ONE of the three promotion steps
  // targets; it is not evidence the other two ran.
  const { code, stdout, calls } = run(
    ["7", "--promote"],
    okConfig({ pr: { ...okConfig().pr, isDraft: false } }),
  );
  assert.equal(code, 0);
  assert.ok(!promoted(calls), "there is no draft to flip, so `gh pr ready` must not be called");
  assert.match(stdout, /not a draft/);

  const put = calls.find((c) => c.startsWith("api -X PUT"));
  assert.ok(put, "the lifecycle label must still be set");
  assert.match(put, /labels\[\]=agent:ready/);
  assert.doesNotMatch(put, /labels\[\]=agent:reviewing/, "the PR must not stay on agent:reviewing");

  const comment = calls.find((c) => c.startsWith("pr comment"));
  assert.ok(comment?.includes(HANDOFF_MARKER), "the hand-off must still be posted");
});

test("exit 0: the hand-off is posted ONCE, however many green rounds run", () => {
  // The flip to ready used to be the de-facto idempotence guard: a PR could only
  // be promoted out of draft once. Promoting a non-draft PR removes that guard,
  // so the marker has to carry it — otherwise every later green panel round
  // re-posts the same hand-off.
  const { code, calls } = run(
    ["7", "--promote"],
    okConfig({
      pr: { ...okConfig().pr, isDraft: false },
      comments: [{ id: 1, body: `${HANDOFF_MARKER}\n## 🤝 Ready for human review` }],
    }),
  );
  assert.equal(code, 0);
  assert.ok(!calls.some((c) => c.startsWith("pr comment")), "no duplicate hand-off");

  // The label is still reasserted: it is a REPLACE and therefore idempotent, and
  // it is the one step that repairs a lifecycle label a later round moved away.
  assert.ok(calls.some((c) => c.startsWith("api -X PUT") && c.includes("labels[]=agent:ready")));
});

test("exit 0: the best-effort label and comment steps cannot change the code", () => {
  // Both run AFTER the flip; a failure there must not be reported as a gate
  // failure (1) or a promotion failure (3) — the PR really is ready.
  for (const fail of [["api -X PUT"], ["pr comment"], ["api -X PUT", "pr comment"]]) {
    const { code, calls, stdout } = run(["7", "--promote"], okConfig({ fail }));
    assert.equal(code, 0, `fail ${JSON.stringify(fail)} must still exit 0`);
    assert.ok(promoted(calls));
    assert.match(stdout, /Promoted PR #7 to ready/);
  }
});

test("the three promotion mutations go out with GH_MUTATION_TOKEN, the reads do not", () => {
  // `ghMutate`'s env override is the fix for "Resource not accessible by
  // integration": the default GITHUB_TOKEN cannot markPullRequestReadyForReview,
  // and dropping the override puts every gate-passing PR back to silently stuck
  // as a draft — with no test failure, because a stub `gh` succeeds either way.
  const { code, tokenCalls } = run(["7", "--promote"]);
  assert.equal(code, 0);

  const tokensFor = (prefix) => tokenCalls.filter((c) => c.call.startsWith(prefix)).map((c) => c.token);
  for (const mutation of ["pr ready", "api -X PUT", "pr comment"]) {
    const seen = tokensFor(mutation);
    assert.ok(seen.length > 0, `${mutation} must have been called`);
    for (const token of seen) {
      assert.equal(token, APP_TOKEN, `'${mutation}' must be sent with GH_MUTATION_TOKEN`);
    }
  }

  const reads = tokenCalls.filter((c) => !/^(pr ready|api -X PUT|pr comment)/.test(c.call));
  assert.ok(reads.length > 0, "the gates must still read the PR, the CI runs and the checks");
  for (const read of reads) {
    assert.equal(read.token, READ_TOKEN, `read '${read.call}' must keep the default token`);
  }
});

// ---- exit 3: gates passed, the flip failed (pages a human) ----------------

test("exit 3, not 1: gates passed but flipping to ready failed", () => {
  const { code, stderr, calls } = run(["7", "--promote"], okConfig({ fail: ["pr ready"] }));
  assert.equal(code, 3, "a permission/tooling failure after the gates is NOT exit 1");
  assert.match(stderr, /All ready-gates passed, but flipping PR #7 to ready FAILED/);
  assert.match(stderr, /GH_MUTATION_TOKEN/);
  // Nothing downstream of the failed flip may run — no label, no hand-off.
  assert.ok(!calls.some((c) => c.startsWith("api -X PUT")));
  assert.ok(!calls.some((c) => c.startsWith("pr comment")));
});

// ---- the contract's other half: the consumer -----------------------------

test("every process.exit in mark-ready is a LITERAL 0, 1, 2 or 3", () => {
  // The promote job's `case` enumerates exactly these four and defaults the
  // rest to a failed job, so a fifth code is now loud rather than silent — but
  // it is still a contract break, and it is still cheapest to catch here.
  //
  // The argument must be a literal, not merely `\d+`: `const c = 5;
  // process.exit(c)` reads as a legal exit site to a `(\d+)` census, and the
  // whole point of the census is that the set of codes is readable from source.
  const args = [...SOURCE.matchAll(/process\.exit\(([^)]*)\)/g)].map((m) => m[1].trim());
  assert.ok(args.length > 0, "the CLI must still signal through process.exit");
  for (const arg of args) {
    assert.match(
      arg,
      /^[0-3]$/,
      `process.exit(${arg}): the promote job branches on the literal codes 0/1/2/3 — ` +
        "a computed or out-of-range status is not part of the contract",
    );
  }
});

test("agent-review-panel.yml handles every mark-ready code, defaults the rest", () => {
  const yml = readFileSync(PANEL_YML, "utf8");
  // `-1` explicitly: `slice(-1)` is the LAST CHARACTER, not the empty string, so
  // a length check would pass on a workflow that no longer calls mark-ready at
  // all and the failure would surface as confusing regex misses instead.
  const at = yml.indexOf("node ./scripts/agent/mark-ready.mjs");
  assert.notEqual(at, -1, "the promote job must still invoke mark-ready.mjs");
  // Bound the window at the step that FOLLOWS, not at a character count. The
  // block is allowed to grow; a count only a little larger than it would keep
  // passing while quietly reading the next step's script instead.
  const nextStep = yml.slice(at).search(/\n {6}- name:/);
  assert.notEqual(nextStep, -1, "the promote step must still be followed by another step");
  const window = yml.slice(at, at + nextStep);

  // The invocation. `--promote` is what makes this a promotion at all — without
  // it mark-ready is a dry run that exits 0 on a satisfied gate and flips
  // nothing, so the job would export ready=true for a PR still sitting as a draft.
  assert.match(
    window,
    /mark-ready\.mjs "\$PR" --promote --require-checks "\$REQUIRED"/,
    "the gate must run with --promote and the panel's required checks",
  );
  // A REDIRECT, not a pipe: `| tee` makes `$?` the status of the last pipeline
  // element and the script's code — the only thing this block reads — is lost.
  //
  // `code=$?` is asserted BEFORE it is used as a slice bound, for the reason the
  // `-1` above exists: a missing needle makes `indexOf` return -1, `slice(0, -1)`
  // is the whole window minus its last character rather than the empty string,
  // and the `||` inside the `1)` branch would then trip the no-pipe check —
  // reporting a deleted `code=$?` as a pipe that isn't there.
  const codeCapture = window.indexOf("code=$?");
  assert.notEqual(codeCapture, -1, "the step must still capture mark-ready's status into $code");
  const invocation = window.slice(0, codeCapture);
  assert.match(invocation, />\s*"\$RUNNER_TEMP\/mark-ready\.log" 2>&1/, "output must be redirected to a log");
  assert.ok(!invocation.includes("|"), "the capture must not be a pipe, or $? is not mark-ready's");

  // The chain itself: a `case` with a default, not bare `if`s that fall through.
  const caseBlock = window.match(/case "\$code" in\n([\s\S]*?)\n\s*esac\b/);
  assert.ok(caseBlock, "branch with a closed `case`, so an unhandled status has somewhere to land");
  // A branch label is a line whose first non-space character starts the pattern,
  // which no comment line can be, running to that branch's `;;`.
  const branches = new Map([...caseBlock[1].matchAll(/^\s*([0-3]|\*)\)([\s\S]*?);;/gm)].map((m) => [m[1], m[2]]));
  assert.deepEqual(
    [...branches.keys()].sort(),
    ["*", "0", "1", "2", "3"],
    "each code mark-ready emits needs a branch, and everything else needs `*)`",
  );

  // What each branch has to DO. Asserted as behavior rather than as the exact
  // echo text, so reflowing a message — or reindenting the block — cannot fail
  // a test that is about exit-code handling.
  //
  // `exit` in command position, with quoted text blanked first: a message that
  // says "exit 1" is prose, not a branch that exits.
  const exits = (branch) =>
    [...branch.replace(/"[^"]*"/g, '""').matchAll(/(?:^|[;{&|(]\s*)exit\s+(\d+)/gm)].map((m) => Number(m[1]));

  assert.match(branches.get("0"), /ready=true.*>>\s*"\$GITHUB_OUTPUT"/, "0 → promoted");
  assert.deepEqual(exits(branches.get("0")), [], "0 → promoted, so the job succeeds");
  assert.deepEqual(exits(branches.get("2")), [2], "2 → tooling error, fail the job");
  assert.deepEqual(exits(branches.get("3")), [3], "3 → promotion failed, page a human");
  assert.deepEqual(
    exits(branches.get("*")),
    [2],
    "an unenumerated status (127, 128+signal) must fail the job, not fall through it",
  );

  // The `1` branch is the one branch that lets the job SUCCEED, so its body is
  // the load-bearing part. 1 is also node's own crash code, so it must
  // corroborate against the script's verdict, and the gates-said-no path — the
  // one that reaches the end of the branch — must not exit at all.
  const one = branches.get("1");
  assert.match(one, /grep -qF "([^"]+)" "\$RUNNER_TEMP\/mark-ready\.log"/, "a bare exit 1 must not be believed");
  assert.match(one, /\|\|\s*\{[^}]*exit 2;\s*\}/, "an exit 1 with no verdict line is a crash → fail the job");
  assert.deepEqual(exits(one), [2], "the crash guard is the branch's only exit; a real verdict leaves it a draft");

  // The `outcome` vocabulary is a contract BETWEEN TWO STEPS of the same file:
  // the gate writes it, and the loop-status step branches on it to name why a
  // PR was not promoted. A rename on either side degrades silently to the
  // "cancelled, killed, or never ran" default — the exact mis-attribution the
  // outcome output exists to remove — so assert both sides agree.
  const written = new Set([...window.matchAll(/outcome=([a-z-]+)/g)].map((m) => m[1]));
  assert.ok(written.has("promoted"), "the promoted branch must record its outcome too");
  // `promoted` is the one outcome loop-status does NOT read off `$OUTCOME` — it
  // takes the `ready=true` arm above the case. Every other one must be named.
  const notPromoted = [...written].filter((o) => o !== "promoted");
  assert.ok(notPromoted.length >= 4, `every non-promoting branch needs an outcome, saw ${notPromoted}`);

  const status = yml.slice(yml.indexOf("- name: Update loop status (promotion)"));
  const caseAt = status.indexOf('case "$OUTCOME" in');
  assert.notEqual(caseAt, -1, "loop-status must branch on the gate's outcome");
  const readCase = status.slice(caseAt, status.indexOf("esac", caseAt));
  const read = new Set([...readCase.matchAll(/^\s+([a-z-]+)\)/gm)].map((m) => m[1]));
  for (const outcome of notPromoted) {
    assert.ok(read.has(outcome), `the gate writes outcome=${outcome}, which loop-status does not name`);
  }
  for (const outcome of read) {
    assert.ok(written.has(outcome), `loop-status names ${outcome}, which no gate branch writes`);
  }
  assert.match(readCase, /^\s+\*\)/m, "an unrecorded outcome (cancelled, killed) needs its own note");

  // CROSS-FILE CONTRACT: the string the workflow greps for has to be a string
  // mark-ready actually prints, or every exit 1 reads as a crash and every
  // legitimately-unready PR reds the job.
  const needle = one.match(/grep -qF "([^"]+)"/)[1];
  const gateSaidNo = run(["7", "--promote"], okConfig({ workflowRuns: [] }));
  assert.equal(gateSaidNo.code, 1);
  assert.ok(
    gateSaidNo.stdout.includes(needle),
    `the promote job greps for ${JSON.stringify(needle)}, which mark-ready no longer prints`,
  );
});
