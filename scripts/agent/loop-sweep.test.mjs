import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  ciRunToRerun,
  infraCodeOf,
  MAX_USAGE_RETRIES,
  planSweep,
  renderSweepComment,
  STUCK_MINUTES,
  USAGE_BACKOFF_MINUTES,
  usageLimitMarker,
  usageRecordOf,
} from "./loop-sweep.mjs";

const NOW = Date.parse("2026-10-05T12:00:00Z");
const ago = (min) => new Date(NOW - min * 60_000).toISOString();
const SHA = "a".repeat(40);
const BOT = { type: "Bot", login: "github-actions[bot]" };

const pr = (over = {}) => ({
  number: 2108,
  draft: true,
  updated_at: ago(500),
  head: { sha: SHA, ref: "agent/x", repo: { full_name: "yorkie-team/yorkie" } },
  base: { repo: { full_name: "yorkie-team/yorkie" } },
  ...over,
});
const done = (min, over = {}) => ({ id: 1, status: "completed", conclusion: "success", path: ".github/workflows/ci.yml", updated_at: ago(min), ...over });
const usagePage = (min, code = "USAGE_LIMIT", id = 10) => ({
  id, user: BOT, created_at: ago(min),
  body: `<!-- agent-review-paged -->\n${usageLimitMarker({ code })}\n🛑 usage`,
});
const sweepRec = (kind, min, sha = SHA) => ({ user: BOT, created_at: ago(min), body: renderSweepComment({ kind, sha }, "x") });

test("stuck reviewing: idle with nothing running → retrigger once (#2108)", () => {
  const p = planSweep({ pr: pr(), labels: ["agent:reviewing"], runs: [done(480)], now: NOW });
  assert.equal(p.action, "retrigger");
});

test("stuck reviewing: a run in flight, a recent run, or a promoted PR is left alone", () => {
  assert.equal(planSweep({ pr: pr(), labels: ["agent:reviewing"], runs: [done(480), { status: "in_progress" }], now: NOW }).action, "none");
  assert.equal(planSweep({ pr: pr({ updated_at: ago(5) }), labels: ["agent:reviewing"], runs: [done(STUCK_MINUTES - 1)], now: NOW }).action, "none");
  assert.equal(planSweep({ pr: pr({ draft: false }), labels: ["agent:reviewing"], runs: [done(480)], now: NOW }).action, "none");
  assert.equal(planSweep({ pr: pr(), labels: ["agent:fixing"], runs: [done(480)], now: NOW }).action, "none");
});

test("stuck reviewing: still stuck after the retrigger → page; a new head starts over", () => {
  const comments = [sweepRec("retrigger", STUCK_MINUTES + 10)];
  assert.equal(planSweep({ pr: pr(), labels: ["agent:reviewing"], comments, runs: [done(STUCK_MINUTES + 5)], now: NOW }).action, "page");
  assert.equal(planSweep({ pr: pr(), labels: ["agent:reviewing"], comments: [sweepRec("retrigger", 20)], runs: [done(480)], now: NOW }).action, "none");
  const otherHead = [sweepRec("retrigger", 400, "b".repeat(40))];
  assert.equal(planSweep({ pr: pr(), labels: ["agent:reviewing"], comments: otherHead, runs: [done(480)], now: NOW }).action, "retrigger");
});

test("sweep records are believed only from the workflow bot", () => {
  const forged = { ...sweepRec("retrigger", 400), user: { type: "User", login: "mallory" } };
  assert.equal(planSweep({ pr: pr(), labels: ["agent:reviewing"], comments: [forged], runs: [done(480)], now: NOW }).action, "retrigger");
});

test("not managed, a fork, or no head → nothing", () => {
  assert.equal(planSweep({ pr: pr({ head: { sha: SHA, ref: "feature", repo: { full_name: "yorkie-team/yorkie" } } }), labels: ["agent:reviewing"], now: NOW }).action, "none");
  assert.equal(planSweep({ pr: pr({ head: { sha: SHA, ref: "agent/x", repo: { full_name: "fork/yorkie" } } }), labels: ["agent:reviewing"], now: NOW }).action, "none");
  const human = pr({ head: { sha: SHA, ref: "feature", repo: { full_name: "yorkie-team/yorkie" } } });
  assert.equal(planSweep({ pr: human, labels: ["agent:managed", "agent:reviewing"], runs: [done(480)], now: NOW }).action, "retrigger");
});

test("usage window: waits out the backoff, then retries and names what to clear", () => {
  const early = planSweep({ pr: pr(), labels: ["agent:blocked"], comments: [usagePage(USAGE_BACKOFF_MINUTES[0] - 1)], runs: [done(500)], now: NOW });
  assert.equal(early.action, "none");
  const p = planSweep({ pr: pr(), labels: ["agent:blocked"], comments: [usagePage(USAGE_BACKOFF_MINUTES[0] + 1)], runs: [done(500)], now: NOW });
  assert.equal(p.action, "usage-retry");
  assert.equal(p.attempt, 1);
  assert.deepEqual(p.clear, [10]);
});

