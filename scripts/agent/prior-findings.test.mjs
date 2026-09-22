import { test } from "node:test";
import assert from "node:assert/strict";
import { readWorkflow, skipWithout } from "./workflow-presence.mjs";

const PANEL_WORKFLOW_NAME = "agent-review-panel.yml";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  tagPriorFindings,
  lensCheckNames,
  collectPrior,
  carryForwardFindings,
  INFRA_SENTINEL,
} from "./prior-findings.mjs";
import { parseArgs, commitCheckRuns, prCommitsWithCheckRuns } from "./gh-checks.mjs";
import { allSamplesFailedError, lensFailureSummary } from "./review-panel.mjs";
import { poolExhaustedError } from "./ask.mjs";

/**
 * The record `review-panel.mjs` synthesises for a lens with no usable verdict,
 * built from the two functions the panel itself composes.
 *
 * The round trip is the point: these tests assert PROPAGATION, and a fixture
 * hand-written to look like the producer's output would keep passing after the
 * producer stopped emitting it. That is exactly how the pool-exhaustion leak
 * survived — both sides were tested, in isolation, against each other's assumed
 * shape. The literal `severity`/`summary`/`infra` spread mirrors the panel's
 * `failFindings`; the classification it depends on is imported, not restated.
 */
const synthesisedRecord = (results) => {
  const err = allSamplesFailedError(results);
  return { severity: "major", summary: lensFailureSummary(err), ...(err.infra ? { infra: true } : {}) };
};

// One drained-pool sample, exactly as `runSample`'s catch projects the thrown
// error onto the results array.
const drainedPoolSample = () => {
  const e = poolExhaustedError({ label: "review", pool: { size: 2 } });
  return { __error: e.message, kind: e.kind, status: e.status, detail: e.detail, code: e.code, reason: e.reason };
};

const NAMES = ["agent-review-correctness", "agent-review-security"];

// --- tagPriorFindings -------------------------------------------------------

test("tagPriorFindings: tags each finding with its lens, parsing lenses independently", () => {
  const runs = new Map([
    ["agent-review-correctness", { output: { text: JSON.stringify([{ severity: "major", summary: "x" }]) } }],
    ["agent-review-security", { output: { text: "{not json" } }],
    ["agent-review-design-fit", { output: { text: JSON.stringify([{ severity: "critical", summary: "y" }]) } }],
  ]);
  // ONE lens's garbage must not zero the others. Prior findings can only re-raise
  // a blocker, never clear one, so losing one lens's carry-forward is strictly
  // better than failing the round for all five.
  assert.deepEqual(tagPriorFindings(runs), [
    { severity: "major", summary: "x", lens: "correctness" },
    { severity: "critical", summary: "y", lens: "design-fit" },
  ]);
});

test("tagPriorFindings: absent output is NOT 'found nothing'; junk never throws", () => {
  // A clean lens legitimately persists "[]", so an ABSENT payload means we cannot
  // see what this lens found — carry nothing for it rather than assert innocence.
  assert.deepEqual(tagPriorFindings({ "agent-review-correctness": { output: { text: "" } } }), []);
  assert.deepEqual(tagPriorFindings({ "agent-review-correctness": { output: {} } }), []);
  assert.deepEqual(tagPriorFindings({ "agent-review-correctness": {} }), []);
  assert.deepEqual(tagPriorFindings({ "agent-review-correctness": { output: { text: "[]" } } }), []);
  // non-array payload, non-object entries inside the array
  assert.deepEqual(tagPriorFindings({ "agent-review-x": { output: { text: '{"a":1}' } } }), []);
  assert.deepEqual(tagPriorFindings({ "agent-review-x": { output: { text: "[null,7,[]]" } } }), []);
  for (const bad of [null, undefined, "x", 7, []]) assert.deepEqual(tagPriorFindings(bad), []);
  // a finding cannot spoof its origin: `lens` is applied last
  assert.equal(
    tagPriorFindings({ "agent-review-security": { output: { text: '[{"summary":"s","lens":"correctness"}]' } } })[0].lens,
    "security",
  );
});

