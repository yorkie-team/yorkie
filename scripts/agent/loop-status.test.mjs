import "./fixtures/use-fixture-config.mjs";
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { PAGED_LATCH } from "./rounds.mjs";
import {
  LOOP_STATUS_MARKER,
  CI_PAGED_LATCH,
  MAX_ROUND_ROWS,
  DEFAULT_MAX_REVIEW_ROUNDS,
  isOwnStatusComment,
  isAnyPagedLatchComment,
  isTrustedLedgerComment,
  neutralizeHiddenMarkers,
  buildRounds,
  lensCell,
  fixerCell,
  summarizeEffort,
  renderLoopStatus,
} from "./loop-status.mjs";

const workflowText = (name) =>
  readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", ".github", "workflows", name),
    "utf8",
  );

const LENSES = ["agent-review-correctness", "agent-review-security"];

const run = (name, conclusion, extra = {}) => ({
  name,
  app: { slug: "github-actions" },
  status: "completed",
  conclusion,
  completed_at: "2026-08-06T10:00:00Z",
  ...extra,
});

// --- buildRounds ---------------------------------------------------------------

test("🔴 the advisory deferred-findings run is NOT evidence about CI", () => {
  // It is written by the panel, always `neutral`, and `neutral` is not a RED
  // conclusion — so if it counted as an "other" check it would be the sole member of
  // that bucket on a commit CI has not reported on yet, and the cell would read a
  // confident ✅ for a run that never happened.
  const rounds = buildRounds(
    [
      {
        sha: "a".repeat(40),
        checkRuns: [
          run("agent-review-correctness", "success"),
          run("agent-deferred-findings", "neutral"),
        ],
      },
    ],
    LENSES,
  );
  assert.equal(rounds.length, 1);
  assert.equal(rounds[0].checks, "none", "the deferred record must not stand in for CI");
  // A real CI check still counts.
  const withCi = buildRounds(
    [
      {
        sha: "a".repeat(40),
        checkRuns: [
          run("agent-review-correctness", "success"),
          run("agent-deferred-findings", "neutral"),
          run("verify-self (22.x)", "success"),
        ],
      },
    ],
    LENSES,
  );
  assert.equal(withCi[0].checks, "success");
});

test("a commit with no lens runs is not a round", () => {
  const rounds = buildRounds(
    [{ sha: "a".repeat(40), checkRuns: [run("verify-self (22.x)", "success")] }],
    LENSES,
  );
  assert.equal(rounds.length, 0);
});

test("latest run per lens wins (a re-run supersedes)", () => {
  const rounds = buildRounds(
    [
      {
        sha: "a".repeat(40),
        checkRuns: [
          run("agent-review-correctness", "failure", { completed_at: "2026-08-06T10:00:00Z" }),
          run("agent-review-correctness", "success", { completed_at: "2026-08-06T11:00:00Z" }),
        ],
      },
    ],
    LENSES,
  );
  assert.equal(rounds.length, 1);
  assert.deepEqual(rounds[0].lenses.map((l) => l.conclusion), ["success"]);
});

test("a same-named check from another app never counts", () => {
  const rounds = buildRounds(
    [
      {
        sha: "a".repeat(40),
        checkRuns: [{ name: "agent-review-correctness", app: { slug: "other-app" }, status: "completed", conclusion: "failure" }],
      },
    ],
    LENSES,
  );
  assert.equal(rounds.length, 0);
});

test("non-lens checks summarize to failure > pending > success", () => {
  const mk = (others) =>
    buildRounds(
      [{ sha: "a".repeat(40), checkRuns: [run("agent-review-security", "success"), ...others] }],
      LENSES,
    )[0].checks;
  assert.equal(mk([run("ci-job", "success")]), "success");
  assert.equal(mk([run("ci-job", "success"), { ...run("ci-2", null), status: "in_progress" }]), "pending");
  assert.equal(mk([run("ci-job", "failure"), { ...run("ci-2", null), status: "in_progress" }]), "failure");
  assert.equal(mk([]), "none");
});

// --- lensCell ------------------------------------------------------------------