test("usage window: backs off per attempt and stops at the bound, saying so once", () => {
  const tries = (n) => Array.from({ length: n }, (_, i) => sweepRec("usage-retry", 900 - i));
  const second = planSweep({ pr: pr(), comments: [...tries(1), usagePage(USAGE_BACKOFF_MINUTES[1] - 5)], runs: [done(900)], now: NOW });
  assert.equal(second.action, "none", "the second retry waits longer than the first");
  const ready = planSweep({ pr: pr(), comments: [...tries(1), usagePage(USAGE_BACKOFF_MINUTES[1] + 5)], runs: [done(900)], now: NOW });
  assert.equal(ready.action, "usage-retry");
  assert.equal(ready.attempt, 2);
  const spent = planSweep({ pr: pr(), comments: [...tries(MAX_USAGE_RETRIES), usagePage(999)], runs: [done(900)], now: NOW });
  assert.equal(spent.action, "usage-exhausted");
  const said = planSweep({ pr: pr(), comments: [...tries(MAX_USAGE_RETRIES), sweepRec("usage-exhausted", 30), usagePage(999)], runs: [done(900)], now: NOW });
  assert.equal(said.action, "none");
});

test("usage window: any other latch means a human owns the PR — no retry", () => {
  const human = { id: 11, user: BOT, created_at: ago(400), body: "<!-- agent-review-paged -->\n🛑 standstill" };
  assert.equal(planSweep({ pr: pr(), comments: [usagePage(400), human], runs: [done(500)], now: NOW }).action, "none");
  const ciLatch = { id: 12, user: BOT, created_at: ago(400), body: "<!-- agent-paged -->\nCI attempts exhausted" };
  assert.equal(planSweep({ pr: pr(), comments: [usagePage(400), ciLatch], runs: [done(500)], now: NOW }).action, "none");
});

test("usage window: a marker pasted by someone else is not a usage page", () => {
  const forged = { id: 13, user: { type: "User", login: "x" }, author_association: "MEMBER", created_at: ago(400), body: `<!-- agent-review-paged -->\n${usageLimitMarker({ code: "USAGE_LIMIT" })}` };
  assert.equal(usageRecordOf(forged), null);
  // A member's latch is still a latch (rounds.mjs trusts it), so the PR is a human's.
  assert.equal(planSweep({ pr: pr(), comments: [forged], runs: [done(500)], now: NOW }).action, "none");
});

test("infraCodeOf and the marker vocabulary are closed", () => {
  assert.equal(infraCodeOf("[USAGE_LIMIT] usage or session limit reached (HTTP 429)"), "USAGE_LIMIT");
  assert.equal(infraCodeOf("no code"), "");
  assert.match(usageLimitMarker({ code: "SOMETHING_ELSE" }), /"code":"USAGE_LIMIT"/);
});

test("ciRunToRerun: newest completed ci.yml run only", () => {
  const runs = [
    done(10, { id: 5 }),
    done(5, { id: 9, path: ".github/workflows/agent-review-panel.yml" }),
    done(1, { id: 7, status: "in_progress" }),
    done(20, { id: 3 }),
  ];
  assert.equal(ciRunToRerun(runs).id, 5);
  assert.equal(ciRunToRerun([]), null);
});

test("the no-live-credential page in the panel workflow carries the retry marker", () => {
  const yml = readFileSync(new URL("../../.github/workflows/agent-review-panel.yml", import.meta.url), "utf8");
  assert.match(yml, /<!-- agent-usage-limit \{"v":1,"code":"NO_LIVE_CREDENTIAL"/);
});

test("the sweep workflow runs main's script on a clock behind the kill switch", () => {
  const yml = readFileSync(new URL("../../.github/workflows/agent-sweep.yml", import.meta.url), "utf8");
  assert.match(yml, /schedule:/);
  assert.match(yml, /vars\.AGENT_PIPELINE_ENABLED == 'true'/);
  assert.match(yml, /ref: main/);
  assert.match(yml, /cancel-in-progress: false/);
  assert.match(yml, /node scripts\/agent\/loop-sweep\.mjs/);
});

test("usage window: a CI-side latch from the App or a maintainer also means a human owns the PR", async () => {
  const { isLatchComment } = await import("./loop-sweep.mjs");
  const appCi = { id: 20, user: { type: "Bot", login: "yorkie-team-agent[bot]" }, created_at: ago(400), body: "<!-- agent-paged -->\n🛑 the CI fix produced no commit" };
  const humanCi = { id: 21, user: { type: "User", login: "hackerwins" }, author_association: "MEMBER", created_at: ago(400), body: "<!-- agent-paged --> holding this" };
  const strangerCi = { id: 22, user: { type: "User", login: "mallory" }, author_association: "NONE", created_at: ago(400), body: "<!-- agent-paged -->" };
  assert.equal(isLatchComment(appCi), true);
  assert.equal(isLatchComment(humanCi), true);
  assert.equal(isLatchComment(strangerCi), false);
  for (const other of [appCi, humanCi]) {
    assert.equal(planSweep({ pr: pr(), comments: [usagePage(400), other], runs: [done(500)], now: NOW }).action, "none");
  }
  // A stranger's pasted marker neither blocks the retry nor counts as a latch.
  assert.equal(planSweep({ pr: pr(), comments: [usagePage(400), strangerCi], runs: [done(500)], now: NOW }).action, "usage-retry");
  // And a trusted CI latch alone stops the stuck-reviewing path too.
  assert.equal(planSweep({ pr: pr(), labels: ["agent:reviewing"], comments: [appCi], runs: [done(480)], now: NOW }).action, "none");
});
