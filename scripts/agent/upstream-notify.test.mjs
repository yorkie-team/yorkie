// The upstream notifier: every row of its decision table, against a fake
// upstream and a fake repository, and what it will and will not write.

import "./fixtures/use-fixture-config.mjs";

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { MANIFEST_PATH, RETRY_DELAYS_MS, manifestDigest } from "./manifest.mjs";
import { BOT_LOGIN, BUDGET_MS, LABEL, MARKER_PREFIX, TITLE_PREFIX, closingComment, isOurIssue, issueBody, issueTitle, markerLine, notifyUpstream } from "./upstream-notify.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const H = (c) => c.repeat(64);

// --- a fake upstream ---------------------------------------------------------------

const OLD_MANIFEST = `${H("a")}  scripts/agent/a.mjs\n`;
const NEW_MANIFEST = `${H("b")}  scripts/agent/a.mjs\n`;
const OLD_DIGEST = manifestDigest(OLD_MANIFEST);
const NEW_DIGEST = manifestDigest(NEW_MANIFEST);
const RECORDED = "0123456789abcdef0123456789abcdef01234567";
const HEAD = "fedcba9876543210fedcba9876543210fedcba98";
const OLDER_HEAD = "1111111111111111111111111111111111111111";
const COPY = { repository: "org/core", commit: RECORDED, manifestDigest: OLD_DIGEST };
const SELF = "me/copy";
const UP = "https://api.github.com/repos/org/core";
const ok = (text) => ({ status: 200, text });
const HEAD_REF = `${UP}/git/ref/heads/trunk`;
const manifestAt = (ref) => `${UP}/contents/${MANIFEST_PATH}?ref=${ref}`;

/** Upstream answers for a copy that passes the pinned check, with the head at `head` carrying `manifest`. */
const upstreamAt = (head, manifest) => ({
  [UP]: ok(JSON.stringify({ default_branch: "trunk" })),
  [`${UP}/compare/trunk...${RECORDED}`]: ok(JSON.stringify({ status: head === RECORDED ? "identical" : "behind" })),
  [manifestAt(RECORDED)]: ok(OLD_MANIFEST),
  // What `checkUpstream`'s own warning asks; the notifier does not use it.
  [`${UP}/contents/${MANIFEST_PATH}`]: ok(manifest),
  [HEAD_REF]: ok(JSON.stringify({ ref: "refs/heads/trunk $(rm -rf /)", object: { type: "commit", sha: head } })),
  [manifestAt(head)]: ok(manifest),
});
const MOVED = upstreamAt(HEAD, NEW_MANIFEST);
const CURRENT = upstreamAt(HEAD, OLD_MANIFEST);

const fetcher = (answers, seen = []) => async (url) => {
  seen.push(url);
  let answer = answers[url];
  if (Array.isArray(answer)) answer = answer.length > 1 ? answer.shift() : answer[0];
  if (answer instanceof Error) throw answer;
  return answer ?? { status: 404, text: "" };
};

// --- a fake repository -------------------------------------------------------------

const STATE = { repository: "org/core", recordedCommit: RECORDED, recordedDigest: OLD_DIGEST, headCommit: HEAD, headDigest: NEW_DIGEST };
const TITLE = issueTitle("org/core");
const issue = (number, body, extra = {}) => ({ number, state: "open", user: { login: BOT_LOGIN }, title: TITLE, body, ...extra });
const OURS = issue(7, issueBody(STATE));
const OURS_OLDER = issue(7, issueBody({ ...STATE, headCommit: OLDER_HEAD }));

/**
 * `api` over a list of open issues. Records every call; `labels` is whether
 * the label exists, and `createLabel` the status creating it answers with.
 */
function repo({ issues = [], labels = false, createLabel = 201 } = {}) {
  const calls = [];
  const api = async (method, apiPath, body) => {
    calls.push({ method, path: apiPath, body });
    const p = apiPath.replace(`/repos/${SELF}`, "");
    if (method === "GET" && p.startsWith("/issues?")) {
      const q = new URL(`https://x${p}`).searchParams;
      const page = Number(q.get("page"));
      // As GitHub lists them: one state, newest first.
      const listed = issues.filter((i) => i.state === q.get("state")).sort((a, b) => b.number - a.number);
      return { status: 200, json: listed.slice((page - 1) * 100, page * 100) };
    }
    if (method === "GET" && p === `/labels/${encodeURIComponent(LABEL)}`) return { status: labels ? 200 : 404, json: {} };
    if (method === "POST" && p === "/labels") return { status: createLabel, json: {} };
    if (method === "POST" && p === "/issues") return { status: 201, json: { number: 42 } };
    if (method === "POST" && /^\/issues\/\d+\/comments$/.test(p)) return { status: 201, json: {} };
    if (method === "PATCH" && /^\/issues\/\d+$/.test(p)) return { status: 200, json: {} };
    return { status: 500, json: null };
  };
  return { api, calls, writes: () => calls.filter((c) => c.method !== "GET") };
}