test("lens cell names failing lenses and counts the rest", () => {
  assert.equal(
    lensCell([
      { lens: "correctness", status: "completed", conclusion: "failure" },
      { lens: "security", status: "completed", conclusion: "success" },
    ]),
    "❌ correctness (1 ✅)",
  );
  assert.equal(
    lensCell([
      { lens: "correctness", status: "completed", conclusion: "success" },
      { lens: "security", status: "completed", conclusion: "success" },
    ]),
    "✅ all 2",
  );
  assert.match(lensCell([{ lens: "correctness", status: "in_progress", conclusion: null }]), /⏳ running/);
});

// --- summarizeEffort -----------------------------------------------------------

test("effort totals sum sessions, cost and duration", () => {
  const e = summarizeEffort([
    { kind: "implement", costUsd: 10, durationMs: 60_000 },
    { kind: "review", costUsd: 5, durationMs: 30_000 },
    { kind: "review", costUsd: 5, durationMs: 30_000 },
    null,
  ]);
  assert.equal(e.sessions, 3);
  assert.equal(e.totalUsd, 20);
  assert.equal(e.totalMs, 120_000);
});

// --- renderLoopStatus ----------------------------------------------------------

const round = (sha, conclusion) => ({
  sha,
  checks: "success",
  lenses: [{ lens: "correctness", status: "completed", conclusion }],
});

test("body starts with the marker and shows rounds newest-first", () => {
  const body = renderLoopStatus({
    rounds: [round("a".repeat(40), "failure"), round("b".repeat(40), "success")],
    failedRounds: 1,
    maxRounds: 3,
    event: "panel",
    now: "2026-08-06T12:00Z",
  });
  assert.ok(body.startsWith(LOOP_STATUS_MARKER));
  assert.match(body, /\*\*Fix rounds dispatched:\*\* 1 of 3/);
  const rowB = body.indexOf("`bbbbbbbbb`");
  const rowA = body.indexOf("`aaaaaaaaa`");
  assert.ok(rowB !== -1 && rowA !== -1 && rowB < rowA, "newest round renders first");
  // The older round links forward to the commit that superseded it.
  assert.match(body, /→ `bbbbbbbbb`/);
});

test("an unknown round cap renders the count alone — never 'of 0', never dropped", () => {
  // Render-level fallback (the CLI now defaults the cap to
  // DEFAULT_MAX_REVIEW_ROUNDS, so callers all render the same "N of M").
  // Number(null) === 0 once made this render "2 of 0".
  const body = renderLoopStatus({ rounds: [], failedRounds: 2, maxRounds: null, now: "t" });
  const line = body.split("\n").find((l) => l.includes("Fix rounds dispatched"));
  assert.equal(line, "**Fix rounds dispatched:** 2");
  assert.ok(!body.includes("of 0"));
});

test("an unmeasured round count drops the budget line entirely", () => {
  const body = renderLoopStatus({ rounds: [], failedRounds: null, maxRounds: 3, now: "t" });
  assert.ok(!body.includes("Fix rounds dispatched"));
});

test("a trusted paged latch beats the event headline", () => {
  const body = renderLoopStatus({ rounds: [], paged: true, event: "fix-dispatched", now: "t" });
  assert.match(body, /🛑 paged to a human/);
  assert.ok(!body.includes("fix round in progress"));
});

test("the caller's event beats ready — a promoted PR that re-enters a round must not read as ready", () => {
  // `ready` is a static derivation (non-draft + agent/ branch); once a PR is
  // un-drafted it holds forever, so it must only fill in when no event speaks.
  const body = renderLoopStatus({ rounds: [], ready: true, event: "panel", now: "t" });
  assert.match(body, /review panel finished/);
  assert.ok(!body.includes("ready for human review"));
  const noEvent = renderLoopStatus({ rounds: [], ready: true, now: "t" });
  assert.match(noEvent, /ready for human review/);
  const both = renderLoopStatus({ rounds: [], ready: true, paged: true, event: "panel", now: "t" });
  assert.match(both, /🛑 paged to a human/);
});