test("tagPriorFindings: an infra/quota record is never carried forward", () => {
  // A lens that hit a 429 session limit writes a synthetic infra record so it
  // fails closed. That is not a code finding — carrying it into the next round
  // would make the panel re-check "the review could not run", and the verifier
  // (biased to keep) cannot refute it. It must be dropped on read — whether it
  // carries the { infra: true } flag (records written after the fix) OR only the
  // stable message prefix with the synthetic shape (records persisted BEFORE the
  // flag existed — a PR contaminated by a pre-fix 429 round, which stuck #632).
  const runs = new Map([
    // Mixed check: the synthetic infra record AND a real finding — the infra one
    // is dropped, the real finding is preserved (CodeRabbit: real prior findings
    // followed by an infrastructure failure must survive).
    ["agent-review-correctness", { output: { text: JSON.stringify([
      { severity: "major", summary: "Review could not run — Claude API/quota error (429): You've hit your session limit", infra: true },
      { severity: "major", file: "src/a.ts", summary: "real blocker" },
    ]) } }],
    // Legacy record: no `infra` flag, matched by prefix + synthetic shape (no file).
    ["agent-review-security", { output: { text: JSON.stringify([
      { severity: "major", summary: "Review could not run — Claude API/quota error (429): You've hit your session limit · resets 3:40am (UTC)" },
    ]) } }],
  ]);
  assert.deepEqual(tagPriorFindings(runs), [
    { severity: "major", file: "src/a.ts", summary: "real blocker", lens: "correctness" },
  ]);
});

test("tagPriorFindings: a REAL finding is not suppressed by mimicking the infra text", () => {
  // `summary` is model output. A genuine finding whose summary happens to begin
  // with the infra sentinel (or one an injected diff crafts to look like it) must
  // stay a blocker: it cites a `file`, which the script-authored synthetic record
  // never does, and only `infra: true` (script-set) is authoritative on its own.
  const runs = new Map([
    ["agent-review-security", { output: { text: JSON.stringify([
      { severity: "critical", file: "src/auth.ts", summary: "Review could not run — Claude API/quota error is logged with the raw token" },
    ]) } }],
  ]);
  assert.deepEqual(tagPriorFindings(runs), [
    { severity: "critical", file: "src/auth.ts", summary: "Review could not run — Claude API/quota error is logged with the raw token", lens: "security" },
  ]);
});

test("lensCheckNames: manifest ids → check names; junk → []", () => {
  assert.deepEqual(lensCheckNames([{ id: "correctness" }, { id: "blast-radius" }]),
    ["agent-review-correctness", "agent-review-blast-radius"]);
  assert.deepEqual(lensCheckNames([{ id: "" }, {}, null, { id: 7 }]), []);
  for (const bad of [null, undefined, "x", 7, {}]) assert.deepEqual(lensCheckNames(bad), []);
});

// --- parseArgs --------------------------------------------------------------

test("parseArgs: flags in any position, and no prototype write", () => {
  const a = parseArgs(["node", "s", "581", "--lenses", "l.json", "--out", "o.json"]);
  assert.equal(a._[0], "581");
  assert.equal(a.lenses, "l.json");
  assert.equal(a.out, "o.json");
  // Flag-first must work too: reading the PR from argv[2] made this a usage error.
  assert.equal(parseArgs(["node", "s", "--lenses", "l.json", "581"])._[0], "581");
  // `--__proto__ x` on an object literal sets the PROTOTYPE, not a key, so the
  // value vanishes and every later object is polluted. Object.create(null) is why.
  const p = parseArgs(["node", "s", "--__proto__", '{"polluted":1}', "7"]);
  assert.equal(({}).polluted, undefined, "Object.prototype must be untouched");
  assert.equal(p.__proto__, '{"polluted":1}', "it is an ordinary key here, not the prototype");
  for (const bad of [null, undefined, "x", 7]) assert.deepEqual(parseArgs(bad)._, []);
});