const slept = [];
const run = (answers, r, { upstream = COPY, localDigest = OLD_DIGEST, self = SELF, dryRun = false, seen = [] } = {}) =>
  notifyUpstream({ upstream, localDigest, self, dryRun, fetchText: fetcher(answers, seen), api: r.api, sleep: async (ms) => void slept.push(ms) });

// --- the decision table ------------------------------------------------------------

test("the origin: nothing is asked of upstream or of this repository", async () => {
  const r = repo();
  const seen = [];
  const result = await run({}, r, { upstream: { repository: null }, self: "yorkie-team/yorkie", seen });
  assert.equal(result.action, "origin");
  assert.match(result.lines[0], /yorkie-team\/yorkie is recorded as the repository the core is written in.*nothing to notify/);
  assert.deepEqual(seen, []);
  assert.deepEqual(r.calls, []);
});

test("behind, no issue open: the label is created and one issue is opened", async () => {
  const r = repo();
  const result = await run(MOVED, r);
  assert.equal(result.action, "create");
  assert.deepEqual(result.issues, [42]);
  assert.deepEqual(
    r.writes().map((c) => `${c.method} ${c.path}`),
    [`POST /repos/${SELF}/labels`, `POST /repos/${SELF}/issues`],
  );
  const [label, created] = r.writes();
  assert.equal(label.body.name, LABEL);
  assert.deepEqual(Object.keys(created.body).sort(), ["body", "labels", "title"]);
  assert.equal(created.body.title, "[agent:harness-sync] The shared harness files are behind org/core");
  assert.deepEqual(created.body.labels, [LABEL]);
  assert.equal(created.body.body, issueBody(STATE));
});

test("behind, no issue open, the label already there: it is not created again", async () => {
  const r = repo({ labels: true });
  await run(MOVED, r);
  assert.deepEqual(r.writes().map((c) => `${c.method} ${c.path}`), [`POST /repos/${SELF}/issues`]);
  assert.deepEqual(r.writes()[0].body.labels, [LABEL]);
});

test("behind, and the token may not create labels: the issue is opened without one", async () => {
  for (const status of [403, 404]) {
    const r = repo({ createLabel: status });
    const result = await run(MOVED, r);
    assert.equal(result.action, "create", `HTTP ${status}`);
    const created = r.writes().at(-1);
    assert.equal(created.path, `/repos/${SELF}/issues`);
    assert.equal(Object.hasOwn(created.body, "labels"), false, "no label is asked for when it could not be created");
    // What is left to find it by: the title's marker for a person, the body's first line for the next run.
    assert.ok(created.body.title.startsWith(`[${LABEL}] `));
    assert.ok(created.body.body.startsWith(MARKER_PREFIX));
    assert.ok(result.lines.some((l) => l.includes(`Could not create the label ${LABEL} (HTTP ${status})`)));
  }
  // A label another run created in between is simply used.
  const raced = repo({ createLabel: 422 });
  await run(MOVED, raced);
  assert.deepEqual(raced.writes().at(-1).body.labels, [LABEL]);
  // Anything else is not "no permission", and is not papered over.
  await assert.rejects(run(MOVED, repo({ createLabel: 500 })), /creating the label agent:harness-sync returned HTTP 500/);
});

test("behind, an issue open for this very state: nothing is written", async () => {
  const r = repo({ issues: [OURS] });
  const result = await run(MOVED, r);
  assert.equal(result.action, "unchanged");
  assert.deepEqual(result.issues, [7]);
  assert.deepEqual(r.writes(), []);
});

test("behind, an issue open for an older head: its body is rewritten, with no comment", async () => {
  const r = repo({ issues: [OURS_OLDER] });
  const result = await run(MOVED, r);
  assert.equal(result.action, "update");
  // The title goes with it: a title someone reworded after the marker, or one naming an upstream since replaced, is put back.
  assert.deepEqual(r.writes(), [{ method: "PATCH", path: `/repos/${SELF}/issues/7`, body: { title: TITLE, body: issueBody(STATE) } }]);
  // …and likewise when it is the copy that moved, to a commit that is still behind.
  const partial = repo({ issues: [issue(7, issueBody({ ...STATE, recordedCommit: OLDER_HEAD }))] });
  assert.equal((await run(MOVED, partial)).action, "update");
  // A body someone edited through the web form comes back with CRLF line endings; that is not a change.
  const crlf = repo({ issues: [issue(7, issueBody(STATE).replaceAll("\n", "\r\n"))] });
  assert.equal((await run(MOVED, crlf)).action, "unchanged");
});