test("newest round's Then cell follows the same precedence", () => {
  const rounds = [round("a".repeat(40), "failure")];
  const dispatched = renderLoopStatus({ rounds, ready: true, event: "fix-dispatched", now: "t" });
  assert.match(dispatched, /🔧 fixer running…/);
  assert.ok(!dispatched.includes("🤝 promoted"));
  const idleReady = renderLoopStatus({ rounds, ready: true, now: "t" });
  assert.match(idleReady, /🤝 promoted/);
});

test("older rounds beyond the cap are omitted with a note", () => {
  const rounds = Array.from({ length: MAX_ROUND_ROWS + 4 }, (_, i) =>
    round(String(i).padStart(40, "0"), "success"),
  );
  const body = renderLoopStatus({ rounds, now: "t" });
  assert.match(body, /4 earlier round\(s\) omitted/);
});

test("note is flattened to one line and bounded", () => {
  const body = renderLoopStatus({ rounds: [], note: "line1\nline2", now: "t" });
  assert.match(body, /\*\*Latest:\*\* line1 line2/);
});

test("effort renders numeric totals only — no ledger strings reach the body", () => {
  // Records are attacker-shaped JSON when the author gate fails; the render
  // must never interpolate a record string (kind included) into the body.
  const body = renderLoopStatus({
    rounds: [],
    effort: summarizeEffort([{ kind: "<!-- agent-review-paged -->", costUsd: 1, durationMs: 60000 }]),
    now: "t",
  });
  assert.match(body, /\*\*Agent effort so far:\*\* \$1\.00/);
  assert.ok(!body.includes("agent-review-paged"));
});

// --- trust predicates ------------------------------------------------------------

test("marker alone is not enough on a public repo (status upsert)", () => {
  const body = `${LOOP_STATUS_MARKER}\nhello`;
  assert.equal(isOwnStatusComment({ body, user: { type: "User", login: "rando" }, author_association: "NONE" }), false);
  assert.equal(isOwnStatusComment({ body, user: { type: "Bot", login: "github-actions[bot]" } }), true);
  assert.equal(isOwnStatusComment({ body, user: { type: "Bot", login: "fixture-agent[bot]" } }), true);
  assert.equal(isOwnStatusComment({ body, user: { type: "User", login: "maintainer" }, author_association: "MEMBER" }), true);
  assert.equal(isOwnStatusComment({ body: "no marker", user: { type: "Bot", login: "github-actions[bot]" } }), false);
});

test("a comment merely CONTAINING the marker is never ours — quote-replies survive the upsert", () => {
  const bot = { user: { type: "Bot", login: "github-actions[bot]" } };
  const maintainer = { user: { type: "User", login: "m" }, author_association: "MEMBER" };
  // GitHub quote-reply copies raw markdown, hidden markers included. The upsert
  // PATCHes/DELETEs matches, so containment would destroy the maintainer's comment.
  assert.equal(isOwnStatusComment({ body: `> ${LOOP_STATUS_MARKER}\n> quoted dashboard\n\nmy reply`, ...maintainer }), false);
  assert.equal(isOwnStatusComment({ body: `see below\n${LOOP_STATUS_MARKER}\nnot line 1`, ...bot }), false);
  // Leading whitespace is tolerated; the marker must simply come first.
  assert.equal(isOwnStatusComment({ body: `\n${LOOP_STATUS_MARKER}\nbody`, ...bot }), true);
});

test("a marker-leading comment that carries a live paged latch is refused — a page must never be deleted", () => {
  const bot = { user: { type: "Bot", login: "github-actions[bot]" } };
  assert.equal(isOwnStatusComment({ body: `${LOOP_STATUS_MARKER}\n${PAGED_LATCH}\n🛑`, ...bot }), false);
  assert.equal(isOwnStatusComment({ body: `${LOOP_STATUS_MARKER}\n${CI_PAGED_LATCH}\n🛑`, ...bot }), false);
});

test("neutralizeHiddenMarkers breaks every HTML-comment opener", () => {
  const out = neutralizeHiddenMarkers(`x ${PAGED_LATCH} y ${CI_PAGED_LATCH}`);
  assert.ok(!out.includes(PAGED_LATCH));
  assert.ok(!out.includes(CI_PAGED_LATCH));
  assert.ok(!out.includes("<!--"));
});