// --- collectPrior: the API half ---------------------------------------------

const COMMITS = ["api", "--paginate", "repos/{owner}/{repo}/pulls/581/commits?per_page=100"];
const isFullRun = (args) => /\/check-runs\/\d+$/.test(args[1]);
const findings = (n) => JSON.stringify([{ severity: "major", summary: n }]);
const run = (name, id, over = {}) => ({
  name, id, status: "completed", app: { slug: "github-actions" },
  completed_at: "2026-07-20T10:00:00Z", ...over,
});
const quiet = () => {};

// The defect this module was reported for: reading output.text off the LIST
// response. GitHub omits or truncates it there, so the payload either fails to
// parse or is absent, and the lens's whole carry-forward silently becomes zero.
test("collectPrior: back-fills output.text with a per-run fetch, not the list copy", () => {
  const calls = [];
  const api = (args) => {
    calls.push(args);
    if (args.join(" ") === COMMITS.join(" ")) return [{ sha: "s1" }];
    // The list response as GitHub actually returns it: no output.text at all.
    if (isFullRun(args)) return { ...run("agent-review-correctness", 11), output: { text: findings("real") } };
    return [{ check_runs: [run("agent-review-correctness", 11, { output: { title: "t" } })] }];
  };
  const got = collectPrior({ pr: "581", names: NAMES, api, log: quiet });
  assert.deepEqual(got, [{ severity: "major", summary: "real", lens: "correctness" }]);
  assert.ok(calls.some((c) => isFullRun(c)), "must fetch the selected run in full");
  // A truncated list copy is worse than an absent one — it fails JSON.parse.
  const truncated = (args) => {
    if (args.join(" ") === COMMITS.join(" ")) return [{ sha: "s1" }];
    if (isFullRun(args)) return { output: { text: findings("real") } };
    return [{ check_runs: [run("agent-review-correctness", 11, { output: { text: findings("real").slice(0, 20) } })] }];
  };
  assert.equal(collectPrior({ pr: "581", names: NAMES, api: truncated, log: quiet }).length, 1);
});

// `--slurp` on the object-wrapped check-runs endpoint. Plain --paginate emits
// concatenated per-page objects, which is invalid JSON; the repo verified this
// once already in review-round-guard.mjs. A stub that returns PAGES proves the
// call asks for slurped output and that pages are flattened.
test("collectPrior: asks for --slurp on check-runs and flattens every page", () => {
  const api = (args) => {
    if (args.join(" ") === COMMITS.join(" ")) return [{ sha: "s1" }];
    if (isFullRun(args)) {
      const id = Number(args[1].split("/").pop());
      return { output: { text: findings(id === 11 ? "page1" : "page2") } };
    }
    assert.ok(args.includes("--slurp"), "check-runs MUST be slurped or the JSON is invalid");
    // Two pages, one lens run on each.
    return [
      { check_runs: [run("agent-review-correctness", 11)] },
      { check_runs: [run("agent-review-security", 12)] },
    ];
  };
  const got = collectPrior({ pr: "581", names: NAMES, api, log: quiet });
  assert.deepEqual(got.map((f) => f.lens).sort(), ["correctness", "security"]);
});

// Fail isolation. The single outer try this replaced turned any one failed call
// into "0 prior findings for all five lenses" — indistinguishable from a clean
// round, and it silently disables the cross-round re-check.
test("collectPrior: one bad commit or one bad run-fetch does not zero the rest", () => {
  const api = (args) => {
    if (args.join(" ") === COMMITS.join(" ")) return [{ sha: "bad" }, { sha: "good" }];
    if (isFullRun(args)) {
      if (args[1].endsWith("/12")) throw new Error("500 on the full fetch");
      return { output: { text: findings("kept") } };
    }
    if (args[1].includes("/bad/")) throw new Error("422 unprocessable");
    return [{
      check_runs: [
        run("agent-review-correctness", 11),
        // Its full fetch throws, so the list copy is used — which here HAS text.
        run("agent-review-security", 12, { output: { text: findings("fallback") } }),
      ],
    }];
  };
  const got = collectPrior({ pr: "581", names: NAMES, api, log: quiet });
  assert.deepEqual(got.map((f) => f.summary).sort(), ["fallback", "kept"]);
});

