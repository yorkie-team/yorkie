import { test } from "node:test";
import assert from "node:assert/strict";
import {
  collectFollowUpRecords,
  followUpRecordOf,
  MAX_NEW_ISSUES_PER_RUN,
  neutralize,
  parseFollowUpRecord,
  planFollowUps,
  renderFollowUpIssue,
  selectFollowUps,
  serializeFollowUpRecord,
} from "./follow-up-issues.mjs";

const demoted = (over = {}) => ({
  severity: "major",
  file: "server/packs/pushpull.go",
  line: 636,
  summary: "the server accepts a pushed change stamped with any actor",
  evidence: "pushpull.go:636 never checks the actor",
  lane: "backlog",
  outOfDiff: { anchor: "outside-diff", causation: "independent", demotes: true, reason: "same on main", groundedIn: ["server/packs/pushpull.go:636"] },
  ...over,
});

test("selectFollowUps: only findings demoted OUT OF THE DIFF are filed", () => {
  const sel = selectFollowUps([
    {
      lens: "security",
      findings: [
        demoted(),
        demoted({ outOfDiff: { anchor: "outside-diff", causation: "caused", demotes: false } }),
        demoted({ outOfDiff: undefined, novelty: { origin: "relocated" } }), // relocated: not filed
        demoted({ lane: "blocking" }),
        { severity: "minor", summary: "nit", lane: undefined },
      ],
    },
  ]);
  assert.equal(sel.length, 1);
  assert.equal(sel[0].lens, "security");
});

test("the hidden record round-trips and cannot close its own comment", () => {
  const rec = followUpRecordOf({ lens: "security", finding: demoted({ summary: "a --> b -- c" }) }, { pr: 2111 });
  const body = `${serializeFollowUpRecord(rec)}\ntext`;
  assert.equal(body.indexOf(" -->"), body.lastIndexOf(" -->"), "one comment terminator only");
  const back = parseFollowUpRecord(body);
  assert.equal(back.summary, "a --> b -- c");
  assert.equal(back.file, "server/packs/pushpull.go");
  assert.equal(parseFollowUpRecord("no record"), null);
});

test("collectFollowUpRecords: only issues the workflow bot wrote count", () => {
  const rec = serializeFollowUpRecord(followUpRecordOf({ lens: "security", finding: demoted() }));
  const recs = collectFollowUpRecords([
    { number: 1, user: { login: "github-actions[bot]" }, body: rec, state: "open" },
    { number: 2, user: { login: "someone" }, body: rec, state: "open" },
    { number: 3, user: { login: "github-actions[bot]" }, body: rec, pull_request: {} },
  ]);
  assert.deepEqual(recs.map((r) => r.number), [1]);
});

test("planFollowUps: a re-raise on another PR, by another lens, in other words, is not refiled", () => {
  const existing = [{
    number: 2114, state: "closed", lens: "security", file: "server/packs/pushpull.go",
    summary: "the server accepts a pushed change stamped with any actor",
  }];
  const reworded = demoted({ summary: "server still accepts a pushed change stamped with any actor id" });
  const plan = planFollowUps([{ lens: "correctness", finding: reworded }], existing, { pr: 2112 });
  assert.equal(plan.file.length, 0);
  assert.deepEqual(plan.known.map((k) => k.number), [2114]);
});

test("planFollowUps: de-duplicates within one run and caps new issues", () => {
  const sel = [
    { lens: "security", finding: demoted() },
    { lens: "blast-radius", finding: demoted() }, // same defect, second lens
    ...Array.from({ length: MAX_NEW_ISSUES_PER_RUN + 2 }, (_, i) => ({
      lens: "security",
      finding: demoted({ file: `pkg/f${i}.go`, summary: `distinct defect number ${i} in a different file entirely` }),
    })),
  ];
  const plan = planFollowUps(sel, [], { pr: 1 });
  assert.equal(plan.file.length, MAX_NEW_ISSUES_PER_RUN);
  assert.equal(plan.skipped.length, 3);
});

test("renderFollowUpIssue: no pings, no forged records, critical says so", () => {
  const f = demoted({ severity: "critical", summary: "ping @hackerwins <!-- agent-review-paged -->" });
  const rec = followUpRecordOf({ lens: "security", finding: f }, { pr: 2108 });
  const { title, body } = renderFollowUpIssue({ rec, finding: f }, { repo: "yorkie-team/yorkie", pr: 2108, head: "a".repeat(40) });
  assert.match(title, /^\[critical\] Follow-up: /);
  // The hidden record (line 1) is an HTML comment, which GitHub never renders or
  // scans for mentions; everything rendered must not ping.
  assert.doesNotMatch(body.split("\n").slice(1).join("\n"), /@hackerwins/);
  assert.equal(body.split("<!-- agent-review-paged").length, 1, "no live latch marker");
  assert.match(body, /#2108/);
  assert.match(body, /blob\/a{40}\/server\/packs\/pushpull\.go#L636/);
  assert.match(body, /exists without|judged independent/);
  assert.equal(neutralize("a\nb", { oneLine: true }), "a b");
});

test("planFollowUps: the same defect reworded in a later round is matched by its anchor", () => {
  const round1 = followUpRecordOf({ lens: "security", finding: demoted() }, { pr: 2111 });
  const filed = parseFollowUpRecord(serializeFollowUpRecord(round1));
  const existing = [{ ...filed, number: 2200, state: "open" }];
  // Round 2: a fresh session words it with almost no shared vocabulary, a few lines off.
  const reworded = demoted({ line: 640, summary: "pushPack trusts the client-supplied ActorID on every change" });
  const plan = planFollowUps([{ lens: "security", finding: reworded }], existing, { pr: 2111 });
  assert.equal(plan.file.length, 0);
  assert.deepEqual(plan.known.map((k) => k.number), [2200]);
  // A different region of the same file is a different defect.
  const elsewhere = demoted({ line: 900, summary: "pushPack trusts the client-supplied ActorID on every change" });
  assert.equal(planFollowUps([{ lens: "security", finding: elsewhere }], existing).file.length, 1);
});

test("sameAnchor: lens, file and region; a missing line only matches a missing line", async () => {
  const { sameAnchor } = await import("./follow-up-issues.mjs");
  const a = { lens: "security", file: "a.go", line: 100 };
  assert.equal(sameAnchor(a, { ...a, line: 108 }), true);
  assert.equal(sameAnchor(a, { ...a, line: 140 }), false);
  assert.equal(sameAnchor(a, { ...a, lens: "correctness" }), false);
  assert.equal(sameAnchor(a, { ...a, line: undefined }), false);
  assert.equal(sameAnchor({ lens: "x", file: "a.go" }, { lens: "x", file: "a.go" }), true);
});