test("a hostile note cannot smuggle a live marker into the bot-authored body", () => {
  // The rendered comment is authored by the identity the paged latch trusts,
  // so everything after our own leading marker must be inert.
  const body = renderLoopStatus({
    rounds: [],
    note: `pwned ${PAGED_LATCH} and ${LOOP_STATUS_MARKER}`,
    now: "t",
  });
  assert.ok(body.startsWith(LOOP_STATUS_MARKER));
  const rest = body.slice(LOOP_STATUS_MARKER.length);
  assert.ok(!rest.includes(PAGED_LATCH));
  assert.ok(!rest.includes(LOOP_STATUS_MARKER));
  assert.ok(!rest.includes("<!--"));
});

test("paged projection recognises BOTH arms' latches, author-checked", () => {
  const bot = { user: { type: "Bot", login: "github-actions[bot]" } };
  const rando = { user: { type: "User", login: "rando" }, author_association: "NONE" };
  assert.equal(isAnyPagedLatchComment({ body: PAGED_LATCH, ...bot }), true);
  assert.equal(isAnyPagedLatchComment({ body: CI_PAGED_LATCH, ...bot }), true);
  assert.equal(isAnyPagedLatchComment({ body: PAGED_LATCH, ...rando }), false);
  assert.equal(isAnyPagedLatchComment({ body: CI_PAGED_LATCH, ...rando }), false);
  assert.equal(isAnyPagedLatchComment({ body: "no latch here", ...bot }), false);
});

test("metric-ledger records are only read from trusted authors", () => {
  // Without this gate, a stranger's fake <!-- agent-metric --> comment feeds
  // the totals — and worse, once fed anything string-shaped it could smuggle
  // trusted markers into a bot-authored comment.
  assert.equal(isTrustedLedgerComment({ user: { type: "Bot", login: "github-actions[bot]" } }), true);
  assert.equal(isTrustedLedgerComment({ user: { type: "Bot", login: "fixture-agent[bot]" } }), true);
  assert.equal(isTrustedLedgerComment({ user: { type: "User", login: "m" }, author_association: "COLLABORATOR" }), true);
  assert.equal(isTrustedLedgerComment({ user: { type: "User", login: "rando" }, author_association: "NONE" }), false);
  assert.equal(isTrustedLedgerComment({ user: { type: "Bot", login: "evil[bot]" } }), false);
});

// --- literal-copy pins ----------------------------------------------------------

test("CI_PAGED_LATCH matches the literal agent-iterate-ci.yml writes", () => {
  // Same discipline as rounds.test.mjs's PAGED_LATCH pin: the workflow cannot
  // import this module, so it carries the literal; a drifted copy would not
  // error, it would silently stop the paged projection from seeing CI pages.
  const wf = workflowText("agent-iterate-ci.yml");
  assert.ok(wf.includes(CI_PAGED_LATCH));
});

test("the CI attempts guard's literal latch predicate matches the module's rule", () => {
  // Third copy of the trust predicate (after rounds.mjs and the panel's gate
  // job — rounds.test.mjs pins that one); a github-script step cannot import,
  // so pin this copy too. A drifted allow-list would not error — it would
  // silently re-open "any account can stop the CI-fix loop" (smaller set:
  // stops honouring a real page; larger set: re-opens the spoof).
  const wf = workflowText("agent-iterate-ci.yml");
  // WHICH logins is the repository's configuration (`repo.botLogins`), so the
  // step reads it rather than carrying a copy that could drift.
  assert.ok(
    wf.includes("const PAGE_AUTHOR_LOGINS = JSON.parse(process.env.BOT_LOGINS);"),
    "attempts guard must take its author allow-list from the configuration",
  );
  assert.ok(
    wf.includes("BOT_LOGINS: ${{ steps.cfg.outputs.bot_logins }}"),
    "attempts guard must be handed repo.botLogins",
  );
  assert.ok(
    wf.includes("const TRUSTED_ASSOCIATIONS = ['OWNER', 'MEMBER', 'COLLABORATOR'];"),
    "attempts guard must carry the byte-identical association allow-list",
  );
  assert.ok(
    wf.includes("const isPagedLatch = (c) =>"),
    "attempts guard must author-check the latch, not match the marker alone",
  );
  // The latch read must cover ALL comment pages — a latch on page 2 of a
  // chatty PR must still stop the loop (the guard's own review-side reader
  // paginates for exactly this reason).
  assert.ok(
    wf.includes("github.paginate(github.rest.issues.listComments"),
    "attempts guard must paginate the latch read",
  );
});