test("collectPrior: never throws — a failed commit list is [] and junk is []", () => {
  const boom = () => { throw new Error("gh: not authenticated"); };
  assert.deepEqual(collectPrior({ pr: "581", names: NAMES, api: boom, log: quiet }), []);
  for (const payload of [null, "x", 7, {}, [null, 7, { sha: null }]]) {
    assert.deepEqual(collectPrior({ pr: "581", names: NAMES, api: () => payload, log: quiet }), []);
  }
  // No lens names → nothing can match, and it must not throw on the way there.
  assert.deepEqual(collectPrior({ pr: "581", names: [], api: () => [{ sha: "s1" }], log: quiet }), []);
});

// --- commitCheckRuns --------------------------------------------------------

test("commitCheckRuns: slurps and flattens, exactly as the multi-commit path does", () => {
  const api = (args) => {
    assert.ok(args.includes("--slurp"), "check-runs MUST be slurped or the JSON is invalid");
    assert.ok(args.some((a) => a.includes("/commits/abc/check-runs")));
    return [{ check_runs: [{ id: 1 }] }, { check_runs: [{ id: 2 }] }];
  };
  assert.deepEqual(commitCheckRuns("abc", { api }), [{ id: 1 }, { id: 2 }]);
  for (const junk of [null, "x", 7, [null, {}, { check_runs: null }]]) {
    assert.deepEqual(commitCheckRuns("abc", { api: () => junk }), []);
  }
});

// The two functions fail in OPPOSITE directions, and callers depend on which.
// `prCommitsWithCheckRuns` swallows per commit, because losing one commit's runs
// must not cost the other commits'. `commitCheckRuns` has no catch at all: its
// caller asked about ONE commit, so swallowing would hand back "this commit had
// no check runs" — which reads as "the panel never ran" — when the truth is "we
// could not look". harvest.mjs relies on being able to tell those apart.
test("commitCheckRuns: THROWS on a bad response, unlike the per-commit path", () => {
  const boom = () => { throw new Error("422 unprocessable"); };
  assert.throws(() => commitCheckRuns("abc", { api: boom }), /422/);
  const api = (args) =>
    args.includes("--paginate") && args.some((a) => a.includes("/pulls/"))
      ? [{ sha: "bad" }]
      : boom();
  assert.deepEqual(prCommitsWithCheckRuns("1", { api, log: () => {} }), [{ sha: "bad", checkRuns: [] }]);
});

test("parseArgs: value-less flags must be DECLARED, not inferred from what follows", () => {
  // Inference fails in both directions. `--append` at the end of argv reads the
  // next token — undefined — and becomes falsy, which is how harvest.mjs printed
  // instead of writing while exiting 0 and reporting success.
  const a = parseArgs(["node", "s", "--pr", "548", "--append"], { booleans: ["append"] });
  assert.equal(a.append, true);
  assert.equal(a.pr, "548");
  // Order must not matter for a boolean either.
  assert.equal(parseArgs(["node", "s", "--append", "--pr", "548"], { booleans: ["append"] }).pr, "548");
  // Undeclared → unchanged, so no existing caller shifts behavior.
  assert.equal(parseArgs(["node", "s", "--append"]).append, undefined);
  // An EMPTY string is a value, not a flag — review-panel.mjs passes possibly-empty
  // --since-sha/--review-mode unconditionally and relies on this.
  assert.equal(parseArgs(["node", "s", "--since-sha", "", "--head", "x"])["since-sha"], "");
});

