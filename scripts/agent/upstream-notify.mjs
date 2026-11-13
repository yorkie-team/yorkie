// Tell a repository that copied the core when the repository it was copied
// from has a newer one: one issue, opened, kept current and closed by the
// weekly run of `agent-upstream-notify.yml`.
//
// `manifest.mjs upstream` already notices. It says so as a warning annotation
// on a green run, which is read by whoever opens that run, which is nobody.
// This says it where a repository's people look. It NOTIFIES ONLY: a person
// runs `sync.mjs`, because a sync ends in configuration edits and a test run
// that only the adopting repository can judge.
//
// WHAT IT COMPARES. Four values:
//
//   recorded commit, recorded digest    `.github/agent-harness/upstream.json`
//   head commit                         upstream's default branch, now
//   head digest                         the manifest at exactly that commit
//
// The head's manifest is fetched BY THE COMMIT just read, not by the branch,
// so the pair describes one tree even if upstream is pushed to in between.
//
// WHAT IT DOES:
//
//   repository is null (the origin)          nothing, and says so
//   the pinned check fails                    THROWS — no issue is touched
//   the head cannot be read                   THROWS — no issue is touched
//   digests differ, no open issue             opens one
//   digests differ, open issue, same state    nothing
//   digests differ, open issue, older state   rewrites its body (no comment)
//   digests equal, open issue(s)              one comment, then closes each
//   digests equal, no open issue              nothing
//
// "The pinned check" is `checkUpstream`: this copy carries the recorded
// manifest, the recorded commit is on upstream's default branch, and the
// manifest there is the recorded one. A copy that fails it is not "behind" —
// its record is wrong, or upstream cannot be asked — and an issue saying
// "upstream moved" would be the wrong message about a different problem. The
// run goes red instead.
//
// WHICH ISSUE IS OURS. Not the one with the right title, and not the one with
// the label: anyone who can open an issue chooses its title, and anyone with
// triage rights its labels. An issue is this script's when it is open, is not
// a pull request, was AUTHORED BY `github-actions[bot]` — the job's own token,
// a login no person can hold — and its body's FIRST LINE is the marker. The
// first line, so that a bot-authored issue quoting somebody's text further
// down cannot be made to carry it. Everything else is left alone, so the
// worst a forged marker does is get ignored.
//
// WHAT GOES INTO THE ISSUE. Only values that were validated as what they are:
// a strict `owner/name`, 40-hex commit ids, sha256 digests computed here.
// Nothing upstream wrote — no commit message, no branch name, no file content
// — reaches the title, the body, or a shell.
//
// Usage:
//   node upstream-notify.mjs             act (needs `issues: write`)
//   node upstream-notify.mjs --dry-run   decide, print the decision and the
//                                        issue body, and send nothing but GETs

import { realpathSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { HARNESS_DIR, isRepoSlug } from "./config.mjs";
import { MANIFEST_PATH, PACKAGE_DIR, UPSTREAM_PATH, WORKFLOW_DIR, checkUpstream, defaultRoot, getWithRetry, githubFetchText, manifestDigest, readManifest, readUpstream } from "./manifest.mjs";

/** The label the issue carries, when this job is allowed to have it exist. */
export const LABEL = "agent:harness-sync";
/** The one author whose issues this script will edit or close. */
export const BOT_LOGIN = "github-actions[bot]";
/** What the first line of the issue's body starts with. */
export const MARKER_PREFIX = `<!-- ${LABEL} `;

const isCommit = (v) => typeof v === "string" && /^[0-9a-f]{40}$/.test(v);
const isDigest = (v) => typeof v === "string" && /^[0-9a-f]{64}$/.test(v);

/** Throws unless every value that will be written into an issue is what it claims to be. */
function assertState({ repository, recordedCommit, recordedDigest, headCommit, headDigest }) {
  const bad = [];
  if (!isRepoSlug(repository)) bad.push("the upstream repository is not `owner/name`");
  if (!isCommit(recordedCommit)) bad.push("the recorded commit is not a 40-character commit id");
  if (!isCommit(headCommit)) bad.push("upstream's head is not a 40-character commit id");
  if (!isDigest(recordedDigest)) bad.push("the recorded digest is not a sha256");
  if (!isDigest(headDigest)) bad.push("upstream's digest is not a sha256");
  if (bad.length > 0) throw new Error(`upstream-notify: refusing to write an issue: ${bad.join("; ")}`);
}

/**
 * The body's first line. It names the state the body was written for, so
 * "has anything changed since?" is a comparison of two lines.
 */
export function markerLine(state) {
  assertState(state);
  return `${MARKER_PREFIX}upstream=${state.repository} recorded=${state.recordedCommit} head=${state.headCommit} -->`;
}

/** The issue's title: a fixed marker, then the upstream's name. */
export function issueTitle(repository) {
  if (!isRepoSlug(repository)) throw new Error("upstream-notify: refusing to write an issue: the upstream repository is not `owner/name`");
  return `[${LABEL}] The shared harness files are behind ${repository}`;
}

/** The issue's body, built from the five validated values and nothing else. */
export function issueBody(state) {
  const { repository, recordedCommit, recordedDigest, headCommit, headDigest } = state;
  return [
    markerLine(state),
    `The shared harness files in this repository — \`scripts/agent/**\` and \`.github/workflows/agent-*.yml\` — are a copy of \`${repository}\`, and its default branch now has a different set.`,
    "",
    "| | Commit | Manifest digest |",
    "|---|---|---|",
    `| This copy (\`${UPSTREAM_PATH}\`) | \`${recordedCommit}\` | \`${recordedDigest}\` |`,
    `| \`${repository}\`, default branch | \`${headCommit}\` | \`${headDigest}\` |`,
    "",
    `What changed upstream: https://github.com/${repository}/compare/${recordedCommit}...${headCommit}`,
    "",
    `That comparison shows every change in \`${repository}\` between the two commits. Only the changes under \`scripts/agent/\` and to \`.github/workflows/agent-*.yml\` are part of a sync; the rest is that repository's own.`,
    "",
    "## To sync",
    "",
    `From this repository's root, with a local checkout of \`${repository}\` on its default branch:`,
    "",
    "```sh",
    "git -C <upstream checkout> pull",
    "node <upstream checkout>/scripts/agent/sync.mjs --from <upstream checkout> --to .",
    "```",
    "",
    `Then do what \`sync.mjs\` prints under "To do:" — the configuration edits it names, if any, then \`git add ${PACKAGE_DIR} ${WORKFLOW_DIR} ${HARNESS_DIR}\` and, in \`${PACKAGE_DIR}\`, \`npm ci && npm test && npm run test:repo\` — and open a pull request with the result.`,
    "",
    "Nothing here syncs for you. The weekly run rewrites this issue when upstream moves again and closes it once the copy matches.",
    "",
  ].join("\n");
}

/** The comment left when the issue is closed. One line. */
export function closingComment({ repository, headCommit, headDigest }) {
  assertState({ repository, recordedCommit: headCommit, recordedDigest: headDigest, headCommit, headDigest });
  return `The shared harness files match \`${repository}\`'s default branch again (\`${headCommit}\`, manifest digest \`${headDigest}\`). Closing.`;
}

const firstLine = (body) => (typeof body === "string" ? body.split(/\r?\n/, 1)[0] : "");

/** Is `issue` (as the REST API lists it) one this script wrote and may touch? */
export function isOurIssue(issue) {
  return (
    issue !== null &&
    typeof issue === "object" &&
    Number.isInteger(issue.number) &&
    issue.state === "open" &&
    issue.pull_request === undefined &&
    issue.user?.login === BOT_LOGIN &&
    firstLine(issue.body).startsWith(MARKER_PREFIX)
  );
}

const PER_PAGE = 100;
const MAX_PAGES = 10;

/**
 * Decide, and — unless `dryRun` — act. Returns `{ action, lines, issues,
 * body? }`, where `action` is one of `origin`, `none`, `create`, `unchanged`,
 * `update`, `close`. Throws when the copy cannot be placed against upstream
 * or GitHub refuses a call; nothing is written before the decision is made.
 *
 * `fetchText(url)` resolves to `{ status, text }` and reads upstream.
 * `api(method, path, body)` resolves to `{ status, json }` and talks to THIS
 * repository; with `dryRun` it is only ever called with `GET`.
 */
export async function notifyUpstream({ upstream, localDigest, self, fetchText, api, dryRun = false, sleep = (ms) => new Promise((r) => setTimeout(r, ms)) }) {
  if (upstream.repository === null) {
    return {
      action: "origin",
      issues: [],
      lines: [`${self ? `${self} is` : "This is"} recorded as the repository the core is written in (${UPSTREAM_PATH}: repository is null); there is no upstream to fall behind, and nothing to notify.`],
    };
  }
  if (!isRepoSlug(self)) throw new Error("upstream-notify: GITHUB_REPOSITORY must name this repository as `owner/name`");

  // --- is the record true? If not, that is the news, and it is not this issue's.
  const pinned = await checkUpstream({ upstream, localDigest, fetchText, self, sleep });
  if (!pinned.ok) {
    throw new Error(
      [...pinned.lines, "This copy cannot be placed against its upstream, so whether upstream has moved on was not asked and no issue was opened, changed or closed."].join("\n"),
    );
  }

  // --- where is upstream now?
  const { repository, commit: recordedCommit, manifestDigest: recordedDigest } = upstream;
  const base = `https://api.github.com/repos/${repository}`;
  const read = async (url, what) => {
    const r = await getWithRetry(url, fetchText, sleep);
    if (r.status === 200) return r.text;
    throw new Error(`Could not read ${what}: ${url} ${r.error !== undefined ? `failed (${r.error})` : `returned HTTP ${r.status}`}. No issue was opened, changed or closed.`);
  };
  const headUrl = `${base}/commits/${encodeURIComponent(pinned.branch)}`;
  const headText = await read(headUrl, `${repository}'s default branch`);
  let headCommit;
  try {
    headCommit = JSON.parse(headText).sha;
  } catch {
    headCommit = undefined;
  }
  if (!isCommit(headCommit)) throw new Error(`${headUrl} did not name a commit. No issue was opened, changed or closed.`);
  const headDigest = manifestDigest(await read(`${base}/contents/${MANIFEST_PATH}?ref=${headCommit}`, `${repository}'s manifest at its head`));
  const state = { repository, recordedCommit, recordedDigest, headCommit, headDigest };
  assertState(state);
  const behind = headDigest !== recordedDigest;
  const lines = [...pinned.lines, `${repository}'s default branch is at ${headCommit} (manifest digest ${headDigest}): ${behind ? "this copy is behind" : "this copy is current"}.`];

  // --- this repository's side.
  const call = async (method, apiPath, body) => {
    if (dryRun && method !== "GET") throw new Error(`upstream-notify: a dry run must not send ${method} ${apiPath}`);
    return api(method, apiPath, body);
  };
  const expect = (r, statuses, what) => {
    if (!statuses.includes(r.status)) throw new Error(`upstream-notify: ${what} returned HTTP ${r.status}`);
    return r;
  };
  const repo = `/repos/${self}`;

  const ours = [];
  for (let page = 1; ; page++) {
    if (page > MAX_PAGES) throw new Error(`upstream-notify: ${self} has more than ${PER_PAGE * MAX_PAGES} open issues by ${BOT_LOGIN}; refusing to guess whether one of them is this one`);
    const listPath = `${repo}/issues?state=open&creator=${encodeURIComponent(BOT_LOGIN)}&per_page=${PER_PAGE}&page=${page}`;
    const r = expect(await call("GET", listPath), [200], `GET ${listPath}`);
    if (!Array.isArray(r.json)) throw new Error(`upstream-notify: GET ${listPath} did not return a list`);
    ours.push(...r.json.filter(isOurIssue));
    if (r.json.length < PER_PAGE) break;
  }
  ours.sort((a, b) => a.number - b.number);
  const did = (now, would) => (dryRun ? `Dry run: would ${would}` : now);

  if (!behind) {
    if (ours.length === 0) return { action: "none", issues: [], lines: [...lines, "No sync issue is open; nothing to do."] };
    const comment = closingComment(state);
    for (const issue of ours) {
      if (!dryRun) {
        expect(await call("POST", `${repo}/issues/${issue.number}/comments`, { body: comment }), [201], `commenting on #${issue.number}`);
        expect(await call("PATCH", `${repo}/issues/${issue.number}`, { state: "closed", state_reason: "completed" }), [200], `closing #${issue.number}`);
      }
      lines.push(did(`Closed #${issue.number}.`, `close #${issue.number} with one comment.`));
    }
    return { action: "close", issues: ours.map((i) => i.number), lines };
  }

  const body = issueBody(state);
  if (ours.length > 0) {
    // The oldest is the one kept current. A second should not exist; if it
    // does it is left as it is, and both close when the copy catches up.
    const [issue, ...extra] = ours;
    if (extra.length > 0) lines.push(`More than one sync issue is open (${ours.map((i) => `#${i.number}`).join(", ")}); #${issue.number} is the one kept current.`);
    if (firstLine(issue.body) === markerLine(state)) {
      return { action: "unchanged", issues: [issue.number], lines: [...lines, `#${issue.number} already says so; nothing to do.`] };
    }
    if (!dryRun) expect(await call("PATCH", `${repo}/issues/${issue.number}`, { body }), [200], `updating #${issue.number}`);
    lines.push(did(`Rewrote the body of #${issue.number}.`, `rewrite the body of #${issue.number}.`));
    return { action: "update", issues: [issue.number], lines, body };
  }

  // The label, if this job may have it exist. A token that cannot create
  // labels still opens the issue: the title's marker is what a person filters
  // by then, and the body's first line is what this script finds it by.
  let labelled = true;
  const labelPath = `${repo}/labels/${encodeURIComponent(LABEL)}`;
  const existing = expect(await call("GET", labelPath), [200, 404], `GET ${labelPath}`);
  if (existing.status === 404) {
    if (dryRun) lines.push(`Dry run: would create the label ${LABEL}.`);
    else {
      const made = await call("POST", `${repo}/labels`, {
        name: LABEL,
        color: "0e8a16",
        description: "The shared agent harness files are behind the repository they were copied from",
      });
      // 422: it exists after all (another run made it). 403, or 404 for a
      // repository the token cannot administer labels in: go without.
      if (made.status === 403 || made.status === 404) {
        labelled = false;
        lines.push(`Could not create the label ${LABEL} (HTTP ${made.status}); opening the issue without it.`);
      } else expect(made, [201, 422], `creating the label ${LABEL}`);
    }
  }
  const title = issueTitle(repository);
  if (dryRun) {
    lines.push(`Dry run: would open an issue titled ${JSON.stringify(title)}, labelled ${LABEL}.`);
    return { action: "create", issues: [], lines, body };
  }
  const created = expect(await call("POST", `${repo}/issues`, { title, body, ...(labelled ? { labels: [LABEL] } : {}) }), [201], "opening the issue");
  const number = created.json?.number;
  lines.push(`Opened #${Number.isInteger(number) ? number : "?"}${labelled ? `, labelled ${LABEL}` : ""}.`);
  return { action: "create", issues: Number.isInteger(number) ? [number] : [], lines, body };
}

/** `api` over the GitHub REST API, for this repository's issues and labels. */
async function githubApi(method, apiPath, body) {
  // The job's token goes to api.github.com, to a repository path, and nowhere
  // else; a redirect is not followed with it.
  if (!apiPath.startsWith("/repos/")) throw new Error(`refusing to call ${apiPath}: not a repository path`);
  const headers = { Accept: "application/vnd.github+json", "User-Agent": "harness-upstream-notify", "X-GitHub-Api-Version": "2022-11-28" };
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(`https://api.github.com${apiPath}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: "error",
    signal: AbortSignal.timeout(30_000),
  });
  let json = null;
  try {
    json = JSON.parse(await res.text());
  } catch {
    json = null;
  }
  return { status: res.status, json };
}

// --- CLI ---------------------------------------------------------------------------

function isDirectRun() {
  if (!process.argv[1]) return false;
  const entry = fileURLToPath(import.meta.url);
  try {
    return realpathSync(process.argv[1]) === realpathSync(entry);
  } catch {
    return path.resolve(process.argv[1]) === path.resolve(entry);
  }
}

async function main(argv) {
  if (argv.length > 1 || (argv.length === 1 && argv[0] !== "--dry-run")) {
    console.error("usage: node upstream-notify.mjs [--dry-run]");
    return 1;
  }
  const dryRun = argv[0] === "--dry-run";
  const root = defaultRoot();
  const result = await notifyUpstream({
    upstream: readUpstream(root),
    localDigest: manifestDigest(readManifest(root)),
    self: process.env.GITHUB_REPOSITORY || "",
    fetchText: githubFetchText,
    api: githubApi,
    dryRun,
  });
  for (const line of result.lines) console.log(line);
  if (dryRun && result.body !== undefined) console.log(`\n--- the issue body ---\n${result.body}`);
  return 0;
}

if (isDirectRun()) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (err) => {
      for (const line of err.message.split("\n")) console.log(`::error::${line}`);
      process.exit(1);
    },
  );
}