test("DEFAULT_MAX_REVIEW_ROUNDS matches the panel workflow's env literal", () => {
  // The cap lives in agent-review-panel.yml's env; arms that do not own that
  // env render this constant instead, and the two must not drift or the
  // budget line flaps between "N of 3" and "N of <old>" depending on which
  // arm updated last.
  assert.ok(
    workflowText("agent-review-panel.yml").includes(`MAX_REVIEW_ROUNDS: "${DEFAULT_MAX_REVIEW_ROUNDS}"`),
    "loop-status's default cap must equal the panel workflow's MAX_REVIEW_ROUNDS",
  );
});

test("the dashboard counts rounds with the SAME reader the guard gates on", () => {
  // These two numbers are shown side by side — the table says "N of M" and the
  // guard pages at M — so a drift between them is not a cosmetic bug, it is the
  // dashboard reporting a decision that was never made. Both must call
  // fixRoundsUsed; neither may fall back to the commit-shape inference directly,
  // which is what made the budget line disagree with reality on #695.
  const status = readFileSync(new URL("./loop-status.mjs", import.meta.url), "utf8");
  const guard = readFileSync(new URL("./review-round-guard.mjs", import.meta.url), "utf8");
  for (const [name, src] of [["loop-status.mjs", status], ["review-round-guard.mjs", guard]]) {
    assert.match(src, /fixRoundsUsed\(comments,\s*commits,/, `${name} must count via fixRoundsUsed`);
    assert.doesNotMatch(
      src,
      /=\s*countFailedReviewRounds\(/,
      `${name} must not read the commit-shape count directly`,
    );
  }
});

test("the guard STAGES the dispatch record rather than posting it", () => {
  // The guard must not spend the round itself. It runs fourteen steps before the
  // fixer, behind a token mint, a checkout and a `pnpm install`, and this workflow
  // is cancel-in-progress — so posting here charges a round to a fixer that a push
  // during setup would stop from ever starting.
  const guard = readFileSync(new URL("./review-round-guard.mjs", import.meta.url), "utf8");
  const write = guard.indexOf("writeFileSync(dispatchFile, dispatchBody);");
  const proceed = guard.indexOf('setOutput("proceed", "true")');
  assert.ok(guard.includes("renderFixDispatchComment({"), "the guard must build a dispatch record");
  assert.ok(write > 0, "the guard must stage it to a file");
  assert.ok(proceed > write, "staged before proceed is set");
  assert.doesNotMatch(
    guard,
    /gh\(\["pr", "comment", String\(pr\), "--body", dispatchBody/,
    "the guard must NOT post the record itself — that is the cancellation window",
  );
  // And it must be a TOP-LEVEL statement. Every fail-safe write in this file is
  // wrapped or suffixed to swallow errors; wrapping this one would let a dispatch
  // happen whose record never landed, which the next guard reads as budget still
  // unspent. Column 0 is the cheap, robust proxy — any try/catch or `if` block
  // indents it.
  const line = guard.slice(guard.lastIndexOf("\n", write) + 1, guard.indexOf("\n", write));
  assert.match(line, /^writeFileSync\(/, "the staging write must be top-level, not best-effort");
});

test("REGRESSION: a fixer cancelled during setup must not consume a round", () => {
  // The round is spent by the step IMMEDIATELY BEFORE the fixer, so everything
  // slow — token mint, checkout, install — happens while the round is still
  // unspent and a cancellation there costs nothing. If any step is ever inserted
  // between the two, the window this test exists to keep closed reopens.
  const wf = readFileSync(
    new URL("../../.github/workflows/agent-review-panel.yml", import.meta.url),
    "utf8",
  );
  const names = [...wf.matchAll(/^ {6}- name: (.+)$/gm)].map((m) => m[1]);
  const post = names.indexOf("Record the fix-round dispatch");
  const fixer = names.indexOf("Address panel findings");
  assert.ok(post > 0 && fixer > 0, "both steps must exist by name");
  assert.equal(fixer - post, 1, "nothing may run between recording the round and the fixer");

  // The staged path must be the one the guard writes. Two steps declare it and
  // they must AGREE — asserting the literal once passes happily while the other
  // occurrence drifts, which is how the post ends up with nothing to send.
  // Verified by mutation: changing only the guard's copy must fail this.
  const paths = [...wf.matchAll(/DISPATCH_FILE: (.+)$/gm)].map((m) => m[1].trim());
  assert.equal(paths.length, 2, "exactly two steps declare the staged path (guard + post)");
  assert.equal(paths[0], paths[1], `the guard stages ${paths[0]} but the post reads ${paths[1]}`);
  assert.match(paths[0], /^\$\{\{ runner\.temp \}\}\//, "must live under RUNNER_TEMP, which survives the checkout");
  const step = wf.slice(wf.indexOf("- name: Record the fix-round dispatch"), wf.indexOf("- name: Address panel findings"));
  assert.match(step, /test -s "\$DISPATCH_FILE"/, "an empty staged file must fail, not post nothing");
  assert.match(step, /gh pr comment "\$PR" --body-file "\$DISPATCH_FILE"/);
  assert.doesNotMatch(step, /continue-on-error/, "an unrecorded dispatch must red the job");
});

// --- the round table becomes a map -------------------------------------------

const roundOf = (sha, lenses) => ({ sha, lenses, checks: "success" });
const okLens = (lens = "correctness") => ({ lens, status: "completed", conclusion: "success" });
const rowFor = (body, sha) => body.split("\n").find((l) => l.includes(sha.slice(0, 9))) ?? "";

test("the head cell LINKS to that round's checks, and degrades without the env", () => {
  // GitHub's Checks tab only ever shows the HEAD commit's runs, so before this
  // the table named a round it gave no way to open.
  const base = "https://github.com/fixture-org/fixture-repo/pull/742/commits";
  const linked = renderLoopStatus({ rounds: [roundOf("4d46871ac0", [okLens()])], commitBase: base });
  assert.match(linked, /\[`4d46871ac`\]\(https:\/\/github\.com\/fixture-org\/fixture-repo\/pull\/742\/commits\/4d46871ac0\)/);
  // No env (local, --dry-run) → today's plain code span, never a broken link.
  const plain = renderLoopStatus({ rounds: [roundOf("4d46871ac0", [okLens()])] });
  assert.match(plain, /\| `4d46871ac` \|/);
  assert.doesNotMatch(plain, /\]\(\//);
});

test("the table carries a Fixer column, and every row fills it", () => {
  const body = renderLoopStatus({
    rounds: [roundOf("aaaaaaaaa1", [okLens()]), roundOf("bbbbbbbbb2", [okLens()])],
    fixer: { aaaaaaaaa1: "3 fixed · 0 skipped · 0 disputed" },
  });
  const header = body.split("\n").find((l) => l.startsWith("| Round |")) ?? "";
  assert.match(header, /\| Fixer \|/);
  // Header, separator and every data row must agree on the column count, or
  // GitHub renders the table as plain text.
  const cols = (l) => l.split("|").length;
  const sep = body.split("\n").find((l) => l.startsWith("|---")) ?? "";
  assert.equal(cols(sep), cols(header));
  assert.equal(cols(rowFor(body, "aaaaaaaaa1")), cols(header));
  assert.match(rowFor(body, "aaaaaaaaa1"), /3 fixed · 0 skipped · 0 disputed/);
  // A round with no entry still fills the cell rather than shifting the row.
  assert.match(rowFor(body, "bbbbbbbbb2"), /\| — \|/);
});

test("fixerCell: a round the fixer was never sent in for reads as a dash", () => {
  // Distinct from "dispatched and said nothing" — collapsing them would hide a
  // fixer that ran and reported nothing.
  assert.equal(fixerCell("aaa", { dispatches: [], reports: [], rebuttals: [] }), "—");
  assert.equal(fixerCell("aaa", { dispatches: [{ from: "bbb", at: 1 }] }), "—");
  assert.equal(fixerCell("aaa"), "—");
});

test("fixerCell: dispatched but silent is its own state", () => {
  assert.equal(fixerCell("aaa", { dispatches: [{ from: "aaa", at: 1000 }] }), "🔧 dispatched");
});

test("fixerCell: a report renders fixed / skipped / disputed", () => {
  const cell = fixerCell("aaa", {
    dispatches: [{ from: "aaa", at: 1000 }],
    reports: [{ head: "aaa", fixed: [1, 2, 3], skipped: [4] }],
  });
  assert.equal(cell, "3 fixed · 1 skipped · 0 disputed");
});

test("fixerCell: 0 disputed is a STATEMENT, which is the whole point", () => {
  // Zero rebuttals have ever been filed on an agent PR, and until this cell
  // existed that was indistinguishable from a channel that silently failed.
  const cell = fixerCell("aaa", {
    dispatches: [{ from: "aaa", at: 1000 }],
    reports: [{ head: "aaa", fixed: [], skipped: [] }],
  });
  assert.match(cell, /0 disputed/);
});

test("fixerCell: a rebuttal lands in the round the fixer was working", () => {
  // Rebuttal records name a FINDING, not a commit, so they are attributed by
  // time: to the newest dispatch at or before they were written.
  const dispatches = [
    { from: "aaa", at: Date.parse("2026-08-10T10:00:00Z") },
    { from: "bbb", at: Date.parse("2026-08-10T11:00:00Z") },
  ];
  const rebuttals = [
    { createdAt: "2026-08-10T10:30:00Z" },
    { createdAt: "2026-08-10T11:30:00Z" },
    { createdAt: "2026-08-10T11:40:00Z" },
  ];
  const reports = [
    { head: "aaa", fixed: [1], skipped: [] },
    { head: "bbb", fixed: [], skipped: [2] },
  ];
  assert.equal(fixerCell("aaa", { dispatches, reports, rebuttals }), "1 fixed · 0 skipped · 1 disputed");
  assert.equal(fixerCell("bbb", { dispatches, reports, rebuttals }), "0 fixed · 1 skipped · 2 disputed");
  // One written BEFORE any dispatch belongs to no round rather than the first.
  const early = [{ createdAt: "2026-08-10T09:00:00Z" }];
  assert.match(fixerCell("aaa", { dispatches, reports, rebuttals: early }), /0 disputed/);
});

test("fixerCell: junk never throws and never invents a count", () => {
  for (const bad of [null, undefined, "x", 7]) {
    assert.equal(fixerCell("aaa", { dispatches: bad, reports: bad, rebuttals: bad }), "—");
  }
  const cell = fixerCell("aaa", { dispatches: [{ from: "aaa", at: 1 }], reports: [{ head: "aaa" }] });
  assert.equal(cell, "0 fixed · 0 skipped · 0 disputed");
});

test("lensCell: a superseded round says so instead of '0 ✅ / 6 neutral'", () => {
  // close-stuck-checks closes a cancelled panel's lenses `cancelled`; those fell
  // through every branch into the trailing one and rendered unreadably.
  const cancelled = (lens) => ({ lens, status: "completed", conclusion: "cancelled" });
  assert.equal(lensCell([cancelled("a"), cancelled("b")]), "⚪ superseded");
  assert.equal(lensCell([cancelled("a"), okLens("b")]), "⚪ superseded (1 ✅)");
  // A real failure still wins — a superseded round must never mask a red one.
  assert.match(lensCell([cancelled("a"), { lens: "b", status: "completed", conclusion: "failure" }]), /^❌ b/);
  // And a still-running lens is pending, not superseded.
  assert.match(lensCell([cancelled("a"), { lens: "b", status: "in_progress" }]), /⏳/);
});

test("main() derives commitBase from the Actions env, not a CLI flag", () => {
  // renderLoopStatus has six call sites across four workflows and the module
  // requires caller-independent values; a flag some arms passed would make the
  // links flap between updates. Verified by mutation.
  const src = readFileSync(new URL("./loop-status.mjs", import.meta.url), "utf8");
  assert.match(src, /GITHUB_REPOSITORY/);
  assert.match(src, /commitBase = repo/);
  assert.doesNotMatch(src, /flags\["commit-base"\]/, "must not be a per-arm flag");
});