test("commitCheckRuns: a non-array check_runs contributes nothing, not itself", () => {
  // `p?.check_runs ?? []` would spread a scalar or object straight into the run
  // list via flatMap, and it would travel downstream as if it were a run.
  for (const payload of [7, "x", { id: 1 }, true]) {
    assert.deepEqual(commitCheckRuns("abc", { api: () => [{ check_runs: payload }] }), []);
  }
  // A good page alongside a junk one still yields the good runs.
  assert.deepEqual(
    commitCheckRuns("abc", { api: () => [{ check_runs: 7 }, { check_runs: [{ id: 2 }] }] }),
    [{ id: 2 }],
  );
});

// --- permissionResolver -----------------------------------------------------

test("permissionResolver: accepts write-ish levels from either field", async () => {
  const { permissionResolver } = await import("./gh-checks.mjs");
  // The legacy `permission` field collapses maintain→write on some responses and
  // the granular `role_name` is absent on others; the workflows accept either.
  for (const d of [{ permission: "admin" }, { permission: "maintain" }, { permission: "write" }, { role_name: "maintain" }]) {
    assert.equal(permissionResolver({ api: () => d })("u"), true, JSON.stringify(d));
  }
  for (const d of [{ permission: "read" }, { permission: "none" }, { role_name: "triage" }, {}]) {
    assert.equal(permissionResolver({ api: () => d })("u"), false, JSON.stringify(d));
  }
});

test("permissionResolver: an API failure is null, not false", async () => {
  const { permissionResolver } = await import("./gh-checks.mjs");
  // A 404 really is "not a collaborator", but a 403/5xx is "we could not ask" and
  // the CLI's exit status cannot tell them apart — so the caller decides.
  const r = permissionResolver({ api: () => { throw new Error("gh: 502"); }, log: () => {} });
  assert.equal(r("u"), null);
  assert.equal(r(""), null, "an empty login is unknown, never a lookup");
  assert.equal(r(undefined), null);
});

test("permissionResolver: memoizes per login, failures included", async () => {
  const { permissionResolver } = await import("./gh-checks.mjs");
  let calls = 0;
  const r = permissionResolver({ api: () => { calls++; return { permission: "write" }; } });
  r("a"); r("a"); r("b"); r("a");
  assert.equal(calls, 2, "one call per distinct login");
  // A login that failed must not be re-asked for every comment on the PR.
  let boom = 0;
  const r2 = permissionResolver({ api: () => { boom++; throw new Error("404"); }, log: () => {} });
  r2("x"); r2("x"); r2("x");
  assert.equal(boom, 1);
});

test("permissionResolver: the login is URL-encoded into the path", async () => {
  const { permissionResolver } = await import("./gh-checks.mjs");
  let seen = "";
  permissionResolver({ api: (a) => { seen = a[1]; return {}; } })("odd name/../x");
  assert.equal(seen.includes("/../"), false, "a login must not escape the path");
  assert.match(seen, /collaborators\/odd%20name%2F\.\.%2Fx\/permission$/);
});

// --- the drained-pool leak, producer to consumer ----------------------------
// Measured on 16 agent PRs: 23 of 536 gate-channel findings were a drained
// credential pool wearing the shape of a blocking code finding. The chain has
// three links and the tests below pin all three, because breaking any one of
// them puts the record back in front of the fix agent.