test("current, an issue open: closed, then one comment", async () => {
  const r = repo({ issues: [OURS_OLDER] });
  const result = await run(CURRENT, r);
  assert.equal(result.action, "close");
  assert.deepEqual(result.issues, [7]);
  const comment = closingComment({ repository: "org/core", headCommit: HEAD, headDigest: OLD_DIGEST });
  assert.equal(comment.includes("\n"), false);
  assert.deepEqual(r.writes(), [
    { method: "PATCH", path: `/repos/${SELF}/issues/7`, body: { state: "closed", state_reason: "completed" } },
    { method: "POST", path: `/repos/${SELF}/issues/7/comments`, body: { body: comment } },
  ]);
});

test("closing: a close that fails leaves no comment behind, and a comment that fails does not fail the run", async () => {
  const refusing = (match, answer) => {
    const r = repo({ issues: [OURS_OLDER] });
    const inner = r.api;
    r.api = async (method, apiPath, body) => {
      if (!match(method, apiPath)) return inner(method, apiPath, body);
      r.calls.push({ method, path: apiPath, body });
      if (answer instanceof Error) throw answer;
      return answer;
    };
    return r;
  };
  // The close is refused: the run fails, and nothing was said — so next week's run cannot say it twice.
  const noClose = refusing((m) => m === "PATCH", { status: 403, json: null });
  await assert.rejects(run(CURRENT, noClose), /closing #7 returned HTTP 403/);
  assert.deepEqual(noClose.writes().map((c) => c.method), ["PATCH"]);
  // The comment is refused, or the connection drops: the issue is closed, and the run passes and says so.
  for (const answer of [{ status: 403, json: null }, new Error("socket hang up")]) {
    const noComment = refusing((m, p) => m === "POST" && p.endsWith("/comments"), answer);
    const result = await run(CURRENT, noComment);
    assert.equal(result.action, "close");
    assert.deepEqual(noComment.writes().map((c) => c.method), ["PATCH", "POST"]);
    assert.ok(result.lines.some((l) => /Closed #7, but could not comment on it/.test(l)));
  }
});

// --- an issue somebody closed --------------------------------------------------------

test("behind, none open, one closed for this very state: it stays closed and none is opened", async () => {
  // Four Mondays, and the issue a person closed after the first.
  const r = repo({ issues: [issue(7, issueBody(STATE), { state: "closed" })] });
  for (let week = 0; week < 4; week++) {
    const result = await run(MOVED, r);
    assert.equal(result.action, "unchanged");
    assert.deepEqual(result.issues, [7]);
    assert.match(result.lines.at(-1), /#7 says so and was closed; it stays closed until org\/core moves again/);
  }
  assert.deepEqual(r.writes(), []);
});

test("behind, none open, one closed for an older state: that issue is reopened and rewritten", async () => {
  const r = repo({ issues: [issue(7, issueBody({ ...STATE, headCommit: OLDER_HEAD }), { state: "closed", title: `${TITLE_PREFIX} an older title` })] });
  const result = await run(MOVED, r);
  assert.equal(result.action, "reopen");
  assert.deepEqual(result.issues, [7]);
  assert.deepEqual(r.writes(), [{ method: "PATCH", path: `/repos/${SELF}/issues/7`, body: { state: "open", title: TITLE, body: issueBody(STATE) } }]);
  // The one the run itself closed when the copy caught up is the same case: one issue, for good.
  const caughtUp = repo({ issues: [issue(7, issueBody({ ...STATE, recordedCommit: OLDER_HEAD, headCommit: RECORDED }), { state: "closed" })] });
  assert.equal((await run(MOVED, caughtUp)).action, "reopen");
});

test("closed issues: the most recently opened of ours decides, and nobody else's counts", async () => {
  const older = issue(7, issueBody({ ...STATE, headCommit: OLDER_HEAD }), { state: "closed" });
  const newer = issue(9, issueBody(STATE), { state: "closed" });
  // #9 was closed for this state; #7, older, would otherwise be reopened.
  const r = repo({ issues: [older, newer, issue(12, "an unrelated report", { state: "closed" })] });
  const result = await run(MOVED, r);
  assert.equal(result.action, "unchanged");
  assert.deepEqual(result.issues, [9]);
  assert.deepEqual(r.writes(), []);
  // A closed issue carrying the marker that the bot did not write is not reopened; a new one is opened.
  const forged = repo({ issues: [issue(7, issueBody({ ...STATE, headCommit: OLDER_HEAD }), { state: "closed", user: { login: "mallory" } })] });
  assert.equal((await run(MOVED, forged)).action, "create");
  assert.deepEqual(forged.writes().map((c) => c.path), [`/repos/${SELF}/labels`, `/repos/${SELF}/issues`]);
  // Current: a closed issue is never touched.
  const current = repo({ issues: [older] });
  assert.equal((await run(CURRENT, current)).action, "none");
  assert.deepEqual(current.calls.filter((c) => c.path.includes("state=closed")), []);
});

test("current, no issue open: nothing is written", async () => {
  const r = repo();
  const result = await run(CURRENT, r);
  assert.equal(result.action, "none");
  assert.deepEqual(r.writes(), []);
  // The copy at upstream's very head is the same answer.
  assert.equal((await run(upstreamAt(RECORDED, OLD_MANIFEST), repo())).action, "none");
});

// --- when the copy cannot be placed -------------------------------------------------

test("the pinned check fails: it throws, and this repository's API is never called", async () => {
  const cases = [
    ["the recorded commit is not on the default branch", { ...MOVED, [`${UP}/compare/trunk...${RECORDED}`]: ok(JSON.stringify({ status: "diverged" })) }, /is not on org\/core's default branch/],
    ["upstream has no such commit", { ...MOVED, [`${UP}/compare/trunk...${RECORDED}`]: { status: 404, text: "" } }, /has no commit/],
    ["the manifest is not there at the recorded commit", { ...MOVED, [manifestAt(RECORDED)]: { status: 404, text: "" } }, /has no scripts\/agent\/MANIFEST\.sha256 at/],
    ["the manifest there is not the recorded one", { ...MOVED, [manifestAt(RECORDED)]: ok(NEW_MANIFEST) }, /The record does not describe that commit/],
    ["upstream cannot be reached", { ...MOVED, [UP]: new Error("getaddrinfo ENOTFOUND") }, /Could not fetch .*ENOTFOUND/],
    ["upstream refuses", { ...MOVED, [UP]: { status: 403, text: "" } }, /returned HTTP 403/],
  ];
  for (const [what, answers, expected] of cases) {
    // An issue is open, and the label is missing: any path that got as far as
    // this repository would have something to do.
    const r = repo({ issues: [OURS_OLDER] });
    await assert.rejects(run(answers, r), (err) => {
      assert.match(err.message, expected, what);
      assert.match(err.message, /no issue was opened, changed or closed/, what);
      return true;
    });
    assert.deepEqual(r.calls, [], what);
  }
  // A copy that was edited without a sync is the same: its record is not true of it.
  const edited = repo({ issues: [OURS_OLDER] });
  await assert.rejects(run(MOVED, edited, { localDigest: NEW_DIGEST }), /This copy's manifest digest is/);
  assert.deepEqual(edited.calls, []);
});

test("upstream's head cannot be read: it throws, and this repository's API is never called", async () => {
  const cases = [
    ["the branch's head", { ...MOVED, [HEAD_REF]: { status: 404, text: "" } }, /Could not read org\/core's default branch: .* returned HTTP 404/],
    ["a head that is not a commit id", { ...MOVED, [HEAD_REF]: ok(JSON.stringify({ object: { type: "commit", sha: "main; echo pwned" } })) }, /did not name a commit/],
    ["a ref that points at a tag object", { ...MOVED, [HEAD_REF]: ok(JSON.stringify({ object: { type: "tag", sha: HEAD } })) }, /did not name a commit/],
    ["a prefix match, which the API answers with a list", { ...MOVED, [HEAD_REF]: ok(JSON.stringify([{ object: { type: "commit", sha: HEAD } }])) }, /did not name a commit/],
    ["a head that is not JSON", { ...MOVED, [HEAD_REF]: ok("<html>") }, /did not name a commit/],
    ["the manifest at the head", { ...MOVED, [manifestAt(HEAD)]: { status: 404, text: "" } }, /Could not read org\/core's manifest at its head: .* returned HTTP 404/],
    ["a network error, after the retries", { ...MOVED, [manifestAt(HEAD)]: new Error("socket hang up") }, /failed \(socket hang up\)/],
  ];
  for (const [what, answers, expected] of cases) {
    const r = repo({ issues: [OURS_OLDER] });
    slept.length = 0;
    await assert.rejects(run(answers, r), expected, what);
    assert.deepEqual(r.calls, [], what);
  }
  assert.deepEqual(slept, [...RETRY_DELAYS_MS], "the last case was retried before it failed");
  // A transient refusal that clears is not a failure.
  const flaky = repo();
  assert.equal((await run({ ...MOVED, [manifestAt(HEAD)]: [{ status: 503, text: "" }, ok(NEW_MANIFEST)] }, flaky)).action, "create");
});

test("the head is the BRANCH's, and its manifest is read at the commit just named", async () => {
  const seen = [];
  await run(MOVED, repo(), { seen });
  // `git/ref/heads/…`: a tag called `trunk` cannot answer for the branch.
  assert.deepEqual(seen.slice(-2), [HEAD_REF, manifestAt(HEAD)]);
});

test("a default branch whose name would change the URL is refused before it is asked for", async () => {
  for (const branch of ["../../../x/y", "a..b", "a//b", "/a", "a/", "./a"]) {
    const answers = {
      ...MOVED,
      [UP]: ok(JSON.stringify({ default_branch: branch })),
      [`${UP}/compare/${encodeURIComponent(branch)}...${RECORDED}`]: ok(JSON.stringify({ status: "behind" })),
    };
    const r = repo();
    const seen = [];
    await assert.rejects(run(answers, r, { seen }), /default branch has a name this script will not put into a URL/, branch);
    assert.equal(seen.some((u) => u.includes("/git/ref/")), false, branch);
    assert.deepEqual(r.calls, [], branch);
  }
  // A branch with a slash in it is an ordinary branch.
  const nested = { ...MOVED, [UP]: ok(JSON.stringify({ default_branch: "release/v1" })), [`${UP}/compare/release%2Fv1...${RECORDED}`]: ok(JSON.stringify({ status: "behind" })), [`${UP}/git/ref/heads/release/v1`]: MOVED[HEAD_REF] };
  assert.equal((await run(nested, repo())).action, "create");
});

// --- which issue is ours ------------------------------------------------------------

test("an issue carrying the marker that the bot did not write is ignored", async () => {
  const forged = issue(3, issueBody({ ...STATE, headCommit: OLDER_HEAD }), { user: { login: "mallory" } });
  // Behind: the forged one is neither rewritten nor taken as "already open".
  const behind = repo({ issues: [forged] });
  const opened = await run(MOVED, behind);
  assert.equal(opened.action, "create");
  assert.deepEqual(behind.writes().map((c) => c.path), [`/repos/${SELF}/labels`, `/repos/${SELF}/issues`]);
  // Current: it is not commented on and not closed.
  const current = repo({ issues: [forged] });
  assert.equal((await run(CURRENT, current)).action, "none");
  assert.deepEqual(current.writes(), []);
});

test("isOurIssue: by the bot, titled as ours, with the whole marker as the first line", () => {
  const body = issueBody(STATE);
  assert.equal(isOurIssue(issue(1, body)), true);
  assert.equal(isOurIssue(issue(1, body, { user: { login: "mallory" } })), false, "another author");
  assert.equal(isOurIssue(issue(1, body, { user: { login: "github-actions" } })), false, "a person may hold this login; the bot's ends in [bot]");
  assert.equal(isOurIssue(issue(1, body, { user: null })), false, "a deleted author");
  assert.equal(isOurIssue(issue(1, body, { pull_request: {} })), false, "a pull request");
  assert.equal(isOurIssue(issue("1", body)), false, "a number that is not a number");
  assert.equal(isOurIssue(null), false);
  // The state asked for, and only that one.
  assert.equal(isOurIssue(issue(1, body, { state: "closed" })), false, "closed, asked as open");
  assert.equal(isOurIssue(issue(1, body, { state: "closed" }), "closed"), true);
  assert.equal(isOurIssue(issue(1, body), "closed"), false, "open, asked as closed");

  // ANOTHER WORKFLOW'S ISSUE, by the same bot. Its first line begins like the
  // marker; it is not the marker, and its title is its own.
  const lookalike = `${MARKER_PREFIX}report for the week -->\nSomething else entirely.`;
  assert.equal(isOurIssue(issue(1, lookalike)), false, "a first line that only starts like the marker");
  assert.equal(isOurIssue(issue(1, body, { title: "Weekly report" })), false, "the marker, under a title that is not ours");
  assert.equal(isOurIssue(issue(1, body, { title: ` ${TITLE}` })), false, "the title's marker not at its start");
  assert.equal(isOurIssue(issue(1, body, { title: undefined })), false, "no title");
  // Reworded after the marker is still ours (and the next rewrite puts the title back).
  assert.equal(isOurIssue(issue(1, body, { title: `${TITLE_PREFIX} sync me, please`, labels: [] })), true);

  // The marker, whole and first.
  const marker = markerLine(STATE);
  for (const [what, first] of [
    ["something after it on the line", `${marker} trailing`],
    ["a space before it", ` ${marker}`],
    ["a byte-order mark before it", `\uFEFF${marker}`],
    ["a short commit id", marker.replace(RECORDED, RECORDED.slice(0, 12))],
    ["an upper-case commit id", marker.replace(HEAD, HEAD.toUpperCase())],
    ["no head", marker.replace(` head=${HEAD}`, "")],
    ["an upstream that is not owner/name", marker.replace("org/core", "org/core/x")],
    ["an owner GitHub would not allow", marker.replace("org/core", "-org/core")],
    ["an unclosed comment", marker.replace(" -->", "")],
  ]) {
    assert.equal(isOurIssue(issue(1, `${first}\nthe rest`)), false, what);
  }
  // The bot posting somebody else's text: the marker is in it, not at its head.
  assert.equal(isOurIssue(issue(1, `A report from a user:\n\n${body}`)), false, "the marker further down");
  assert.equal(isOurIssue(issue(1, null)), false, "no body");
  // An issue for another upstream, from before the copy changed where it syncs from, is still ours to rewrite.
  assert.equal(isOurIssue(issue(1, issueBody({ ...STATE, repository: "old-org/core" }))), true);
});

test("another workflow's bot issue that starts like the marker is neither rewritten nor closed", async () => {
  const theirs = issue(3, `${MARKER_PREFIX}weekly -->\nA report.`, { title: "Weekly report" });
  const behind = repo({ issues: [theirs] });
  assert.equal((await run(MOVED, behind)).action, "create");
  assert.deepEqual(behind.writes().map((c) => c.path), [`/repos/${SELF}/labels`, `/repos/${SELF}/issues`]);
  const current = repo({ issues: [theirs] });
  assert.equal((await run(CURRENT, current)).action, "none");
  assert.deepEqual(current.writes(), []);
});

test("a note put above the marker makes the issue no longer ours: it is left alone and another is opened", async () => {
  const noted = issue(7, `Assigned to me.\n${issueBody(STATE)}`);
  const r = repo({ issues: [noted] });
  assert.equal((await run(MOVED, r)).action, "create");
  assert.equal(r.writes().some((c) => c.path.endsWith("/issues/7")), false);
  // A note below it survives while the state is the same, and goes when the state changes.
  const below = repo({ issues: [issue(7, `${issueBody(STATE)}\nNOTE: blocked on #12\n`)] });
  assert.equal((await run(MOVED, below)).action, "unchanged");
  const moved = repo({ issues: [issue(7, `${issueBody({ ...STATE, headCommit: OLDER_HEAD })}\nNOTE: blocked on #12\n`)] });
  assert.equal((await run(MOVED, moved)).action, "update");
  assert.equal(moved.writes()[0].body.body.includes("NOTE"), false);
});

test("the bot's issues are asked for by author, page by page, and a flood is refused", async () => {
  const others = Array.from({ length: 150 }, (_, i) => issue(1000 + i, "an unrelated report"));
  const r = repo({ issues: [...others, OURS] });
  assert.equal((await run(MOVED, r)).action, "unchanged");
  const lists = r.calls.filter((c) => c.path.includes("/issues?"));
  assert.deepEqual(lists.map((c) => c.path), [1, 2].map((p) => `/repos/${SELF}/issues?state=open&creator=github-actions%5Bbot%5D&sort=created&direction=desc&per_page=100&page=${p}`));
  const flood = repo({ issues: Array.from({ length: 1000 }, (_, i) => issue(i + 1, "noise")) });
  await assert.rejects(run(MOVED, flood), /more than 1000 open issues by github-actions\[bot\]/);
  assert.deepEqual(flood.writes(), []);
});

test("two issues open: the older is kept current, and both close when the copy catches up", async () => {
  const second = issue(9, issueBody({ ...STATE, headCommit: OLDER_HEAD }));
  const behind = repo({ issues: [second, OURS_OLDER] });
  const result = await run(MOVED, behind);
  assert.equal(result.action, "update");
  assert.deepEqual(behind.writes().map((c) => c.path), [`/repos/${SELF}/issues/7`]);
  const current = repo({ issues: [second, OURS_OLDER] });
  assert.deepEqual((await run(CURRENT, current)).issues, [7, 9]);
  assert.deepEqual(current.writes().map((c) => `${c.method} ${c.path.replace(`/repos/${SELF}`, "")}`), [
    "PATCH /issues/7",
    "POST /issues/7/comments",
    "PATCH /issues/9",
    "POST /issues/9/comments",
  ]);
});

test("a refusal from this repository's API is an error, not a quiet pass", async () => {
  const refuse = (match, status) => {
    const r = repo();
    const inner = r.api;
    r.api = async (method, apiPath, body) => (match(method, apiPath) ? { status, json: null } : inner(method, apiPath, body));
    return r;
  };
  await assert.rejects(run(MOVED, refuse((m, p) => m === "GET" && p.includes("/issues?"), 403)), /GET \/repos\/me\/copy\/issues\?.* returned HTTP 403/);
  await assert.rejects(run(MOVED, refuse((m, p) => m === "POST" && p.endsWith("/issues"), 410)), /opening the issue returned HTTP 410/);
  await assert.rejects(run(MOVED, repo(), { self: "not a slug" }), /GITHUB_REPOSITORY must name this repository/);
});

test("a read of this repository is retried like a read of upstream; a write is sent once", async () => {
  const flaky = (failures, match) => {
    const r = repo();
    const inner = r.api;
    let left = failures;
    r.api = async (method, apiPath, body) => {
      if (match(method, apiPath) && left > 0) {
        left--;
        r.calls.push({ method, path: apiPath, body });
        if (left % 2 === 0) throw new Error("socket hang up");
        return { status: 502, json: null };
      }
      return inner(method, apiPath, body);
    };
    return r;
  };
  const isList = (m, p) => m === "GET" && p.includes("/issues?");
  // Three bad answers, then a good one: the run goes on as if nothing happened.
  slept.length = 0;
  const recovered = flaky(3, isList);
  assert.equal((await run(MOVED, recovered)).action, "create");
  assert.deepEqual(slept, [...RETRY_DELAYS_MS]);
  // Four: it fails, having written nothing.
  const lasting = flaky(4, isList);
  await assert.rejects(run(MOVED, lasting), /GET \/repos\/me\/copy\/issues\?.* (?:returned HTTP 502|failed \(socket hang up\))/);
  assert.deepEqual(lasting.writes(), []);
  // A write that is refused is not sent again: a second POST could be a second issue.
  const once = flaky(1, (m, p) => m === "POST" && p.endsWith("/issues"));
  await assert.rejects(run(MOVED, once), /POST \/repos\/me\/copy\/issues failed \(socket hang up\)/);
  assert.equal(once.calls.filter((c) => c.method === "POST" && c.path.endsWith("/issues")).length, 1);
});

test("a repository with Issues turned off is told nothing, and the run passes", async () => {
  for (const status of [410, 404]) {
    const r = repo();
    const inner = r.api;
    r.api = async (method, apiPath, body) => (apiPath.includes("/issues?") ? { status, json: { message: "Issues are disabled for this repo" } } : inner(method, apiPath, body));
    const result = await run(MOVED, r);
    assert.equal(result.action, "disabled", `HTTP ${status}`);
    assert.match(result.lines.at(-1), /me\/copy has Issues turned off/);
    assert.deepEqual(r.writes(), []);
  }
  // …but only after the record was checked: a wrong record is still a red run there.
  const r = repo();
  r.api = async () => ({ status: 410, json: null });
  await assert.rejects(run({ ...MOVED, [manifestAt(RECORDED)]: { status: 404, text: "" } }, r), /no issue was opened/);
});

// --- what is written -----------------------------------------------------------------

test("the issue is built from the five validated values and nothing else", () => {
  const body = issueBody(STATE);
  assert.equal(body.split("\n")[0], `<!-- agent:harness-sync upstream=org/core recorded=${RECORDED} head=${HEAD} -->`);
  assert.equal(body.split("\n")[0], markerLine(STATE));
  for (const value of [RECORDED, HEAD, OLD_DIGEST, NEW_DIGEST]) assert.ok(body.includes(`\`${value}\``), value);
  assert.ok(body.includes(`https://github.com/org/core/compare/${RECORDED}...${HEAD}\n`));
  assert.match(body, /Only the changes under `scripts\/agent\/` and to `\.github\/workflows\/agent-\*\.yml` are part of a sync/);
  assert.ok(body.includes("git -C <upstream checkout> switch <its default branch> && git -C <upstream checkout> pull\nnode <upstream checkout>/scripts/agent/sync.mjs --from <upstream checkout> --to .\n"));
  // sync.mjs's other to-do: the pin, without which a copy can declare itself the origin.
  assert.ok(body.includes('`.github/agent-harness/repo.test.mjs` must assert that `.github/agent-harness/upstream.json`\'s `repository` is `"org/core"`'));
  assert.ok(readFileSync(path.join(HERE, "sync.mjs"), "utf8").includes("Pin the upstream in"), "sync.mjs no longer asks for the pin; the issue body repeats it");
  assert.match(body, /what `sync\.mjs` prints under "To do:"/);
  assert.ok(body.includes("`git add scripts/agent .github/workflows .github/agent-harness`"));
  // The heading sync.mjs prints, and the command it tells the adopter to run, are the ones the body repeats.
  const sync = readFileSync(path.join(HERE, "sync.mjs"), "utf8");
  assert.ok(sync.includes('console.log("\\nTo do:")'), "sync.mjs no longer prints `To do:`; the issue body names it");
  assert.ok(sync.includes("npm ci && npm test && npm run test:repo"), "sync.mjs's test command changed; the issue body repeats it");

  // Anything that is not what it claims to be is refused, not escaped.
  const bad = [
    { repository: "org/core`\n# injected" },
    { repository: "org/core/extra" },
    { recordedCommit: "main" },
    { recordedCommit: RECORDED.toUpperCase() },
    { headCommit: `${HEAD}\n@everyone` },
    { headCommit: HEAD.slice(0, 39) },
    { recordedDigest: "sha256:abc" },
    { headDigest: undefined },
  ];
  for (const change of bad) {
    assert.throws(() => issueBody({ ...STATE, ...change }), /refusing to write an issue/, JSON.stringify(change));
    assert.throws(() => markerLine({ ...STATE, ...change }), /refusing to write an issue/, JSON.stringify(change));
  }
  assert.throws(() => issueTitle("org/core\n[bot]"), /refusing to write an issue/);
  assert.throws(() => closingComment({ repository: "org/core", headCommit: "HEAD", headDigest: OLD_DIGEST }), /refusing to write an issue/);
});

test("nothing upstream wrote reaches a write: only the commit id is taken from the head", async () => {
  // The fake head's commit message carries the marker and a shell command.
  const r = repo();
  await run(MOVED, r);
  const sent = JSON.stringify(r.writes());
  assert.equal(sent.includes("rm -rf"), false);
  assert.equal(sent.includes("trunk"), false, "the branch's name is upstream's to choose, and is not written");
  assert.equal(r.writes().at(-1).body.body, issueBody(STATE));
});

// --- the dry run ----------------------------------------------------------------------

test("a dry run decides the same and sends nothing but GETs", async () => {
  const cases = [
    [MOVED, {}, "create"],
    [MOVED, { issues: [OURS_OLDER] }, "update"],
    [MOVED, { issues: [OURS] }, "unchanged"],
    [MOVED, { issues: [{ ...OURS_OLDER, state: "closed" }] }, "reopen"],
    [MOVED, { issues: [{ ...OURS, state: "closed" }] }, "unchanged"],
    [CURRENT, { issues: [OURS] }, "close"],
    [CURRENT, {}, "none"],
  ];
  for (const [answers, options, action] of cases) {
    const r = repo(options);
    const result = await run(answers, r, { dryRun: true });
    assert.equal(result.action, action);
    assert.deepEqual(r.writes(), [], action);
    if (action === "create" || action === "update" || action === "reopen") assert.equal(result.body, issueBody(STATE));
    if (action !== "none" && action !== "unchanged") assert.ok(result.lines.some((l) => l.startsWith("Dry run: would ")), action);
  }
  await assert.rejects(run({ ...MOVED, [manifestAt(RECORDED)]: { status: 404, text: "" } }, repo(), { dryRun: true }), /no issue was opened/);
});

test("the job is given longer than the script gives itself", () => {
  const workflow = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-upstream-notify.yml"), "utf8");
  const minutes = Number(/^ {4}timeout-minutes: (\d+)$/m.exec(workflow)?.[1]);
  assert.ok(minutes * 60_000 > BUDGET_MS, `timeout-minutes is ${minutes}; the script's own budget is ${BUDGET_MS / 60_000} minutes and must end first`);
  // …and the budget is more than one fetch's worst case, or it would cut off a fetch that was about to answer.
  const oneFetch = 4 * 30_000 + RETRY_DELAYS_MS.reduce((a, b) => a + b, 0);
  assert.ok(BUDGET_MS > 2 * oneFetch);
});

// --- the CLI ---------------------------------------------------------------------------

test("CLI: in the repository the core is written in it says so and exits 0, with no token and no network", () => {
  const upstream = JSON.parse(readFileSync(path.join(HERE, "..", "..", ".github", "agent-harness", "upstream.json"), "utf8"));
  if (upstream.repository !== null) return; // a copy: the CLI would go to the network
  const env = { ...process.env, GITHUB_REPOSITORY: "owner/origin" };
  delete env.GH_TOKEN;
  delete env.GITHUB_TOKEN;
  for (const args of [[], ["--dry-run"]]) {
    const run = spawnSync(process.execPath, [path.join(HERE, "upstream-notify.mjs"), ...args], { encoding: "utf8", env });
    assert.equal(run.status, 0, run.stdout + run.stderr);
    assert.match(run.stdout, /owner\/origin is recorded as the repository the core is written in.*nothing to notify/);
  }
  const usage = spawnSync(process.execPath, [path.join(HERE, "upstream-notify.mjs"), "--close-everything"], { encoding: "utf8", env });
  assert.equal(usage.status, 1);
  assert.match(usage.stderr, /usage: node upstream-notify\.mjs \[--dry-run\]/);
});
