// File the out-of-diff findings the gate deferred as follow-up issues, so "not
// in this PR" does not quietly become "never".
//
// WHY THIS EXISTS. `out-of-diff.mjs` takes a finding off a PR's gate when git
// places it outside the diff and an independent judge finds the change did not
// cause it. Those findings are real — the verifier confirmed each one — and the
// `agent-deferred-findings` check already records them. But a check run is
// attached to one commit of one PR: it scrolls away with the next push and is
// gone when the PR merges. The deferred channel was built as an archive for
// triage, not as a to-do list anyone is shown.
//
// The defects that motivated the gate are exactly the ones that need a to-do
// list: "the server accepts a pushed change stamped with any actor" (#2111)
// became #2114 only because a maintainer filed it by hand after fifteen rounds.
// So the panel files the issue itself, once, and every later PR that re-raises
// the same defect finds it already filed.
//
// WHAT IT WILL NOT DO:
//   - file a finding the gate did not demote out of the diff. Native minors,
//     relocated code and fix-round surface stay in the deferred record only —
//     they are scheduling decisions about THIS PR, not new work.
//   - file more than `MAX_NEW_ISSUES_PER_RUN` per panel run. A lens that went
//     wide must not be able to flood the tracker; the rest stay in the deferred
//     record and are filed on a later round if they recur.
//   - trust an issue it did not write. Dedup reads only issues authored by the
//     workflow token's bot, because the label alone can be applied by anyone
//     with triage access and a forged record would suppress a real filing.
//   - echo model text raw. Findings are model output derived from the diff, so a
//     contributor can get chosen text into them; `@mentions` are broken (no
//     pings from a bot) and `<!--` is neutralised (no forged hidden record).
//
// Usage:
//   node follow-up-issues.mjs file --review-dir .agent-review --lenses <lenses.json>
//     --pr <n> [--head <sha>] [--dry-run]
// Best-effort: always exits 0. A follow-up that could not be filed is still in
// the deferred record, which is where it was before this existed.

import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { parseArgs } from "./gh-checks.mjs";
import { readLensFindings } from "./deferred-findings.mjs";
import { findingLocation } from "./novelty.mjs";
import { normalizeSeverity } from "./severity.mjs";
import { findingSimilarity, DEFAULT_SIMILARITY } from "./rounds.mjs";
import { outOfDiffDemotes } from "./out-of-diff.mjs";

export const FOLLOW_UP_LABEL = "agent:follow-up";
export const FOLLOW_UP_MARKER = "<!-- agent-follow-up ";
export const FOLLOW_UP_VERSION = 1;
/** REST `user.login` of the workflow token, the only author whose records count. */
export const FOLLOW_UP_AUTHOR_LOGINS = Object.freeze(["github-actions[bot]"]);
export const MAX_NEW_ISSUES_PER_RUN = 3;

const str = (v) => (typeof v === "string" ? v : "");
const clip = (s, n) => {
  const t = str(s);
  return t.length > n ? `${t.slice(0, n)}…` : t;
};

/** Model text made safe to post as the bot: no pings, no hidden records, one line where asked. */
export function neutralize(s, { oneLine = false } = {}) {
  let t = str(s).replace(/<!--/g, "<!-‌-").replace(/@(?=[A-Za-z0-9_-])/g, "@​");
  if (oneLine) t = t.replace(/[\r\n]+/g, " ");
  return t;
}

/** The findings this module files: blocking severity, lane backlog, demoted out of the diff. */
export function selectFollowUps(lensFindings) {
  const out = [];
  for (const entry of Array.isArray(lensFindings) ? lensFindings : []) {
    const lens = str(entry?.lens);
    if (!lens) continue;
    for (const f of Array.isArray(entry?.findings) ? entry.findings : []) {
      if (!f || typeof f !== "object" || f.lane !== "backlog") continue;
      if (!outOfDiffDemotes(f.outOfDiff)) continue;
      out.push({ lens, finding: f });
    }
  }
  return out;
}

/** The hidden record an issue carries, and the identity dedup compares. */
export function followUpRecordOf({ lens, finding }, { pr = null } = {}) {
  const loc = findingLocation(finding);
  return {
    v: FOLLOW_UP_VERSION,
    lens: clip(lens, 40),
    file: clip(loc?.file ?? finding?.file ?? "", 300),
    ...(Number.isInteger(loc?.line) ? { line: loc.line } : {}),
    severity: normalizeSeverity(finding?.severity),
    summary: clip(finding?.summary, 400),
    ...(Number.isInteger(pr) && pr > 0 ? { pr } : {}),
  };
}