test("a drained credential pool is INFRASTRUCTURE, and never reaches the next round", () => {
  const record = synthesisedRecord([drainedPoolSample()]);
  // Link 1: the producer classifies it. `infra: true` is the authoritative,
  // unforgeable signal — the consumer's other branch is a legacy prefix match on
  // model-controlled text, and must not be the thing that saves us here.
  assert.equal(record.infra, true);
  // Link 2: it is published under the wire-format prefix both parsers recognise,
  // carrying the closed-vocabulary code rather than "did not produce a verdict".
  assert.match(record.summary, /^Review could not run — Claude API\/quota error: \[POOL_EXHAUSTED\]/);
  // Link 3: the consumer drops it. This is the whole point — carrying it forward
  // hands the fix agent "the review could not run" as a work item and sends it to
  // a verifier that cannot refute it on grounded evidence.
  const runs = new Map([
    ["agent-review-blast-radius", { output: { text: JSON.stringify([record]) } }],
    // A real finding from another lens in the same round still survives.
    ["agent-review-correctness", { output: { text: JSON.stringify([{ severity: "major", file: "src/a.ts", summary: "real blocker" }]) } }],
  ]);
  assert.deepEqual(tagPriorFindings(runs), [
    { severity: "major", file: "src/a.ts", summary: "real blocker", lens: "correctness" },
  ]);
});

test("a genuine no-verdict is NOT infrastructure, and IS carried forward", () => {
  // THE safety property, and the reason this fix is narrow rather than a widened
  // summary match. The model ran here: it spent 26 turns and failed to produce
  // valid structured output. There IS a review to re-attempt, so the record stays
  // an ordinary fail-closed blocker. Two of the 25 measured records are this, and
  // they are supposed to survive. Marking them infra would be the #521 false
  // negative — a round that found nothing usable silently ceasing to block.
  const ranButProducedNothing = {
    __error: "review query hit a run limit: [RUN_LIMIT_OUTPUT_RETRIES] structured-output retry ceiling reached (26 turns)",
    kind: "limit", status: null, code: "RUN_LIMIT_OUTPUT_RETRIES",
  };
  const record = synthesisedRecord([ranButProducedNothing]);
  assert.equal(record.infra, undefined, "a model that ran must not be tagged infrastructure");
  assert.match(record.summary, /^Reviewer did not produce a valid verdict:/);
  const runs = new Map([["agent-review-security", { output: { text: JSON.stringify([record]) } }]]);
  assert.deepEqual(tagPriorFindings(runs), [{ ...record, lens: "security" }]);
  // Same for a session that returned nothing at all.
  const noOutput = synthesisedRecord([{ __error: "review query: structured output not produced", kind: "no-output" }]);
  assert.equal(noOutput.infra, undefined);
  assert.deepEqual(tagPriorFindings(new Map([["agent-review-security", { output: { text: JSON.stringify([noOutput]) } }]])),
    [{ ...noOutput, lens: "security" }]);
});

test("a MIXED round — one sample never ran, one ran and produced nothing — is infrastructure", () => {
  // ANY never-ran sample decides, not every one, and the quantifier is deliberate.
  // Samples fan out concurrently (`sampleWithWarmup`), so a sibling lens can drain
  // the pool while this lens's other sample is still burning turns; the mix is
  // reachable, in either order.
  //
  // Requiring EVERY sample to have never run would put this round back on the leak
  // path: no `infra` flag, so a content-free "major" record — no `file`, no
  // evidence, nothing to fix — is carried to the fix agent and to a verifier that
  // cannot refute it, which is the defect this whole change removes. Tagging it
  // infrastructure costs far less: the round still fails (`conclusion: "failure"`
  // and `valid: false` are set on BOTH branches, so blocking is not affected by
  // this flag at all) and the lens simply runs again next round.
  const pool = drainedPoolSample();
  const limit = { __error: "review query hit a run limit: [RUN_LIMIT_TURNS] turn ceiling reached (26 turns)", kind: "limit", status: null, code: "RUN_LIMIT_TURNS" };
  // Order-independent: `results[0]` supplies the message, but the flag comes from
  // the first never-ran sample wherever it sits.
  for (const [label, results] of [["never-ran first", [pool, limit]], ["never-ran second", [limit, pool]]]) {
    const record = synthesisedRecord(results);
    assert.equal(record.infra, true, `${label}: a drained pool must still be infrastructure`);
    assert.match(record.summary, /^Review could not run — Claude API\/quota error: \[POOL_EXHAUSTED\]/, label);
    assert.deepEqual(tagPriorFindings(new Map([["agent-review-security", { output: { text: JSON.stringify([record]) } }]])), [], label);
  }
});

