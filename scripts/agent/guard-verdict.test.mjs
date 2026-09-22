import test from "node:test";
import assert from "node:assert/strict";
import {
  guardVerdictLine,
  renderGuardSummary,
  runUrlFromEnv,
  whereToLookLine,
  emitBestEffortWarning,
} from "./guard-verdict.mjs";

test("proceed line names the round being dispatched, not the failed count", () => {
  const line = guardVerdictLine({
    decision: "proceed",
    failedRounds: 1,
    max: 3,
    stall: { reason: "progressing", stalls: 0, rounds: 2 },
    standstillCount: 0,
  });
  assert.match(line, /proceed/);
  assert.match(line, /fix round 2 of 3/);
  assert.match(line, /progressing/);
  assert.match(line, /standstill: 0/);
});

test("verdict line is single-line even when inputs carry newlines", () => {
  const line = guardVerdictLine({ decision: "page", reason: "stall\nmultiline" });
  assert.ok(!/\n/.test(line));
});

test("page line names the known reason in plain language", () => {
  assert.match(guardVerdictLine({ decision: "page", reason: "round-cap" }), /budget is exhausted/);
  assert.match(guardVerdictLine({ decision: "page", reason: "infra" }), /API\/quota/);
  // Unknown reasons pass through rather than throwing — the guard may grow
  // a page path faster than this map.
  assert.match(guardVerdictLine({ decision: "page", reason: "novel-reason" }), /novel-reason/);
});

test("latched decision renders without any round data", () => {
  assert.match(guardVerdictLine({ decision: "latched" }), /already paged/);
  assert.match(renderGuardSummary({ decision: "latched" }), /SKIPPED \(already paged\)/);
});

test("page summary tolerates the early paths (no rounds counted yet)", () => {
  // infra and invalid-verdict page BEFORE commits are fetched: failedRounds is
  // null and must not render as "null of 3".
  const md = renderGuardSummary({ decision: "page", reason: "infra", detail: "quota exceeded" });
  assert.match(md, /PAGED \(infra\)/);
  assert.ok(!md.includes("null"));
  // An uncounted round budget must not render as a confident "0 of 3".
  assert.ok(!/Fix rounds dispatched/.test(md));
  assert.match(md, /```text\nquota exceeded\n```/);
});

test("page detail is fenced inert — embedded fences and markdown cannot break out", () => {
  // Stall/standstill pages embed finding summaries derived from the untrusted
  // diff; a crafted summary must not render as structure on the run page.
  const md = renderGuardSummary({
    decision: "page",
    reason: "stall",
    detail: "x\n```\n## fake heading <img src=x>\n````",
  });
  const fenced = md.slice(md.indexOf("```text"));
  assert.ok(!fenced.slice(7).includes("```\n## fake"), "inner fence is defanged");
  assert.match(md, /···/);
});

test("page summary includes the round count when it WAS measured", () => {
  const md = renderGuardSummary({ decision: "page", reason: "round-cap", failedRounds: 3, max: 3 });
  assert.match(md, /Fix rounds dispatched so far: 3 of 3/);
});

test("proceed summary carries every decision input", () => {
  const md = renderGuardSummary({
    decision: "proceed",
    failedRounds: 0,
    max: 3,
    stall: { reason: "too-few-rounds", stalls: 0, rounds: 1 },
    standstillCount: 0,
    rebuttalLimit: 2,
    infra: "",
    heldByRerun: false,
    rerunAt: null,
    requiredCheckNames: ["agent-review-correctness"],
  });
  assert.match(md, /PROCEED/);
  assert.match(md, /0 of 3/);
  assert.match(md, /`too-few-rounds`/);
  assert.match(md, /2-uphold rebuttal limit/);
  assert.match(md, /Infra: none/);
  assert.match(md, /agent-review-correctness/);
});

test("rerun hand-back is stated when it holds the softer pages", () => {
  const md = renderGuardSummary({
    decision: "proceed",
    failedRounds: 0,
    max: 3,
    heldByRerun: true,
    standstillCount: 0,
  });
  assert.match(md, /held for this one attempt/);
});

// --- "Where to look" (Phase 2) ----------------------------------------------

test("runUrlFromEnv: a full env yields the run URL; any missing piece yields null", () => {
  const env = {
    GITHUB_SERVER_URL: "https://github.com",
    GITHUB_REPOSITORY: "wafflebase/wafflebase",
    GITHUB_RUN_ID: "123457",
  };
  assert.equal(runUrlFromEnv(env), "https://github.com/wafflebase/wafflebase/actions/runs/123457");
  // Null, never a partial URL — ".../runs/undefined" is worse than no link.
  for (const k of Object.keys(env)) {
    assert.equal(runUrlFromEnv({ ...env, [k]: "" }), null, `${k}=""`);
    const rest = { ...env };
    delete rest[k];
    assert.equal(runUrlFromEnv(rest), null, `${k} absent`);
  }
  assert.equal(runUrlFromEnv({}), null);
});

test("whereToLookLine: renders the run, job, step and artifact it is given — and nothing it is not", () => {
  const full = whereToLookLine({
    runUrl: "https://github.com/o/r/actions/runs/1",
    job: "fix",
    step: "Review-round guard",
    artifact: "claude-fix-execution-output",
  });
  assert.equal(
    full,
    '\n\nWhere to look: [this run](https://github.com/o/r/actions/runs/1) → job `fix`, step "Review-round guard"; transcript in the `claude-fix-execution-output` artifact.',
  );
  // No URL → empty string, so a page posted outside Actions renders as today.
  assert.equal(whereToLookLine({ job: "fix" }), "");
  assert.equal(whereToLookLine(), "");
  // A step without a job would dangle — it renders only alongside one.
  assert.equal(
    whereToLookLine({ runUrl: "u", step: "S" }),
    "\n\nWhere to look: [this run](u).",
  );
  assert.equal(whereToLookLine({ runUrl: "u", job: "j" }), "\n\nWhere to look: [this run](u) → job `j`.");
});

// --- best-effort failure breadcrumbs (Phase 3) --------------------------------

test("emitBestEffortWarning: a ::warning:: annotation inside Actions, silence outside", () => {
  const logged = [];
  const orig = console.log;
  console.log = (s) => logged.push(s);
  try {
    // Outside Actions (local runs, tests): nothing — the caller's own stderr
    // message is the record there.
    emitBestEffortWarning("set-state failed: boom", {});
    assert.equal(logged.length, 0);
    // Inside Actions: one single-line stdout workflow command — the runner
    // only scans stdout, and a newline would end the command mid-message.
    emitBestEffortWarning("loop-status failed:\nmulti line", { GITHUB_ACTIONS: "true" });
    const warning = logged.find((s) => s.startsWith("::warning::"));
    assert.ok(warning, "no ::warning:: emitted inside Actions");
    assert.match(warning, /loop-status failed: multi line/);
    assert.doesNotMatch(warning, /\n/);
  } finally {
    console.log = orig;
  }
});