export function serializeFollowUpRecord(rec) {
  // `--` cannot appear inside an HTML comment's payload; escaping `>` keeps a
  // summary from closing the comment early.
  const json = JSON.stringify(rec).replace(/--/g, "-\\u002d").replace(/>/g, "\\u003e");
  return `${FOLLOW_UP_MARKER}${json} -->`;
}

export function parseFollowUpRecord(body) {
  const b = str(body);
  const i = b.indexOf(FOLLOW_UP_MARKER);
  if (i < 0) return null;
  const end = b.indexOf(" -->", i);
  if (end < 0) return null;
  try {
    const rec = JSON.parse(b.slice(i + FOLLOW_UP_MARKER.length, end));
    if (!rec || typeof rec !== "object" || rec.v !== FOLLOW_UP_VERSION) return null;
    return { lens: str(rec.lens), file: str(rec.file), summary: str(rec.summary) };
  } catch {
    return null;
  }
}

/** Records from issues this module wrote. Anything else is ignored, by author. */
export function collectFollowUpRecords(issues) {
  const out = [];
  for (const issue of Array.isArray(issues) ? issues : []) {
    if (!issue || issue.pull_request) continue;
    if (!FOLLOW_UP_AUTHOR_LOGINS.includes(str(issue.user?.login))) continue;
    const rec = parseFollowUpRecord(issue.body);
    if (rec) out.push({ ...rec, number: issue.number, state: str(issue.state) });
  }
  return out;
}

/**
 * Is this defect already filed? Same file, similar summary, ACROSS lenses: a
 * pre-existing defect is the same defect whichever lens trips on it, and the
 * same one recurring on another PR is the case this exists to absorb. A CLOSED
 * match counts too — a maintainer who closed it as won't-fix has answered, and
 * refiling it every round would be the loop arguing with them.
 */
export function knownFollowUp(rec, existing, { threshold = DEFAULT_SIMILARITY } = {}) {
  const a = { lens: "", file: rec.file, summary: rec.summary };
  return (Array.isArray(existing) ? existing : []).find(
    (e) => findingSimilarity(a, { lens: "", file: e.file, summary: e.summary }) >= threshold,
  ) ?? null;
}

/** What to file this run: new, de-duplicated against the tracker AND each other, capped. */
export function planFollowUps(selected, existing, { pr = null, max = MAX_NEW_ISSUES_PER_RUN } = {}) {
  const file = [];
  const known = [];
  const skipped = [];
  const seen = [...(Array.isArray(existing) ? existing : [])];
  for (const s of Array.isArray(selected) ? selected : []) {
    const rec = followUpRecordOf(s, { pr });
    const hit = knownFollowUp(rec, seen);
    if (hit) {
      if (hit.number) known.push({ rec, number: hit.number });
      continue;
    }
    if (file.length >= max) {
      skipped.push(rec);
      continue;
    }
    file.push({ rec, finding: s.finding });
    seen.push(rec);
  }
  return { file, known, skipped };
}