test("the legacy summary-prefix branch is unchanged by the fix", () => {
  // Widening `isInfraRecord` to also match "Reviewer did not produce a valid
  // verdict" would have fixed the leak too, and it was the wrong fix: `summary` is
  // MODEL output, so it would hand a model a way to write a summary that gets its
  // own finding silently dropped. These pin that the fallback still matches only
  // the one legacy sentinel, and still requires the synthetic no-file shape.
  const runs = new Map([
    // Still dropped: legacy sentinel + no file, no flag (records predating it).
    ["agent-review-correctness", { output: { text: JSON.stringify([
      { severity: "major", summary: "Review could not run — Claude API/quota error (429): session limit" },
    ]) } }],
    // Still NOT dropped: the other synthetic prefix without the flag. A record
    // that reaches here unflagged is a genuine no-verdict, and prose alone must
    // never be enough to drop it.
    ["agent-review-security", { output: { text: JSON.stringify([
      { severity: "major", summary: "Reviewer did not produce a valid verdict: something went wrong" },
    ]) } }],
    // Still NOT dropped: a real finding quoting the sentinel. It cites a file,
    // which the script-authored record never does.
    ["agent-review-design-fit", { output: { text: JSON.stringify([
      { severity: "critical", file: "src/log.ts", summary: "Review could not run — Claude API/quota error is logged verbatim with the token" },
    ]) } }],
  ]);
  assert.deepEqual(tagPriorFindings(runs), [
    { severity: "major", summary: "Reviewer did not produce a valid verdict: something went wrong", lens: "security" },
    { severity: "critical", file: "src/log.ts", summary: "Review could not run — Claude API/quota error is logged verbatim with the token", lens: "design-fit" },
  ]);
});

// --- the local carry-forward projection --------------------------------------

test("carryForwardFindings: blocking, not demoted, lens-tagged", () => {
  const verdict = { findings: [
    { severity: "critical", file: "a.ts", summary: "one" },
    { severity: "major", file: "b.ts", summary: "two" },
    { severity: "minor", file: "c.ts", summary: "not blocking" },
    { severity: "nit", file: "d.ts", summary: "not blocking" },
    { severity: "critical", file: "e.ts", summary: "demoted", lane: "backlog" },
  ] };
  assert.deepEqual(carryForwardFindings(verdict, "correctness"), [
    { severity: "critical", file: "a.ts", summary: "one", lens: "correctness" },
    { severity: "major", file: "b.ts", summary: "two", lens: "correctness" },
  ]);
});

test("carryForwardFindings: unknown severity is blocking (fail-safe), junk is []", () => {
  // `normalizeSeverity` maps anything unrecognised to `major`, so a lens that
  // invents a severity cannot drop its own finding out of the carry-forward.
  assert.deepEqual(
    carryForwardFindings({ findings: [{ severity: "spicy", file: "a.ts", summary: "x" }] }, "docs"),
    [{ severity: "spicy", file: "a.ts", summary: "x", lens: "docs" }],
  );
  assert.deepEqual(carryForwardFindings(undefined, "docs"), []);
  assert.deepEqual(carryForwardFindings({ findings: "nope" }, "docs"), []);
  assert.deepEqual(carryForwardFindings({ findings: [null, 42, ["x"]] }, "docs"), []);
});

test("carryForwardFindings: drops the synthesised infra record", () => {
  const verdict = { findings: [
    { severity: "major", summary: `${INFRA_SENTINEL} (429): session limit`, infra: true },
    { severity: "major", file: "a.ts", summary: "real" },
  ] };
  assert.deepEqual(carryForwardFindings(verdict, "security"), [
    { severity: "major", file: "a.ts", summary: "real", lens: "security" },
  ]);
});

// THE DRIFT GUARD. `agent-review-panel.yml` applies this same selection inline,
// as github-script, when it writes a lens check run's `output.text` — the cloud's
// carry-forward channel. That copy cannot import (the step does no checkout of
// this file's directory into the step's module graph), so the two must agree by
// inspection. A drifted copy does not error: the cloud and the local loop would
// simply gate on different findings, which is the silent failure the extraction
// exists to prevent. Mirrors the PAGED_LATCH guard in rounds.test.mjs.
test("the panel workflow's inline copy applies both selection filters", skipWithout(PANEL_WORKFLOW_NAME), () => {
  const workflow = readWorkflow(PANEL_WORKFLOW_NAME);
  assert.ok(
    workflow.includes("norm(f.severity) === 'critical' || norm(f.severity) === 'major'"),
    "the inline copy must still carry blocking severities only",
  );
  assert.ok(
    workflow.includes("f.lane !== 'backlog'"),
    "the inline copy must still drop backlog-demoted findings",
  );
});

test("carryForwardFindings: the projection carries an explicit field list, nothing else", () => {
  // verdict.json holds MODEL OUTPUT. A spread would carry every key a lens chose
  // to write — `infra`, `lane`, `valid` — into the next round's verifier prompt
  // and into `isInfraRecord`'s reach. The cloud's projection rebuilds from an
  // explicit field list; this asserts the local one does too, on the EVIDENCE
  // fields as well as the identifying ones, since those are what the verifier
  // re-checks against.
  //
  // It deliberately does NOT use a `lane: "backlog"` fixture: the lane filter
  // runs before the projection, so such a record is dropped by the lane rule and
  // an assertion on it would hold whether or not the projection exists. The
  // forged-`infra` ordering contract is asserted on its own, below.
  const noisy = {
    severity: "critical",
    file: "a.ts",
    line: 12,
    summary: "a real finding",
    evidence: "a.ts:12 does the thing",
    claimType: "absence",
    searchedFor: ["theThing("],
    mergedFrom: ["security"],
    adjudication: { upheld: 2, notes: "dropped" },
    valid: false,
    lane: "primary",
    extra: "model chatter",
  };
  assert.deepEqual(carryForwardFindings({ findings: [noisy] }, "security"), [{
    severity: "critical",
    file: "a.ts",
    line: 12,
    summary: "a real finding",
    evidence: "a.ts:12 does the thing",
    claimType: "absence",
    searchedFor: ["theThing("],
    mergedFrom: ["security"],
    adjudication: { upheld: 2 },
    lens: "security",
  }]);
});

test("carryForwardFindings: the lane rule drops a backlog-demoted finding", () => {
  // Separate from the projection test above so neither passes for the other's
  // reason: a demoted finding is nobody's to fix and can never shrink round over
  // round, so carrying it would make the loop unable to converge.
  const demoted = { severity: "critical", file: "a.ts", summary: "a real finding", lane: "backlog" };
  assert.deepEqual(carryForwardFindings({ findings: [demoted] }, "security"), []);
});

test("carryForwardFindings: a forged `infra` key cannot suppress a real finding", () => {
  // THE ORDER IS THE FIX. `isInfraRecord` treats `infra: true` as authoritative
  // because the PRODUCER sets it — true of a check run's projected text, false of
  // a raw verdict.json, where the key sits on model output. Filtering before the
  // projection let a finding drop itself from its own lens's report AND from
  // every later round, after gating the one that raised it.
  const forged = { severity: "critical", file: "a.ts", summary: "a real finding", infra: true };
  assert.deepEqual(carryForwardFindings({ findings: [forged] }, "security"), [
    { severity: "critical", file: "a.ts", summary: "a real finding", lens: "security" },
  ]);
  // The genuine synthesised record still goes, on its shape rather than its flag:
  // no file, and the stable sentinel prefix.
  const synthetic = { severity: "major", summary: `${INFRA_SENTINEL} (429): session limit`, infra: true };
  assert.deepEqual(carryForwardFindings({ findings: [synthetic] }, "security"), []);
});