export function renderFollowUpIssue({ rec, finding }, { repo = "", pr = null, head = "" } = {}) {
  const loc = rec.line ? `${rec.file}:${rec.line}` : rec.file;
  const critical = rec.severity === "critical";
  const title = `${critical ? "[critical] " : ""}Follow-up: ${neutralize(clip(rec.summary, 90), { oneLine: true })}`;
  const o = finding?.outOfDiff ?? {};
  const link = repo && /^[0-9a-f]{40}$/.test(str(head)) && rec.file
    ? `https://github.com/${repo}/blob/${head}/${rec.file}${rec.line ? `#L${rec.line}` : ""}`
    : "";
  const body = [
    serializeFollowUpRecord(rec),
    `The review panel raised this on ${pr ? `#${pr}` : "a pull request"} and deferred it: the code is outside that PR's diff, and an independent judge found the change did not cause it. It was confirmed real by the panel's verifier, so it is filed here instead of being fixed inside an unrelated PR.`,
    "",
    `- **Where:** ${link ? `[\`${neutralize(loc, { oneLine: true })}\`](${link})` : `\`${neutralize(loc, { oneLine: true })}\``}`,
    `- **Severity:** ${rec.severity} (lens: ${neutralize(rec.lens, { oneLine: true })})`,
    "",
    "**Finding** (model output, quoted — verify before acting):",
    "",
    `> ${neutralize(clip(finding?.summary, 2000)).replace(/\n/g, "\n> ")}`,
    ...(str(finding?.evidence)
      ? ["", "**Evidence the reviewer gave:**", "", `> ${neutralize(clip(finding.evidence, 3000)).replace(/\n/g, "\n> ")}`]
      : []),
    ...(str(o.reason)
      ? ["", "**Why it was judged independent of the change:**", "", `> ${neutralize(clip(o.reason, 1500)).replace(/\n/g, "\n> ")}`]
      : []),
    ...(Array.isArray(o.groundedIn) && o.groundedIn.length
      ? ["", "Cited by the judge:", ...o.groundedIn.slice(0, 8).map((c) => `- \`${neutralize(str(c), { oneLine: true }).replace(/`/g, "'")}\``)]
      : []),
    "",
    "_Filed automatically by the agent review loop (`scripts/agent/follow-up-issues.mjs`). Close it if the finding is wrong or not worth doing; a closed issue is not refiled._",
  ].join("\n");
  return { title, body };
}

function main() {
  const a = parseArgs(process.argv, { booleans: ["dry-run"] });
  const [verb] = a._;
  const pr = Number(a.pr);
  if (verb !== "file" || !Number.isInteger(pr) || pr <= 0 || !str(a.lenses)) {
    console.error("usage: follow-up-issues.mjs file --review-dir <dir> --lenses <lenses.json> --pr <n> [--head <sha>] [--dry-run]");
    return;
  }
  let manifest = [];
  try {
    manifest = JSON.parse(readFileSync(a.lenses, "utf8"));
  } catch (e) {
    console.error(`follow-up-issues: cannot read ${a.lenses} (${e.message}); filing nothing.`);
    return;
  }
  const selected = selectFollowUps(readLensFindings(str(a["review-dir"]) || ".agent-review", manifest));
  if (selected.length === 0) {
    console.error("follow-up-issues: no finding was deferred out of the diff this round.");
    return;
  }
  const gh = (args, input) => execFileSync("gh", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, ...(input ? { input } : {}) });
  let existing = [];
  try {
    const issues = JSON.parse(gh(["api", "--paginate", `repos/{owner}/{repo}/issues?labels=${encodeURIComponent(FOLLOW_UP_LABEL)}&state=all&per_page=100`]));
    existing = collectFollowUpRecords(issues);
  } catch (e) {
    // Without the tracker there is no dedup, and filing blind would duplicate on
    // every round. Stop; the deferred record still holds them.
    console.error(`follow-up-issues: could not list existing follow-ups (${e.message}); filing nothing.`);
    return;
  }
  const plan = planFollowUps(selected, existing, { pr });
  for (const k of plan.known) console.error(`follow-up-issues: already filed as #${k.number}: ${clip(k.rec.summary, 100)}`);
  for (const s of plan.skipped) console.error(`follow-up-issues: over the per-run cap, left in the deferred record: ${clip(s.summary, 100)}`);
  if (plan.file.length === 0) return;
  const repo = str(process.env.GITHUB_REPOSITORY);
  if (!a["dry-run"]) {
    try {
      gh(["label", "create", FOLLOW_UP_LABEL, "--color", "C5DEF5", "--description", "Real finding deferred out of an unrelated PR by the agent review loop"]);
    } catch {
      // Already exists (the common case) or no permission; the create below says which.
    }
  }
  for (const item of plan.file) {
    const { title, body } = renderFollowUpIssue(item, { repo, pr, head: str(a.head) });
    if (a["dry-run"]) {
      console.log(`--- would file: ${title}\n${body}\n`);
      continue;
    }
    try {
      const out = gh(["issue", "create", "--title", title, "--label", FOLLOW_UP_LABEL, "--body-file", "-"], body);
      console.error(`follow-up-issues: filed ${out.trim()}`);
    } catch (e) {
      console.error(`follow-up-issues: could not file "${clip(title, 80)}" (${e.message}).`);
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
